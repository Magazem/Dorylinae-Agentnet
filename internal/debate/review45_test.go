package debate

import (
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// Review 45 H1, as 3.3a sharpened it: a modified A cannot close B's mirror
// with an outcome B's own transcript contradicts (agreement exists only as
// B's own answer). closeMatches runs before any derivation: B refuses
// (peer_refused, debate.sign refused "mismatch", its mirror cancelled and the
// debate broken) and never signs.
func TestCloseOutcomeCheckedAgainstAnswer(t *testing.T) {
	closeBody := func(n *dnode, reqID, sid, outcome, reason string) sentMail {
		return sentMail{kind: MailClose, body: map[string]any{
			"at": wireTime(n.clock), "constraints": []any{}, "entries": 6, "outcome": outcome, "reason": reason,
			"request": reqID, "session": sid,
			"decision": strings.Repeat("a", 64), "sig": strings.Repeat("A", 86),
		}}
	}
	refused := func(t *testing.T, b *dnode, sid string) {
		t.Helper()
		if p := phaseOf(t, b, sid); p != PhaseBroken {
			t.Fatalf("B phase %s, want broken", p)
		}
		if st, o := sessionState(t, b, sid); st != worksession.StateClosed || o != worksession.OutcomeCancelled {
			t.Fatalf("B session %s/%s", st, o)
		}
		if b.events.count(EventAgreed) != 0 || b.events.count(EventBroken) != 1 || !b.audit.has("decision.refuse", `"reason":"outcome"`) {
			t.Fatalf("B events/audit:\n%s", b.audit.all())
		}
		var sigs int
		if err := b.db.QueryRow(`SELECT COUNT(*) FROM decisions WHERE sig_respondent IS NOT NULL`).Scan(&sigs); err != nil || sigs != 0 {
			t.Fatalf("B signed %d Decisions (%v)", sigs, err)
		}
		sm := b.ob.take(t, MailSign)
		if sm.body["refused"] != refusedMismatch || sm.body["sig"] != nil {
			t.Fatalf("debate.sign %v", sm.body)
		}
	}
	t.Run("agreed before any answer", func(t *testing.T) {
		a, b, reqID, sid := openPositions(t, 1)
		mustDeliver(t, b, keyA, closeBody(a, reqID, sid, OutcomeAgreed, ReasonAccepted))
		refused(t, b, sid)
		// A later close is stored for the record and changes nothing.
		mustDeliver(t, b, keyA, closeBody(a, reqID, sid, OutcomeEscalated, ReasonRejected))
		if p := phaseOf(t, b, sid); p != PhaseBroken {
			t.Fatalf("B phase %s", p)
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
		refused(t, b, sid)
	})
	t.Run("timeout needs B's position", func(t *testing.T) {
		a, b := newDNode(t, keyA), newDNode(t, keyB)
		reqID, sid := startDebate(t, a, b, 1)
		if _, err := b.req.Accept(t.Context(), reqID, keyA); err != nil {
			t.Fatal(err)
		}
		mustDeliver(t, b, keyA, closeBody(a, reqID, sid, OutcomeEscalated, ReasonTimeout))
		refused(t, b, sid)
	})
	t.Run("cancelled/timeout before B's position still closes", func(t *testing.T) {
		a, b := newDNode(t, keyA), newDNode(t, keyB)
		reqID, sid := startDebate(t, a, b, 1)
		if _, err := b.req.Accept(t.Context(), reqID, keyA); err != nil {
			t.Fatal(err)
		}
		body := closeBody(a, reqID, sid, OutcomeCancelled, ReasonTimeout)
		delete(body.body, "decision")
		delete(body.body, "sig")
		mustDeliver(t, b, keyA, body)
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
