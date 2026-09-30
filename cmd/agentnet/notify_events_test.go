package main

import (
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/notify"
)

// TestNotifyEventFlagAcceptsEveryDaemonEvent (R55-120): the CLI list follows
// notify.DefaultEvents, so the debate events can be turned off.
func TestNotifyEventFlagAcceptsEveryDaemonEvent(t *testing.T) {
	for ev := range notify.DefaultEvents {
		flags := make(eventFlags)
		if err := flags.Set(ev + "=off"); err != nil {
			t.Errorf("--event %s=off: %v", ev, err)
		}
	}
	if err := make(eventFlags).Set("debate.agreed=off"); err != nil {
		t.Errorf("debate.agreed: %v", err)
	}
	if err := make(eventFlags).Set("nope=off"); err == nil {
		t.Error("unknown event accepted")
	}
}
