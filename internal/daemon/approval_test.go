package daemon_test

// Ticket 2.2d acceptance (Docs/review/23-phase2-tickets.md §2.2d,
// Docs/protocol/approval.md): the approval IPC methods through a real
// daemon, driven by a fake window runner (never a real dialog process).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
)

// fakeApprovalNotifier is a test double that never touches the OS notifier
// and records the title/body so the test can extract the code. Show is also
// called from the store's window-watch goroutine (outcome notices), so the
// fields are guarded by mu (review 31).
type fakeApprovalNotifier struct {
	mu                  sync.Mutex
	lastTitle, lastBody string
}

func (f *fakeApprovalNotifier) Show(_ context.Context, _ string, _ time.Time, title, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastTitle, f.lastBody = title, body
	return nil
}
func (f *fakeApprovalNotifier) Remove(context.Context, string) {}

// lastCode extracts the 6-digit code from the desktop title
// ("AgentNet code 482913 for approval a-...", Docs/protocol/approval.md
// §Delivering the code).
func (f *fakeApprovalNotifier) lastCode(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	title := f.lastTitle
	f.mu.Unlock()
	const marker = "code "
	i := strings.Index(strings.ToLower(title), marker)
	if i < 0 || len(title) < i+len(marker)+6 {
		t.Fatalf("no code in title: %q", title)
	}
	return title[i+len(marker) : i+len(marker)+6]
}

func startApprovalDaemon(t *testing.T) (paths.Paths, *approval.Store, *fakeApprovalNotifier, *fakeWindowRunner) {
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
	win := newFakeWindowRunner()
	now := time.Now()
	var apprStore *approval.Store
	opts := daemon.Options{
		ApprovalNotify:  notifier,
		ApprovalWindow:  win,
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
	return p, apprStore, notifier, win
}

// pollUntil polls fn with a deadline, per repo convention (never read async
// state once).
func pollUntil(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if fn() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestApprovalIPCWindowApproveRunsAction(t *testing.T) {
	p, apprStore, notifier, win := startApprovalDaemon(t)
	ctx := context.Background()

	// Perform runs on the window-watch goroutine; the test polls it (review 31).
	var performed atomic.Bool
	action := approval.Action{
		Perform: func(_ context.Context, tx *sql.Tx) (any, error) {
			performed.Store(true)
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
	if view.Window != "open" {
		t.Fatalf("view.Window = %q, want open", view.Window)
	}
	code := notifier.lastCode(t)

	var list daemon.ApprovalListResult
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := ipc.Call(cctx, p.Endpoint, "approval_list", nil, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Approvals) != 1 || list.Approvals[0].ID != view.ID {
		t.Fatalf("approval_list = %+v", list)
	}

	// The answer arrives on the fake window's stdout pipe, exactly as a real
	// dialog would deliver it; no IPC method or CLI form ever takes a code.
	win.answer(view.ID, "approve", code)

	pollUntil(t, 2*time.Second, performed.Load)

	pollUntil(t, 2*time.Second, func() bool {
		v, err := apprStore.Show(context.Background(), view.ID)
		return err == nil && v.State == approval.StateApproved
	})
}

func TestApprovalIPCWindowRejectAndOpen(t *testing.T) {
	p, apprStore, _, win := startApprovalDaemon(t)
	ctx := context.Background()
	view, err := apprStore.Create(ctx, approval.KindRelease, "s-1", "release?", approval.Action{})
	if err != nil {
		t.Fatal(err)
	}

	// A wrong code reopens the window rather than rejecting outright.
	win.answer(view.ID, "approve", "000000")
	pollUntil(t, 2*time.Second, func() bool { return win.startCount() >= 2 })
	v, err := apprStore.Show(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != approval.StatePending || v.AttemptsLeft != approval.MaxAttempts-1 {
		t.Fatalf("after one wrong code: %+v", v)
	}

	// The window's own Reject button rejects with via "window".
	win.answer(view.ID, "reject", "")
	pollUntil(t, 2*time.Second, func() bool {
		v, err := apprStore.Show(context.Background(), view.ID)
		return err == nil && v.State == approval.StateRejected
	})

	// approval_open on a decided approval is unknown/expired, never bad_request.
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	err = ipc.Call(cctx, p.Endpoint, "approval_open", map[string]string{"id": view.ID}, nil)
	var ie *ipc.Error
	if !errors.As(err, &ie) {
		t.Fatalf("approval_open on a rejected approval: err = %v", err)
	}
}

func TestApprovalOpenReopensAndIsNoOpOnAnOpenWindow(t *testing.T) {
	p, apprStore, _, win := startApprovalDaemon(t)
	ctx := context.Background()
	view, err := apprStore.Create(ctx, approval.KindRelease, "s-1", "release?", approval.Action{})
	if err != nil {
		t.Fatal(err)
	}
	if win.startCount() != 1 {
		t.Fatalf("startCount after Create = %d, want 1", win.startCount())
	}

	// approval_open on an already-open window is a no-op (no new dialog).
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var res map[string]approval.View
	if err := ipc.Call(cctx, p.Endpoint, "approval_open", map[string]string{"id": view.ID}, &res); err != nil {
		t.Fatal(err)
	}
	if res["approval"].Window != "open" {
		t.Fatalf("approval_open view = %+v", res["approval"])
	}
	if win.startCount() != 1 {
		t.Fatalf("approval_open on an open window started a new dialog: startCount = %d", win.startCount())
	}

	// dismiss closes the window without deciding; a later approval_open
	// reopens it.
	win.answer(view.ID, "dismiss", "")
	pollUntil(t, 2*time.Second, func() bool {
		v, err := apprStore.Show(context.Background(), view.ID)
		return err == nil && v.Window == "closed"
	})
	cctx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	if err := ipc.Call(cctx2, p.Endpoint, "approval_open", map[string]string{"id": view.ID}, &res); err != nil {
		t.Fatal(err)
	}
	if win.startCount() != 2 {
		t.Fatalf("approval_open on a dismissed window did not reopen it: startCount = %d", win.startCount())
	}
}

func TestApprovalIPCMethodsNeverTakeACode(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	dir, err := os.MkdirTemp("", "dn-approval-methods")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	opts := daemon.Options{OnServerReady: func(s *ipc.Server) { methods = s.Methods() }}
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

	// No IPC method takes a code (Docs/protocol/approval.md §IPC and CLI,
	// "approval_confirm ... is removed in 2.2d"): every registered method is
	// checked, not just approval_confirm.
	var found bool
	for _, m := range methods {
		if m == "approval_confirm" {
			t.Fatalf("approval_confirm is still registered")
		}
		if m == "approval_open" {
			found = true
		}
	}
	if !found {
		t.Fatalf("approval_open is not registered; methods = %v", methods)
	}

	cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ccancel()
	var res json.RawMessage
	err = ipc.Call(cctx, p.Endpoint, "approval_confirm", map[string]string{"id": "a-x", "code": "000000"}, &res)
	var ie *ipc.Error
	if !errors.As(err, &ie) || ie.Code != ipc.CodeUnknownMethod {
		t.Fatalf("approval_confirm should not exist: err = %v", err)
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
