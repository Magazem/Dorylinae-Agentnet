package worksession

import (
	"context"
	"errors"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// TestRequestCompleteShorthand: request_complete on an open session submits
// a result; in other session states it is bad_state
// (Docs/protocol/work-session.md, "request_complete while a session
// exists").
func TestRequestCompleteShorthand(t *testing.T) {
	t.Run("open: submits a result", func(t *testing.T) {
		a, b, reqID, sid := setupAcceptedSession(t)
		status := request.ResultPass
		v, err := b.req.Complete(context.Background(), reqID, testA, "notes here", &request.Result{Status: status})
		if err != nil {
			t.Fatalf("Complete = %+v, %v", v, err)
		}
		// The request itself is unchanged (still accepted): completion
		// happens only when the session closes.
		if v.State != "accepted" {
			t.Fatalf("request state = %q, want accepted (session not closed yet)", v.State)
		}
		// A ws.result mail went out, not a request.complete.
		sm := b.ob.last(t, KindResult)
		if sm.to != testA {
			t.Fatalf("ws.result to %q, want %q", sm.to, testA)
		}
		if err := deliver(t, a, testB, sm); err != nil {
			t.Fatalf("deliver ws.result: %v", err)
		}
		av, err := a.ws.Get(context.Background(), sid)
		if err != nil || av.State != StateAwaitingResult || av.Result == nil || av.Result.Notes != "notes here" {
			t.Fatalf("A after shorthand result = %+v, %v", av, err)
		}
	})

	t.Run("without --status defaults to n/a", func(t *testing.T) {
		a, b, reqID, _ := setupAcceptedSession(t)
		if _, err := b.req.Complete(context.Background(), reqID, testA, "", nil); err != nil {
			t.Fatal(err)
		}
		sm := b.ob.last(t, KindResult)
		status, _ := sm.body["result"].(map[string]any)["status"]
		if status != "n/a" {
			t.Fatalf("status = %v, want n/a", status)
		}
		_ = a
	})

	t.Run("bad_state once the session left open", func(t *testing.T) {
		a, b, reqID, sid := setupAcceptedSession(t)
		submitAndDeliverResult(t, a, b, reqID, validResult())
		deliverState(t, a, b) // B's mirror learns awaiting_result
		if _, err := a.ws.AcceptResult(context.Background(), sid); err != nil {
			t.Fatal(err)
		}
		deliverState(t, a, b) // B's mirror learns closed
		var bse *BadStateError
		_, err := b.req.Complete(context.Background(), reqID, testA, "", nil)
		if !errors.As(err, &bse) {
			t.Fatalf("Complete on a session not open: err = %v, want *BadStateError", err)
		}
	})

	t.Run("no session: falls back to Phase 1 complete", func(t *testing.T) {
		// A Phase 1-shaped store: Sessions is nil, so Complete behaves exactly
		// as before 2.1a.
		b := newNode(t, testB)
		a := newNode(t, testA)
		outcome, err := a.req.Submit(context.Background(), request.SubmitParams{
			From: testA, To: testB, Team: testTeam, Type: request.TypeTask, Title: "t", Brief: "What: x", Urgency: request.UrgencyNormal,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := deliver(t, b, testA, a.ob.last(t, "request")); err != nil {
			t.Fatal(err)
		}
		b.req.Sessions = nil // no worksession wiring at all
		if _, err := b.req.Accept(context.Background(), outcome.Request.ID, testA); err != nil {
			t.Fatal(err)
		}
		v, err := b.req.Complete(context.Background(), outcome.Request.ID, testA, "done", nil)
		if err != nil || v.State != "completed" {
			t.Fatalf("Complete without sessions = %+v, %v", v, err)
		}
	})
}
