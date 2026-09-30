package worksession

import (
	"context"
	"testing"
)

// Review 78 probe (security review of R55-F18): after a Phase 2 exchange in
// this very session (B's ws.result R1 reviewed, changes requested, round 2
// open), a modified B skips review with request.complete{R-evil}. The
// open-state early complete (Phase 1 compat) stores R-evil in A's request
// record, although B has proven it is Phase 2 in this session.
func TestSec78Round2EarlyCompleteStoresUnreviewed(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	if _, err := a.ws.RequestChanges(ctx, sid, "please redo"); err != nil {
		t.Fatal(err)
	}
	injectComplete(t, a, reqID, outSeq(t, a, reqID)+1, "", "never reviewed R-evil")
	av, err := a.ws.Get(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	arv, err := a.req.Show(ctx, reqID, testB)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("session state=%s outcome=%s round=%d; record state=%s result=%+v", av.State, av.Outcome, av.Round, arv.State, arv.Result)
	if arv.Result != nil && arv.Result.Summary == "never reviewed R-evil" {
		t.Logf("CONFIRMED: unreviewed R-evil is stored in A's request record")
	}
}
