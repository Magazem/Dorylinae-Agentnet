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
	// Room for the limit plus "\r\n". An over-long line is refused and
	// skipped up to its newline; it never stops the reader, which would end
	// code entry for the daemon's lifetime (review 30, M8).
	r := bufio.NewReaderSize(stdin, terminalLineLimit+2)
	for {
		raw, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = r.ReadSlice('\n')
			}
			_, _ = fmt.Fprintf(stderr, "AgentNet: line longer than %d bytes ignored\n", terminalLineLimit)
			if err != nil {
				return
			}
			continue
		}
		if ctx.Err() != nil {
			return
		}
		line := strings.TrimSpace(string(raw))
		if len(line) > terminalLineLimit {
			_, _ = fmt.Fprintf(stderr, "AgentNet: line longer than %d bytes ignored\n", terminalLineLimit)
		} else if line != "" {
			handleTerminalLine(ctx, stderr, as, line)
		}
		if err != nil {
			return // stdin closed (a final unterminated line was handled above)
		}
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
	// A value that is not exactly 6 ASCII digits is a typo, not a guess: it
	// is not checked and uses no attempt, as in the window (R55-F5, review 55
	// R55-148).
	if !isSixDigits(code) {
		_, _ = fmt.Fprintf(stderr, "AgentNet: enter the 6-digit code\n")
		return
	}
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

// isSixDigits reports whether v is exactly 6 ASCII digits.
func isSixDigits(v string) bool {
	if len(v) != 6 {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return true
}
