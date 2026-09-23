package daemon_test

// Review 26 (Docs/review/26-2.2a-review.md), ticket 2.2a/2.2d acceptance:
// the code appears in the fake notifier's call and in no IPC result and no
// daemon log line, with the answer arriving through a fake window (never a
// real dialog process).

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestApprovalCodeNeverInIPCResultOrLog(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	dir, err := os.MkdirTemp("", "dn-approval-rv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}
	logs := &lockedBuffer{}
	notifier := &fakeApprovalNotifier{}
	win := newFakeWindowRunner()
	var apprStore *approval.Store
	opts := daemon.Options{
		Logger:          slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		ApprovalNotify:  notifier,
		ApprovalWindow:  win,
		OnApprovalReady: func(s *approval.Store) { apprStore = s },
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.RunWithOptions(ctx, p, ready, opts) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("daemon did not stop")
		}
	}
	t.Cleanup(stop)
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}

	view, err := apprStore.Create(context.Background(), approval.KindGrant, "g-1", "approve grant to bob?", approval.Action{})
	if err != nil {
		t.Fatal(err)
	}
	code := notifier.lastCode(t)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}

	var results []string
	call := func(method string, params any) {
		t.Helper()
		cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer ccancel()
		var raw json.RawMessage
		err := ipc.Call(cctx, p.Endpoint, method, params, &raw)
		results = append(results, string(raw))
		if err != nil {
			results = append(results, err.Error())
		}
	}
	call("approval_list", nil)

	// The wrong code arrives through the fake window's answer, exactly as a
	// real dialog's stdout would deliver it, and reopens the window.
	win.answer(view.ID, "approve", wrong)
	pollUntil(t, 2*time.Second, func() bool { return win.startCount() >= 2 })
	call("approval_list", nil)

	win.answer(view.ID, "approve", code)
	pollUntil(t, 2*time.Second, func() bool {
		v, err := apprStore.Show(context.Background(), view.ID)
		return err == nil && v.State == approval.StateApproved
	})
	call("approval_list", nil)
	stop()

	for i, r := range results {
		if strings.Contains(r, code) {
			t.Fatalf("IPC result %d contains the code: %s", i, r)
		}
	}
	if strings.Contains(logs.String(), code) {
		t.Fatalf("daemon log contains the code:\n%s", logs.String())
	}
}
