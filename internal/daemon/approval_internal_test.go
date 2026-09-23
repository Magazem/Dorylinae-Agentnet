package daemon

import (
	"errors"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

func TestResolveApprovalMode(t *testing.T) {
	cases := []struct {
		env       string
		want      string
		debug     bool
		stderrTTY bool
		stdinTTY  bool
		wantErr   bool
	}{
		{env: "", stderrTTY: false, stdinTTY: false, debug: false, want: ApprovalModeDesktop},
		{env: "", stderrTTY: true, stdinTTY: true, debug: false, want: ApprovalModeDesktop},
		{env: "terminal", stderrTTY: true, stdinTTY: true, debug: false, want: ApprovalModeTerminal},
		{env: "terminal", stderrTTY: true, stdinTTY: false, debug: false, wantErr: true},
		{env: "terminal", stderrTTY: false, stdinTTY: true, debug: false, wantErr: true},
		{env: "terminal", stderrTTY: false, stdinTTY: false, debug: true, want: ApprovalModeTerminalDebug},
		{env: "terminal", stderrTTY: false, stdinTTY: false, debug: false, wantErr: true},
	}
	for i, c := range cases {
		got, err := resolveApprovalMode(c.env, c.debug, c.stderrTTY, c.stdinTTY)
		if c.wantErr {
			if !errors.Is(err, ErrApprovalRequiresTerminal) {
				t.Errorf("case %d: err = %v, want ErrApprovalRequiresTerminal", i, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("case %d: got (%q, %v), want (%q, nil)", i, got, err, c.want)
		}
	}
}

func TestApprovalErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{approval.ErrUnknown, "unknown_approval"},
		{approval.ErrExpired, "approval_expired"},
		{approval.ErrLimit, "approval_limit"},
		{approval.ErrLocked, "approval_locked"},
		{approval.ErrUnavailable, "approval_unavailable"},
		{approval.ErrTerminalMode, ipc.CodeBadRequest},
		{&approval.BadCodeError{AttemptsLeft: 2}, "bad_code"},
	}
	for _, c := range cases {
		got := approvalError(c.err)
		var ie *ipc.Error
		if !errors.As(got, &ie) || ie.Code != c.code {
			t.Errorf("approvalError(%v) = %v, want code %q", c.err, got, c.code)
		}
	}
	// A precondition/perform error that already carries its own *ipc.Error
	// passes through unchanged (Action's contract).
	own := &ipc.Error{Code: "bad_state", Message: "session closed"}
	if got := approvalError(own); !errors.Is(got, own) {
		t.Errorf("approvalError passed through an *ipc.Error unexpectedly transformed: %v", got)
	}
}
