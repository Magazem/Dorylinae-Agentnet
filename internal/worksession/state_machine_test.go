package worksession

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

func alwaysQuarantine(context.Context, *sql.Tx, string, string, int) (bool, error) { return true, nil }

// TestSessionStateMachine is the 2.1a acceptance item "every transition of
// the diagram, and nothing else" (Docs/protocol/work-session.md §State
// machine), including the two OD-P2-6 (c) edges from quarantined.

func TestSessionStateMachine_OpenToAwaitingResult(t *testing.T) {
	a, b, _, sid := setupAcceptedSession(t)
	v := submitAndDeliverResult(t, a, b, session2ReqID(t, sid, a), validResult())
	if v.State != StateAwaitingResult || v.Round != 1 || v.Seq != 1 {
		t.Fatalf("A after result = %+v", v)
	}
	deliverState(t, a, b)
	bv, err := b.ws.Get(context.Background(), sid)
	if err != nil || bv.State != StateAwaitingResult || bv.Seq != 1 {
		t.Fatalf("B's mirror = %+v, %v", bv, err)
	}
}

func TestSessionStateMachine_OpenToQuarantined(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	a.ws.Quarantine = alwaysQuarantine
	v := submitAndDeliverResult(t, a, b, reqID, validResult())
	if v.State != StateQuarantined {
		t.Fatalf("A after result = %+v, want quarantined", v)
	}
	// While quarantined, A's Get still returns the result decoded (2.4 owns
	// hiding it from IPC views); the row itself keeps it until release,
	// discard or request-changes.
	if v.Result == nil {
		t.Fatal("stored result missing while quarantined")
	}
	deliverState(t, a, b)
	bv, err := b.ws.Get(context.Background(), sid)
	if err != nil || bv.State != StateQuarantined {
		t.Fatalf("B's mirror = %+v, %v", bv, err)
	}
}

func TestSessionStateMachine_AwaitingResultToClosedAccepted(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	v, err := a.ws.AcceptResult(context.Background(), sid)
	if err != nil || v.State != StateClosed || v.Outcome != OutcomeAccepted {
		t.Fatalf("AcceptResult = %+v, %v", v, err)
	}
	deliverState(t, a, b)
	bv, err := b.ws.Get(context.Background(), sid)
	if err != nil || bv.State != StateClosed || bv.Outcome != OutcomeAccepted {
		t.Fatalf("B's mirror = %+v, %v", bv, err)
	}
	// B's request completed, and the request.complete mail reached A, whose
	// own request mirror also ends completed (Docs/protocol/work-session.md
	// §Closing the request).
	if err := deliver(t, a, testB, b.ob.last(t, request.KindComplete)); err != nil {
		t.Fatalf("deliver request.complete: %v", err)
	}
	brv, err := b.req.Show(context.Background(), reqID, testA)
	if err != nil || brv.State != "completed" || brv.Result == nil || brv.Result.Status != request.ResultPass {
		t.Fatalf("B's request = %+v, %v", brv, err)
	}
	arv, err := a.req.Show(context.Background(), reqID, testB)
	if err != nil || arv.State != "completed" {
		t.Fatalf("A's request = %+v, %v", arv, err)
	}
}

func TestSessionStateMachine_AwaitingResultToOpenViaRequestChanges(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	v, err := a.ws.RequestChanges(context.Background(), sid, "please fix X")
	if err != nil || v.State != StateOpen || v.Round != 2 || v.Result != nil {
		t.Fatalf("RequestChanges = %+v, %v", v, err)
	}
	deliverState(t, a, b)
	bv, err := b.ws.Get(context.Background(), sid)
	if err != nil || bv.State != StateOpen || bv.Round != 2 || bv.Changes != "please fix X" {
		t.Fatalf("B's mirror = %+v, %v", bv, err)
	}
	// B can submit a new result for the new round.
	v2 := submitAndDeliverResult(t, a, b, reqID, validResult())
	if v2.State != StateAwaitingResult || v2.Round != 2 {
		t.Fatalf("round 2 result = %+v", v2)
	}
}

