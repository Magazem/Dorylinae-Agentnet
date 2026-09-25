package debate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// Timeouts on a fake clock (Docs/protocol/debate.md §Timeouts): a missing
// slot 1 closes cancelled/timeout with no Decision; a missing later slot
// (either side's) closes escalated/timeout; B never closes on time; a debate
// IPC call on A applies the rule too.
func TestTimeouts(t *testing.T) {
	ctx := context.Background()
	t.Run("slot 1 missing", func(t *testing.T) {
		a, b := newDNode(t, keyA), newDNode(t, keyB)
		reqID, sid := startDebate(t, a, b, 2)
		if _, err := b.req.Accept(ctx, reqID, keyA); err != nil {
			t.Fatal(err)
		}
		pass(t, b, a, request.KindAccept)
		a.advance(3599 * time.Second)
		if n, err := a.ds.Sweep(ctx); err != nil || n != 0 {
			t.Fatalf("sweep before the deadline closed %d (%v)", n, err)
		}
		a.advance(2 * time.Second)
		b.advance(2 * time.Hour)
		if n, err := b.ds.Sweep(ctx); err != nil || n != 0 {
			t.Fatalf("B closed on time: %d (%v)", n, err)
		}
		if n, err := a.ds.Sweep(ctx); err != nil || n != 1 {
			t.Fatalf("sweep closed %d (%v)", n, err)
		}
		v := view(t, a, sid)
		if v.Phase != PhaseClosed || v.Outcome != OutcomeCancelled || v.Reason != ReasonTimeout {
			t.Fatalf("A: %+v", v)
		}
		if st, out := sessionState(t, a, sid); st != worksession.StateClosed || out != worksession.OutcomeCancelled {
			t.Fatalf("A session %s/%s", st, out)
		}
		cl := a.ob.take(t, MailClose)
		if cl.body["outcome"] != OutcomeCancelled || cl.body["reason"] != ReasonTimeout {
			t.Fatalf("close %v", cl.body)
		}
		if _, has := cl.body["decision"]; has {
			t.Fatal("a cancelled close carries a decision")
		}
		mustDeliver(t, b, keyA, cl)
		if v := view(t, b, sid); v.Phase != PhaseClosed || v.Outcome != OutcomeCancelled {
			t.Fatalf("B: %+v", v)
		}
		if !a.audit.has("debate.close", `"reason":"timeout"`) {
			t.Error("timeout close not audited")
		}
	})
	for _, tc := range []struct {
		name  string
		steps func(t *testing.T, a, b *dnode, sid string)
	}{
		{"A's own move missing", func(*testing.T, *dnode, *dnode, string) {}},
		{"B's move missing", func(t *testing.T, a, b *dnode, sid string) {
			submit(t, a, sid, KindMove, pass0())
			pass(t, a, b, MailEntry)
		}},
		{"B's answer missing", func(t *testing.T, a, b *dnode, sid string) {
			submit(t, a, sid, KindMove, pass0())
			pass(t, a, b, MailEntry)
			submit(t, b, sid, KindMove, pass0())
			pass(t, b, a, MailEntry)
			submit(t, a, sid, KindProposal, proposal())
			pass(t, a, b, MailEntry)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b, _, sid := openPositions(t, 2)
			tc.steps(t, a, b, sid)
			a.advance(time.Hour + time.Second)
			// The IPC path applies the rule before anything else.
			_, err := a.ds.Submit(ctx, sid, "", KindMove, pass0())
			var bse *BadStateError
			var nyt *NotYourTurnError
			if !errors.As(err, &bse) && !errors.As(err, &nyt) {
				t.Fatalf("submit after the deadline: %v", err)
			}
			if p := phaseOf(t, a, sid); p != PhaseClosing {
				t.Fatalf("A phase %s, want closing (escalated)", p)
			}
			cl := a.ob.take(t, MailClose)
			if cl.body["outcome"] != OutcomeEscalated || cl.body["reason"] != ReasonTimeout {
				t.Fatalf("close %v", cl.body)
			}
			if a.events.count(EventEscalated) != 1 {
				t.Error("debate.escalated not fired on A")
			}
			if st, out := sessionState(t, a, sid); st != worksession.StateClosed || out != worksession.OutcomeAccepted {
				t.Fatalf("A session %s/%s", st, out)
			}
			mustDeliver(t, b, keyA, cl)
			if v := view(t, b, sid); v.Phase != PhaseClosed || v.Outcome != OutcomeEscalated || v.Reason != ReasonTimeout {
				t.Fatalf("B: %+v", v)
			}
			if b.events.count(EventEscalated) != 1 {
				t.Error("debate.escalated not fired on B")
			}
		})
	}
}

