package notify

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// D25: session.result and session.changes are on by default, render only the
// peer name and the request title, and reach the webhook without content.

func TestSessionEventsDefaultOn(t *testing.T) {
	for _, k := range []string{EventSessionResult, EventSessionChanges} {
		if !ValidEvent(k) || !DefaultEvents[k] {
			t.Errorf("%s must be a known event, on by default", k)
		}
	}
}

func TestBuildTextSessionEvents(t *testing.T) {
	title, body := buildText(Event{Kind: EventSessionResult, PeerName: "bob\x01x", Title: "T\x01itle"})
	if title != "bob x's result is ready for your review" || body != "T itle" {
		t.Errorf("session.result = %q | %q", title, body)
	}
	title, body = buildText(Event{Kind: EventSessionChanges, PeerName: "alice", Title: "Fix it"})
	if title != "alice asked for changes" || body != "Fix it" {
		t.Errorf("session.changes = %q | %q", title, body)
	}
}

type fakeShown struct {
	mu    sync.Mutex
	texts []string
}

func (f *fakeShown) show(_ context.Context, title, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, title+"|"+body)
	return nil
}

func (f *fakeShown) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.texts...)
}

func TestSessionEventsFireOnceAndHonourToggle(t *testing.T) {
	ctx := context.Background()
	s := openSettings(t)
	if err := s.SetEvent(ctx, EventSessionChanges, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	f := &fakeShown{}
	tr := &Trigger{Settings: s, Show: f.show}
	tr.Fire(ctx, Event{Kind: EventSessionResult, PeerName: "bob", Title: "T"})
	tr.Fire(ctx, Event{Kind: EventSessionChanges, PeerName: "bob", Title: "T"})
	deadline := time.Now().Add(5 * time.Second)
	for len(f.all()) < 1 || tr.queued.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("notifications did not settle: %v", f.all())
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := f.all()
	if len(got) != 1 || got[0] != "bob's result is ready for your review|T" {
		t.Fatalf("shown = %v, want only the session.result text (session.changes is off)", got)
	}
}

func TestSessionEventPayloadIsContentFree(t *testing.T) {
	for _, kind := range []string{EventSessionResult, EventSessionChanges} {
		ev := Event{Kind: kind, PeerName: "bob", PeerFP: "FP", Type: "task", Title: "the title", RequestID: "r-1", State: "open",
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		for _, withTitle := range []bool{false, true} {
			body, err := marshalPayload(buildPayload("w-1", ev, withTitle))
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(body, &m); err != nil {
				t.Fatal(err)
			}
			if m["event"] != kind {
				t.Errorf("event = %v, want %s", m["event"], kind)
			}
			if !withTitle && strings.Contains(string(body), "the title") {
				t.Errorf("%s: title leaked with title:false: %s", kind, body)
			}
		}
	}
}