func TestSessionStateMachine_QuarantinedEdges(t *testing.T) {
	t.Run("release to awaiting_result", func(t *testing.T) {
		a, b, reqID, sid := setupAcceptedSession(t)
		a.ws.Quarantine = alwaysQuarantine
		submitAndDeliverResult(t, a, b, reqID, validResult())
		v, err := a.ws.Release(context.Background(), sid)
		if err != nil || v.State != StateAwaitingResult || !v.Released {
			t.Fatalf("Release = %+v, %v", v, err)
		}
		if _, err := a.ws.AcceptResult(context.Background(), sid); err != nil {
			t.Fatalf("AcceptResult after release: %v", err)
		}
	})

	t.Run("discard to closed cancelled, result deleted unseen (OD-P2-6c)", func(t *testing.T) {
		a, b, reqID, sid := setupAcceptedSession(t)
		a.ws.Quarantine = alwaysQuarantine
		submitAndDeliverResult(t, a, b, reqID, validResult())
		v, err := a.ws.Discard(context.Background(), sid)
		if err != nil || v.State != StateClosed || v.Outcome != OutcomeCancelled {
			t.Fatalf("Discard = %+v, %v", v, err)
		}
		if v.Result != nil {
			t.Fatal("discarded result surfaced through the view")
		}
		var raw sql.NullString
		if err := a.db.QueryRow(`SELECT result FROM work_sessions WHERE id = ?`, sid).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if raw.Valid {
			t.Fatal("discarded result still stored")
		}
		deliverState(t, a, b)
		bv, err := b.ws.Get(context.Background(), sid)
		if err != nil || bv.State != StateClosed || bv.Outcome != OutcomeCancelled {
			t.Fatalf("B's mirror = %+v, %v", bv, err)
		}
		// B learns only closed/cancelled, nothing about the content A saw.
		if bv.Result != nil {
			t.Fatal("B's mirror carries the discarded result")
		}
		if err := deliver(t, a, testB, b.ob.last(t, request.KindComplete)); err != nil {
			t.Fatalf("deliver request.complete: %v", err)
		}
		brv, err := b.req.Show(context.Background(), reqID, testA)
		if err != nil || brv.State != "completed" || brv.Result != nil || brv.Note != "session cancelled" {
			t.Fatalf("B's request after discard = %+v, %v", brv, err)
		}
	})

	t.Run("request-changes without release, result deleted unseen (OD-P2-6c)", func(t *testing.T) {
		a, b, reqID, sid := setupAcceptedSession(t)
		a.ws.Quarantine = alwaysQuarantine
		submitAndDeliverResult(t, a, b, reqID, validResult())
		v, err := a.ws.RequestChanges(context.Background(), sid, "try again")
		if err != nil || v.State != StateOpen || v.Round != 2 || v.Result != nil {
			t.Fatalf("RequestChanges from quarantined = %+v, %v", v, err)
		}
		deliverState(t, a, b)
		bv, err := b.ws.Get(context.Background(), sid)
		if err != nil || bv.State != StateOpen || bv.Round != 2 || bv.Changes != "try again" {
			t.Fatalf("B's mirror = %+v, %v", bv, err)
		}
		if bv.Result != nil {
			t.Fatal("B's mirror carries the quarantined result")
		}
		if got := a.audit.actions(); !containsAction(got, "ws.request_changes") {
			t.Errorf("audit = %v, want ws.request_changes", got)
		}
	})
}

