package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
//
// wait blocks until daemon.Run has actually returned (closed its store,
// released its IPC endpoint) and returns its error; it is safe to call from
// both the test body and the registered cleanup, since only the first call
// reads from the underlying channel. The cleanup always calls it, so a test
// never returns (and lets TempDir's RemoveAll run) while the daemon still
// holds files open under home - that race caused "directory not empty"
// cleanup failures on macOS, where (unlike Windows) TempDir does not retry.
func runningDaemon(t *testing.T) (home string, wait func() error) {
	t.Helper()
	t.Setenv(identity.KeystoreEnv, "file") // never touch the real keychain from tests
	home = filepath.Join(testutil.TempDir(t), "home")
	p, err := paths.In(home)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- daemon.Run(ctx, p, ready) }()
	var once sync.Once
	var werr error
	wait = func() error {
		once.Do(func() { werr = <-done })
		return werr
	}
	t.Cleanup(func() {
		cancel()
		waited := make(chan struct{})
		go func() { _ = wait(); close(waited) }()
		select {
		case <-waited:
		case <-time.After(10 * time.Second):
			t.Error("daemon did not stop")
		}
	})
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}
	return home, wait
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
	home, wait := runningDaemon(t)

	code, out, errs := invoke(t, "stop", "--home", home)
	if code != 0 || !strings.Contains(out, "stopped") {
		t.Fatalf("stop: code %d, out %q, stderr %q", code, out, errs)
	}
	waited := make(chan error, 1)
	go func() { waited <- wait() }()
	select {
	case err := <-waited:
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
