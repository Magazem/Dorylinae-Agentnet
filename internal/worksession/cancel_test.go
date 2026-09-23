package worksession

import (
	"context"
	"testing"
)

// TestCancel_ByA: open -> closed cancelled (A's own cancel).
func TestCancel_ByA(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	v, err := a.ws.Cancel(context.Background(), sid, "changed my mind")
	if err != nil || v.State != StateClosed || v.Outcome != OutcomeCancelled {
		t.Fatalf("Cancel = %+v, %v", v, err)
	}
	deliverState(t, a, b)
	bv, err := b.ws.Get(context.Background(), sid)
	if err != nil || bv.State != StateClosed || bv.Outcome != OutcomeCancelled {
		t.Fatalf("B's mirror = %+v, %v", bv, err)
	}
	if err := deliver(t, a, testB, b.ob.last(t, "request.complete")); err != nil {
		t.Fatalf("deliver request.complete: %v", err)
	}
	brv, err := b.req.Show(context.Background(), reqID, testA)
	if err != nil || brv.State != "completed" || brv.Result != nil || brv.Note != "session cancelled" {
		t.Fatalf("B's request after cancel = %+v, %v", brv, err)
	}
}

// cancelMailFrom builds a ws.cancel body as B would send it, without needing
// a B-side submit method (2.1a has none; the CLI/IPC send path is 2.1b's).
func cancelMailFrom(from, to, reqID string, reason string, at string) sentMail {
	sid := DeriveID(to, from, reqID) // A=to, B=from
	body := map[string]any{"at": at, "request": reqID, "session": sid}
	if reason != "" {
		body["reason"] = reason
	}
	return sentMail{to: to, kind: KindCancel, body: body}
}

// TestCancel_ByB_Open: a valid ws.cancel from B is applied automatically on
// A while open (Docs/protocol/work-session.md §Cancel, "B").
func TestCancel_ByB_Open(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	sm := cancelMailFrom(testB, testA, reqID, "cannot do this", wireTime(a.clock))
	if err := deliver(t, a, testB, sm); err != nil {
		t.Fatalf("deliver ws.cancel: %v", err)
	}
	av, err := a.ws.Get(context.Background(), sid)
	if err != nil || av.State != StateClosed || av.Outcome != OutcomeCancelled {
		t.Fatalf("A after B's cancel = %+v, %v", av, err)
	}
	if got := a.audit.actions(); !containsAction(got, "ws.cancel_in") {
		t.Errorf("audit = %v, want ws.cancel_in", got)
	}
	deliverState(t, a, b)
	bv, err := b.ws.Get(context.Background(), sid)
	if err != nil || bv.State != StateClosed || bv.Outcome != OutcomeCancelled {
		t.Fatalf("B's mirror = %+v, %v", bv, err)
	}
}

// TestCancel_ByB_RefusedElsewhere: a ws.cancel from B while A's state is not
// open is refused and A re-sends its last_state.
func TestCancel_ByB_RefusedElsewhere(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	before, err := a.ws.Get(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	sm := cancelMailFrom(testB, testA, reqID, "", wireTime(a.clock))
	if err := deliver(t, a, testB, sm); err != nil {
		t.Fatalf("deliver ws.cancel: %v", err)
	}
	after, err := a.ws.Get(context.Background(), sid)
	if err != nil || after.State != before.State || after.Seq != before.Seq {
		t.Fatalf("A's row changed on a refused cancel: before=%+v after=%+v", before, after)
	}
	if got := a.audit.actions(); !containsAction(got, "ws.cancel_in") {
		t.Errorf("audit = %v, want ws.cancel_in", got)
	}
	// A re-sends last_state so B catches up (throttled to 10 min; here it is
	// the first echo since the transition, so it goes out immediately).
	a.ob.last(t, KindState)
}

// TestCancel_ByB_Orphan: a ws.cancel for a session A has never heard of is
// ignored and audited ws.orphan.
func TestCancel_ByB_Orphan(t *testing.T) {
	a := newNode(t, testA)
	unknownReq := "r-ffffffffffffffffffffffffffffffff"
	sm := cancelMailFrom(testB, testA, unknownReq, "", wireTime(a.clock))
	if err := deliver(t, a, testB, sm); err != nil {
		t.Fatalf("deliver ws.cancel: %v", err)
	}
	if got := a.audit.actions(); !containsAction(got, "ws.orphan") {
		t.Errorf("audit = %v, want ws.orphan", got)
	}
}
