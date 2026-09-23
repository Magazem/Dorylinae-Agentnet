//go:build darwin

package notify

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestShowApprovalCodeNeverInArgvDarwin: on macOS the code reaches osascript
// through the environment, the script is fixed text (Docs/protocol/approval.md
// §Delivering the code; review 26, L-5).
func TestShowApprovalCodeNeverInArgvDarwin(t *testing.T) {
	orig := run
	defer func() { run = orig }()
	var gotName string
	var gotArgs, gotEnv []string
	run = func(_ context.Context, name string, args []string, env []string) error {
		gotName, gotArgs, gotEnv = name, args, env
		return nil
	}
	body := "approve grant git.read to bob for 2h? Code 482913"
	if err := showApproval(context.Background(), "a-1", time.Now().Add(time.Minute), "AgentNet approval", body); err != nil {
		t.Fatal(err)
	}
	if gotName != "/usr/bin/osascript" {
		t.Fatalf("name = %q", gotName)
	}
	for _, a := range gotArgs {
		if strings.Contains(a, "482913") {
			t.Fatalf("code found on the command line: %q", a)
		}
	}
	if len(gotArgs) != 2 || gotArgs[1] != approvalScript {
		t.Fatalf("args = %q, want the fixed script", gotArgs)
	}
	want := "AGENTNET_A_BODY=" + body
	found := false
	for _, e := range gotEnv {
		if e == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("env = %q, missing body", gotEnv)
	}
}
