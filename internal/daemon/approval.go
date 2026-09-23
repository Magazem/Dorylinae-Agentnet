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
// whether stderr is a terminal (Docs/protocol/approval.md §Headless
// machines). Pulled out of RunWithOptions so it is unit-testable without a
// real terminal.
func resolveApprovalMode(envVal string, debug, stderrIsTerminal bool) (string, error) {
	if envVal != "terminal" {
		return ApprovalModeDesktop, nil
	}
	if stderrIsTerminal {
		return ApprovalModeTerminal, nil
	}
	if debug {
		return ApprovalModeTerminalDebug, nil
	}
	return "", ErrApprovalRequiresTerminal
}

// isTerminal reports whether w is a terminal, for the stderr passed to
// resolveApprovalMode.
func isTerminal(w io.Writer) bool {
	f, ok := w.(interface{ Fd() uintptr })
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
	case errors.As(err, &bce):
		return &ipc.Error{Code: "bad_code", Message: bce.Error()}
	default:
		return err
	}
}

// mergeApproval JSON-merges an "approval" field carrying view into result
// (Docs/protocol/approval.md §IPC and CLI, "The waiting action's result,
// plus approval: <view>"). result may be nil (no Action.Perform was
// registered) or any JSON-object-shaped value.
func mergeApproval(result any, view approval.View) (any, error) {
	m := map[string]json.RawMessage{}
	if result != nil {
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &m) // non-object results (nil, scalars) leave m empty
	}
	viewRaw, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	m["approval"] = viewRaw
	return m, nil
}

// registerApproval wires "approval_list", "approval_confirm" and
// "approval_reject" (Docs/protocol/approval.md §IPC and CLI).
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

	srv.Handle("approval_confirm", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ ID, Code string }
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" || p.Code == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id and code are required"}
		}
		result, err := as.Confirm(ctx, p.ID, p.Code)
		if err != nil {
			return nil, approvalError(err)
		}
		view, verr := as.Show(ctx, p.ID)
		if verr != nil {
			return nil, verr
		}
		return mergeApproval(result, view)
	})

	srv.Handle("approval_reject", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ ID string }
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		view, err := as.Reject(ctx, p.ID)
		if err != nil {
			return nil, approvalError(err)
		}
		return map[string]approval.View{"approval": view}, nil
	})
}