// TestSessionStateMachine_Disallowed: every other (state, action) pair is
// refused with BadStateError, and nothing else is reachable
// (Docs/protocol/work-session.md, "Nothing else is reachable").
func TestSessionStateMachine_Disallowed(t *testing.T) {
	t.Run("AcceptResult from open", func(t *testing.T) {
		a, _, _, sid := setupAcceptedSession(t)
		assertBadState(t, func() error { _, err := a.ws.AcceptResult(context.Background(), sid); return err })
	})
	t.Run("RequestChanges from open", func(t *testing.T) {
		a, _, _, sid := setupAcceptedSession(t)
		assertBadState(t, func() error { _, err := a.ws.RequestChanges(context.Background(), sid, "x"); return err })
	})
	t.Run("Discard from open", func(t *testing.T) {
		a, _, _, sid := setupAcceptedSession(t)
		assertBadState(t, func() error { _, err := a.ws.Discard(context.Background(), sid); return err })
	})
	t.Run("Release from open", func(t *testing.T) {
		a, _, _, sid := setupAcceptedSession(t)
		assertBadState(t, func() error { _, err := a.ws.Release(context.Background(), sid); return err })
	})
	t.Run("Discard from awaiting_result", func(t *testing.T) {
		a, b, reqID, sid := setupAcceptedSession(t)
		submitAndDeliverResult(t, a, b, reqID, validResult())
		assertBadState(t, func() error { _, err := a.ws.Discard(context.Background(), sid); return err })
	})
	t.Run("Release from awaiting_result", func(t *testing.T) {
		a, b, reqID, sid := setupAcceptedSession(t)
		submitAndDeliverResult(t, a, b, reqID, validResult())
		assertBadState(t, func() error { _, err := a.ws.Release(context.Background(), sid); return err })
	})
	t.Run("Cancel from awaiting_result", func(t *testing.T) {
		a, b, reqID, sid := setupAcceptedSession(t)
		submitAndDeliverResult(t, a, b, reqID, validResult())
		assertBadState(t, func() error { _, err := a.ws.Cancel(context.Background(), sid, ""); return err })
	})
	t.Run("Cancel from quarantined", func(t *testing.T) {
		a, b, reqID, sid := setupAcceptedSession(t)
		a.ws.Quarantine = alwaysQuarantine
		submitAndDeliverResult(t, a, b, reqID, validResult())
		assertBadState(t, func() error { _, err := a.ws.Cancel(context.Background(), sid, ""); return err })
	})
	t.Run("AcceptResult from quarantined without release", func(t *testing.T) {
		a, b, reqID, sid := setupAcceptedSession(t)
		a.ws.Quarantine = alwaysQuarantine
		submitAndDeliverResult(t, a, b, reqID, validResult())
		assertBadState(t, func() error { _, err := a.ws.AcceptResult(context.Background(), sid); return err })
	})
	t.Run("every action from closed", func(t *testing.T) {
		a, b, reqID, sid := setupAcceptedSession(t)
		submitAndDeliverResult(t, a, b, reqID, validResult())
		if _, err := a.ws.AcceptResult(context.Background(), sid); err != nil {
			t.Fatal(err)
		}
		assertBadState(t, func() error { _, err := a.ws.AcceptResult(context.Background(), sid); return err })
		assertBadState(t, func() error { _, err := a.ws.RequestChanges(context.Background(), sid, "x"); return err })
		assertBadState(t, func() error { _, err := a.ws.Discard(context.Background(), sid); return err })
		assertBadState(t, func() error { _, err := a.ws.Cancel(context.Background(), sid, ""); return err })
		assertBadState(t, func() error { _, err := a.ws.Release(context.Background(), sid); return err })
	})
	t.Run("B cannot call A-only methods", func(t *testing.T) {
		_, b, _, sid := setupAcceptedSession(t)
		if _, err := b.ws.AcceptResult(context.Background(), sid); !errors.Is(err, ErrNotRequester) {
			t.Errorf("AcceptResult from B: err = %v, want ErrNotRequester", err)
		}
		if _, err := b.ws.Cancel(context.Background(), sid, ""); !errors.Is(err, ErrNotRequester) {
			t.Errorf("Cancel from B: err = %v, want ErrNotRequester", err)
		}
	})
}

func assertBadState(t *testing.T, run func() error) {
	t.Helper()
	err := run()
	var bse *BadStateError
	if !errors.As(err, &bse) {
		t.Fatalf("err = %v, want *BadStateError", err)
	}
}

func containsAction(actions []string, want string) bool {
	for _, a := range actions {
		if a == want {
			return true
		}
	}
	return false
}

// session2ReqID is a helper for tests that only have the session id: since
// both are derived from the same (peer, request) pair, and A's node knows
// its own out row, we simply keep requestID alongside sid in
// setupAcceptedSession for every other test; this one recovers it from the
// stored row for the one caller that only has sid in scope.
func session2ReqID(t *testing.T, sid string, a *node) string {
	t.Helper()
	v, err := a.ws.Get(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	return v.RequestID
}
