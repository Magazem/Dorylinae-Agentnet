package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
)

// terminalLineLimit is "It reads lines of at most 128 bytes"
// (Docs/protocol/approval.md §Headless machines, "Code entry on the
// daemon's own stdin").
const terminalLineLimit = 128

// runTerminalApprovalReader reads `<tag> <code>` and `reject <tag>` lines
// from the daemon's own stdin (never a CLI argument or an IPC call) and
// writes the outcome to stderr (Docs/protocol/approval.md §Headless
// machines, "Code entry on the daemon's own stdin"). It returns when stdin
// is closed or ctx is done.
func runTerminalApprovalReader(ctx context.Context, stdin io.Reader, stderr io.Writer, as *approval.Store) {
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, terminalLineLimit), terminalLineLimit)
	for sc.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		handleTerminalLine(ctx, stderr, as, line)
	}
}

func handleTerminalLine(ctx context.Context, stderr io.Writer, as *approval.Store, line string) {
	if rest, ok := strings.CutPrefix(line, "reject "); ok {
		tag := strings.TrimSpace(rest)
		id, err := as.ResolveTag(tag)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "AgentNet: %s\n", terminalTagError(tag, err))
			return
		}
		if _, err := as.Reject(ctx, id, "terminal"); err != nil {
			_, _ = fmt.Fprintf(stderr, "AgentNet: %v\n", err)
			return
		}
		_, _ = fmt.Fprintf(stderr, "AgentNet: %s rejected\n", id)
		return
	}

	fields := strings.Fields(line)
	if len(fields) != 2 {
		_, _ = fmt.Fprintf(stderr, "AgentNet: expected \"<tag> <code>\" or \"reject <tag>\"\n")
		return
	}
	tag, code := fields[0], fields[1]
	id, err := as.ResolveTag(tag)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "AgentNet: %s\n", terminalTagError(tag, err))
		return
	}
	_, err = as.Confirm(ctx, id, code)
	var bce *approval.BadCodeError
	switch {
	case err == nil:
		_, _ = fmt.Fprintf(stderr, "AgentNet: %s approved\n", id)
	case errors.As(err, &bce):
		_, _ = fmt.Fprintf(stderr, "AgentNet: wrong code, %d attempt(s) left\n", bce.AttemptsLeft)
	default:
		_, _ = fmt.Fprintf(stderr, "AgentNet: not approved: %v\n", err)
	}
}

func terminalTagError(tag string, err error) string {
	switch {
	case errors.Is(err, approval.ErrAmbiguousTag):
		return fmt.Sprintf("%q matches more than one pending approval", tag)
	case errors.Is(err, approval.ErrUnknown):
		return fmt.Sprintf("no pending approval matches %q", tag)
	default:
		return err.Error()
	}
}
