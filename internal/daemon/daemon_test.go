package daemon_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"dorylinae/internal/audit"
	"dorylinae/internal/daemon"
	"dorylinae/internal/ipc"
	"dorylinae/internal/paths"
	"dorylinae/internal/store"
)

func TestLifecycle(t *testing.T) {
	dir, err := os.MkdirTemp("", "dn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, p, ready) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}

	// status reports our PID, and answers quickly.
	var res daemon.StatusResult
	start := time.Now()
	cctx, ccancel := context.WithTimeout(ctx, 2*time.Second)
	if err := ipc.Call(cctx, p.Endpoint, "status", nil, &res); err != nil {
		t.Fatalf("status: %v", err)
	}
	ccancel()
	if time.Since(start) > 2*time.Second {
		t.Fatal("status too slow")
	}
	if res.PID != os.Getpid() || res.UptimeSeconds < 0 || res.StartedAt == "" {
		t.Fatalf("unexpected status: %+v", res)
	}

	// A second daemon on the same home must refuse to start.
	if err := daemon.Run(context.Background(), p, nil); err == nil {
		t.Fatal("second daemon should fail")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not stop")
	}

	// Endpoint is gone after stop.
	cctx, ccancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer ccancel()
	if err := ipc.Call(cctx, p.Endpoint, "status", nil, nil); !errors.Is(err, ipc.ErrNotRunning) {
		t.Fatalf("after stop: %v, want ErrNotRunning", err)
	}

	// DB exists with a migrations table and exactly one start + one stop row.
	st, err := store.Open(context.Background(), p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM migrations`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("migrations rows = %d, err = %v", n, err)
	}
	evs, err := audit.New(st.DB()).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].Action != audit.ActionDaemonStart || evs[1].Action != audit.ActionDaemonStop {
		t.Fatalf("audit events = %+v", evs)
	}
}
