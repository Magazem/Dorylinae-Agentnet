package worksession

import (
	"context"
	"testing"
	"time"
)

// R55-114: B's mirror is awaiting_result, B cancels, A refuses and echoes its
// last_state: B's view shows cancel "refused", and a second ws_cancel is not
// a duplicate. A new round clears the mark.
func TestCancelRefusedMark(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	deliverState(t, a, b) // B: awaiting_result

	if _, _, dup, err := b.ws.SubmitCancel(ctx, sid, "", ByAgent); err != nil || dup {
		t.Fatalf("B's cancel: dup %v, %v", dup, err)
	}
	// A refuses it and re-sends last_state (past the 10-minute rule).
	a.clock = a.clock.Add(11 * time.Minute)
	if err := deliver(t, a, testB, b.ob.last(t, KindCancel)); err != nil {
		t.Fatal(err)
	}
	deliverState(t, a, b) // the echo, same seq
	bv, err := b.ws.Get(ctx, sid)
	if err != nil || bv.Cancel != "refused" || bv.State != StateAwaitingResult {
		t.Fatalf("B's view = %+v, %v; want cancel refused", bv, err)
	}
	if _, mailID, dup, err := b.ws.SubmitCancel(ctx, sid, "", ByAgent); err != nil || dup || mailID == "" {
		t.Fatalf("second cancel after refused: mail %q dup %v, %v; want a new mail", mailID, dup, err)
	}
	b.ob.last(t, KindCancel)

	// A new ws.state open (round 2) clears the mark.
	if _, err := a.ws.RequestChanges(ctx, sid, "again"); err != nil {
		t.Fatal(err)
	}
	deliverState(t, a, b)
	if bv, err := b.ws.Get(ctx, sid); err != nil || bv.Cancel != "" || bv.Round != 2 {
		t.Fatalf("B's view after round 2 = %+v, %v; want cancel absent", bv, err)
	}
}

// Review 69b F4: a reordered older ws.state (seq 2, awaiting_result) that
// arrives after seq 3 (open, round 2) does not mark a live cancel refused.
func TestCancelMarkIgnoresReorderedState(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	older := a.ob.last(t, KindState) // awaiting_result, held back (reordered)
	if _, err := a.ws.RequestChanges(ctx, sid, "again"); err != nil {
		t.Fatal(err)
	}
	deliverState(t, a, b) // the newer open, round 2
	if _, _, _, err := b.ws.SubmitCancel(ctx, sid, "", ByAgent); err != nil {
		t.Fatal(err)
	}
	bv, err := b.ws.Get(ctx, sid)
	if err != nil || bv.Cancel != "requested" {
		t.Fatalf("B's view = %+v, %v; want cancel requested", bv, err)
	}
	if err := deliver(t, b, testA, older); err != nil {
		t.Fatal(err)
	}
	if bv, err := b.ws.Get(ctx, sid); err != nil || bv.Cancel != "requested" || bv.State != StateOpen {
		t.Fatalf("B's view after a reordered older state = %+v, %v; want cancel still requested", bv, err)
	}
}
