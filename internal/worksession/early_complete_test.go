package worksession

import (
	"context"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// completeMailFrom builds a request.complete body as B would send it early
// (before its session for the request is closed).
func completeMailFrom(reqID string, seq int, note string, result map[string]any, at string) sentMail {
	body := map[string]any{"at": at, "request": reqID, "seq": seq}
	if note != "" {
		body["note"] = note
	}
	if result != nil {
		body["result"] = result
	}
	return sentMail{kind: request.KindComplete, body: body}
}

// TestEarlyComplete_ClosesOpenSession: a request.complete applied on A while
// the session is open completes the request and closes the session
// cancelled (Docs/protocol/work-session.md §Early complete and Phase 1
// workers). Before grants exist (2.1a), the quarantine rule never holds, so
// the result is kept.
func TestEarlyComplete_ClosesOpenSession(t *testing.T) {
	a, _, reqID, sid := setupAcceptedSession(t)
	sm := completeMailFrom(reqID, 2, "done early", map[string]any{"status": "pass"}, wireTime(a.clock))
	if err := deliver(t, a, testB, sm); err != nil {
		t.Fatalf("deliver request.complete: %v", err)
	}
	av, err := a.ws.Get(context.Background(), sid)
	if err != nil || av.State != StateClosed || av.Outcome != OutcomeCancelled {
		t.Fatalf("A's session after early complete = %+v, %v", av, err)
	}
	arv, err := a.req.Show(context.Background(), reqID, testB)
	if err != nil || arv.State != "completed" || arv.Note != "done early" || arv.Result == nil || arv.Result.Status != "pass" {
		t.Fatalf("A's request after early complete = %+v, %v", arv, err)
	}
	// A ws.state (closed/cancelled) went to B, ending the session on both sides.
	sm2 := a.ob.last(t, KindState)
	if sm2.body["state"] != StateClosed || sm2.body["outcome"] != OutcomeCancelled {
		t.Fatalf("ws.state after early complete = %+v", sm2.body)
	}
}

// TestEarlyComplete_AwaitingResultLeftUnchanged: an early complete while the
// session is awaiting_result or quarantined (only a misbehaving Phase 2
// worker can cause this) leaves the session to A untouched.
func TestEarlyComplete_AwaitingResultLeftUnchanged(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	before, err := a.ws.Get(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	sm := completeMailFrom(reqID, 2, "", map[string]any{"status": "pass"}, wireTime(a.clock))
	if err := deliver(t, a, testB, sm); err != nil {
		t.Fatalf("deliver request.complete: %v", err)
	}
	after, err := a.ws.Get(context.Background(), sid)
	if err != nil || after.State != before.State || after.Seq != before.Seq {
		t.Fatalf("session changed by an early complete while awaiting_result: before=%+v after=%+v", before, after)
	}
	// The request mirror still applies Phase 1's rule (completed, by seq),
	// with the content kept since the quarantine rule cannot hold yet.
	arv, err := a.req.Show(context.Background(), reqID, testB)
	if err != nil || arv.State != "completed" {
		t.Fatalf("A's request = %+v, %v", arv, err)
	}
}
