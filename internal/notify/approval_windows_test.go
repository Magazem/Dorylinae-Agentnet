//go:build windows

package notify

import (
	"context"
	"strings"
	"testing"
	"time"
)

const approvalCode = "482913"

func TestShowApprovalCodeNeverInArgv(t *testing.T) {
	orig := run
	defer func() { run = orig }()
	var gotArgs, gotEnv []string
	run = func(_ context.Context, _ string, args []string, env []string) error {
		gotArgs, gotEnv = args, env
		return nil
	}
	body := "approve grant git.read to bob for 2h? Code " + approvalCode
	expires := time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)
	if err := showApproval(context.Background(), "a-1234", expires, "AgentNet approval", body); err != nil {
		t.Fatal(err)
	}
	for _, a := range gotArgs {
		if strings.Contains(a, approvalCode) {
			t.Fatalf("code found on the command line: %q", a)
		}
	}
	titleVal := decodeEnv(t, gotEnv, "AGENTNET_A_TITLE")
	bodyVal := decodeEnv(t, gotEnv, "AGENTNET_A_BODY")
	if titleVal != "AgentNet approval" {
		t.Errorf("title = %q", titleVal)
	}
	if bodyVal != body {
		t.Errorf("body = %q, want %q", bodyVal, body)
	}
	if !hasEnv(gotEnv, "AGENTNET_A_TAG=a-1234") {
		t.Errorf("env = %v, missing tag", gotEnv)
	}
	if !hasEnv(gotEnv, "AGENTNET_A_GROUP="+approvalGroup) {
		t.Errorf("env = %v, missing group", gotEnv)
	}
}

func TestRemoveApprovalUsesTagAndGroup(t *testing.T) {
	orig := run
	defer func() { run = orig }()
	var gotEnv []string
	run = func(_ context.Context, _ string, _ []string, env []string) error {
		gotEnv = env
		return nil
	}
	removeApproval(context.Background(), "a-5678")
	if !hasEnv(gotEnv, "AGENTNET_A_TAG=a-5678") || !hasEnv(gotEnv, "AGENTNET_A_GROUP="+approvalGroup) {
		t.Fatalf("env = %v", gotEnv)
	}
}

func TestApprovalScriptIsFixed(t *testing.T) {
	if strings.Contains(approvalToastScript, approvalCode) {
		t.Error("the script source must never contain a code")
	}
	if !strings.Contains(approvalToastScript, "AGENTNET_A_TITLE") || !strings.Contains(approvalToastScript, "AGENTNET_A_BODY") {
		t.Error("the script must read title/body from the environment")
	}
	if !strings.Contains(approvalToastScript, ".Tag") || !strings.Contains(approvalToastScript, ".Group") {
		t.Error("the script must set Tag and Group so Remove can find it")
	}
}

func hasEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}
