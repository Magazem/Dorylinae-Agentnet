package debate

import (
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// Review 45 H1: a modified A cannot close B's mirror with an outcome B's own
// transcript contradicts (agreement exists only as B's own answer).
func TestCloseOutcomeCheckedAgainstAnswer(t *testing.T) {
	closeBody := func(n *dnode, reqID, sid, outcome, reason string) sentMail {
		return sentMail{kind: MailClose, body: map[string]any{
			"at": wireTime(n.clock), "constraints": []any{}, "entries": 6, "outcome": outcome, "reason": reason,
			"request": reqID, "session": sid,
		}}
	}
	t.Run("agreed before any answer", func(t *testing.T) {
		a, b, reqID, sid := openPositions(t, 1)
		mustDeliver(t, b, keyA, closeBody(a, reqID, sid, OutcomeAgreed, ReasonAccepted))
		mustDeliver(t, b, keyA, closeBody(a, reqID, sid, OutcomeEscalated, ReasonRejected))
		if p := phaseOf(t, b, sid); p != PhaseRounds {
			t.Fatalf("B phase %s, want rounds", p)
		}
		if b.events.count(EventAgreed) != 0 || !b.audit.has("debate.ignored", "outcome") {
			t.Fatal("fabricated close not ignored")
		}
	})
	t.Run("agreed after a refusal", func(t *testing.T) {
		a, b, reqID, sid := openPositions(t, 1)
		submit(t, a, sid, KindMove, pass0())
		pass(t, a, b, MailEntry)
		submit(t, b, sid, KindMove, pass0())
		pass(t, b, a, MailEntry)
		submit(t, a, sid, KindProposal, proposal())
		pass(t, a, b, MailEntry)
		submit(t, b, sid, KindAnswer, answer(false))
		mustDeliver(t, b, keyA, closeBody(a, reqID, sid, OutcomeAgreed, ReasonAccepted))
		if p := phaseOf(t, b, sid); p != PhaseConverge {
			t.Fatalf("B phase %s after a fabricated agreement", p)
		}
		if st, _ := sessionState(t, b, sid); st != worksession.StateOpen {
			t.Fatalf("B session %s", st)
		}
		// The genuine close (escalated, rejected) still applies.
		pass(t, b, a, MailEntry)
		pass(t, a, b, MailClose)
		if v := view(t, b, sid); v.Phase != PhaseClosed || v.Outcome != OutcomeEscalated {
			t.Fatalf("B after the real close: %+v", v)
		}
	})
	t.Run("timeout needs B's position", func(t *testing.T) {
		a, b := newDNode(t, keyA), newDNode(t, keyB)
		reqID, sid := startDebate(t, a, b, 1)
		if _, err := b.req.Accept(t.Context(), reqID, keyA); err != nil {
			t.Fatal(err)
		}
		mustDeliver(t, b, keyA, closeBody(a, reqID, sid, OutcomeEscalated, ReasonTimeout))
		if p := phaseOf(t, b, sid); p != PhasePositions {
			t.Fatalf("B phase %s, want positions", p)
		}
		mustDeliver(t, b, keyA, closeBody(a, reqID, sid, OutcomeCancelled, ReasonTimeout))
		if v := view(t, b, sid); v.Phase != PhaseClosed || v.Outcome != OutcomeCancelled {
			t.Fatalf("B after cancelled/timeout: %+v", v)
		}
	})
}

// Review 45 M1: a request.complete from a modified B for a debate A still
// holds invited ends the debate, so it no longer blocks sensitive grants.
func TestCompleteBeforeAcceptEndsInvitedDebate(t *testing.T) {
	a, b := newDNode(t, keyA), newDNode(t, keyB)
	reqID, sid := startDebate(t, a, b, 1)
	body := map[string]any{"at": wireTime(b.clock), "note": "skip", "request": reqID, "seq": 1}
	mustDeliver(t, a, keyB, sentMail{kind: request.KindComplete, body: body})
	if v := view(t, a, sid); v.Phase != PhaseClosed || v.Outcome != OutcomeCancelled {
		t.Fatalf("A: %+v", v)
	}
	if err := capability.CheckSensitiveGrant(t.Context(), a.db, keyB, true); err != nil {
		t.Fatalf("sensitive grant still refused: %v", err)
	}
	if a.ob.count(MailReveal) != 0 {
		t.Fatal("A revealed")
	}
}