// Review 43 M3: a slot-1 entry that reaches A before the request.accept
// opens A's session as if the accept had arrived first, is applied, and the
// reveal is sent; the accept then creates nothing new.
func TestSlot1BeforeAccept(t *testing.T) {
	a, b := newDNode(t, keyA), newDNode(t, keyB)
	reqID, sid := startDebate(t, a, b, 2)
	submit(t, b, sid, KindPosition, testPosition("B: fixed retry"))
	pass(t, b, a, MailEntry) // overtakes the accept
	if st, _ := sessionState(t, a, sid); st != worksession.StateOpen {
		t.Fatalf("A session %s", st)
	}
	if a.ob.count(MailReveal) != 1 {
		t.Fatal("A did not reveal")
	}
	if v := view(t, a, sid); v.Phase != PhaseRounds || v.NextSlot != 2 {
		t.Fatalf("A: %+v", v)
	}
	pass(t, b, a, request.KindAccept)
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM work_sessions WHERE request_id = ?`, reqID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("sessions on A: %d (%v)", n, err)
	}
	if v := view(t, a, sid); v.Phase != PhaseRounds || v.NextSlot != 2 {
		t.Fatalf("A after the accept: %+v", v)
	}
	var state string
	if err := a.db.QueryRow(`SELECT state FROM requests WHERE direction = 'out' AND id = ?`, reqID).Scan(&state); err != nil || state != request.StateAccepted {
		t.Fatalf("A request %s (%v)", state, err)
	}
	pass(t, a, b, MailReveal)
	same(t, a, b, sid, "after reveal")

	// The same entry for a request A cancelled is ignored.
	a2, b2 := newDNode(t, keyA), newDNode(t, keyB)
	req2, sid2 := startDebate(t, a2, b2, 2)
	if _, err := a2.req.Cancel(context.Background(), req2, ""); err != nil {
		t.Fatal(err)
	}
	submit(t, b2, sid2, KindPosition, testPosition("B: fixed retry"))
	pass(t, a2, b2, request.KindCancel) // refused on B: already accepted
	pass(t, b2, a2, request.KindCancelled)
	if _, err := a2.db.Exec(`UPDATE requests SET state = 'cancelled' WHERE direction = 'out' AND id = ?`, req2); err != nil {
		t.Fatal(err)
	}
	pass(t, b2, a2, MailEntry)
	if a2.ob.count(MailReveal) != 0 || !a2.audit.has("debate.ignored", `"reason":"state"`) {
		t.Fatal("A applied slot 1 for a cancelled request")
	}
}

// Review 43 M4: a request.complete from B while A's debate is open closes it
// cancelled (reason cancelled, no Decision); its result and note are dropped
// unread and the inbox copy is blank. On B, request_complete on a debate is
// bad_state.
func TestEarlyCompleteOnDebate(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := openPositions(t, 2)
	if _, err := b.req.Complete(ctx, reqID, keyA, "done", nil); err == nil {
		t.Fatal("request_complete on an open debate accepted")
	} else {
		var bse *worksession.BadStateError
		if !errors.As(err, &bse) {
			t.Fatalf("request_complete: %v, want bad_state", err)
		}
	}
	// A modified B completes directly, with a note and a result.
	body := map[string]any{"at": wireTime(b.clock), "note": "secret note", "request": reqID, "seq": 2,
		"result": map[string]any{"status": "pass", "summary": "secret summary"}}
	mustDeliver(t, a, keyB, sentMail{kind: request.KindComplete, body: body})
	v := view(t, a, sid)
	if v.Phase != PhaseClosed || v.Outcome != OutcomeCancelled || v.Reason != ReasonCancelled {
		t.Fatalf("A: %+v", v)
	}
	if st, out := sessionState(t, a, sid); st != worksession.StateClosed || out != worksession.OutcomeCancelled {
		t.Fatalf("A session %s/%s", st, out)
	}
	var note, result, signed string
	if err := a.db.QueryRow(`SELECT COALESCE(note, ''), COALESCE(result, '') FROM requests WHERE direction = 'out' AND id = ?`, reqID).Scan(&note, &result); err != nil {
		t.Fatal(err)
	}
	if note != "" || result != "" {
		t.Fatalf("A stored note %q result %q", note, result)
	}
	if err := a.db.QueryRow(`SELECT signed FROM mail_inbox WHERE kind = ?`, request.KindComplete).Scan(&signed); err != nil || signed != "" {
		t.Fatalf("inbox copy %q (%v)", signed, err)
	}
	if a.ob.count(MailClose) != 1 {
		t.Fatal("A sent no close")
	}
}

// In closing an early complete changes nothing.
func TestEarlyCompleteInClosing(t *testing.T) {
	a, b, reqID, sid := openPositions(t, 1)
	submit(t, a, sid, KindMove, pass0())
	pass(t, a, b, MailEntry)
	submit(t, b, sid, KindMove, pass0())
	pass(t, b, a, MailEntry)
	submit(t, a, sid, KindProposal, proposal())
	pass(t, a, b, MailEntry)
	submit(t, b, sid, KindAnswer, answer(true))
	pass(t, b, a, MailEntry)
	if p := phaseOf(t, a, sid); p != PhaseClosing {
		t.Fatalf("phase %s", p)
	}
	body := map[string]any{"at": wireTime(b.clock), "note": "late", "request": reqID, "seq": 2}
	mustDeliver(t, a, keyB, sentMail{kind: request.KindComplete, body: body})
	if p := phaseOf(t, a, sid); p != PhaseClosing {
		t.Fatalf("phase %s after the early complete", p)
	}
}

// Quarantine edges (Docs/protocol/debate.md §Quarantine interplay): A cannot
// start and B cannot accept (either way) while the rule-2 test holds from
// its own side; a grant long expired does not count.
func TestQuarantineEdges(t *testing.T) {
	ctx := context.Background()
	t.Run("start on A", func(t *testing.T) {
		a, b := newDNode(t, keyA), newDNode(t, keyB)
		seedSensitiveGrant(t, a, keyB, a.clock.Add(-6*24*time.Hour))
		_, err := a.ds.Start(ctx, StartParams{Submit: request.SubmitParams{From: keyA, To: keyB, Team: testTeam, Type: request.TypeDebate,
			Title: "t", Brief: "topic", Urgency: request.UrgencyNormal}, Position: testPosition("A: x")})
		if !errors.Is(err, ErrQuarantineActive) {
			t.Fatalf("Start: %v, want quarantine_active", err)
		}
		var n int
		_ = a.db.QueryRow(`SELECT (SELECT COUNT(*) FROM requests) + (SELECT COUNT(*) FROM debates) + (SELECT COUNT(*) FROM outbox)`).Scan(&n)
		if n != 0 {
			t.Fatalf("a refused start left %d rows", n)
		}
		_ = b
	})
	t.Run("expired grant does not hold", func(t *testing.T) {
		a, b := newDNode(t, keyA), newDNode(t, keyB)
		seedSensitiveGrant(t, a, keyB, a.clock.Add(-8*24*time.Hour))
		startDebate(t, a, b, 1)
	})
	for _, oneStep := range []bool{false, true} {
		name := "accept on B"
		if oneStep {
			name = "one-step accept + position on B"
		}
		t.Run(name, func(t *testing.T) {
			a, b := newDNode(t, keyA), newDNode(t, keyB)
			reqID, sid := startDebate(t, a, b, 1)
			seedSensitiveGrant(t, b, keyA, b.clock.Add(time.Hour))
			var err error
			if oneStep {
				_, err = b.ds.Submit(ctx, sid, "", KindPosition, testPosition("B: y"))
			} else {
				_, err = b.req.Accept(ctx, reqID, keyA)
			}
			if !errors.Is(err, ErrQuarantineActive) {
				t.Fatalf("accept: %v, want quarantine_active", err)
			}
			var state string
			if err := b.db.QueryRow(`SELECT state FROM requests WHERE id = ?`, reqID).Scan(&state); err != nil || state != request.StatePending {
				t.Fatalf("request %s (%v)", state, err)
			}
			var n int
			_ = b.db.QueryRow(`SELECT (SELECT COUNT(*) FROM work_sessions) + (SELECT COUNT(*) FROM debate_entries) + (SELECT COUNT(*) FROM outbox)`).Scan(&n)
			if n != 0 || phaseOf(t, b, sid) != PhaseInvited {
				t.Fatalf("a refused accept left %d rows, phase %s", n, phaseOf(t, b, sid))
			}
		})
	}
}

// A decline, a request.cancel or an auto-decline closes both debate rows
// cancelled; A's position is never sent.
func TestEndedBeforeAccept(t *testing.T) {
	ctx := context.Background()
	t.Run("decline", func(t *testing.T) {
		a, b := newDNode(t, keyA), newDNode(t, keyB)
		reqID, sid := startDebate(t, a, b, 1)
		if _, err := b.req.Decline(ctx, reqID, keyA, "not now"); err != nil {
			t.Fatal(err)
		}
		pass(t, b, a, request.KindDecline)
		for _, n := range []*dnode{a, b} {
			if v := view(t, n, sid); v.Phase != PhaseClosed || v.Outcome != OutcomeCancelled {
				t.Fatalf("%s: %+v", n.name(), v)
			}
		}
		if a.ob.count(MailReveal) != 0 {
			t.Fatal("A revealed")
		}
	})
	t.Run("cancel", func(t *testing.T) {
		a, b := newDNode(t, keyA), newDNode(t, keyB)
		reqID, sid := startDebate(t, a, b, 1)
		if _, err := a.req.Cancel(ctx, reqID, ""); err != nil {
			t.Fatal(err)
		}
		pass(t, a, b, request.KindCancel)
		pass(t, b, a, request.KindCancelled)
		for _, n := range []*dnode{a, b} {
			if v := view(t, n, sid); v.Phase != PhaseClosed || v.Outcome != OutcomeCancelled {
				t.Fatalf("%s: %+v", n.name(), v)
			}
		}
	})
}

// A's cancel after accept (ws_cancel) and B's ws.cancel close the debate
// cancelled with no Decision; B learns it from debate.close, never ws.state.
func TestCancelAfterAccept(t *testing.T) {
	ctx := context.Background()
	t.Run("A cancels", func(t *testing.T) {
		a, b, _, sid := openPositions(t, 2)
		if _, err := a.ws.Cancel(ctx, sid, ""); err != nil {
			t.Fatal(err)
		}
		if a.ob.count(worksession.KindState) != 0 {
			t.Fatal("A sent ws.state for a debate")
		}
		pass(t, a, b, MailClose)
		if v := view(t, b, sid); v.Phase != PhaseClosed || v.Outcome != OutcomeCancelled || v.Reason != ReasonCancelled {
			t.Fatalf("B: %+v", v)
		}
		if st, _ := sessionState(t, b, sid); st != worksession.StateClosed {
			t.Fatalf("B session %s", st)
		}
	})
	t.Run("B cancels", func(t *testing.T) {
		a, b, _, sid := openPositions(t, 2)
		if _, _, _, err := b.ws.SubmitCancel(ctx, sid, "no time"); err != nil {
			t.Fatal(err)
		}
		pass(t, b, a, worksession.KindCancel)
		if v := view(t, a, sid); v.Phase != PhaseClosed || v.Outcome != OutcomeCancelled {
			t.Fatalf("A: %+v", v)
		}
		if a.ob.count(worksession.KindState) != 0 {
			t.Fatal("A sent ws.state for a debate")
		}
		pass(t, a, b, MailClose)
		if v := view(t, b, sid); v.Phase != PhaseClosed {
			t.Fatalf("B: %+v", v)
		}
	})
}

// A debate's work session refuses ws_result (and request_complete's
// shorthand), ws_accept_result, ws_request_changes, ws_release and
// ws_discard, and ignores a ws.result ({reason: "kind"}, inbox copy blank)
// and a ws.state.
func TestDebateSessionRefusesWorkTransitions(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := openPositions(t, 2)
	res := &worksession.Result{Status: request.ResultPass, Summary: "x", Verification: worksession.VerificationNone}
	wantBS := func(name string, err error) {
		t.Helper()
		var bse *worksession.BadStateError
		if !errors.As(err, &bse) || !strings.Contains(bse.Msg, "debate") {
			t.Errorf("%s: %v, want bad_state", name, err)
		}
	}
	_, _, err := b.ws.SubmitResult(ctx, keyA, reqID, res)
	wantBS("ws_result", err)
	_, err = a.ws.AcceptResult(ctx, sid)
	wantBS("ws_accept_result", err)
	_, err = a.ws.RequestChanges(ctx, sid, "more")
	wantBS("ws_request_changes", err)
	_, err = a.ws.ReleaseApproved(ctx, sid, "a-1")
	wantBS("ws_release", err)
	_, err = a.ws.Discard(ctx, sid)
	wantBS("ws_discard", err)
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = a.ws.AcceptResultInTx(ctx, tx, sid, a.clock)
	_ = tx.Rollback()
	wantBS("ws_accept_result --human", err)

	body := map[string]any{"at": wireTime(b.clock), "request": reqID, "round": 1, "session": sid,
		"result": map[string]any{"status": "pass", "summary": "secret result", "verification": "none"}}
	mustDeliver(t, a, keyB, sentMail{kind: worksession.KindResult, body: body})
	if !a.audit.has("ws.ignored", `"reason":"kind"`) {
		t.Error("ws.result not ignored with reason kind")
	}
	var signed string
	if err := a.db.QueryRow(`SELECT signed FROM mail_inbox WHERE kind = ?`, worksession.KindResult).Scan(&signed); err != nil || signed != "" {
		t.Fatalf("inbox copy %q (%v)", signed, err)
	}
	if st, _ := sessionState(t, a, sid); st != worksession.StateOpen {
		t.Fatalf("A session %s", st)
	}
	st := map[string]any{"at": wireTime(a.clock), "outcome": "cancelled", "request": reqID, "round": 1, "seq": 5, "session": sid, "state": "closed"}
	mustDeliver(t, b, keyA, sentMail{kind: worksession.KindState, body: st})
	if s, _ := sessionState(t, b, sid); s != worksession.StateOpen || phaseOf(t, b, sid) != PhaseRounds {
		t.Fatal("B applied a ws.state to a debate session")
	}
}

// Review 43 L1: C1 characters and U+2028/U+2029 are refused in every debate
// string at IPC (bad_request naming the field), including argument; the
// entry cap is entry_too_large.
func TestSubmitRefusesDebateText(t *testing.T) {
	a, _, _, sid := openPositions(t, 2)
	for name, arg := range map[string]string{"C1": "a\u0085b", "CSI": "a\u009bb", "U+2028": "a\u2028b", "U+2029": "a\u2029b", "ESC": "a\x1bb"} {
		m := map[string]any{"challenges": []any{map[string]any{"targets": []any{"claim"}, "argument": arg}}}
		_, err := a.ds.Submit(context.Background(), sid, "", KindMove, m)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != "entry.challenges[0].argument" {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A target that does not exist in the other side's current position.
	_, err := a.ds.Submit(context.Background(), sid, "", KindMove, challenge("assumptions/3"))
	var fe *FieldError
	if !errors.As(err, &fe) || !strings.HasPrefix(fe.Field, "entry.challenges[0].targets") {
		t.Errorf("out-of-range target: %v", err)
	}
	// Ten pieces of evidence with 1024-code-point refs of 4-byte characters:
	// every field cap holds, the canonical entry does not.
	ev := make([]any, 10)
	for i := range ev {
		ev[i] = map[string]any{"kind": "doc", "ref": strings.Repeat("\U0001F600", 1024)}
	}
	big := map[string]any{"challenges": []any{}, "revision": map[string]any{"claim": "x", "argument": "y", "evidence": ev}}
	_, err = a.ds.Submit(context.Background(), sid, "", KindMove, big)
	var tl *TooLargeError
	if !errors.As(err, &tl) {
		t.Errorf("large move: %v, want entry_too_large", err)
	}
	if v := view(t, a, sid); v.NextSlot != 2 {
		t.Errorf("a refused entry moved the turn to %d", v.NextSlot)
	}
}
