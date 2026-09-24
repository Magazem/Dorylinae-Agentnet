package notify

import (
	"context"
	"sync"
	"testing"
	"time"
)

// D22 (review 36 L5): device.linked is a known event, on by default, and its
// text is content-free: the cleaned peer name and its role, no body.
func TestDeviceLinkedEvent(t *testing.T) {
	if !ValidEvent(EventDeviceLinked) || !DefaultEvents[EventDeviceLinked] {
		t.Fatal("device.linked must be a known event, on by default")
	}
	title, body := buildText(Event{Kind: EventDeviceLinked, PeerName: "desk\u202etop\nbox", Type: "helper"})
	if title != "desk top box is now linked as your helper" || body != "" {
		t.Fatalf("title %q, body %q", title, body)
	}
}

// The event reaches the desktop, and can be switched off.
func TestDeviceLinkedFiresAndToggles(t *testing.T) {
	ctx := context.Background()
	s := openSettings(t)
	var mu sync.Mutex
	var titles []string
	tr := &Trigger{Settings: s, Show: func(_ context.Context, title, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		titles = append(titles, title)
		return nil
	}}
	tr.Fire(ctx, Event{Kind: EventDeviceLinked, PeerName: "laptop", Type: "controller"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(titles)
		mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("device.linked was not shown")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := s.SetEvent(ctx, EventDeviceLinked, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	tr.Fire(ctx, Event{Kind: EventDeviceLinked, PeerName: "laptop", Type: "controller"})
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(titles) != 1 || titles[0] != "laptop is now linked as your controller" {
		t.Fatalf("titles = %q", titles)
	}
}
