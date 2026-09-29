//go:build darwin

package notify

import (
	"context"
	"time"
)

// approvalScript reads title/body from the environment, never argv
// (Docs/protocol/approval.md §Delivering the code: "title and body go
// through the environment of osascript"), as base64 UTF-8 decoded by
// envText, so a non-ASCII summary is not garbled (R55-203).
const approvalScript = osaScriptHeader + osaEnvTextHandler + `
on run
	set t to my envText("AGENTNET_A_TITLE")
	set b to my envText("AGENTNET_A_BODY")
	display notification b with title t
end run
`

func showApproval(ctx context.Context, _ string, _ time.Time, title, body string) error {
	env := []string{
		osaEnv("AGENTNET_A_TITLE", title),
		osaEnv("AGENTNET_A_BODY", body),
	}
	return run(ctx, "/usr/bin/osascript", []string{"-e", approvalScript}, env)
}

// removeApproval is a no-op: macOS gives no documented API to withdraw a
// specific notification by tag (Docs/protocol/approval.md §Delivering the
// code covers Windows only).
func removeApproval(context.Context, string) {}
