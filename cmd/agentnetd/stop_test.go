package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func TestVersionCommand(t *testing.T) {
	if code, out, _ := invoke(t, "version"); code != 0 || !strings.HasPrefix(out, "agentnetd ") {
		t.Fatalf("version: code %d, out %q", code, out)
	}
	if code, out, _ := invoke(t, "version", "--json"); code != 0 || !strings.Contains(out, `"ok":true`) || !strings.Contains(out, `"name":"agentnetd"`) {
		t.Fatalf("version --json: code %d, out %q", code, out)
	}
}

func TestStopNotRunning(t *testing.T) {
	home := filepath.Join(testutil.TempDir(t), "home")
	if code, _, errs := invoke(t, "stop", "--home", home); code != 3 || !strings.Contains(errs, "not running") {
		t.Fatalf("code = %d, stderr = %q", code, errs)
	}
}

// runningDaemon starts a real daemon in-process (the harness used by
// cmd/agentnet's TestStatusRunning) so the CLI's own run() can be exercised
// against a live endpoint: a second instance for the same home, and stop.
func runningDaemon(t *testing.T) (home string, done chan error) {
	t.Helper()
	t.Setenv(identity.KeystoreEnv, "file") // never touch the real keychain from tests
	home = filepath.Join(testutil.TempDir(t), "home")
	p, err := paths.In(home)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	done = make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- daemon.Run(ctx, p, ready) }()
	// cancel is enough to unblock daemon.Run if a test ends without already
	// having stopped it; a test that stops it itself drains done on its own,
	// so this cleanup must not also try (the channel only ever sends once).
	t.Cleanup(cancel)
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}
	return home, done
}

func TestSecondInstanceReportsClearError(t *testing.T) {
	home, _ := runningDaemon(t)

	code, _, errs := invoke(t, "--home", home)
	if code != exitAlreadyRunning {
		t.Fatalf("code = %d, want %d; stderr %q", code, exitAlreadyRunning, errs)
	}
	if !strings.Contains(errs, "already running for") || !strings.Contains(errs, "(pid "+strconv.Itoa(os.Getpid())+")") {
		t.Fatalf("stderr = %q", errs)
	}
}

func TestStopStopsDaemon(t *testing.T) {
	home, done := runningDaemon(t)

	code, out, errs := invoke(t, "stop", "--home", home)
	if code != 0 || !strings.Contains(out, "stopped") {
		t.Fatalf("stop: code %d, out %q, stderr %q", code, out, errs)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon exited with error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not stop")
	}

	st, err := store.Open(context.Background(), filepath.Join(home, "dorylinae.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	evs, err := audit.New(st.DB()).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var sawRequested, sawStop bool
	for _, e := range evs {
		switch e.Action {
		case audit.ActionDaemonStopRequested:
			sawRequested = e.Actor == audit.ActorCLI
		case audit.ActionDaemonStop:
			sawStop = true
		}
	}
	if !sawRequested || !sawStop {
		t.Fatalf("audit events = %+v", evs)
	}

	// A second stop finds nothing running.
	if code, _, errs := invoke(t, "stop", "--home", home); code != 3 || !strings.Contains(errs, "not running") {
		t.Fatalf("second stop: code %d, stderr %q", code, errs)
	}
}
