package request

// Ticket 1.8a unit acceptance for the notification hook wired into the mail
// kinds' After callbacks (Docs/protocol/notify.md §Triggers). Uses the
// newTestStore/deliverRequest/deliverMirror/storePendingIn helpers of
// store_test.go and lifecycle_test.go.

import (
	"context"
	"sync"
	"testing"
	"time"
)

// capturedNotify records every Store.Notify call, safe for concurrent use
// (the mail receiver's real After hooks run on one goroutine, but Trigger.Fire
// itself is async in production; here Notify is called synchronously).
type capturedNotify struct {
	mu     sync.Mutex
	events []string
	info   []NotifyInfo
}

func (c *capturedNotify) fn() NotifyFunc {
	return func(_ context.Context, event string, info NotifyInfo) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.events = append(c.events, event)
		c.info = append(c.info, info)
	}
}

func (c *capturedNotify) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.events...)
}

func TestNotifyFiresOnNewPendingRequest(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	c := &capturedNotify{}
	s.Notify = c.fn()
	if err := deliverRequest(t, s, receivedRequest(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := c.all(); len(got) != 1 || got[0] != EventReceived {
		t.Fatalf("events = %v, want [%s]", got, EventReceived)
	}
	info := c.info[0]
	if info.Peer != testFrom || info.Type != TypeTask || info.Urgency != UrgencyNormal || info.Title != "A valid title" {
		t.Errorf("info = %+v", info)
	}
}

func TestNotifyDoesNotFireForDuplicate(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	req := receivedRequest()
	if err := deliverRequest(t, s, req, time.Now()); err != nil {
		t.Fatal(err)
	}
	c := &capturedNotify{}
	s.Notify = c.fn()
	if err := deliverRequest(t, s, req, time.Now()); err != nil { // exact repeat: dup
		t.Fatal(err)
	}
	if got := c.all(); len(got) != 0 {
		t.Errorf("events = %v, want none for a duplicate", got)
	}
}

func TestNotifyDoesNotFireForConflict(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	req := receivedRequest()
	if err := deliverRequest(t, s, req, time.Now()); err != nil {
		t.Fatal(err)
	}
	c := &capturedNotify{}
	s.Notify = c.fn()
	changed := *req
	changed.Title = "a different title"
	if err := deliverRequest(t, s, &changed, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := c.all(); len(got) != 0 {
		t.Errorf("events = %v, want none for a body-hash conflict", got)
	}
}

func TestNotifyDoesNotFireForAutoDecline(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{inactive: true}) // unknown_team
	c := &capturedNotify{}
	s.Notify = c.fn()
	if err := deliverRequest(t, s, receivedRequest(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := c.all(); len(got) != 0 {
		t.Errorf("events = %v, want none for an auto-decline", got)
	}
}

func TestNotifyDoesNotFireForTombstonedRequest(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	req := receivedRequest()
	if _, err := s.DB.Exec(`INSERT INTO request_cancels (peer, id, reason, received_at) VALUES (?, ?, ?, ?)`,
		req.From, req.ID, "already cancelled", storeTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	c := &capturedNotify{}
	s.Notify = c.fn()
	if err := deliverRequest(t, s, req, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := c.all(); len(got) != 0 {
		t.Errorf("events = %v, want none: stored cancelled via a tombstone is not a notification", got)
	}
}

func TestNotifyMirrorEventsFireOnRealTransition(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	ctx := context.Background()
	c := &capturedNotify{}
	s.Notify = c.fn()

	out, err := s.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	id := out.Request.ID
	now := time.Now()

	acceptBody := map[string]any{"at": wireTime(now), "request": id, "seq": 1}
	if err := deliverMirror(t, s, KindAccept, testTo, now, acceptBody); err != nil {
		t.Fatal(err)
	}
	if got := c.all(); len(got) != 1 || got[0] != EventAccepted {
		t.Fatalf("events after accept = %v, want [%s]", got, EventAccepted)
	}
	if info := c.info[0]; info.Peer != testTo || info.Type != TypeTask || info.Title != "t" {
		t.Errorf("accept info = %+v", info)
	}
}

func TestNotifyDeferredCarriesUntil(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	ctx := context.Background()
	c := &capturedNotify{}
	s.Notify = c.fn()

	out, err := s.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	id := out.Request.ID
	now := time.Now()
	until := now.Add(48 * time.Hour).UTC().Truncate(time.Second)
	body := map[string]any{"at": wireTime(now), "request": id, "seq": 1, "until": wireTime(until)}
	if err := deliverMirror(t, s, KindDefer, testTo, now, body); err != nil {
		t.Fatal(err)
	}
	if got := c.all(); len(got) != 1 || got[0] != EventDeferred {
		t.Fatalf("events = %v, want [%s]", got, EventDeferred)
	}
	if !c.info[0].Until.Equal(until) {
		t.Errorf("until = %v, want %v", c.info[0].Until, until)
	}
}

func TestNotifyCompletedCarriesResultStatusOnly(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	ctx := context.Background()
	c := &capturedNotify{}
	s.Notify = c.fn()

	out, err := s.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	id := out.Request.ID
	now := time.Now()
	body := map[string]any{
		"at": wireTime(now), "request": id, "seq": 1,
		"result": map[string]any{"status": "pass", "summary": "looks good", "output": "secret output"},
	}
	if err := deliverMirror(t, s, KindComplete, testTo, now, body); err != nil {
		t.Fatal(err)
	}
	if got := c.all(); len(got) != 1 || got[0] != EventCompleted {
		t.Fatalf("events = %v, want [%s]", got, EventCompleted)
	}
	info := c.info[0]
	if !info.HasResult || info.ResultStatus != "pass" {
		t.Fatalf("info = %+v, want HasResult=true ResultStatus=pass", info)
	}
	// D14: the notify path only ever carries the status, never summary/output/artifacts.
}

// TestNotifyMirrorCancelledDoesNotFire: the sender-side mirror of the
// recipient's cancel acknowledgement (KindCancelled) is not a notification
// trigger; request.cancelled fires only at the recipient (see
// TestNotifyCancelFiresOnlyOnCancelled).
func TestNotifyMirrorCancelledDoesNotFire(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	ctx := context.Background()
	c := &capturedNotify{}
	s.Notify = c.fn()

	out, err := s.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	id := out.Request.ID
	now := time.Now()
	body := map[string]any{"at": wireTime(now), "request": id, "seq": 1}
	if err := deliverMirror(t, s, KindCancelled, testTo, now, body); err != nil {
		t.Fatal(err)
	}
	if got := c.all(); len(got) != 0 {
		t.Errorf("events = %v, want none for the sender-side cancel mirror", got)
	}
}

func TestNotifyMirrorOrphanDoesNotFire(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	c := &capturedNotify{}
	s.Notify = c.fn()
	now := time.Now()
	body := map[string]any{"at": wireTime(now), "request": NewID(), "seq": 1}
	if err := deliverMirror(t, s, KindAccept, testTo, now, body); err != nil {
		t.Fatal(err)
	}
	if got := c.all(); len(got) != 0 {
		t.Errorf("events = %v, want none for an orphan mirror", got)
	}
}

// TestNotifyCancelFiresOnlyOnCancelled is request.cancelled
// (Docs/protocol/notify.md §Triggers): the recipient commits a pending or
// deferred request as cancelled by its sender. Not for a cancel that arrives
// before its request (early), and not for a refused or duplicate cancel.
func TestNotifyCancelFiresOnlyOnCancelled(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	id := storePendingIn(t, s)
	c := &capturedNotify{}
	s.Notify = c.fn()
	now := time.Now()
	body := map[string]any{"at": wireTime(now), "reason": "no longer needed", "request": id}
	if err := deliverMirror(t, s, KindCancel, testFrom, now, body); err != nil {
		t.Fatal(err)
	}
	if got := c.all(); len(got) != 1 || got[0] != EventCancelled {
		t.Fatalf("events after cancel = %v, want [%s]", got, EventCancelled)
	}
	if info := c.info[0]; info.Peer != testFrom || info.Type != TypeTask {
		t.Errorf("cancel info = %+v", info)
	}

	// A second cancel of the same (now cancelled) request is a duplicate: no event.
	body2 := map[string]any{"at": wireTime(now.Add(time.Second)), "reason": "again", "request": id}
	if err := deliverMirror(t, s, KindCancel, testFrom, now.Add(time.Second), body2); err != nil {
		t.Fatal(err)
	}
	if got := c.all(); len(got) != 1 {
		t.Errorf("events after duplicate cancel = %v, want still 1", got)
	}
}

func TestNotifyCancelEarlyDoesNotFire(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	c := &capturedNotify{}
	s.Notify = c.fn()
	now := time.Now()
	body := map[string]any{"at": wireTime(now), "request": NewID()}
	if err := deliverMirror(t, s, KindCancel, testFrom, now, body); err != nil {
		t.Fatal(err)
	}
	if got := c.all(); len(got) != 0 {
		t.Errorf("events = %v, want none for an early cancel (no row yet)", got)
	}
}

// TestNotifyNilStoreNotifyIsSafe: a nil Store.Notify (the daemon default when
// no Trigger is wired) never panics.
func TestNotifyNilStoreNotifyIsSafe(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	s.Notify = nil
	if err := deliverRequest(t, s, receivedRequest(), time.Now()); err != nil {
		t.Fatal(err)
	}
}
