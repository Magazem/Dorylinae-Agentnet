package worksession

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// Review 55 C21-01 (R55-022), inverted from the reviewer's test
// (Docs/review/55-code-review/tests/zz_review55_C21-01_test.go.txt): B
// submits at most one result per round, so B's second ws_result in round 1 is
// bad_state, and A's request record ends with the result A accepted.
func TestOneResultPerRound(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := setupAcceptedSession(t)

	r1 := &Result{Status: request.ResultPass, Summary: "reviewed result R1", Verification: VerificationNone}
	r2 := &Result{Status: request.ResultPass, Summary: "never reviewed R2", Verification: VerificationNone}
	if ok, _, err := b.ws.SubmitResult(ctx, testA, reqID, r1, ByAgent); !ok || err != nil {
		t.Fatalf("submit R1: %v", err)
	}
	m1 := b.ob.last(t, KindResult)
	_, _, err := b.ws.SubmitResult(ctx, testA, reqID, r2, ByAgent)
	var bse *BadStateError
	if !errors.As(err, &bse) || !strings.Contains(bse.Msg, "round 1") {
		t.Fatalf("second result in round 1: err = %v, want bad_state naming the round", err)
	}
	if n := b.ob.sentCount(KindResult); n != 0 {
		t.Fatalf("a second ws.result was sent: %d", n)
	}

	if err := deliver(t, a, testB, m1); err != nil {
		t.Fatal(err)
	}
	av, err := a.ws.AcceptResult(ctx, sid)
	if err != nil || av.Result == nil || av.Result.Summary != r1.Summary {
		t.Fatalf("accept: %+v %v", av, err)
	}
	deliverState(t, a, b)
	if err := deliver(t, a, testB, b.ob.last(t, request.KindComplete)); err != nil {
		t.Fatal(err)
	}
	arv, err := a.req.Show(ctx, reqID, "")
	if err != nil || arv.Result == nil || arv.Result.Summary != r1.Summary {
		t.Fatalf("A's request record = %+v, %v; want R1", arv.Result, err)
	}
	if containsAction(a.audit.actions(), "ws.ignored") {
		t.Errorf("an honest B's complete was audited as a mismatch: %v", a.audit.entries)
	}
}

// TestNewRoundAllowsNextResult: a request for changes starts a new round on
// B's mirror, which allows the next result (the mirror clears B's copy).
func TestNewRoundAllowsNextResult(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	deliverState(t, a, b)
	if _, err := a.ws.RequestChanges(ctx, sid, "again"); err != nil {
		t.Fatal(err)
	}
	deliverState(t, a, b)
	if ok, _, err := b.ws.SubmitResult(ctx, testA, reqID, validResult(), ByAgent); !ok || err != nil {
		t.Fatalf("round 2 result: %v", err)
	}
}

// injectComplete delivers a request.complete from a (modified) B with the
// given seq, note and result summary ("" = no result).
func injectComplete(t *testing.T, a *node, reqID string, seq int, note, summary string) {
	t.Helper()
	var res map[string]any
	if summary != "" {
		res = map[string]any{"status": "pass", "summary": summary}
	}
	if err := deliver(t, a, testB, completeMailFrom(reqID, seq, note, res, wireTime(a.clock))); err != nil {
		t.Fatalf("deliver injected request.complete: %v", err)
	}
}

func outSeq(t *testing.T, a *node, reqID string) int {
	t.Helper()
	var seq int
	if err := a.db.QueryRow(`SELECT state_seq FROM requests WHERE direction = 'out' AND id = ?`, reqID).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	return seq
}

func auditWith(a *node, action, substr string) bool {
	a.audit.mu.Lock()
	defer a.audit.mu.Unlock()
	for _, e := range a.audit.entries {
		if e.action == action && strings.Contains(e.detail, substr) {
			return true
		}
	}
	return false
}

