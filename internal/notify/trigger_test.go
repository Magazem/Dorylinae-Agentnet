package notify

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBuildText(t *testing.T) {
	cases := []struct {
		name      string
		ev        Event
		wantTitle string
		wantBody  string
	}{
		{
			"received", Event{Kind: EventReceived, PeerName: "bob", Type: "review", Urgency: "high", Title: "Fix the thing"},
			"High review request from bob", "Fix the thing",
		},
		{
			"accepted", Event{Kind: EventAccepted, PeerName: "bob", Type: "task", Title: "Do the thing"},
			"bob accepted your task request", "Do the thing",
		},
		{
			"declined", Event{Kind: EventDeclined, PeerName: "bob", Type: "question", Title: "Q"},
			"bob declined your question request", "Q",
		},
		{
			"deferred", Event{Kind: EventDeferred, PeerName: "bob", Type: "review", Title: "T", Until: time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC)},
			"bob deferred your review request until " + time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC).Local().Format("2006-01-02 15:04"), "T",
		},
		{
			"completed no result", Event{Kind: EventCompleted, PeerName: "bob", Type: "task", Title: "T"},
			"bob completed your task request", "T",
		},
		{
			"completed with result", Event{Kind: EventCompleted, PeerName: "bob", Type: "task", Title: "T", HasResult: true, ResultStatus: "pass"},
			"bob completed your task request (pass)", "T",
		},
		{
			"cancelled", Event{Kind: EventCancelled, PeerName: "bob", Type: "review", Title: "T"},
			"bob cancelled their review request", "T",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			title, body := buildText(c.ev)
			if title != c.wantTitle {
				t.Errorf("title = %q, want %q", title, c.wantTitle)
			}
			if body != c.wantBody {
				t.Errorf("body = %q, want %q", body, c.wantBody)
			}
		})
	}
}

// TestBuildTextCompletedNeverShowsResultDetail is D14: only status may
// appear, never a summary, exit code, output or artifacts
// (Docs/protocol/notify.md §Text and sanitising).
func TestBuildTextCompletedNeverShowsResultDetail(t *testing.T) {
	title, body := buildText(Event{
		Kind: EventCompleted, PeerName: "bob", Type: "task", Title: "T",
		HasResult: true, ResultStatus: "fail",
	})
	if title != "bob completed your task request (fail)" {
		t.Errorf("title = %q", title)
	}
	if body != "T" {
		t.Errorf("body = %q, want only the title", body)
	}
}

func TestBuildTextSanitisesPeerAndTitle(t *testing.T) {
	title, body := buildText(Event{Kind: EventAccepted, PeerName: "bob\x01evil", Type: "task", Title: "T\x01itle"})
	if title != "bob evil accepted your task request" {
		t.Errorf("title = %q", title)
	}
	if body != "T itle" {
		t.Errorf("body = %q", body)
	}
}

func TestTriggerFireHonoursEventToggle(t *testing.T) {
	ctx := context.Background()
	s := openSettings(t)
	if err := s.SetEvent(ctx, EventCompleted, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	shown := make(chan struct{}, 1)
	tr := &Trigger{Settings: s, Show: func(context.Context, string, string) error {
		shown <- struct{}{}
		return nil
	}}
	tr.Fire(ctx, Event{Kind: EventCompleted, PeerName: "bob", Type: "task", Title: "T"})
	select {
	case <-shown:
		t.Fatal("Show was called for a disabled event")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTriggerFireHonoursDesktopOff(t *testing.T) {
	ctx := context.Background()
	s := openSettings(t)
	if err := s.SetDesktopEnabled(ctx, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	shown := make(chan struct{}, 1)
	tr := &Trigger{Settings: s, Show: func(context.Context, string, string) error {
		shown <- struct{}{}
		return nil
	}}
	tr.Fire(ctx, Event{Kind: EventReceived, PeerName: "bob", Type: "task", Title: "T"})
	select {
	case <-shown:
		t.Fatal("Show was called while desktop notifications are off")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestTriggerFireShowsWithin5s is the 1.8a e2e acceptance shape with a fake
// Desktop: Fire must not block the caller, and the notification must arrive
// well inside 5 s (Docs/protocol/notify.md §Triggers "Latency").
func TestTriggerFireShowsWithin5s(t *testing.T) {
	ctx := context.Background()
	s := openSettings(t)
	shown := make(chan [2]string, 1)
	tr := &Trigger{Settings: s, Show: func(_ context.Context, title, body string) error {
		shown <- [2]string{title, body}
		return nil
	}}
	start := time.Now()
	tr.Fire(ctx, Event{Kind: EventReceived, PeerName: "bob", Type: "review", Urgency: "normal", Title: "Fix it"})
	select {
	case got := <-shown:
		if time.Since(start) > 5*time.Second {
			t.Fatalf("notification arrived after %s, want < 5s", time.Since(start))
		}
		if got[0] != "Normal review request from bob" || got[1] != "Fix it" {
			t.Errorf("got %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("notification did not arrive within 5s")
	}
}

func TestTriggerReportsFailAtMostOncePerHour(t *testing.T) {
	s := openSettings(t)
	var audited int
	audit := auditFunc(func(context.Context, string, string, any) error {
		audited++
		return nil
	})
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tr := &Trigger{
		Settings: s, Audit: audit, Now: func() time.Time { return now },
		Show: func(context.Context, string, string) error { return errors.New("boom") },
	}
	// fire is synchronous (Fire is the goroutine wrapper), so no
	// synchronization is needed between calls.
	tr.fire(context.Background(), Event{Kind: EventReceived, PeerName: "bob", Type: "task", Title: "T"})
	if audited != 1 {
		t.Fatalf("audited = %d after first failure, want 1", audited)
	}
	tr.fire(context.Background(), Event{Kind: EventReceived, PeerName: "bob", Type: "task", Title: "T"})
	if audited != 1 {
		t.Fatalf("audited = %d after second failure within the hour, want 1 (throttled)", audited)
	}
	now = now.Add(time.Hour + time.Second)
	tr.fire(context.Background(), Event{Kind: EventReceived, PeerName: "bob", Type: "task", Title: "T"})
	if audited != 2 {
		t.Fatalf("audited = %d after an hour, want 2", audited)
	}
}

type auditFunc func(ctx context.Context, actor, action string, detail any) error

func (f auditFunc) Append(ctx context.Context, actor, action string, detail any) error {
	return f(ctx, actor, action, detail)
}
