package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
)

type approveFakeNotifier struct{ lastBody string }

func (f *approveFakeNotifier) Show(_ context.Context, _ string, _ time.Time, _, body string) error {
	f.lastBody = body
	return nil
}
func (f *approveFakeNotifier) Remove(context.Context, string) {}

func startApproveDaemon(t *testing.T) *approval.Store {
	t.Helper()
	t.Setenv(identity.KeystoreEnv, "file")
	p := shortHome(t)
	notifier := &approveFakeNotifier{}
	var apprStore *approval.Store
	opts := daemon.Options{
		ApprovalNotify:  notifier,
		OnApprovalReady: func(s *approval.Store) { apprStore = s },
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.RunWithOptions(ctx, p, ready, opts) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}
	approveTestNotifier = notifier
	return apprStore
}

var approveTestNotifier *approveFakeNotifier

func TestApproveConfirmHumanAndJSON(t *testing.T) {
	apprStore := startApproveDaemon(t)
	view, err := apprStore.Create(context.Background(), approval.KindGrant, "g-1", "approve grant git.read to bob for 2h?", approval.Action{
		Perform: func(context.Context, *sql.Tx) (any, error) { return map[string]string{"status": "ok"}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	code := approveTestNotifier.lastBody[len(approveTestNotifier.lastBody)-6:]

	var out, errb bytes.Buffer
	if c := run([]string{"approve", view.ID, code}, &out, &errb); c != exitOK {
		t.Fatalf("code = %d, stderr = %q", c, errb.String())
	}
	if !strings.Contains(out.String(), "Approved") {
		t.Fatalf("human output = %q", out.String())
	}
}

func TestApproveConfirmJSON(t *testing.T) {
	apprStore := startApproveDaemon(t)
	view, err := apprStore.Create(context.Background(), approval.KindGrant, "g-1", "s", approval.Action{})
	if err != nil {
		t.Fatal(err)
	}
	code := approveTestNotifier.lastBody[len(approveTestNotifier.lastBody)-6:]

	var out, errb bytes.Buffer
	if c := run([]string{"approve", view.ID, code, "--json"}, &out, &errb); c != exitOK {
		t.Fatalf("code = %d, stderr = %q", c, errb.String())
	}
	var body struct {
		OK       bool `json:"ok"`
		Approval struct {
			State string `json:"state"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("stdout not JSON: %q: %v", out.String(), err)
	}
	if !body.OK || body.Approval.State != "approved" {
		t.Fatalf("body = %+v", body)
	}
}

func TestApproveBadCode(t *testing.T) {
	apprStore := startApproveDaemon(t)
	view, err := apprStore.Create(context.Background(), approval.KindGrant, "g-1", "s", approval.Action{})
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if c := run([]string{"approve", view.ID, "000000", "--json"}, &out, &errb); c != exitError {
		t.Fatalf("code = %d", c)
	}
	var body struct {
		OK    bool `json:"ok"`
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("stdout not JSON: %q: %v", out.String(), err)
	}
	if body.OK || body.Error.Code != "bad_code" {
		t.Fatalf("body = %+v", body)
	}
}

func TestApproveRejectAndList(t *testing.T) {
	apprStore := startApproveDaemon(t)
	view, err := apprStore.Create(context.Background(), approval.KindGrant, "g-1", "approve grant to bob", approval.Action{})
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if c := run([]string{"approve", "--list", "--json"}, &out, &errb); c != exitOK {
		t.Fatalf("list code = %d, stderr = %q", c, errb.String())
	}
	var list struct {
		OK        bool `json:"ok"`
		Approvals []struct {
			ID string `json:"id"`
		} `json:"approvals"`
	}
	if err := json.Unmarshal(out.Bytes(), &list); err != nil {
		t.Fatalf("stdout not JSON: %q: %v", out.String(), err)
	}
	if len(list.Approvals) != 1 || list.Approvals[0].ID != view.ID {
		t.Fatalf("list = %+v", list)
	}

	out.Reset()
	if c := run([]string{"approve", "--reject", view.ID}, &out, &errb); c != exitOK {
		t.Fatalf("reject code = %d, stderr = %q", c, errb.String())
	}
	if !strings.Contains(out.String(), "Rejected") {
		t.Fatalf("human output = %q", out.String())
	}
}

func TestApproveUsageErrors(t *testing.T) {
	shortHome(t)
	var out, errb bytes.Buffer
	if c := run([]string{"approve"}, &out, &errb); c != exitUsage {
		t.Fatalf("no args: code = %d", c)
	}
	out.Reset()
	errb.Reset()
	if c := run([]string{"approve", "--list", "a-1"}, &out, &errb); c != exitUsage {
		t.Fatalf("--list with id: code = %d", c)
	}
}
