//go:build darwin

package notify

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
)

// approvalDialogScript reads tag/kind/summary from the environment with
// AppleScript's "system attribute", never argv (Docs/protocol/approval.md
// §The approval window, macOS row). It writes "ready" as soon as the dialog
// call is issued (osascript blocks inside display dialog, so there is no
// separate "shown" event to hook; the ready check below also accepts "still
// running after 1.5 s" per the spec) and its decoded answer on exit.
const approvalDialogScript = `
on run
	set t to system attribute "AGENTNET_W_TAG"
	set k to system attribute "AGENTNET_W_KIND"
	set s to system attribute "AGENTNET_W_SUMMARY"
	tell application "System Events" to activate
	try
		set r to display dialog (k & ": " & s) with title ("AgentNet approval " & t) default answer "" buttons {"Reject", "Approve"} giving up after 600
	on error
		return "dismiss"
	end try
	if gave up of r is true then
		return "dismiss"
	else if button returned of r is "Reject" then
		return "reject"
	else
		return "approve " & (text returned of r)
	end if
end run
`

// readyGrace is "still running after 1.5 s" (Docs/protocol/approval.md §The
// approval window, macOS row, "Ready when").
const readyGrace = 1500 * time.Millisecond

func startDialog(ctx context.Context, id, tag, kind, summary string, expires time.Time) (approval.WindowHandle, error) {
	env := []string{
		"AGENTNET_W_TAG=" + tag,
		"AGENTNET_W_KIND=" + kind,
		"AGENTNET_W_SUMMARY=" + summary,
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", approvalDialogScript) //nolint:gosec // fixed path, fixed script
	cmd.Env = append(cmd.Environ(), env...)
	var out bytes.Buffer
	cmd.Stdout = &out

	handle := newDialogHandle(func() { _ = cmd.Process.Kill() })

	if err := cmd.Start(); err != nil {
		handle.markNotReady()
		return handle, nil
	}

	go func() {
		t := time.NewTimer(readyGrace)
		defer t.Stop()
		done := make(chan struct{})
		var waitErr error
		go func() { waitErr = cmd.Wait(); close(done) }()
		select {
		case <-t.C:
			handle.markReady()
			<-done
		case <-done:
			if waitErr == nil {
				handle.markReady() // exited with a valid answer before the grace period
			} else {
				handle.markNotReady() // an early non-zero exit is a failure
			}
		}
		sc := bufio.NewScanner(&out)
		line := "dismiss"
		if sc.Scan() {
			line = sc.Text()
		}
		handle.deliver(parseAnswerLine(line))
	}()

	return handle, nil
}
