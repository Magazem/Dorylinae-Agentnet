package worksession

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// Regression tests for review 27 (Docs/review/27-2.1a-review.md).

// withTimeout fails the test if run does not return within d (a deadlock on
// the daemon's one SQLite connection shows up as a hang, not an error).
func withTimeout(t *testing.T, d time.Duration, run func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); run() }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("timed out: deadlock on the single SQLite connection?")
	}
}

// C1: B's mirror close completes the request inside the mail transaction.
// With the real audit.Log (same *sql.DB, MaxOpenConns 1) an audit Append
// inside that transaction blocked forever.
func TestReview27_MirrorCloseWithRealAuditDoesNotDeadlock(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	b.ws.Audit = audit.New(b.db)
	b.req.Audit = audit.New(b.db)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	deliverState(t, a, b) // awaiting_result
	if _, err := a.ws.AcceptResult(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	sm := a.ob.last(t, KindState)
	withTimeout(t, 10*time.Second, func() {
		if err := deliver(t, b, testA, sm); err != nil {
			t.Errorf("deliver closed ws.state: %v", err)
		}
	})
	var n int
	if err := b.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'request.complete'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("request.complete audit rows = %d, %v; want 1 (after commit)", n, err)
	}
}

// C1, Phase 1 fallback path: same deadlock through CheckPhase1Fallback.
func TestReview27_Phase1FallbackWithRealAuditDoesNotDeadlock(t *testing.T) {
	_, b, reqID, _ := setupAcceptedSession(t)
	b.ws.Audit = audit.New(b.db)
	b.req.Audit = audit.New(b.db)
	if ok, err := b.ws.SubmitResult(context.Background(), testA, reqID, validResult()); !ok || err != nil {
		t.Fatalf("SubmitResult: %v %v", ok, err)
	}
	if _, err := b.db.Exec(`UPDATE outbox SET state = 'failed', error = 'unsupported_kind' WHERE kind = ?`, KindResult); err != nil {
		t.Fatal(err)
	}
	withTimeout(t, 10*time.Second, func() {
		if err := b.ws.CheckPhase1Fallback(context.Background(), testA, reqID); err != nil {
			t.Errorf("CheckPhase1Fallback: %v", err)
		}
	})
}

// M2: a second ws.state "closed" with a higher seq (a misbehaving A) must be
// applied or ignored, never fail the mail transaction (an unacked mail is
// resent for 14 days).
func TestReview27_RepeatedClosedStateIsNotAnError(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	if _, err := a.ws.Cancel(context.Background(), sid, ""); err != nil {
		t.Fatal(err)
	}
	first := a.ob.last(t, KindState)
	if err := deliver(t, b, testA, first); err != nil {
		t.Fatal(err)
	}
	again := sentMail{kind: KindState, body: map[string]any{
		"at": wireTime(a.clock), "request": reqID, "round": 1, "seq": 9, "session": sid,
		"state": StateClosed, "outcome": OutcomeAccepted,
	}}
	if err := deliver(t, b, testA, again); err != nil {
		t.Fatalf("repeated closed ws.state failed the transaction: %v", err)
	}
	brv, err := b.req.Show(context.Background(), reqID, testA)
	if err != nil || brv.State != "completed" || brv.Note != "session cancelled" {
		t.Fatalf("B's request = %+v, %v", brv, err)
	}
}

