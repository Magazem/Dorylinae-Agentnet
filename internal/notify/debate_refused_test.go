package notify

import (
	"context"
	"testing"
	"time"
)

// R55-F29 test 8 (R55-126): debate.refused has a neutral title, is on by
// default, and stays on for a user who turned debate.broken off before it
// existed (the stored JSON merges over the defaults).
func TestDebateRefusedEvent(t *testing.T) {
	if EventDebateRefused != "debate.refused" || !ValidEvent(EventDebateRefused) || !DefaultEvents[EventDebateRefused] {
		t.Fatalf("debate.refused: valid %v, default %v", ValidEvent(EventDebateRefused), DefaultEvents[EventDebateRefused])
	}
	got := titleLine(Event{Kind: EventDebateRefused, PeerName: "Bob"})
	if want := "Debate with Bob stopped: the two daemons' records of the Decision differ (see agentnet log)"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}

	ctx := context.Background()
	s := openSettings(t)
	if err := s.SetEvent(ctx, EventDebateBroken, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	on, err := s.EventEnabled(ctx, EventDebateRefused)
	if err != nil || !on {
		t.Fatalf("debate.refused enabled = %v, %v; want true", on, err)
	}
}
