package daemon

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

func TestResolveApprovalMode(t *testing.T) {
	cases := []struct {
		env, want  string
		debug, tty bool
		wantErr    bool
	}{
		{env: "", tty: false, debug: false, want: ApprovalModeDesktop},
		{env: "", tty: true, debug: false, want: ApprovalModeDesktop},
		{env: "terminal", tty: true, debug: false, want: ApprovalModeTerminal},
		{env: "terminal", tty: false, debug: true, want: ApprovalModeTerminalDebug},
		{env: "terminal", tty: false, debug: false, wantErr: true},
	}
	for i, c := range cases {
		got, err := resolveApprovalMode(c.env, c.debug, c.tty)
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

func TestMergeApproval(t *testing.T) {
	view := approval.View{ID: "a-1", State: "approved"}
	merged, err := mergeApproval(map[string]string{"status": "ok"}, view)
	if err != nil {
		t.Fatal(err)
	}
	// Round-trip through JSON to check both the caller's field and "approval" survive.
	raw, err := json.Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Status   string        `json:"status"`
		Approval approval.View `json:"approval"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Status != "ok" || decoded.Approval.ID != "a-1" {
		t.Fatalf("decoded = %+v", decoded)
	}

	// A nil result still carries the approval view.
	merged, err = mergeApproval(nil, view)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	var decoded2 struct {
		Approval approval.View `json:"approval"`
	}
	if err := json.Unmarshal(raw, &decoded2); err != nil {
		t.Fatal(err)
	}
	if decoded2.Approval.ID != "a-1" {
		t.Fatalf("decoded2 = %+v", decoded2)
	}
}