// M3: discard deletes everything derived from the quarantined result,
// including B's claimed verification.
func TestReview27_DiscardClearsVerification(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	a.ws.Quarantine = alwaysQuarantine
	r := validResult()
	r.Verification = VerificationTestsPassed
	submitAndDeliverResult(t, a, b, reqID, r)
	v, err := a.ws.Discard(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	if v.Verification != "" || v.ResultBytes != 0 || v.ResultStatus != "" {
		t.Fatalf("discarded view keeps content-derived fields: %+v", v)
	}
	var ver sql.NullString
	if err := a.db.QueryRow(`SELECT verification FROM work_sessions WHERE id = ?`, sid).Scan(&ver); err != nil || ver.Valid {
		t.Fatalf("verification after discard = %v, %v", ver, err)
	}
}

// M4: an early request.complete for a request A has no session row for (B
// skipped the accept) still evaluates the quarantine rule (its peer-wide
// clause), so the content is stored nowhere in the tables the request and
// session code own.
func TestReview27_EarlyCompleteWithoutSessionHonoursQuarantine(t *testing.T) {
	ctx := context.Background()
	a := newNode(t, testA)
	var gotRound = -1
	var reqID string
	a.ws.Quarantine = func(_ context.Context, _ *sql.Tx, sid, peer string, round int) (bool, error) {
		if sid != DeriveID(testA, testB, reqID) || peer != testB {
			t.Errorf("quarantine called with sid=%s peer=%s", sid, peer)
		}
		gotRound = round
		return true, nil
	}
	out, err := a.req.Submit(ctx, request.SubmitParams{
		From: testA, To: testB, Team: testTeam, Type: request.TypeTask, Title: "t", Brief: "What: x", Urgency: request.UrgencyNormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	reqID = out.Request.ID
	const marker = "EXFIL-MARKER-27"
	sm := completeMailFrom(reqID, 2, marker, map[string]any{"status": "pass", "summary": marker}, wireTime(a.clock))
	if err := deliver(t, a, testB, sm); err != nil {
		t.Fatalf("deliver request.complete: %v", err)
	}
	if gotRound != 0 {
		t.Fatalf("quarantine rule not evaluated without a session (round=%d)", gotRound)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM requests WHERE COALESCE(note,'') LIKE '%` + marker + `%' OR COALESCE(result,'') LIKE '%` + marker + `%'`,
		`SELECT COUNT(*) FROM work_sessions WHERE COALESCE(result,'') LIKE '%` + marker + `%'`,
	} {
		var n int
		if err := a.db.QueryRow(q).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s = %d, %v", q, n, err)
		}
	}
	for _, e := range a.audit.entries {
		if strings.Contains(e.detail, marker) {
			t.Fatalf("marker in audit %s", e.action)
		}
	}
}

// M5: a failed/unsupported ws.* mail from before this session opened (a
// peer that was Phase 1 and has since upgraded) does not trigger the
// Phase 1 fallback.
func TestReview27_Phase1FallbackIgnoresOlderFailures(t *testing.T) {
	_, b, reqID, sid := setupAcceptedSession(t)
	if _, err := b.db.Exec(`INSERT INTO outbox (id, to_key, kind, created, state, updated, error)
VALUES ('m-old', ?, ?, '2020-01-01T00:00:00.000Z', 'failed', '2020-01-01T00:00:00.000Z', 'unsupported_kind')`, testA, KindResult); err != nil {
		t.Fatal(err)
	}
	if err := b.ws.CheckPhase1Fallback(context.Background(), testA, reqID); err != nil {
		t.Fatal(err)
	}
	bv, err := b.ws.Get(context.Background(), sid)
	if err != nil || bv.State != StateOpen {
		t.Fatalf("session closed by a stale failure: %+v, %v", bv, err)
	}
}

// L2: A's closes are audited ws.close with rounds and age_s.
func TestReview27_WSCloseAudited(t *testing.T) {
	a, _, _, sid := setupAcceptedSession(t)
	if _, err := a.ws.Cancel(context.Background(), sid, "no longer needed"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range a.audit.entries {
		if e.action == "ws.close" {
			found = true
			if !strings.Contains(e.detail, `"rounds":1`) || !strings.Contains(e.detail, `"age_s":`) || !strings.Contains(e.detail, `"outcome":"cancelled"`) {
				t.Fatalf("ws.close detail = %s", e.detail)
			}
		}
		if strings.Contains(e.detail, "no longer needed") {
			t.Fatalf("cancel reason in audit %s", e.action)
		}
	}
	if !found {
		t.Fatalf("no ws.close audit: %v", a.audit.actions())
	}
}
