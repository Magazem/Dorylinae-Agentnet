//go:build darwin

package notify

import "context"

// showDesktop runs osascript with a fixed script and the peer text passed as
// argv, never interpolated into AppleScript source (Docs/protocol/notify.md
// §Desktop). Requires the Aqua session (launchd user agent).
func showDesktop(ctx context.Context, title, body string) error {
	args := []string{
		"-e", "on run argv",
		"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
		"-e", "end run",
		"--", title, body,
	}
	return run(ctx, "/usr/bin/osascript", args, nil)
}
