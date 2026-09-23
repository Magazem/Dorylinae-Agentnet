package daemon_test

// Ticket 2.2a acceptance (Docs/review/23-phase2-tickets.md §2.2a,
// Docs/protocol/approval.md): the approval IPC methods through a real
// daemon. 2.2a has no caller yet that creates an approval over IPC (that
// arrives with 2.2c/2.4/2.D1), so these tests seed one directly through the
// approval.Store via the OnApprovalReady test hook.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
)

// fakeApprovalNotifier is a test double that never touches the OS notifier
// and records the code so the test can confirm it.
type fakeApprovalNotifier struct {
	lastBody string
}

func (f *fakeApprovalNotifier) Show(_ context.Context, _ string, _ time.Time, _, body string) error {
	f.lastBody = body
	return nil
}
func (f *fakeApprovalNotifier) Remove(context.Context, string) {}

func startApprovalDaemon(t *testing.T) (paths.Paths, *approval.Store, *fakeApprovalNotifier, *time.Time) {
	t.Helper()
	t.Setenv(identity.KeystoreEnv, "file") // never touch the real keychain from tests
	dir, err := os.MkdirTemp("", "dn-approval")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}
	notifier := &fakeApprovalNotifier{}
	now := time.Now()
	var apprStore *approval.Store
	opts := daemon.Options{
		ApprovalNotify:  notifier,
		ApprovalNow:     func() time.Time { return now },
		OnApprovalReady: func(s *approval.Store) { apprStore = s },
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.RunWithOptions(ctx, p, ready, opts) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("daemon did not stop")
		}
	})
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}
	return p, apprStore, notifier, &now
}

func TestApprovalIPCConfirmRunsAction(t *testing.T) {
	p, apprStore, notifier, _ := startApprovalDaemon(t)
	ctx := context.Background()

	var performed bool
	action := approval.Action{
		Perform: func(_ context.Context, tx *sql.Tx) (any, error) {
			performed = true
			if tx == nil {
				t.Fatal("expected a non-nil tx")
			}
			return map[string]string{"status": "ok"}, nil
		},
	}
	view, err := apprStore.Create(ctx, approval.KindGrant, "g-1", "approve grant git.read to bob for 2h?", action)
	if err != nil {
		t.Fatal(err)
	}
	code := notifier.lastBody[len(notifier.lastBody)-6:]

	var list daemon.ApprovalListResult
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := ipc.Call(cctx, p.Endpoint, "approval_list", nil, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Approvals) != 1 || list.Approvals[0].ID != view.ID {
		t.Fatalf("approval_list = %+v", list)
	}

	var confirmed map[string]json.RawMessage
	cctx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	if err := ipc.Call(cctx2, p.Endpoint, "approval_confirm", map[string]string{"id": view.ID, "code": code}, &confirmed); err != nil {
		t.Fatal(err)
	}
	if !performed {
		t.Fatal("action.Perform was not called")
	}
	var av approval.View
	if err := json.Unmarshal(confirmed["approval"], &av); err != nil {
		t.Fatal(err)
	}
	if av.State != approval.StateApproved {
		t.Fatalf("approval view = %+v", av)
	}
	var status string
	_ = json.Unmarshal(confirmed["status"], &status)
	if status != "ok" {
		t.Fatalf("confirmed = %+v, missing merged action result", confirmed)
	}
}

func TestApprovalIPCBadCodeAndReject(t *testing.T) {
	p, apprStore, _, _ := startApprovalDaemon(t)
	ctx := context.Background()
	view, err := apprStore.Create(ctx, approval.KindRelease, "s-1", "release?", approval.Action{})
	if err != nil {
		t.Fatal(err)
	}

	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	err = ipc.Call(cctx, p.Endpoint, "approval_confirm", map[string]string{"id": view.ID, "code": "000000"}, nil)
	var ie *ipc.Error
	if !errors.As(err, &ie) || ie.Code != "bad_code" {
		t.Fatalf("err = %v, want bad_code", err)
	}

	var rejected map[string]approval.View
	cctx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	if err := ipc.Call(cctx2, p.Endpoint, "approval_reject", map[string]string{"id": view.ID}, &rejected); err != nil {
		t.Fatal(err)
	}
	if rejected["approval"].State != approval.StateRejected {
		t.Fatalf("rejected = %+v", rejected)
	}

	cctx3, cancel3 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel3()
	err = ipc.Call(cctx3, p.Endpoint, "approval_confirm", map[string]string{"id": view.ID, "code": "111111"}, nil)
	if !errors.As(err, &ie) || ie.Code != "unknown_approval" {
		t.Fatalf("confirm after reject: err = %v, want unknown_approval", err)
	}
}

func TestStatusReportsApprovalMode(t *testing.T) {
	p, _, _, _ := startApprovalDaemon(t)
	var res daemon.StatusResult
	cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := ipc.Call(cctx, p.Endpoint, "status", nil, &res); err != nil {
		t.Fatal(err)
	}
	if res.Approval != daemon.ApprovalModeDesktop {
		t.Fatalf("status.approval = %q, want %q", res.Approval, daemon.ApprovalModeDesktop)
	}
}
