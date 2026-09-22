package notify

import (
	"context"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func openSettings(t *testing.T) *Settings {
	t.Helper()
	ctx := context.Background()
	dir := testutil.TempDir(t)
	st, err := store.Open(ctx, dir+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewSettings(st.DB())
}

func TestSettingsDefaults(t *testing.T) {
	ctx := context.Background()
	s := openSettings(t)
	enabled, err := s.GetDesktopEnabled(ctx)
	if err != nil || !enabled {
		t.Fatalf("default desktop enabled = %v, %v; want true, nil", enabled, err)
	}
	events, err := s.GetEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range DefaultEvents {
		if events[k] != want {
			t.Errorf("default event %s = %v, want %v", k, events[k], want)
		}
	}
}

func TestSettingsSetDesktopEnabled(t *testing.T) {
	ctx := context.Background()
	s := openSettings(t)
	if err := s.SetDesktopEnabled(ctx, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	enabled, err := s.GetDesktopEnabled(ctx)
	if err != nil || enabled {
		t.Fatalf("after off: enabled = %v, %v; want false, nil", enabled, err)
	}
	if err := s.SetDesktopEnabled(ctx, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	enabled, err = s.GetDesktopEnabled(ctx)
	if err != nil || !enabled {
		t.Fatalf("after on: enabled = %v, %v; want true, nil", enabled, err)
	}
}

func TestSettingsSetEventLeavesOthers(t *testing.T) {
	ctx := context.Background()
	s := openSettings(t)
	if err := s.SetEvent(ctx, EventCompleted, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	events, err := s.GetEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !events[EventCompleted] {
		t.Error("request.completed should now be on")
	}
	if !events[EventReceived] {
		t.Error("request.received should still be on (default), untouched by SetEvent")
	}

	if err := s.SetEvent(ctx, EventCancelled, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	on, err := s.EventEnabled(ctx, EventCancelled)
	if err != nil || on {
		t.Fatalf("request.cancelled = %v, %v; want false, nil", on, err)
	}
	on, err = s.EventEnabled(ctx, EventCompleted)
	if err != nil || !on {
		t.Fatalf("request.completed = %v, %v; want true, nil (unaffected by the later SetEvent)", on, err)
	}
}

func TestSettingsSetEventUnknown(t *testing.T) {
	ctx := context.Background()
	s := openSettings(t)
	if err := s.SetEvent(ctx, "request.bogus", true, time.Now()); err == nil {
		t.Fatal("expected an error for an unknown event")
	}
}