// A modified B injects request.complete{R2} after A's accepted close of R1:
// A's record holds R1, and the mismatch is audited (R55-022 A side).
func TestModifiedWorkerCompleteAfterAcceptedClose(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := setupAcceptedSession(t)
	r1 := &Result{Status: request.ResultPass, Summary: "reviewed result R1", Verification: VerificationNone}
	submitAndDeliverResult(t, a, b, reqID, r1)
	if _, err := a.ws.AcceptResult(ctx, sid); err != nil {
		t.Fatal(err)
	}
	injectComplete(t, a, reqID, outSeq(t, a, reqID)+1, "", "never reviewed R2")

	arv, err := a.req.Show(ctx, reqID, "")
	if err != nil || arv.State != "completed" || arv.Result == nil || arv.Result.Summary != r1.Summary || arv.Note != "" {
		t.Fatalf("A's request record = %+v (result %+v), %v; want R1 and no note", arv, arv.Result, err)
	}
	if !auditWith(a, "ws.ignored", `"reason":"result_mismatch"`) {
		t.Fatalf("no result_mismatch audit: %v", a.audit.entries)
	}
}

// A cancelled close plus a request.complete carrying a result: A stores no
// result and the note "session cancelled".
func TestModifiedWorkerCompleteAfterCancelledClose(t *testing.T) {
	ctx := context.Background()
	a, _, reqID, sid := setupAcceptedSession(t)
	if _, err := a.ws.Cancel(ctx, sid, ""); err != nil {
		t.Fatal(err)
	}
	injectComplete(t, a, reqID, outSeq(t, a, reqID)+1, "all good", "sneaked in")
	arv, err := a.req.Show(ctx, reqID, "")
	if err != nil || arv.State != "completed" || arv.Result != nil || arv.Note != "session cancelled" {
		t.Fatalf("A's request record = %+v, %v; want no result and note \"session cancelled\"", arv, err)
	}
	if !auditWith(a, "ws.ignored", `"reason":"result_mismatch"`) {
		t.Fatalf("no result_mismatch audit: %v", a.audit.entries)
	}
}

// Review 69b F1: request.complete{R2} while A's session is awaiting_result
// with R1 stores no content (audited early_complete); A's later accept writes
// R1 into the completed record.
func TestEarlyCompleteMidReviewThenAccept(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := setupAcceptedSession(t)
	r1 := &Result{Status: request.ResultPass, Summary: "reviewed result R1", Verification: VerificationNone}
	submitAndDeliverResult(t, a, b, reqID, r1)
	injectComplete(t, a, reqID, outSeq(t, a, reqID)+1, "", "never reviewed R2")

	arv, err := a.req.Show(ctx, reqID, "")
	if err != nil || arv.State != "completed" || arv.Result != nil || arv.Note != "" {
		t.Fatalf("A's record mid-review = %+v, %v; want completed with no content", arv, err)
	}
	if !auditWith(a, "ws.ignored", `"reason":"early_complete"`) {
		t.Fatalf("no early_complete audit: %v", a.audit.entries)
	}
	if av, err := a.ws.Get(ctx, sid); err != nil || av.State != StateAwaitingResult {
		t.Fatalf("session = %+v, %v; want awaiting_result, left to A", av, err)
	}
	if _, err := a.ws.AcceptResult(ctx, sid); err != nil {
		t.Fatal(err)
	}
	arv, err = a.req.Show(ctx, reqID, "")
	if err != nil || arv.Result == nil || arv.Result.Summary != r1.Summary || arv.Note != "" {
		t.Fatalf("A's record after accept = %+v (result %+v), %v; want R1", arv, arv.Result, err)
	}
}

