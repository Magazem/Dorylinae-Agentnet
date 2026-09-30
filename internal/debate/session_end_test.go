package debate

import (
	"context"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// R55-060: the Phase 1 fallback skips debate sessions. B has a debate
// session and a work session with A, and a ws.result to A ended
// failed/unsupported_kind: only the work session closes, and the debates row
// is untouched.
func TestPhase1FallbackSkipsDebates(t *testing.T) {
	ctx := context.Background()
	a, b, dreq, dsid := openPositions(t, 2)
	out, err := a.req.Submit(ctx, request.SubmitParams{
		From: a.self, To: b.self, Team: testTeam, Type: request.TypeTask, Title: "work", Brief: "What: x", Urgency: request.UrgencyNormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	reqID := out.Request.ID
	mustDeliver(t, b, a.self, a.ob.take(t, "request"))
	if _, err := b.req.Accept(ctx, reqID, a.self); err != nil {
		t.Fatal(err)
	}
	wsid := worksession.DeriveID(a.self, b.self, reqID)
	if ok, _, err := b.ws.SubmitResult(ctx, a.self, reqID, &worksession.Result{Status: request.ResultPass, Verification: worksession.VerificationNone}, worksession.ByAgent); !ok || err != nil {
		t.Fatal(err)
	}
	if _, err := b.db.Exec(`UPDATE outbox SET state = 'failed', error = 'unsupported_kind' WHERE kind = ?`, worksession.KindResult); err != nil {
		t.Fatal(err)
	}
	phaseBefore := phaseOf(t, b, dsid)

	b.ws.CheckPhase1FallbackForPeer(ctx, a.self)
	if err := b.ws.CheckPhase1Fallback(ctx, a.self, dreq); err != nil {
		t.Fatal(err)
	}

	if v, err := b.ws.Get(ctx, wsid); err != nil || v.State != worksession.StateClosed {
		t.Fatalf("work session = %+v, %v; want closed", v, err)
	}
	if v, err := b.ws.Get(ctx, dsid); err != nil || v.State != worksession.StateOpen {
		t.Fatalf("debate session = %+v, %v; want open", v, err)
	}
	if p := phaseOf(t, b, dsid); p != phaseBefore {
		t.Fatalf("debate phase %s -> %s; want untouched", phaseBefore, p)
	}
}

// R55-062: a late request.decline for an accepted debate closes A's open
// debate session through the debate (cancelled).
func TestLateDeclineClosesDebate(t *testing.T) {
	ctx := context.Background()
	a, _, reqID, sid := openPositions(t, 2)
	var seq int
	if err := a.db.QueryRow(`SELECT state_seq FROM requests WHERE direction = 'out' AND id = ?`, reqID).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	mustDeliver(t, a, keyB, sentMail{kind: request.KindDecline, body: map[string]any{
		"at": wireTime(a.clock), "request": reqID, "seq": seq + 1, "code": "user", "reason": "no",
	}})
	if v, err := a.ws.Get(ctx, sid); err != nil || v.State != worksession.StateClosed || v.Outcome != worksession.OutcomeCancelled {
		t.Fatalf("A's debate session = %+v, %v; want closed cancelled", v, err)
	}
	if p := phaseOf(t, a, sid); p != "closed" {
		t.Fatalf("debate phase = %s, want closed", p)
	}
}
