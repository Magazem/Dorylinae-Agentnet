package request

// Ticket 1.6a unit acceptance (Docs/review/11-phase1-tickets.md §1.6a,
// Docs/protocol/request.md §Lifecycle, §Cancel (OD-P1-11), §Result payload
// (D14)). Uses the newTestStore/deliverRequest/countRows helpers of
// store_test.go (1.4c).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// deliverMirror simulates a lifecycle mail (kind is one of the five
// recipient->sender kinds, or "request.cancel" sender->recipient) arriving at
// s, exactly as the real mail receiver would run it: Apply inside a
// transaction, then After on commit.
func deliverMirror(t *testing.T, s *Store, kind, from string, created time.Time, rawBody map[string]any) error {
	t.Helper()
	ctx := context.Background()
	b, err := json.Marshal(rawBody)
	if err != nil {
		t.Fatal(err)
	}
	v, err := agentcard.ParseStrict(b)
	if err != nil {
		t.Fatal(err)
	}
	body := v.(map[string]any)
	op := &mail.Opened{Msg: mail.Msg{V: 1, ID: "m-" + NewID()[2:], From: from, To: s.Self, Created: created, Kind: kind, Body: body}}

	var applyFn func(context.Context, *sql.Tx, *mail.Opened) error
	var afterFn func(context.Context, *mail.Opened)
	switch kind {
	case KindAccept:
		applyFn, afterFn = s.applyAccept, s.afterMirror
	case KindDecline:
		applyFn, afterFn = s.applyDecline, s.afterMirror
	case KindDefer:
		applyFn, afterFn = s.applyDefer, s.afterMirror
	case KindComplete:
		applyFn, afterFn = s.applyComplete, s.afterMirror
	case KindCancelled:
		applyFn, afterFn = s.applyCancelled, s.afterMirror
	case KindCancel:
		applyFn, afterFn = s.applyCancel, s.afterCancel
	default:
		t.Fatalf("deliverMirror: unknown kind %q", kind)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyFn(ctx, tx, op); err != nil {
		_ = tx.Rollback()
		pendingMirror.Delete(op)
		pendingCancel.Delete(op)
		return err
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	afterFn(ctx, op)
	return nil
}

// storePendingIn stores a pending `in` row from testFrom and returns its id.
func storePendingIn(t *testing.T, s *Store) string {
	t.Helper()
	req := receivedRequest()
	if err := deliverRequest(t, s, req, time.Now()); err != nil {
		t.Fatal(err)
	}
	return req.ID
}

// TestLifecycleAllowedTransitions is the 1.6a state-machine acceptance test:
// every allowed transition succeeds and every disallowed one is bad_state
// (Docs/protocol/request.md §State machine).
func TestLifecycleAllowedTransitions(t *testing.T) {
	t.Run("accept from pending", func(t *testing.T) {
		s, _, _ := newTestStore(t, testTo, &policy{})
		id := storePendingIn(t, s)
		v, err := s.Accept(context.Background(), id, testFrom)
		if err != nil || v.State != StateAccepted {
			t.Fatalf("Accept = %+v, %v", v, err)
		}
	})
	t.Run("decline from pending", func(t *testing.T) {
		s, _, _ := newTestStore(t, testTo, &policy{})
		id := storePendingIn(t, s)
		v, err := s.Decline(context.Background(), id, testFrom, "not now")
		if err != nil || v.State != StateDeclined || v.DeclineCode != "user" || v.Reason != "not now" {
			t.Fatalf("Decline = %+v, %v", v, err)
		}
	})
	t.Run("defer then accept", func(t *testing.T) {
		s, _, _ := newTestStore(t, testTo, &policy{})
		id := storePendingIn(t, s)
		until := time.Now().Add(48 * time.Hour)
		v, err := s.Defer(context.Background(), id, testFrom, until)
		if err != nil || v.State != StateDeferred {
			t.Fatalf("Defer = %+v, %v", v, err)
		}
		v, err = s.Accept(context.Background(), id, testFrom)
		if err != nil || v.State != StateAccepted {
			t.Fatalf("Accept after defer = %+v, %v", v, err)
		}
	})
	t.Run("accept then complete", func(t *testing.T) {
		s, _, _ := newTestStore(t, testTo, &policy{})
		id := storePendingIn(t, s)
		if _, err := s.Accept(context.Background(), id, testFrom); err != nil {
			t.Fatal(err)
		}
		v, err := s.Complete(context.Background(), id, testFrom, "done", nil)
		if err != nil || v.State != StateCompleted {
			t.Fatalf("Complete = %+v, %v", v, err)
		}
	})
	t.Run("first_response set once", func(t *testing.T) {
		s, _, _ := newTestStore(t, testTo, &policy{})
		id := storePendingIn(t, s)
		if _, err := s.Defer(context.Background(), id, testFrom, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		var fr string
		if err := s.DB.QueryRow(`SELECT first_response FROM requests WHERE direction='in' AND id=?`, id).Scan(&fr); err != nil {
			t.Fatal(err)
		}
		if fr != "defer" {
			t.Fatalf("first_response = %q, want defer", fr)
		}
		if _, err := s.Accept(context.Background(), id, testFrom); err != nil {
			t.Fatal(err)
		}
		if err := s.DB.QueryRow(`SELECT first_response FROM requests WHERE direction='in' AND id=?`, id).Scan(&fr); err != nil {
			t.Fatal(err)
		}
		if fr != "defer" {
			t.Fatalf("first_response changed to %q, want it to stay defer", fr)
		}
	})
}

// TestLifecycleRefusedTransitions: every disallowed transition is bad_state.
func TestLifecycleRefusedTransitions(t *testing.T) {
	newAccepted := func(t *testing.T) (*Store, string) {
		s, _, _ := newTestStore(t, testTo, &policy{})
		id := storePendingIn(t, s)
		if _, err := s.Accept(context.Background(), id, testFrom); err != nil {
			t.Fatal(err)
		}
		return s, id
	}
	newDeclined := func(t *testing.T) (*Store, string) {
		s, _, _ := newTestStore(t, testTo, &policy{})
		id := storePendingIn(t, s)
		if _, err := s.Decline(context.Background(), id, testFrom, "no"); err != nil {
			t.Fatal(err)
		}
		return s, id
	}
	newCompleted := func(t *testing.T) (*Store, string) {
		s, id := newAccepted(t)
		if _, err := s.Complete(context.Background(), id, testFrom, "", nil); err != nil {
			t.Fatal(err)
		}
		return s, id
	}

	var bse *BadStateError
	t.Run("accept an accepted request", func(t *testing.T) {
		s, id := newAccepted(t)
		if _, err := s.Accept(context.Background(), id, testFrom); !errors.As(err, &bse) {
			t.Fatalf("err = %v, want BadStateError", err)
		}
	})
	t.Run("decline a declined request", func(t *testing.T) {
		s, id := newDeclined(t)
		if _, err := s.Decline(context.Background(), id, testFrom, "again"); !errors.As(err, &bse) {
			t.Fatalf("err = %v, want BadStateError", err)
		}
	})
	t.Run("complete a pending request", func(t *testing.T) {
		s, _, _ := newTestStore(t, testTo, &policy{})
		id := storePendingIn(t, s)
		if _, err := s.Complete(context.Background(), id, testFrom, "", nil); !errors.As(err, &bse) {
			t.Fatalf("err = %v, want BadStateError", err)
		}
	})
	t.Run("complete a completed request", func(t *testing.T) {
		s, id := newCompleted(t)
		if _, err := s.Complete(context.Background(), id, testFrom, "again", nil); !errors.As(err, &bse) {
			t.Fatalf("err = %v, want BadStateError", err)
		}
	})
	t.Run("defer a declined request", func(t *testing.T) {
		s, id := newDeclined(t)
		if _, err := s.Defer(context.Background(), id, testFrom, time.Now().Add(time.Hour)); !errors.As(err, &bse) {
			t.Fatalf("err = %v, want BadStateError", err)
		}
	})
}

// TestSenderMirrorOutOfOrderSeq: a complete (seq 2) that overtakes its accept
// (seq 1, never delivered) still ends the out row completed
// (Docs/protocol/request.md §Sender mirror step 4).
func TestSenderMirrorOutOfOrderSeq(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	ctx := context.Background()
	out, err := s.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	id := out.Request.ID
	now := time.Now()
	completeBody := map[string]any{"at": wireTime(now), "request": id, "seq": 2}
	if err := deliverMirror(t, s, KindComplete, testTo, now, completeBody); err != nil {
		t.Fatal(err)
	}
	var state string
	var seq int
	if err := s.DB.QueryRow(`SELECT state, state_seq FROM requests WHERE direction='out' AND id=?`, id).Scan(&state, &seq); err != nil {
		t.Fatal(err)
	}
	if state != StateCompleted || seq != 2 {
		t.Fatalf("out row = %s seq %d, want completed seq 2", state, seq)
	}
	// A late, lower-seq accept is now ignored.
	acceptBody := map[string]any{"at": wireTime(now), "request": id, "seq": 1}
	if err := deliverMirror(t, s, KindAccept, testTo, now, acceptBody); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT state, state_seq FROM requests WHERE direction='out' AND id=?`, id).Scan(&state, &seq); err != nil {
		t.Fatal(err)
	}
	if state != StateCompleted || seq != 2 {
		t.Fatalf("out row after late accept = %s seq %d, want completed seq 2 (unchanged)", state, seq)
	}
}

// TestSenderMirrorOrphan: a lifecycle mail for an unknown out row is acked
// and ignored, and audited request.orphan.
func TestSenderMirrorOrphan(t *testing.T) {
	s, _, al := newTestStore(t, testFrom, &policy{})
	now := time.Now()
	body := map[string]any{"at": wireTime(now), "request": NewID(), "seq": 1}
	if err := deliverMirror(t, s, KindAccept, testTo, now, body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range al.actions() {
		if a == "request.orphan" {
			found = true
		}
	}
	if !found {
		t.Errorf("audit = %v, want request.orphan", al.actions())
	}
}

// --- Cancel (OD-P1-11) ---

// TestCancelPendingAndDeferredUnit: cancel of a pending and a deferred
// request ends the recipient's row cancelled with a reply.
func TestCancelPendingAndDeferredUnit(t *testing.T) {
	for _, state := range []string{"pending", "deferred"} {
		t.Run(state, func(t *testing.T) {
			s, _, al := newTestStore(t, testTo, &policy{})
			id := storePendingIn(t, s)
			if state == "deferred" {
				if _, err := s.Defer(context.Background(), id, testFrom, time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			al.entries = nil
			now := time.Now()
			body := map[string]any{"at": wireTime(now), "reason": "no longer needed", "request": id}
			if err := deliverMirror(t, s, KindCancel, testFrom, now, body); err != nil {
				t.Fatal(err)
			}
			var st, reason string
			if err := s.DB.QueryRow(`SELECT state, reason FROM requests WHERE direction='in' AND id=?`, id).Scan(&st, &reason); err != nil {
				t.Fatal(err)
			}
			if st != StateCancelled || reason != "no longer needed" {
				t.Fatalf("row = %s %q, want cancelled %q", st, reason, "no longer needed")
			}
			if n := countRows(t, s.DB, `SELECT COUNT(*) FROM outbox WHERE kind = 'request.cancelled'`); n != 1 {
				t.Errorf("request.cancelled mails = %d, want 1", n)
			}
			foundCancelled := false
			for _, a := range al.actions() {
				if a == "request.cancel_in" {
					foundCancelled = true
				}
			}
			if !foundCancelled {
				t.Errorf("audit = %v, want request.cancel_in", al.actions())
			}
		})
	}
}

// TestCancelRefusedAfterAcceptUnit: a cancel that arrives after the recipient
// already accepted is refused; the sender mirror's cancel column becomes
// refused once it sees the accepted state (Docs/protocol/request.md §Cancel
// (OD-P1-11), the race bullet).
func TestCancelRefusedAfterAcceptUnit(t *testing.T) {
	// Recipient side: cancel arrives after accept -> refused, unchanged state.
	s, _, al := newTestStore(t, testTo, &policy{})
	id := storePendingIn(t, s)
	if _, err := s.Accept(context.Background(), id, testFrom); err != nil {
		t.Fatal(err)
	}
	al.entries = nil
	now := time.Now()
	body := map[string]any{"at": wireTime(now), "request": id}
	if err := deliverMirror(t, s, KindCancel, testFrom, now, body); err != nil {
		t.Fatal(err)
	}
	var st string
	if err := s.DB.QueryRow(`SELECT state FROM requests WHERE direction='in' AND id=?`, id).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != StateAccepted {
		t.Fatalf("state = %s, want accepted (cancel refused)", st)
	}

	// Sender side: the out row sees accept arrive after it already asked to
	// cancel -> cancel becomes refused, state stays accepted.
	sender, _, senderAudit := newTestStore(t, testFrom, &policy{})
	ctx := context.Background()
	out, err := sender.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	// Fake the peer as paired so Cancel can seal a mail (Submit already did).
	if _, err := sender.Cancel(ctx, out.Request.ID, ""); err != nil {
		t.Fatal(err)
	}
	senderAudit.entries = nil
	acceptBody := map[string]any{"at": wireTime(now), "request": out.Request.ID, "seq": 1}
	if err := deliverMirror(t, sender, KindAccept, testTo, now, acceptBody); err != nil {
		t.Fatal(err)
	}
	var outState, cancel string
	if err := sender.DB.QueryRow(`SELECT state, cancel FROM requests WHERE direction='out' AND id=?`, out.Request.ID).Scan(&outState, &cancel); err != nil {
		t.Fatal(err)
	}
	if outState != StateAccepted || cancel != "refused" {
		t.Fatalf("out row = %s cancel=%s, want accepted cancel=refused", outState, cancel)
	}
	foundRefused := false
	for _, a := range senderAudit.actions() {
		if a == "request.cancel_refused" {
			foundRefused = true
		}
	}
	if !foundRefused {
		t.Errorf("audit = %v, want request.cancel_refused", senderAudit.actions())
	}
}

// TestCancelTombstone: a cancel for an id not yet known leaves a tombstone; a
// later request with that id is stored cancelled with no separate
// notification, and the tombstone is consumed.
func TestCancelTombstone(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	now := time.Now()
	req := receivedRequest()
	body := map[string]any{"at": wireTime(now), "reason": "too early", "request": req.ID}
	if err := deliverMirror(t, s, KindCancel, testFrom, now, body); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM request_cancels WHERE peer = ? AND id = ?`, testFrom, req.ID); n != 1 {
		t.Fatalf("tombstones = %d, want 1", n)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM outbox WHERE kind = 'request.cancelled'`); n != 1 {
		t.Errorf("cancelled mails = %d, want 1", n)
	}

	if err := deliverRequest(t, s, req, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var st, reason string
	if err := s.DB.QueryRow(`SELECT state, reason FROM requests WHERE direction='in' AND id=?`, req.ID).Scan(&st, &reason); err != nil {
		t.Fatal(err)
	}
	if st != StateCancelled || reason != "too early" {
		t.Fatalf("row = %s %q, want cancelled %q", st, reason, "too early")
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM request_cancels`); n != 0 {
		t.Errorf("tombstones after consumption = %d, want 0", n)
	}
}

// TestCancelTombstoneLimit: the 1001st tombstone from one sender is ignored.
func TestCancelTombstoneLimit(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	now := time.Now()
	for i := 0; i < maxTombstones; i++ {
		if _, err := s.DB.Exec(`INSERT INTO request_cancels (peer, id, received_at) VALUES (?, ?, ?)`,
			testFrom, NewID(), storeTime(now)); err != nil {
			t.Fatal(err)
		}
	}
	body := map[string]any{"at": wireTime(now), "request": NewID()}
	if err := deliverMirror(t, s, KindCancel, testFrom, now, body); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM request_cancels WHERE peer = ?`, testFrom); n != maxTombstones {
		t.Fatalf("tombstones = %d, want %d (1001st ignored)", n, maxTombstones)
	}
}

// TestResendRefusedWithCancel: request_resend of a cancelled request, or one
// with a cancel in flight, is bad_state.
func TestResendRefusedWithCancel(t *testing.T) {
	s, ob, _ := newTestStore(t, testFrom, &policy{})
	ctx := context.Background()
	out, err := s.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(ctx, out.Request.ID, ""); err != nil {
		t.Fatal(err)
	}
	_ = ob
	var bse *BadStateError
	if _, err := s.Resend(ctx, out.Request.ID); !errors.As(err, &bse) {
		t.Fatalf("resend with cancel in flight: err = %v, want BadStateError", err)
	}
}

// --- Result payload (D14) ---

// TestValidateCompleteCaps is the D14 table test: every cap of
// ValidateComplete at its limit (accepted) and one over (rejected), plus the
// structural rules.
func TestValidateCompleteCaps(t *testing.T) {
	valid := func() *Result { return &Result{Status: ResultPass} }

	t.Run("status values", func(t *testing.T) {
		for _, s := range []string{ResultPass, ResultFail, ResultPartial, ResultNA} {
			if err := ValidateComplete("", &Result{Status: s}); err != nil {
				t.Errorf("status %q rejected: %v", s, err)
			}
		}
		if err := ValidateComplete("", &Result{Status: "unknown"}); err == nil {
			t.Error("unknown status accepted")
		}
	})
	t.Run("summary code points", func(t *testing.T) {
		r := valid()
		r.Summary = repeatRunes('a', 280)
		if err := ValidateComplete("", r); err != nil {
			t.Errorf("280 code points rejected: %v", err)
		}
		r.Summary = repeatRunes('a', 281)
		if err := ValidateComplete("", r); err == nil {
			t.Error("281 code points accepted")
		}
		r.Summary = repeatRunes('\U0001F600', 280) // four-byte code points
		if err := ValidateComplete("", r); err != nil {
			t.Errorf("280 four-byte code points rejected: %v", err)
		}
		r.Summary = "bad\x01char"
		if err := ValidateComplete("", r); err == nil {
			t.Error("control character in summary accepted")
		}
	})
	t.Run("exit_code range", func(t *testing.T) {
		for _, ec := range []int64{-2147483648, 4294967295} {
			r := valid()
			r.ExitCode = &ec
			if err := ValidateComplete("", r); err != nil {
				t.Errorf("exit_code %d rejected: %v", ec, err)
			}
		}
		for _, ec := range []int64{-2147483649, 4294967296} {
			r := valid()
			r.ExitCode = &ec
			if err := ValidateComplete("", r); err == nil {
				t.Errorf("exit_code %d accepted", ec)
			}
		}
	})
	t.Run("output bytes", func(t *testing.T) {
		r := valid()
		r.Output = asciiFill(32768)
		if err := ValidateComplete("", r); err != nil {
			t.Errorf("32768 bytes rejected: %v", err)
		}
		r.Output = asciiFill(32769)
		if err := ValidateComplete("", r); err == nil {
			t.Error("32769 bytes accepted")
		}
		r = valid()
		r.Output = "line one\nline\ttwo"
		if err := ValidateComplete("", r); err != nil {
			t.Errorf("\\n and \\t rejected: %v", err)
		}
		for _, bad := range []string{"\x1b[31m", "\r\n", "\x7f"} {
			r = valid()
			r.Output = bad
			if err := ValidateComplete("", r); err == nil {
				t.Errorf("control byte %q in output accepted", bad)
			}
		}
	})
	t.Run("artifacts count", func(t *testing.T) {
		r := valid()
		r.Artifacts = []Artifact{}
		if err := ValidateComplete("", r); err == nil {
			t.Error("empty artifacts array accepted")
		}
		r.Artifacts = []Artifact{{Path: "p"}}
		if err := ValidateComplete("", r); err != nil {
			t.Errorf("1 artifact rejected: %v", err)
		}
		many := make([]Artifact, 20)
		for i := range many {
			many[i] = Artifact{Path: "p"}
		}
		r.Artifacts = many
		if err := ValidateComplete("", r); err != nil {
			t.Errorf("20 artifacts rejected: %v", err)
		}
		r.Artifacts = append(many, Artifact{Path: "p"})
		if err := ValidateComplete("", r); err == nil {
			t.Error("21 artifacts accepted")
		}
	})
	t.Run("artifact member limits", func(t *testing.T) {
		cases := []struct {
			name string
			a    Artifact
			ok   bool
		}{
			{"url max", Artifact{URL: "https://" + asciiFill(2040)}, true},
			{"url max+1", Artifact{URL: "https://" + asciiFill(2041)}, false},
			{"branch max", Artifact{Branch: asciiFill(255)}, true},
			{"branch max+1", Artifact{Branch: asciiFill(256)}, false},
			{"commit min", Artifact{Commit: asciiFillHex(7)}, true},
			{"commit min-1", Artifact{Commit: asciiFillHex(6)}, false},
			{"commit max", Artifact{Commit: asciiFillHex(64)}, true},
			{"commit max+1", Artifact{Commit: asciiFillHex(65)}, false},
			{"path max", Artifact{Path: asciiFill(1024)}, true},
			{"path max+1", Artifact{Path: asciiFill(1025)}, false},
		}
		for _, tc := range cases {
			r := valid()
			r.Artifacts = []Artifact{tc.a}
			err := ValidateComplete("", r)
			if tc.ok && err != nil {
				t.Errorf("%s: rejected: %v", tc.name, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("%s: accepted, want rejected", tc.name)
			}
		}
	})
	t.Run("note code points", func(t *testing.T) {
		if err := ValidateComplete(repeatRunes('a', 2000), nil); err != nil {
			t.Errorf("2000 code point note rejected: %v", err)
		}
		if err := ValidateComplete(repeatRunes('a', 2001), nil); err == nil {
			t.Error("2001 code point note accepted")
		}
	})
	t.Run("unknown member rejected by DecodeResult", func(t *testing.T) {
		if _, err := DecodeResult(map[string]any{"status": "pass", "bogus": "x"}); err == nil {
			t.Error("unknown member accepted")
		}
	})
	t.Run("null optional member rejected", func(t *testing.T) {
		if _, err := DecodeResult(map[string]any{"status": "pass", "summary": nil}); err == nil {
			t.Error("null summary accepted")
		}
	})
}

func asciiFillHex(n int) string { return strings.Repeat("a", n) }

// TestCompleteResultTotalSizeCap: a maximal valid complete body (each field
// individually valid) that totals over MaxCompleteBody is result_too_large
// at IPC (TooLargeCompleteError) and mail.ErrBadBody on the sender-mirror
// path, which keeps the previous state.
func TestCompleteResultTotalSizeCap(t *testing.T) {
	result := &Result{Status: ResultFail, Output: asciiFill(32768)}
	many := make([]Artifact, 20)
	for i := range many {
		many[i] = Artifact{URL: "https://x/" + asciiFill(2030)}
	}
	result.Artifacts = many
	if err := ValidateComplete("", result); err != nil {
		t.Fatalf("fields individually valid but ValidateComplete failed: %v", err)
	}
	canon, err := completeBodyCanonical(testID, 1, time.Now(), "", result)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckCompleteSize(canon); err == nil {
		t.Fatal("oversize complete body accepted")
	}

	// Recipient IPC path.
	s, _, _ := newTestStore(t, testTo, &policy{})
	id := storePendingIn(t, s)
	if _, err := s.Accept(context.Background(), id, testFrom); err != nil {
		t.Fatal(err)
	}
	var tl *TooLargeCompleteError
	if _, err := s.Complete(context.Background(), id, testFrom, "", result); !errors.As(err, &tl) {
		t.Fatalf("Complete err = %v, want TooLargeCompleteError", err)
	}
	var st string
	if err := s.DB.QueryRow(`SELECT state FROM requests WHERE direction='in' AND id=?`, id).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != StateAccepted {
		t.Fatalf("state after oversize complete = %s, want accepted (unchanged)", st)
	}

	// Sender-mirror path: an oversize complete mail is mail.ErrBadBody, and
	// the mirror's state is unchanged.
	sender, _, _ := newTestStore(t, testFrom, &policy{})
	out, err := sender.Submit(context.Background(), submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	body := map[string]any{"at": wireTime(now), "request": out.Request.ID, "seq": 1, "result": resultWire(result)}
	err = deliverMirror(t, sender, KindComplete, testTo, now, body)
	if !errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("mirror complete oversize: err = %v, want mail.ErrBadBody", err)
	}
	var outState string
	if err := sender.DB.QueryRow(`SELECT state FROM requests WHERE direction='out' AND id=?`, out.Request.ID).Scan(&outState); err != nil {
		t.Fatal(err)
	}
	if outState != StatePending {
		t.Fatalf("out state after rejected oversize complete = %s, want pending (unchanged)", outState)
	}
}

// TestCompleteResultRoundTrip is the 1.6a acceptance test: B completes with
// every result member set; A's out row result is byte-identical to
// canonical(result) on B's in row and inside B's last_reply.
func TestCompleteResultRoundTrip(t *testing.T) {
	b, _, _ := newTestStore(t, testTo, &policy{})
	id := storePendingIn(t, b)
	if _, err := b.Accept(context.Background(), id, testFrom); err != nil {
		t.Fatal(err)
	}
	exitCode := int64(1)
	result := &Result{
		Status: ResultFail, Summary: "3 of 212 tests failed", ExitCode: &exitCode,
		Output:    "--- FAIL: TestRetry (0.01s)\n",
		Artifacts: []Artifact{{Branch: "fix/retry", Commit: "1a2b3c4"}},
	}
	bView, err := b.Complete(context.Background(), id, testFrom, "Ran on Windows 11.", result)
	if err != nil {
		t.Fatal(err)
	}
	bCanon, err := CanonicalResult(result)
	if err != nil {
		t.Fatal(err)
	}
	var bResultCol, bLastReply string
	if err := b.DB.QueryRow(`SELECT result, last_reply FROM requests WHERE direction='in' AND id=?`, id).Scan(&bResultCol, &bLastReply); err != nil {
		t.Fatal(err)
	}
	if bResultCol != string(bCanon) {
		t.Fatalf("B's in row result = %s, want %s", bResultCol, bCanon)
	}
	if !strings.Contains(bLastReply, `"result":`) || !strings.Contains(bLastReply, "1a2b3c4") {
		t.Fatalf("B's last_reply missing result: %s", bLastReply)
	}
	if bView.Result == nil || bView.Result.Summary != result.Summary {
		t.Fatalf("B's view result = %+v", bView.Result)
	}

	// A's mirror: deliver B's request.complete mail.
	a, _, _ := newTestStore(t, testFrom, &policy{})
	out, err := a.Submit(context.Background(), submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	body := map[string]any{
		"at": wireTime(now), "note": "Ran on Windows 11.", "request": out.Request.ID, "seq": 1,
		"result": resultWire(result),
	}
	if err := deliverMirror(t, a, KindComplete, testTo, now, body); err != nil {
		t.Fatal(err)
	}
	var aResultCol string
	if err := a.DB.QueryRow(`SELECT result FROM requests WHERE direction='out' AND id=?`, out.Request.ID).Scan(&aResultCol); err != nil {
		t.Fatal(err)
	}
	if aResultCol != string(bCanon) {
		t.Fatalf("A's out row result = %s, want %s (byte-identical to B's)", aResultCol, bCanon)
	}

	aView, err := a.Show(context.Background(), out.Request.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if aView.Result == nil || aView.Result.Output != result.Output || aView.OutputBytes != len(result.Output) {
		t.Fatalf("A's Show result = %+v, output_bytes %d", aView.Result, aView.OutputBytes)
	}
}

// TestCompletionWithoutResultLeavesColumnNull: the pre-D14 behaviour is
// unchanged.
func TestCompletionWithoutResultLeavesColumnNull(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	id := storePendingIn(t, s)
	if _, err := s.Accept(context.Background(), id, testFrom); err != nil {
		t.Fatal(err)
	}
	v, err := s.Complete(context.Background(), id, testFrom, "just a note", nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.Result != nil {
		t.Fatalf("view result = %+v, want nil", v.Result)
	}
	var result sql.NullString
	if err := s.DB.QueryRow(`SELECT result FROM requests WHERE direction='in' AND id=?`, id).Scan(&result); err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Fatalf("result column = %v, want NULL", result)
	}
}