// The same with quarantined followed by discard: no result, note "session
// cancelled".
func TestEarlyCompleteMidQuarantineThenDiscard(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := setupAcceptedSession(t)
	a.ws.Quarantine = alwaysQuarantine
	submitAndDeliverResult(t, a, b, reqID, validResult())
	if av, err := a.ws.Get(ctx, sid); err != nil || av.State != StateQuarantined {
		t.Fatalf("session = %+v, %v; want quarantined", av, err)
	}
	injectComplete(t, a, reqID, outSeq(t, a, reqID)+1, "trust me", "never reviewed R2")
	arv, err := a.req.Show(ctx, reqID, "")
	if err != nil || arv.State != "completed" || arv.Result != nil || arv.Note != "" {
		t.Fatalf("A's record mid-quarantine = %+v, %v; want completed with no content", arv, err)
	}
	if _, err := a.ws.Discard(ctx, sid); err != nil {
		t.Fatal(err)
	}
	arv, err = a.req.Show(ctx, reqID, "")
	if err != nil || arv.Result != nil || arv.Note != "session cancelled" {
		t.Fatalf("A's record after discard = %+v, %v; want note \"session cancelled\"", arv, err)
	}
}

// Review 69b F2: a stale request.complete (lower seq than the record's) on a
// closed session with other content changes nothing and audits no mismatch.
func TestStaleCompleteNoMismatchAudit(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := setupAcceptedSession(t)
	r1 := &Result{Status: request.ResultPass, Summary: "reviewed result R1", Verification: VerificationNone}
	submitAndDeliverResult(t, a, b, reqID, r1)
	if _, err := a.ws.AcceptResult(ctx, sid); err != nil {
		t.Fatal(err)
	}
	deliverState(t, a, b)
	if err := deliver(t, a, testB, b.ob.last(t, request.KindComplete)); err != nil {
		t.Fatal(err)
	}
	seq := outSeq(t, a, reqID)
	before, err := a.req.Show(ctx, reqID, "")
	if err != nil {
		t.Fatal(err)
	}
	injectComplete(t, a, reqID, seq-1, "", "stale R2")
	after, err := a.req.Show(ctx, reqID, "")
	if err != nil || after.Result == nil || after.Result.Summary != before.Result.Summary || outSeq(t, a, reqID) != seq {
		t.Fatalf("record changed by a stale complete: before %+v after %+v, %v", before.Result, after.Result, err)
	}
	if auditWith(a, "ws.ignored", "result_mismatch") {
		t.Fatalf("a stale complete was audited as a mismatch: %v", a.audit.entries)
	}
}

// Review 78 S1 (owner decision): after a Phase 2 exchange in this session
// (B's ws.result reviewed, changes requested, round 2 open), a modified B's
// request.complete{R-evil} closes the session cancelled but its content is
// not stored: A's record holds "session cancelled" and no result, and the
// drop is audited early_complete.
func TestEarlyCompleteAfterRound1StoresAView(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	if _, err := a.ws.RequestChanges(ctx, sid, "please redo"); err != nil {
		t.Fatal(err)
	}
	injectComplete(t, a, reqID, outSeq(t, a, reqID)+1, "", "never reviewed R-evil")
	if av, err := a.ws.Get(ctx, sid); err != nil || av.State != StateClosed || av.Outcome != OutcomeCancelled {
		t.Fatalf("A's session = %+v, %v; want closed cancelled", av, err)
	}
	arv, err := a.req.Show(ctx, reqID, "")
	if err != nil || arv.State != "completed" || arv.Result != nil || arv.Note != "session cancelled" {
		t.Fatalf("A's record = %+v (result %+v), %v; want no result and note \"session cancelled\"", arv, arv.Result, err)
	}
	if !auditWith(a, "ws.ignored", `"reason":"early_complete"`) {
		t.Fatalf("no early_complete audit: %v", a.audit.entries)
	}
	var signed sql.NullString
	if err := a.db.QueryRow(`SELECT signed FROM mail_inbox WHERE kind = 'request.complete'`).Scan(&signed); err != nil || (signed.Valid && signed.String != "") {
		t.Fatalf("inbox copy = %v, %v; want blank", signed, err)
	}
}
