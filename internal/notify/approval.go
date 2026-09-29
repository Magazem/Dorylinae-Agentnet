package notify

import (
	"context"
	"strings"
	"time"
)

// Approval shows and withdraws an approval's one-time code notification
// (Docs/protocol/approval.md §Delivering the code). Unlike Desktop (request
// lifecycle events), it never falls back to a second mechanism: if the
// primary call fails, Show returns an error and the caller treats the
// approval as unavailable. The code never reaches a child process's argument
// list: Linux delivers it with an in-process D-Bus call (no gdbus/notify-send
// subprocess), macOS and Windows pass it through the environment.
type Approval struct{}

// Show displays title/body tagged with id, expiring at expires (Windows:
// toast ExpirationTime; other platforms ignore it beyond the notification
// server's own default timeout).
func (Approval) Show(ctx context.Context, id string, expires time.Time, title, body string) error {
	ctx, cancel := context.WithTimeout(ctx, showTimeout)
	defer cancel()
	return showApproval(ctx, id, expires, title, body)
}

// Remove best-effort withdraws the notification tagged id from the OS
// notification history (Windows only; other platforms no-op).
func (Approval) Remove(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(ctx, showTimeout)
	defer cancel()
	removeApproval(ctx, id)
}

// removable reports whether the notification tagged id can later be
// withdrawn with Remove: an approval's code notification. The Store never
// removes its outcome ("outcome-<id>") and lock ("lock-<time>") notices, so
// a platform that keeps an id map must not record them, or the map grows by
// one entry per outcome for the daemon's lifetime (review 55 R55-105 /
// C12-04; internal/approval/store.go notifyOutcome and the lock notice).
func removable(id string) bool {
	return !strings.HasPrefix(id, "outcome-") && !strings.HasPrefix(id, "lock-")
}
