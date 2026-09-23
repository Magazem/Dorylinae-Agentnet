package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mattn/go-isatty"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

// Environment variables for the headless approval channel
// (Docs/protocol/approval.md §Headless machines).
const (
	ApprovalEnv = "DORYLINAE_APPROVAL"
	DebugEnv    = "DORYLINAE_DEBUG"
)

// Approval modes, also the "approval" field of "status"
// (Docs/protocol/approval.md §Headless machines).
const (
	ApprovalModeDesktop       = "desktop"
	ApprovalModeTerminal      = "terminal"
	ApprovalModeTerminalDebug = "terminal-debug"
)

// ErrApprovalRequiresTerminal is returned by RunWithOptions when
// DORYLINAE_APPROVAL=terminal is set but stderr is not a terminal and
// DORYLINAE_DEBUG=1 was not set (Docs/protocol/approval.md §Headless
// machines). cmd/agentnetd maps it to exit code 2.
var ErrApprovalRequiresTerminal = errors.New("DORYLINAE_APPROVAL=terminal requires stderr to be a terminal (or DORYLINAE_DEBUG=1)")

// resolveApprovalMode decides the approval channel from the environment and
// whether stderr and stdin are terminals (Docs/protocol/approval.md
// §Headless machines: "stderr must be a terminal ... the daemon's own stdin
// ... under the same rule and the same DORYLINAE_DEBUG=1 exception as
// stderr"). Pulled out of RunWithOptions so it is unit-testable without a
// real terminal.
func resolveApprovalMode(envVal string, debug, stderrIsTerminal, stdinIsTerminal bool) (string, error) {
	if envVal != "terminal" {
		return ApprovalModeDesktop, nil
	}
	if stderrIsTerminal && stdinIsTerminal {
		return ApprovalModeTerminal, nil
	}
	if debug {
		return ApprovalModeTerminalDebug, nil
	}
	return "", ErrApprovalRequiresTerminal
}

// isTerminal reports whether x (an io.Writer such as stderr, or an
// io.Reader such as stdin) is a terminal, for resolveApprovalMode.
func isTerminal(x any) bool {
	f, ok := x.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// terminalNotifier writes the approval summary and code straight to w (the
// daemon's own stderr), never to the log file
// (Docs/protocol/approval.md §Headless machines).
type terminalNotifier struct{ w io.Writer }

func (t terminalNotifier) Show(_ context.Context, _ string, _ time.Time, title, body string) error {
	_, err := fmt.Fprintf(t.w, "%s: %s\n", title, body)
	return err
}

func (t terminalNotifier) Remove(context.Context, string) {}

// ApprovalListResult is the result of "approval_list".
type ApprovalListResult struct {
	Approvals []approval.View `json:"approvals"`
}

// approvalError maps an approval package error to an *ipc.Error
// (Docs/protocol/approval.md §IPC and CLI). Any other error (in particular
// one already carrying its own *ipc.Error from a waiting action's
// Precondition/Perform, per approval.Action's contract) is returned
// unchanged.
func approvalError(err error) error {
	var bce *approval.BadCodeError
	switch {
	case errors.Is(err, approval.ErrUnknown):
		return &ipc.Error{Code: "unknown_approval", Message: "no such approval"}
	case errors.Is(err, approval.ErrExpired):
		return &ipc.Error{Code: "approval_expired", Message: "approval expired"}
	case errors.Is(err, approval.ErrLimit):
		return &ipc.Error{Code: "approval_limit", Message: "too many approvals"}
	case errors.Is(err, approval.ErrLocked):
		return &ipc.Error{Code: "approval_locked", Message: "approvals are locked after too many wrong codes"}
	case errors.Is(err, approval.ErrUnavailable):
		return &ipc.Error{Code: "approval_unavailable", Message: "the desktop notifier is unavailable"}
	case errors.Is(err, approval.ErrTerminalMode):
		return &ipc.Error{Code: ipc.CodeBadRequest, Message: "answer on the daemon's terminal"}
	case errors.As(err, &bce):
		return &ipc.Error{Code: "bad_code", Message: bce.Error()}
	default:
		return err
	}
}

// registerApproval wires "approval_list", "approval_open" and
// "approval_reject" (Docs/protocol/approval.md §IPC and CLI).
// "approval_confirm" is removed by 2.2d, together with the `approve <a-id>
// <code>` CLI form: no IPC method and no CLI form takes a code (review 26,
// L7; D19).
func registerApproval(srv *ipc.Server, as *approval.Store) {
	srv.Handle("approval_list", func(ctx context.Context, _ json.RawMessage) (any, error) {
		views, err := as.List(ctx)
		if err != nil {
			return nil, err
		}
		if views == nil {
			views = []approval.View{}
		}
		return ApprovalListResult{Approvals: views}, nil
	})

	srv.Handle("approval_open", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ ID string }
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		view, err := as.OpenWindow(ctx, p.ID)
		if err != nil {
			return nil, approvalError(err)
		}
		return map[string]approval.View{"approval": view}, nil
	})

	srv.Handle("approval_reject", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ ID string }
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		view, err := as.Reject(ctx, p.ID, "ipc")
		if err != nil {
			return nil, approvalError(err)
		}
		return map[string]approval.View{"approval": view}, nil
	})
}
