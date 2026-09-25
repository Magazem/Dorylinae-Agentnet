package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
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
	PeerFP       string    // the peer's fingerprint (webhook payload only)
	Type         string    // review, task or question
	Urgency      string    // low, normal, high or blocking (EventReceived only)
	Title        string    // the request title
	Until        time.Time // EventDeferred only
	ResultStatus string    // EventCompleted only, with a result: pass, fail, partial or n/a
	HasResult    bool      // EventCompleted only
	RequestID    string    // webhook payload only
	Session      string    // webhook payload only (debate events: Docs/protocol/debate.md §Notifications)
	State        string    // the request's state after this event; webhook payload only
	TeamID       string    // webhook payload only; empty when not a team request
	TeamName     string    // webhook payload only
	CreatedAt    time.Time // when the event happened; webhook payload's "ts" and id timestamp
}

// Trigger dispatches lifecycle events to the desktop channel
// (Docs/protocol/notify.md §Desktop). Fire is meant to be called from a mail
// kind's After hook: it starts a goroutine and returns at once, so it never
// blocks the mail receiver.
type Trigger struct {
	Settings *Settings
	Show     ShowFunc  // nil uses Desktop{}.Show
	Webhook  *Webhook  // nil disables the webhook channel (1.8b)
	Audit    AuditSink // notify.fail; may be nil
	Log      *slog.Logger
	Now      func() time.Time // defaults to time.Now

	mu       sync.Mutex
	lastFail time.Time
	lastDrop time.Time

	// queued counts events accepted by Fire and not yet finished; showMu
	// makes those events show one at a time, so a peer flooding requests
	// runs at most one notifier process and holds at most maxQueued
	// goroutines.
	queued atomic.Int32
	showMu sync.Mutex
}

// maxQueued bounds the events waiting for or running Show. Beyond it, Fire
// drops the event: at a 3 s timeout per call, a longer queue could not be
// shown within the 5 s latency anyway.
const maxQueued = 16

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
// the single envelope, so the goroutine may keep using it. Events are shown
// one at a time; when maxQueued are already pending, the event is dropped
// and a warning is logged at most once a minute.
func (t *Trigger) Fire(ctx context.Context, ev Event) {
	if t.queued.Add(1) > maxQueued {
		t.queued.Add(-1)
		t.reportDrop(ev.Kind)
		return
	}
	go func() {
		defer t.queued.Add(-1)
		t.showMu.Lock()
		defer t.showMu.Unlock()
		t.fire(ctx, ev)
	}()
}

func (t *Trigger) reportDrop(kind string) {
	t.mu.Lock()
	now := t.now()
	throttled := !t.lastDrop.IsZero() && now.Sub(t.lastDrop) < time.Minute
	if !throttled {
		t.lastDrop = now
	}
	t.mu.Unlock()
	if !throttled {
		t.log().Warn("notify: queue full, dropping notification", "event", "notify_desktop_drop", "kind", kind)
	}
}

func (t *Trigger) fire(ctx context.Context, ev Event) {
	if t.Settings != nil {
		on, err := t.Settings.EventEnabled(ctx, ev.Kind)
		if err != nil || !on {
			return
		}
	}
	t.fireDesktop(ctx, ev)
	if ev.Kind != EventDeviceLinked { // local device news only, never to a webhook
		t.fireWebhook(ctx, ev)
	}
}

func (t *Trigger) fireDesktop(ctx context.Context, ev Event) {
	if t.Settings != nil {
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

func (t *Trigger) fireWebhook(ctx context.Context, ev Event) {
	if t.Webhook == nil {
		return
	}
	if err := t.Webhook.Enqueue(ctx, ev); err != nil {
		t.log().Warn("notify: webhook enqueue failed", "event", "notify_webhook_enqueue_fail", "error", err)
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
	_ = t.Audit.Append(ctx, "daemon", "notify.fail", map[string]string{"channel": "desktop", "code": "desktop_failed"})
}

// buildText renders the desktop title and body for ev
// (Docs/protocol/notify.md §Text and sanitising).
func buildText(ev Event) (title, body string) {
	title = titleLine(ev)
	if ev.Kind == EventCompleted && ev.HasResult && ev.ResultStatus != "" {
		title += fmt.Sprintf(" (%s)", ev.ResultStatus)
	}
	body = Clean(ev.Title, maxTitleCodePoints)
	return title, body
}

// titleLine renders the event's title line without the completion status
// suffix, shared by the desktop title (which always adds the suffix) and the
// webhook "text" field (which adds it only with title:true,
// Docs/protocol/notify.md §Payload).
func titleLine(ev Event) string {
	name := Clean(ev.PeerName, maxNameCodePoints)
	switch ev.Kind {
	case EventReceived:
		return fmt.Sprintf("%s %s request from %s", capitalize(ev.Urgency), ev.Type, name)
	case EventAccepted:
		return fmt.Sprintf("%s accepted your %s request", name, ev.Type)
	case EventDeclined:
		return fmt.Sprintf("%s declined your %s request", name, ev.Type)
	case EventDeferred:
		return fmt.Sprintf("%s deferred your %s request until %s", name, ev.Type, ev.Until.Local().Format("2006-01-02 15:04"))
	case EventCompleted:
		return fmt.Sprintf("%s completed your %s request", name, ev.Type)
	case EventCancelled:
		return fmt.Sprintf("%s cancelled their %s request", name, ev.Type)
	case EventQuarantined:
		return fmt.Sprintf("%s's result is quarantined and waits for your release", name)
	case EventSessionResult:
		return fmt.Sprintf("%s's result is ready for your review", name)
	case EventSessionChanges:
		return fmt.Sprintf("%s asked for changes", name)
	case EventDeviceLinked:
		// Type holds the peer's role in the link: helper or controller.
		return fmt.Sprintf("%s is now linked as your %s", name, ev.Type)
	case EventDebateConstraint:
		return fmt.Sprintf("%s added a constraint to your debate", name)
	case EventDebateAgreed:
		return fmt.Sprintf("Debate with %s ended in agreement", name)
	case EventDebateEscalated:
		return fmt.Sprintf("Debate with %s needs your decision: no agreement", name)
	case EventDebateBroken:
		return fmt.Sprintf("Debate with %s stopped: the opening position did not match its commitment", name)
	default:
		return ""
	}
}

// buildWebhookText renders the webhook "text" field: the title line, plus
// ": <title>" when title is true (Docs/protocol/notify.md §Payload).
func buildWebhookText(ev Event, title bool) string {
	if ev.Kind == EventTest {
		return "agentnet test notification"
	}
	text := titleLine(ev)
	if title && ev.Kind == EventCompleted && ev.HasResult && ev.ResultStatus != "" {
		text += fmt.Sprintf(" (%s)", ev.ResultStatus)
	}
	if title {
		text += ": " + Clean(ev.Title, maxTitleCodePoints)
	}
	return text
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
