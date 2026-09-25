package debate

import (
	"context"
	"testing"
)

// Ticket 3.3b: DecisionList (internal/daemon's decision_list reads through
// it). A signed Decision shows up on both sides, narrowed by state and peer,
// newest first; a debate with no Decision (still open, or cancelled) never
// appears.
func TestDecisionList(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	toConverge(t, a, b, sid)
	cl := closeTo(t, a, b, sid)
	mustDeliver(t, b, a.self, cl)
	pass(t, b, a, MailSign)
	da := bothSigned(t, a, b, sid, OutcomeAgreed, ReasonAccepted)

	ctx := context.Background()
	list, err := a.ds.DecisionList(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != da.ID || list[0].State != DecisionSigned {
		t.Fatalf("DecisionList(\"\",\"\") = %+v, want one signed row for %s", list, da.ID)
	}

	if list, err := a.ds.DecisionList(ctx, DecisionSigned, ""); err != nil || len(list) != 1 {
		t.Fatalf("DecisionList(signed,\"\") = %+v, %v", list, err)
	}
	if list, err := a.ds.DecisionList(ctx, DecisionAwaitingPeer, ""); err != nil || len(list) != 0 {
		t.Fatalf("DecisionList(awaiting_peer,\"\") = %+v, %v, want none", list, err)
	}
	if list, err := a.ds.DecisionList(ctx, "", b.self); err != nil || len(list) != 1 {
		t.Fatalf("DecisionList(\"\",peer) = %+v, %v", list, err)
	}
	if list, err := a.ds.DecisionList(ctx, "", "Kx-not-a-real-peer"); err != nil || len(list) != 0 {
		t.Fatalf("DecisionList(\"\",other peer) = %+v, %v, want none", list, err)
	}
}
