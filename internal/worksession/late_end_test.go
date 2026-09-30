package worksession

import (
	"context"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// lateEndMail builds a request.decline (code user) or request.cancelled from
// a modified B, with seq.
func lateEndMail(kind, reqID string, seq int, at string) sentMail {
	body := map[string]any{"at": at, "request": reqID, "seq": seq}
	if kind == request.KindDecline {
		body["code"] = "user"
		body["reason"] = "changed my mind"
	}
	return sentMail{kind: kind, body: body}
}

// R55-062: a late request.decline or request.cancelled closes A's open
// session cancelled in the same transaction: its grants end and a ws.state
// goes to B.
func TestLateDeclineOrCancelledClosesOpenSession(t *testing.T) {
	for _, kind := range []string{request.KindDecline, request.KindCancelled} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			a, _, reqID, sid := setupAcceptedSession(t)
			spy := &spyRevoke{}
			a.ws.RevokeGrants = spy.hook
			if err := deliver(t, a, testB, lateEndMail(kind, reqID, outSeq(t, a, reqID)+1, wireTime(a.clock))); err != nil {
				t.Fatal(err)
			}
			av, err := a.ws.Get(ctx, sid)
			if err != nil || av.State != StateClosed || av.Outcome != OutcomeCancelled {
				t.Fatalf("A's session = %+v, %v; want closed cancelled", av, err)
			}
			if spy.count(sid) != 1 {
				t.Fatalf("grants revoked %d times, want 1 (in the closing transaction)", spy.count(sid))
			}
			if st := a.ob.last(t, KindState); st.body["state"] != StateClosed || st.body["outcome"] != OutcomeCancelled {
				t.Fatalf("ws.state = %+v", st.body)
			}
			if !containsAction(a.audit.actions(), "ws.close") {
				t.Fatalf("no ws.close audit: %v", a.audit.actions())
			}
		})
	}
}

// OD-F18-7: in awaiting_result the session is left to A.
func TestLateDeclineAwaitingResultUnchanged(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	before, err := a.ws.Get(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, a, testB, lateEndMail(request.KindDecline, reqID, outSeq(t, a, reqID)+1, wireTime(a.clock))); err != nil {
		t.Fatal(err)
	}
	after, err := a.ws.Get(ctx, sid)
	if err != nil || after.State != before.State || after.Seq != before.Seq {
		t.Fatalf("session changed: before %+v after %+v, %v", before, after, err)
	}
}

// A stale decline (seq not above the record's) is not applied and closes
// nothing.
func TestStaleDeclineClosesNothing(t *testing.T) {
	ctx := context.Background()
	a, _, reqID, sid := setupAcceptedSession(t)
	if err := deliver(t, a, testB, lateEndMail(request.KindDecline, reqID, outSeq(t, a, reqID), wireTime(a.clock))); err != nil {
		t.Fatal(err)
	}
	if av, err := a.ws.Get(ctx, sid); err != nil || av.State != StateOpen {
		t.Fatalf("session = %+v, %v; want open", av, err)
	}
}
