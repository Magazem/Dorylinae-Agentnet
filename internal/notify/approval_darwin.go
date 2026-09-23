//go:build darwin

package notify

import (
	"context"
	"time"
)

// approvalScript reads title/body from the environment with AppleScript's
// "system attribute", never argv (Docs/protocol/approval.md §Delivering the
// code: "title and body go through the environment of osascript").
const approvalScript = `
on run
	set t to system attribute "AGENTNET_A_TITLE"
	set b to system attribute "AGENTNET_A_BODY"
	display notification b with title t
end run
`

func showApproval(ctx context.Context, id string, expires time.Time, title, body string) error {
	env := []string{
		"AGENTNET_A_TITLE=" + title,
		"AGENTNET_A_BODY=" + body,
	}
	return run(ctx, "/usr/bin/osascript", []string{"-e", approvalScript}, env)
}

// removeApproval is a no-op: macOS gives no documented API to withdraw a
// specific notification by tag (Docs/protocol/approval.md §Delivering the
// code covers Windows only).
func removeApproval(context.Context, string) {}
