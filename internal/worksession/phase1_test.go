package worksession

import (
	"context"
	"testing"
)

// TestPhase1RequesterFallback: a ws.result whose outbox row ends
// failed/unsupported_kind (a fake Phase 1 peer that acks unsupported)
// completes B's request with the D14 part and closes B's mirror
// (Docs/protocol/work-session.md §Early complete and Phase 1 workers,
// "Phase 1 requester").
func TestPhase1RequesterFallback(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	_ = a // A never actually understands ws.* in this scenario

	result := validResult()
	result.Summary = "worked around it"
	if ok, _, err := b.ws.SubmitResult(context.Background(), testA, reqID, result); !ok || err != nil {
		t.Fatalf("SubmitResult: ok=%v err=%v", ok, err)
	}
	_ = b.ob.last(t, KindResult)

	// Simulate A being a Phase 1 daemon: it acked the mail as unsupported,
	// which the real mail.Outbox.OnAck turns into failed/unsupported_kind.
	if _, err := b.db.Exec(`UPDATE outbox SET state = 'failed', error = 'unsupported_kind' WHERE kind = ?`, KindResult); err != nil {
		t.Fatal(err)
	}

	if err := b.ws.CheckPhase1Fallback(context.Background(), testA, reqID); err != nil {
		t.Fatalf("CheckPhase1Fallback: %v", err)
	}
	bv, err := b.ws.Get(context.Background(), sid)
	if err != nil || bv.State != StateClosed || bv.Outcome != OutcomeCancelled {
		t.Fatalf("B's mirror after fallback = %+v, %v", bv, err)
	}
	brv, err := b.req.Show(context.Background(), reqID, testA)
	if err != nil || brv.State != "completed" || brv.Result == nil || brv.Result.Summary != "worked around it" {
		t.Fatalf("B's request after fallback = %+v, %v", brv, err)
	}
	if got := b.audit.actions(); !containsAction(got, "ws.close") {
		t.Errorf("audit = %v, want ws.close", got)
	}
	// No mail is sent to the Phase 1 peer as part of the fallback (it would
	// not understand ws.* anyway).
	if n := b.ob.sentCount(KindState); n != 0 {
		t.Errorf("ws.state sent to a Phase 1 peer: %d", n)
	}
}

// TestPhase1RequesterFallback_NoOpWhenSupported: without a failed
// unsupported_kind mail, the fallback does nothing.
func TestPhase1RequesterFallback_NoOpWhenSupported(t *testing.T) {
	_, b, reqID, sid := setupAcceptedSession(t)
	if err := b.ws.CheckPhase1Fallback(context.Background(), testA, reqID); err != nil {
		t.Fatal(err)
	}
	bv, err := b.ws.Get(context.Background(), sid)
	if err != nil || bv.State != StateOpen {
		t.Fatalf("session changed with no failed mail: %+v, %v", bv, err)
	}
}
