package request

import (
	"context"
	"time"
)

// Notification events (Docs/protocol/notify.md §Triggers). Kept as separate
// string constants, not a dependency on internal/notify, so this package
// stays free of the notification channel's concerns; the daemon wires
// NotifyFunc to internal/notify.
const (
	EventReceived  = "request.received"
	EventAccepted  = "request.accepted"
	EventDeclined  = "request.declined"
	EventDeferred  = "request.deferred"
	EventCompleted = "request.completed"
	EventCancelled = "request.cancelled"
)

// NotifyInfo is what a notification needs about one lifecycle event
// (Docs/protocol/notify.md §Text and sanitising).
type NotifyInfo struct {
	Peer         string // the other party's identity key
	Type         string // review, task or question
	Urgency      string // EventReceived only
	Title        string
	Until        time.Time // EventDeferred only
	ResultStatus string    // EventCompleted only, with a result
	HasResult    bool      // EventCompleted only
}

// NotifyFunc is called once, after commit, for each event in
// Docs/protocol/notify.md §Triggers. It must not block for long; a nil Store.Notify
// disables notifications.
type NotifyFunc func(ctx context.Context, event string, info NotifyInfo)
