package request

// Review 20 (Docs/review/20-1.6a-review.md): regression tests for the fixes,
// and 1.6a acceptance items (Docs/review/11-phase1-tickets.md §1.6a) that had
// no test.

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

func inState(t *testing.T, s *Store, id string) (string, int) {
	t.Helper()
	var st string
	var seq int
	if err := s.DB.QueryRow(`SELECT state, state_seq FROM requests WHERE direction='in' AND id=?`, id).Scan(&st, &seq); err != nil {
		t.Fatal(err)
	}
	return st, seq
}

func outRow(t *testing.T, s *Store, id string) (state string, seq int, cancel, result sql.NullString) {
	t.Helper()
	if err := s.DB.QueryRow(`SELECT state, state_seq, cancel, result FROM requests WHERE direction='out' AND id=?`, id).
		Scan(&state, &seq, &cancel, &result); err != nil {
		t.Fatal(err)
	}
	return state, seq, cancel, result
}

// TestCompleteSizeCapUsesRealSeq: the total cap is checked on the body that
// is sent, whose seq grows with every defer. Before the fix the cap was
// checked with seq 1, so a body at the limit with seq 1 but seq 12 on the
// wire (65537 bytes) was sent, and the sender mirror rejected it (and every
// echo of it) as bad_body forever. Also: the 65536-byte body seals into one
// mail under MaxMailPlaintext.
func TestCompleteSizeCapUsesRealSeq(t *testing.T) {
	ctx := context.Background()
	b, _, _ := newTestStore(t, testTo, &policy{})
	id := storePendingIn(t, b)
	for i := 0; i < 10; i++ {
		if _, err := b.Defer(ctx, id, testFrom, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Accept(ctx, id, testFrom); err != nil {
		t.Fatal(err)
	}
	if _, seq := inState(t, b, id); seq != 11 {
		t.Fatalf("seq = %d, want 11", seq)
	}

	// A result whose complete body is exactly 65536 bytes with seq 1 (one
	// digit), so 65537 with the real seq 12.
	mk := func(quotes int) *Result {
		return &Result{Status: ResultFail, Output: strings.Repeat(`"`, quotes) + asciiFill(32768-quotes)}
	}
	size := func(r *Result, seq int) int {
		canon, err := completeBodyCanonical(id, seq, time.Now(), "", r)
		if err != nil {
			t.Fatal(err)
		}
		return len(canon)
	}
	q := MaxCompleteBody - size(mk(0), 1)
	if got := size(mk(q), 1); got != MaxCompleteBody {
		t.Fatalf("seq-1 body = %d bytes, want %d", got, MaxCompleteBody)
	}
	var tl *TooLargeCompleteError
	if _, err := b.Complete(ctx, id, testFrom, "", mk(q)); !errors.As(err, &tl) || tl.Size != MaxCompleteBody+1 {
		t.Fatalf("Complete at 65536 (seq 1) / 65537 (seq 12): err = %v, want TooLargeCompleteError of 65537", err)
	}
	if st, _ := inState(t, b, id); st != StateAccepted {
		t.Fatalf("state after refused complete = %s, want accepted", st)
	}

	// One byte less is exactly 65536 on the wire: accepted, and the sent body
	// is that size and seals into one mail.
	if _, err := b.Complete(ctx, id, testFrom, "", mk(q-1)); err != nil {
		t.Fatalf("Complete at exactly 65536 bytes: %v", err)
	}
	var lastReply string
	if err := b.DB.QueryRow(`SELECT last_reply FROM requests WHERE direction='in' AND id=?`, id).Scan(&lastReply); err != nil {
		t.Fatal(err)
	}
	lr, err := agentcard.ParseStrict([]byte(lastReply))
	if err != nil {
		t.Fatal(err)
	}
	sentBody := lr.(map[string]any)["body"].(map[string]any)
	canon, err := agentcard.CanonicalValue(sentBody)
	if err != nil {
		t.Fatal(err)
	}
	if len(canon) != MaxCompleteBody {
		t.Fatalf("sent complete body = %d bytes, want %d", len(canon), MaxCompleteBody)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mbox, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := mail.Seal(mail.SealInput{
		Priv: priv, To: testFrom, MailboxPub: mbox.PublicKey().Bytes(),
		Kind: KindComplete, Body: sentBody, Created: time.Now(),
	})
	if err != nil {
		t.Fatalf("seal maximal complete: %v", err)
	}
	if len(sealed.Signed) > mail.MaxMailPlaintext {
		t.Fatalf("signed plaintext = %d, over MaxMailPlaintext %d", len(sealed.Signed), mail.MaxMailPlaintext)
	}

	// The sender mirror accepts that body.
	a, _, _ := newTestStore(t, testFrom, &policy{})
	out, err := a.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	sentBody["request"] = out.Request.ID // same length id
	if err := deliverMirror(t, a, KindComplete, testTo, time.Now(), sentBody); err != nil {
		t.Fatalf("mirror of a 65536-byte complete: %v", err)
	}
}

// TestEmptyOptionalMembersRejected: optional members are absent, never
// empty (Docs/protocol/request.md §Result payload (D14), §Lifecycle Kinds).
func TestEmptyOptionalMembersRejected(t *testing.T) {
	for _, m := range []string{"summary", "output"} {
		if _, err := DecodeResult(map[string]any{"status": "pass", m: ""}); err == nil {
			t.Errorf("empty %s accepted", m)
		}
	}

	a, _, _ := newTestStore(t, testFrom, &policy{})
	out, err := a.Submit(context.Background(), submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cases := []struct {
		kind string
		body map[string]any
	}{
		{KindComplete, map[string]any{"at": wireTime(now), "note": "", "request": out.Request.ID, "seq": 2}},
		{KindComplete, map[string]any{"at": wireTime(now), "request": out.Request.ID, "result": map[string]any{"status": "pass", "summary": ""}, "seq": 2}},
		{KindDecline, map[string]any{"at": wireTime(now), "code": "not_team_member", "reason": "", "request": out.Request.ID, "seq": 1}},
	}
	for _, tc := range cases {
		if err := deliverMirror(t, a, tc.kind, testTo, now, tc.body); !errors.Is(err, mail.ErrBadBody) {
			t.Errorf("%s %v: err = %v, want bad_body", tc.kind, tc.body, err)
		}
	}
	if st, _, _, _ := outRow(t, a, out.Request.ID); st != StatePending {
		t.Fatalf("out state = %s, want pending (unchanged)", st)
	}

	b, _, _ := newTestStore(t, testTo, &policy{})
	id := storePendingIn(t, b)
	body := map[string]any{"at": wireTime(now), "reason": "", "request": id}
	if err := deliverMirror(t, b, KindCancel, testFrom, now, body); !errors.Is(err, mail.ErrBadBody) {
		t.Errorf("cancel with empty reason: err = %v, want bad_body", err)
	}
}

// TestLifecycleRefusedMatrix: every (state, action) pair outside the state
// machine is bad_state and changes nothing (Docs/protocol/request.md §State
// machine).
func TestLifecycleRefusedMatrix(t *testing.T) {
	ctx := context.Background()
	reach := map[string]func(*testing.T, *Store, string) error{
		StatePending: func(*testing.T, *Store, string) error { return nil },
		StateAccepted: func(_ *testing.T, s *Store, id string) error {
			_, err := s.Accept(ctx, id, testFrom)
			return err
		},
		StateDeclined: func(_ *testing.T, s *Store, id string) error {
			_, err := s.Decline(ctx, id, testFrom, "no")
			return err
		},
		StateDeferred: func(_ *testing.T, s *Store, id string) error {
			_, err := s.Defer(ctx, id, testFrom, time.Now().Add(time.Hour))
			return err
		},
		StateCompleted: func(_ *testing.T, s *Store, id string) error {
			if _, err := s.Accept(ctx, id, testFrom); err != nil {
				return err
			}
			_, err := s.Complete(ctx, id, testFrom, "", nil)
			return err
		},
		StateCancelled: func(t *testing.T, s *Store, id string) error {
			now := time.Now()
			return deliverMirror(t, s, KindCancel, testFrom, now, map[string]any{"at": wireTime(now), "request": id})
		},
	}
	actions := map[string]func(*Store, string) error{
		"accept": func(s *Store, id string) error {
			_, err := s.Accept(ctx, id, testFrom)
			return err
		},
		"decline": func(s *Store, id string) error {
			_, err := s.Decline(ctx, id, testFrom, "no")
			return err
		},
		"defer": func(s *Store, id string) error {
			_, err := s.Defer(ctx, id, testFrom, time.Now().Add(2*time.Hour))
			return err
		},
		"complete": func(s *Store, id string) error {
			_, err := s.Complete(ctx, id, testFrom, "", nil)
			return err
		},
	}
	allowed := map[string]map[string]bool{
		StatePending:  {"accept": true, "decline": true, "defer": true},
		StateDeferred: {"accept": true, "decline": true, "defer": true},
		StateAccepted: {"complete": true},
	}
	for state, reachFn := range reach {
		for action, act := range actions {
			if allowed[state][action] {
				continue
			}
			t.Run(action+" from "+state, func(t *testing.T) {
				s, _, _ := newTestStore(t, testTo, &policy{})
				id := storePendingIn(t, s)
				if err := reachFn(t, s, id); err != nil {
					t.Fatal(err)
				}
				_, seqBefore := inState(t, s, id)
				var bse *BadStateError
				if err := act(s, id); !errors.As(err, &bse) || bse.State != state {
					t.Fatalf("err = %v, want bad_state naming %s", err, state)
				}
				if st, seq := inState(t, s, id); st != state || seq != seqBefore {
					t.Fatalf("row = %s seq %d, want unchanged %s seq %d", st, seq, state, seqBefore)
				}
			})
		}
	}
}

// TestResendAfterExpiredIdempotent is the D10 acceptance test at unit level:
// the sender resends after a forced expired; the recipient sees a duplicate
// and re-sends the last answer; the sender mirror applies it once.
func TestResendAfterExpiredIdempotent(t *testing.T) {
	ctx := context.Background()
	a, _, _ := newTestStore(t, testFrom, &policy{})
	out, err := a.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	id := out.Request.ID
	var bse *BadStateError
	if _, err := a.Resend(ctx, id); !errors.As(err, &bse) {
		t.Fatalf("resend while queued: err = %v, want bad_state", err)
	}
	if _, err := a.DB.Exec(`UPDATE outbox SET state = 'expired' WHERE id = ?`, out.MailID); err != nil {
		t.Fatal(err)
	}
	res, err := a.Resend(ctx, id)
	if err != nil || res.MailID == "" || res.MailID == out.MailID {
		t.Fatalf("Resend = %+v, %v", res, err)
	}

	// The recipient: first copy stored and accepted, then the resent copy (a
	// new mail) arrives over 10 minutes later.
	var bodyText string
	if err := a.DB.QueryRow(`SELECT body FROM requests WHERE direction='out' AND id=?`, id).Scan(&bodyText); err != nil {
		t.Fatal(err)
	}
	obj, err := parseStoredBodyObj(bodyText)
	if err != nil {
		t.Fatal(err)
	}
	req, err := Decode(obj)
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := newTestStore(t, testTo, &policy{})
	base := time.Now()
	b.Now = func() time.Time { return base }
	if err := deliverRequest(t, b, req, base); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Accept(ctx, id, testFrom); err != nil {
		t.Fatal(err)
	}
	b.Now = func() time.Time { return base.Add(11 * time.Minute) }
	if err := deliverRequest(t, b, req, base.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, b.DB, `SELECT COUNT(*) FROM outbox WHERE kind = ?`, KindAccept); n != 2 {
		t.Fatalf("request.accept mails = %d, want 2 (the answer re-sent)", n)
	}
	if n := countRows(t, b.DB, `SELECT COUNT(*) FROM requests WHERE direction='in'`); n != 1 {
		t.Fatalf("in rows = %d, want 1", n)
	}

	// The mirror: both copies of the accept arrive; the second is ignored.
	acc := map[string]any{"at": wireTime(base), "request": id, "seq": 1}
	for i := 0; i < 2; i++ {
		if err := deliverMirror(t, a, KindAccept, testTo, base, acc); err != nil {
			t.Fatal(err)
		}
	}
	if st, seq, _, _ := outRow(t, a, id); st != StateAccepted || seq != 1 {
		t.Fatalf("out row = %s seq %d, want accepted seq 1", st, seq)
	}
	if _, err := a.Resend(ctx, id); !errors.As(err, &bse) {
		t.Fatalf("resend after accept: err = %v, want bad_state", err)
	}
}

// TestResendTooOld: a request 21 d old cannot be resent.
func TestResendTooOld(t *testing.T) {
	ctx := context.Background()
	a, _, _ := newTestStore(t, testFrom, &policy{})
	out, err := a.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`UPDATE outbox SET state = 'failed' WHERE id = ?`, out.MailID); err != nil {
		t.Fatal(err)
	}
	a.Now = func() time.Time { return time.Now().Add(21 * 24 * time.Hour) }
	var bse *BadStateError
	if _, err := a.Resend(ctx, out.Request.ID); !errors.As(err, &bse) {
		t.Fatalf("resend at 21 d: err = %v, want bad_state", err)
	}
	a.Now = func() time.Time { return time.Now().Add(20 * 24 * time.Hour) }
	if _, err := a.Resend(ctx, out.Request.ID); err != nil {
		t.Fatalf("resend at 20 d: %v", err)
	}
}

// TestCancelIdempotency: a second cancel while in flight is a duplicate with
// one mail; after the cancel mail expires a new one is sent; after cancelled
// it is a duplicate. On the recipient a duplicate cancel changes nothing, and
// accept of a cancelled request is bad_state.
func TestCancelIdempotency(t *testing.T) {
	ctx := context.Background()
	a, _, _ := newTestStore(t, testFrom, &policy{})
	out, err := a.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	id := out.Request.ID
	first, err := a.Cancel(ctx, id, "")
	if err != nil || first.Duplicate || first.MailID == "" {
		t.Fatalf("first cancel = %+v, %v", first, err)
	}
	second, err := a.Cancel(ctx, id, "")
	if err != nil || !second.Duplicate || second.MailID != "" {
		t.Fatalf("second cancel = %+v, %v; want duplicate, nothing sent", second, err)
	}
	if n := countRows(t, a.DB, `SELECT COUNT(*) FROM outbox WHERE kind = ?`, KindCancel); n != 1 {
		t.Fatalf("cancel mails = %d, want 1", n)
	}
	if _, err := a.DB.Exec(`UPDATE outbox SET state = 'expired' WHERE id = ?`, first.MailID); err != nil {
		t.Fatal(err)
	}
	third, err := a.Cancel(ctx, id, "")
	if err != nil || third.Duplicate || third.MailID == "" || third.MailID == first.MailID {
		t.Fatalf("cancel after expired = %+v, %v; want a new mail", third, err)
	}
	now := time.Now()
	if err := deliverMirror(t, a, KindCancelled, testTo, now, map[string]any{"at": wireTime(now), "request": id, "seq": 1}); err != nil {
		t.Fatal(err)
	}
	fourth, err := a.Cancel(ctx, id, "")
	if err != nil || !fourth.Duplicate || fourth.View.State != StateCancelled {
		t.Fatalf("cancel after cancelled = %+v, %v", fourth, err)
	}

	b, _, _ := newTestStore(t, testTo, &policy{})
	bid := storePendingIn(t, b)
	cancel := map[string]any{"at": wireTime(now), "request": bid}
	for i := 0; i < 2; i++ {
		if err := deliverMirror(t, b, KindCancel, testFrom, now, cancel); err != nil {
			t.Fatal(err)
		}
	}
	if st, seq := inState(t, b, bid); st != StateCancelled || seq != 1 {
		t.Fatalf("in row = %s seq %d, want cancelled seq 1", st, seq)
	}
	if n := countRows(t, b.DB, `SELECT COUNT(*) FROM outbox WHERE kind = ?`, KindCancelled); n != 1 {
		t.Fatalf("cancelled mails = %d, want 1 (duplicate within 10 min sends nothing)", n)
	}
	var bse *BadStateError
	if _, err := b.Accept(ctx, bid, testFrom); !errors.As(err, &bse) {
		t.Fatalf("accept of cancelled: err = %v, want bad_state", err)
	}
}

// TestTombstonePrunedAfter31Days: tombstones older than 31 d are pruned (fake
// clock), and a tombstone is per sender: another peer's cancel for the same
// id does not pre-cancel it.
func TestTombstonePrunedAfter31Days(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	base := time.Now().UTC().Truncate(time.Second)
	s.Now = func() time.Time { return base }
	old := NewID()
	if err := deliverMirror(t, s, KindCancel, testFrom, base, map[string]any{"at": wireTime(base), "request": old}); err != nil {
		t.Fatal(err)
	}
	later := base.Add(31*24*time.Hour + time.Minute)
	s.Now = func() time.Time { return later }
	fresh := NewID()
	if err := deliverMirror(t, s, KindCancel, testFrom, later, map[string]any{"at": wireTime(later), "request": fresh}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM request_cancels WHERE id = ?`, old); n != 0 {
		t.Errorf("31 d old tombstone still present")
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM request_cancels WHERE id = ?`, fresh); n != 1 {
		t.Errorf("fresh tombstone missing")
	}

	// A third party's cancel for testID leaves its own tombstone only: the
	// real sender's request with that id is stored pending.
	s2, _, _ := newTestStore(t, testTo, &policy{})
	now := time.Now()
	if err := deliverMirror(t, s2, KindCancel, testKey(3), now, map[string]any{"at": wireTime(now), "request": testID}); err != nil {
		t.Fatal(err)
	}
	if err := deliverRequest(t, s2, receivedRequest(), now); err != nil {
		t.Fatal(err)
	}
	if st, _ := inState(t, s2, testID); st != StatePending {
		t.Fatalf("request after a third party's cancel = %s, want pending", st)
	}
}

// TestRefusedCancelEchoKeepsResult: a cancel refused after completion is
// answered by the stored complete, with the same result; the mirror ignores
// it by seq, sets cancel = refused and keeps its result.
func TestRefusedCancelEchoKeepsResult(t *testing.T) {
	ctx := context.Background()
	b, _, _ := newTestStore(t, testTo, &policy{})
	base := time.Now()
	b.Now = func() time.Time { return base }
	id := storePendingIn(t, b)
	if _, err := b.Accept(ctx, id, testFrom); err != nil {
		t.Fatal(err)
	}
	result := &Result{Status: ResultPass, Summary: "all green", Output: "ok\n"}
	if _, err := b.Complete(ctx, id, testFrom, "", result); err != nil {
		t.Fatal(err)
	}
	b.Now = func() time.Time { return base.Add(11 * time.Minute) }
	at := base.Add(11 * time.Minute)
	if err := deliverMirror(t, b, KindCancel, testFrom, at, map[string]any{"at": wireTime(at), "request": id}); err != nil {
		t.Fatal(err)
	}
	rows, err := b.DB.Query(`SELECT signed FROM outbox WHERE kind = ?`, KindComplete)
	if err != nil {
		t.Fatal(err)
	}
	var signed []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		signed = append(signed, s)
	}
	_ = rows.Close()
	if len(signed) != 2 || signed[0] == signed[1] {
		t.Fatalf("complete mails = %d, want the original and an echo", len(signed))
	}
	for _, s := range signed {
		if !strings.Contains(s, `"summary":"all green"`) {
			t.Fatalf("complete mail without the result: %s", s)
		}
	}

	a, _, _ := newTestStore(t, testFrom, &policy{})
	out, err := a.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	aid := out.Request.ID
	if _, err := a.Cancel(ctx, aid, ""); err != nil {
		t.Fatal(err)
	}
	complete := map[string]any{"at": wireTime(base), "request": aid, "seq": 2, "result": resultWire(result)}
	if err := deliverMirror(t, a, KindComplete, testTo, base, complete); err != nil {
		t.Fatal(err)
	}
	_, _, _, before := outRow(t, a, aid)
	echo := map[string]any{"at": wireTime(base), "request": aid, "seq": 2, "result": resultWire(&Result{Status: ResultFail})}
	if err := deliverMirror(t, a, KindComplete, testTo, base, echo); err != nil {
		t.Fatal(err)
	}
	st, seq, cancel, after := outRow(t, a, aid)
	if st != StateCompleted || seq != 2 || cancel.String != "refused" || after != before {
		t.Fatalf("out row = %s seq %d cancel %v result %v, want completed seq 2 refused result %v", st, seq, cancel, after, before)
	}
}

// TestAuditHasNoResultContent: no audit entry carries the summary, output,
// artifacts, note, status or exit code, on either side.
func TestAuditHasNoResultContent(t *testing.T) {
	ctx := context.Background()
	const mSummary, mOutput, mPath, mNote = "MARKSUMMARY", "MARKOUTPUT", "MARKPATH", "MARKNOTE"
	exit := int64(7)
	result := &Result{
		Status: ResultPartial, Summary: mSummary, ExitCode: &exit, Output: mOutput + "\n",
		Artifacts: []Artifact{{Path: mPath}},
	}
	b, _, bAudit := newTestStore(t, testTo, &policy{})
	id := storePendingIn(t, b)
	if _, err := b.Accept(ctx, id, testFrom); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Complete(ctx, id, testFrom, mNote, result); err != nil {
		t.Fatal(err)
	}
	a, _, aAudit := newTestStore(t, testFrom, &policy{})
	out, err := a.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	body := map[string]any{"at": wireTime(now), "note": mNote, "request": out.Request.ID, "seq": 2, "result": resultWire(result)}
	if err := deliverMirror(t, a, KindComplete, testTo, now, body); err != nil {
		t.Fatal(err)
	}
	sawSizes := 0
	for _, al := range []*fakeAudit{aAudit, bAudit} {
		for _, e := range al.entries {
			for _, bad := range []string{mSummary, mOutput, mPath, mNote, `"status"`, `"exit_code"`, ResultPartial} {
				if strings.Contains(e.detail, bad) {
					t.Errorf("audit %s detail %s contains %s", e.action, e.detail, bad)
				}
			}
			if strings.Contains(e.detail, `"result_bytes"`) && strings.Contains(e.detail, `"output_bytes"`) && strings.Contains(e.detail, `"artifacts":1`) {
				sawSizes++
			}
		}
	}
	if sawSizes != 2 {
		t.Errorf("audit entries with result sizes = %d, want 2 (request.complete and request.state)", sawSizes)
	}
}
