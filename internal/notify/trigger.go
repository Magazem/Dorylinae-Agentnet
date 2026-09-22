package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Code-point limits (Docs/protocol/notify.md §Text and sanitising).
const (
	maxTitleCodePoints = 80
	maxNameCodePoints  = 40
)

// AuditSink is the part of audit.Log the trigger needs.
type AuditSink interface {
	Append(ctx context.Context, actor, action string, detail any) error
}

// ShowFunc matches Desktop.Show, so callers and tests can substitute a fake.
type ShowFunc func(ctx context.Context, title, body string) error

// Event is one fired lifecycle event (Docs/protocol/notify.md §Triggers,
// §Text and sanitising). PeerName and Title are peer-supplied and are
// cleaned by Fire before use; callers must not clean them first (Clean is
// not idempotent-sensitive, but truncation would double-apply).
type Event struct {
	Kind         string    // one of the Event* constants in settings.go
	PeerName     string    // the peer's local name
	Type         string    // review, task or question
	Urgency      string    // low, normal, high or blocking (EventReceived only)
	Title        string    // the request title
	Until        time.Time // EventDeferred only
	ResultStatus string    // EventCompleted only, with a result: pass, fail, partial or n/a
	HasResult    bool      // EventCompleted only
}

// Trigger dispatches lifecycle events to the desktop channel
// (Docs/protocol/notify.md §Desktop). Fire is meant to be called from a mail
// kind's After hook: it starts a goroutine and returns at once, so it never
// blocks the mail receiver.
type Trigger struct {
	Settings *Settings
	Show     ShowFunc  // nil uses Desktop{}.Show
	Audit    AuditSink // notify.fail; may be nil
	Log      *slog.Logger
	Now      func() time.Time // defaults to time.Now

	mu       sync.Mutex
	lastFail time.Time
}

func (t *Trigger) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Trigger) log() *slog.Logger {
	if t.Log != nil {
		return t.Log
	}
	return slog.Default()
}

func (t *Trigger) show() ShowFunc {
	if t.Show != nil {
		return t.Show
	}
	return Desktop{}.Show
}

// Fire checks the event and desktop settings and, if both are on, shows the
// notification on a new goroutine. It never blocks the caller. ctx is the
// mail receiver's long-lived context (Docs/protocol/mail.md): it outlives
// the single envelope, so the goroutine may keep using it.
func (t *Trigger) Fire(ctx context.Context, ev Event) {
	go t.fire(ctx, ev)
}

func (t *Trigger) fire(ctx context.Context, ev Event) {
	if t.Settings != nil {
		on, err := t.Settings.EventEnabled(ctx, ev.Kind)
		if err != nil || !on {
			return
		}
		enabled, err := t.Settings.GetDesktopEnabled(ctx)
		if err != nil || !enabled {
			return
		}
	}
	title, body := buildText(ev)
	if err := t.show()(ctx, title, body); err != nil {
		t.reportFail(ctx, err)
	}
}

// reportFail audits a desktop failure at most once per hour
// (Docs/protocol/notify.md §Desktop).
func (t *Trigger) reportFail(ctx context.Context, cause error) {
	t.mu.Lock()
	now := t.now()
	throttled := !t.lastFail.IsZero() && now.Sub(t.lastFail) < time.Hour
	if !throttled {
		t.lastFail = now
	}
	t.mu.Unlock()
	t.log().Warn("notify: desktop failed", "event", "notify_desktop_fail", "error", cause)
	if throttled || t.Audit == nil {
		return
	}
	_ = t.Audit.Append(ctx, "daemon", "notify.fail", map[string]string{"channel": "desktop", "error": cause.Error()})
}

// buildText renders the desktop title and body for ev
// (Docs/protocol/notify.md §Text and sanitising).
func buildText(ev Event) (title, body string) {
	name := Clean(ev.PeerName, maxNameCodePoints)
	body = Clean(ev.Title, maxTitleCodePoints)
	switch ev.Kind {
	case EventReceived:
		title = fmt.Sprintf("%s %s request from %s", capitalize(ev.Urgency), ev.Type, name)
	case EventAccepted:
		title = fmt.Sprintf("%s accepted your %s request", name, ev.Type)
	case EventDeclined:
		title = fmt.Sprintf("%s declined your %s request", name, ev.Type)
	case EventDeferred:
		title = fmt.Sprintf("%s deferred your %s request until %s", name, ev.Type, ev.Until.Local().Format("2006-01-02 15:04"))
	case EventCompleted:
		title = fmt.Sprintf("%s completed your %s request", name, ev.Type)
		if ev.HasResult && ev.ResultStatus != "" {
			title += fmt.Sprintf(" (%s)", ev.ResultStatus)
		}
	case EventCancelled:
		title = fmt.Sprintf("%s cancelled their %s request", name, ev.Type)
	}
	return title, body
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
