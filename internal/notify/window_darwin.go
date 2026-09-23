//go:build darwin

package notify

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
)

// approvalDialogScript reads tag/kind/summary from the environment with
// AppleScript's "system attribute", never argv (Docs/protocol/approval.md
// §The approval window, macOS row). osascript blocks inside display dialog,
// so there is no "shown" event: the ready check below is "still running
// after 1.5 s". It prints its decoded answer on exit. It activates osascript
// itself, not System Events (which needs an Automation/TCC grant), and a
// dialog error other than user-cancel (-128) is re-raised, so osascript
// exits non-zero and an early failure is "not ready", never an answer
// (review 30, M7).
const approvalDialogScript = `
on run
	set t to system attribute "AGENTNET_W_TAG"
	set k to system attribute "AGENTNET_W_KIND"
	set s to system attribute "AGENTNET_W_SUMMARY"
	set secs to (system attribute "AGENTNET_W_TIMEOUT_S") as integer
	activate
	try
		set r to display dialog (k & ": " & s) with title ("AgentNet approval " & t) default answer "" buttons {"Reject", "Approve"} giving up after secs
	on error errMsg number errNum
		if errNum is -128 then return "dismiss"
		error errMsg number errNum
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

func startDialog(ctx context.Context, _, tag, kind, summary string, expires time.Time) (approval.WindowHandle, error) {
	secs := int(time.Until(expires).Seconds())
	if secs < 1 {
		secs = 1
	}
	env := []string{
		"AGENTNET_W_TAG=" + tag,
		"AGENTNET_W_KIND=" + kind,
		"AGENTNET_W_SUMMARY=" + summary,
		fmt.Sprintf("AGENTNET_W_TIMEOUT_S=%d", secs),
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", approvalDialogScript) //nolint:gosec // fixed path, fixed script
	cmd.Env = append(cmd.Environ(), env...)
	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Start(); err != nil {
		// No process: Kill must not touch cmd.Process, which is nil here
		// (review 30, H2: Create always calls Kill on a not-ready handle).
		h := newDialogHandle(func() {})
		h.markNotReady()
		return h, nil
	}
	handle := newDialogHandle(func() { _ = cmd.Process.Kill() })

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
