package daemon_test

// Ticket 2.2a acceptance: DORYLINAE_APPROVAL=terminal writes the code to the
// daemon's own stderr only, refuses to start when stderr is not a terminal
// unless DORYLINAE_DEBUG=1, audits approval.mode, and status reports it
// (Docs/protocol/approval.md §Headless machines).

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// syncBuffer is a *bytes.Buffer safe for the daemon's background goroutines
// to write to while the test reads it. It never reports Fd(), so
// isTerminal(w) is always false through it: exactly a piped stderr, which is
// what DORYLINAE_DEBUG=1 is for (Docs/protocol/approval.md §Headless
// machines, "test harnesses such as 2.H read stderr through a pipe").
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestTerminalModeRefusesWithoutTTYUnlessDebug(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	t.Setenv(daemon.ApprovalEnv, "terminal")
	dir, err := os.MkdirTemp("", "dn-terminal")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = daemon.RunWithOptions(context.Background(), p, nil, daemon.Options{Stderr: &syncBuffer{}})
	if !errors.Is(err, daemon.ErrApprovalRequiresTerminal) {
		t.Fatalf("err = %v, want ErrApprovalRequiresTerminal", err)
	}
}

func TestTerminalModeWritesCodeToStderrOnly(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file")
	t.Setenv(daemon.ApprovalEnv, "terminal")
	t.Setenv(daemon.DebugEnv, "1")
	dir, err := os.MkdirTemp("", "dn-terminal-debug")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}
	stderr := &syncBuffer{}
	var apprStore *approval.Store
	opts := daemon.Options{
		Stderr:          stderr,
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

	var res daemon.StatusResult
	cctx, ccancel := context.WithTimeout(ctx, 2*time.Second)
	defer ccancel()
	if err := ipc.Call(cctx, p.Endpoint, "status", nil, &res); err != nil {
		t.Fatal(err)
	}
	if res.Approval != daemon.ApprovalModeTerminalDebug {
		t.Fatalf("status.approval = %q, want %q", res.Approval, daemon.ApprovalModeTerminalDebug)
	}

	if _, err := apprStore.Create(context.Background(), approval.KindGrant, "g-1", "approve grant?", approval.Action{}); err != nil {
		t.Fatal(err)
	}
	out := stderr.String()
	if !bytes.Contains([]byte(out), []byte("approve grant?")) {
		t.Fatalf("stderr = %q, missing the approval summary", out)
	}

	// approval.mode is audited.
	st, err := store.Open(context.Background(), p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	evs, err := audit.New(st.DB()).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range evs {
		if e.Action == "approval.mode" {
			found = true
		}
	}
	if !found {
		t.Fatal("approval.mode was not audited")
	}
}
