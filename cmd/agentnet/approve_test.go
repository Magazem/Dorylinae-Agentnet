package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
)

type approveFakeNotifier struct{ lastTitle, lastBody string }

func (f *approveFakeNotifier) Show(_ context.Context, _ string, _ time.Time, title, body string) error {
	f.lastTitle, f.lastBody = title, body
	return nil
}
func (f *approveFakeNotifier) Remove(context.Context, string) {}

// approveFakeWindow is a fake approval.WindowRunner: it never spawns a real
// dialog process (Docs/review/23-phase2-tickets.md 2.2d).
type approveFakeWindow struct {
	mu      sync.Mutex
	handles map[string]*approveFakeHandle
}

func newApproveFakeWindow() *approveFakeWindow {
	return &approveFakeWindow{handles: map[string]*approveFakeHandle{}}
}

func (w *approveFakeWindow) Start(_ context.Context, id, _, _, _ string, _ time.Time) (approval.WindowHandle, error) {
	h := &approveFakeHandle{ready: make(chan struct{}), answerCh: make(chan [2]string, 1)}
	close(h.ready)
	w.mu.Lock()
	w.handles[id] = h
	w.mu.Unlock()
	return h, nil
}

func (w *approveFakeWindow) answer(id, kind, code string) {
	w.mu.Lock()
	h := w.handles[id]
	w.mu.Unlock()
	if h != nil {
		h.answerCh <- [2]string{kind, code}
	}
}

type approveFakeHandle struct {
	ready    chan struct{}
	answerCh chan [2]string
}

func (h *approveFakeHandle) Ready(ctx context.Context) bool {
	select {
	case <-h.ready:
		return true
	case <-ctx.Done():
		return false
	}
}

func (h *approveFakeHandle) Answer(ctx context.Context) (kind, code string, err error) {
	select {
	case a := <-h.answerCh:
		return a[0], a[1], nil
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}

func (h *approveFakeHandle) Kill() {}

func startApproveDaemon(t *testing.T) (*approval.Store, *approveFakeNotifier, *approveFakeWindow) {
	t.Helper()
	t.Setenv(identity.KeystoreEnv, "file")
	p := shortHome(t)
	notifier := &approveFakeNotifier{}
	win := newApproveFakeWindow()
	var apprStore *approval.Store
	opts := daemon.Options{
		ApprovalNotify:  notifier,
		ApprovalWindow:  win,
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
	return apprStore, notifier, win
}

func pollApprove(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if fn() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestApproveList(t *testing.T) {
	apprStore, _, _ := startApproveDaemon(t)
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
}

func TestApproveReject(t *testing.T) {
	apprStore, _, _ := startApproveDaemon(t)
	view, err := apprStore.Create(context.Background(), approval.KindGrant, "g-1", "approve grant to bob", approval.Action{})
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if c := run([]string{"approve", "--reject", view.ID}, &out, &errb); c != exitOK {
		t.Fatalf("reject code = %d, stderr = %q", c, errb.String())
	}
	if !strings.Contains(out.String(), "Rejected") {
		t.Fatalf("human output = %q", out.String())
	}
}

func TestApproveOpen(t *testing.T) {
	apprStore, _, win := startApproveDaemon(t)
	var performed bool
	view, err := apprStore.Create(context.Background(), approval.KindGrant, "g-1", "s", approval.Action{
		Perform: func(context.Context, *sql.Tx) (any, error) {
			performed = true
			return map[string]string{"status": "ok"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if c := run([]string{"approve", "--open", view.ID, "--json"}, &out, &errb); c != exitOK {
		t.Fatalf("open code = %d, stderr = %q", c, errb.String())
	}
	var body struct {
		OK       bool `json:"ok"`
		Approval struct {
			Window string `json:"window"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("stdout not JSON: %q: %v", out.String(), err)
	}
	if !body.OK || body.Approval.Window != "open" {
		t.Fatalf("body = %+v", body)
	}

	// The window's own answer, not this command, is what confirms the
	// approval: the CLI never accepts a code.
	win.answer(view.ID, "approve", "999999") // the fake window never learns the real code
	pollApprove(t, func() bool {
		v, _ := apprStore.Show(context.Background(), view.ID)
		return v.AttemptsLeft < approval.MaxAttempts
	})
	if performed {
		t.Fatal("action performed with a code the CLI never saw or supplied")
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
	out.Reset()
	errb.Reset()
	// The old "agentnet approve <a-id> <code>" form is a usage error, not a
	// confirm: no CLI form takes a code (D19, review 26 L7).
	if c := run([]string{"approve", "a-0123456789abcdef0123456789abcdef", "482913"}, &out, &errb); c != exitUsage {
		t.Fatalf("<a-id> <code>: code = %d", c)
	}
	if !strings.Contains(errb.String(), "approval window") {
		t.Fatalf("usage error should point at the approval window: %q", errb.String())
	}
	out.Reset()
	errb.Reset()
	if c := run([]string{"approve", "--open", "a-1", "--reject", "a-1"}, &out, &errb); c != exitUsage {
		t.Fatalf("--open with --reject: code = %d", c)
	}
}
