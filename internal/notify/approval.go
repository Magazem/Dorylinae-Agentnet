package notify

import (
	"context"
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
