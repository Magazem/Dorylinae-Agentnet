package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
)

func shortHome(t *testing.T) paths.Paths {
	t.Helper()
	dir, err := os.MkdirTemp("", "dn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv(paths.HomeEnv, dir)
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStatusNotRunning(t *testing.T) {
	shortHome(t)

	var out, errb bytes.Buffer
	if code := run([]string{"status"}, &out, &errb); code != exitDaemonNotFound {
		t.Fatalf("code = %d, want %d", code, exitDaemonNotFound)
	}
	if !strings.Contains(errb.String(), "not running") {
		t.Fatalf("stderr = %q", errb.String())
	}

	out.Reset()
	errb.Reset()
	if code := run([]string{"status", "--json"}, &out, &errb); code != exitDaemonNotFound {
		t.Fatalf("json code = %d", code)
	}
	var body struct {
		OK    bool `json:"ok"`
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("stdout not JSON: %q: %v", out.String(), err)
	}
	if body.OK || body.Error.Code != "daemon_not_running" || body.Error.Message == "" {
		t.Fatalf("body = %+v", body)
	}
}

func TestStatusRunning(t *testing.T) {
	t.Setenv(identity.KeystoreEnv, "file") // never touch the real keychain from tests
	p := shortHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, p, ready) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}

	var out, errb bytes.Buffer
	start := time.Now()
	code := run([]string{"status", "--json"}, &out, &errb)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("status took %v", elapsed)
	}
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %q", code, errb.String())
	}
	var body struct {
		OK            bool    `json:"ok"`
		PID           int     `json:"pid"`
		UptimeSeconds float64 `json:"uptime_seconds"`
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil {
		t.Fatalf("stdout not JSON: %q: %v", out.String(), err)
	}
	if !body.OK || body.PID != os.Getpid() {
		t.Fatalf("body = %+v", body)
	}

	out.Reset()
	if code := run([]string{"status"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "running") ||
		!strings.Contains(out.String(), "outbox:  0 queued, 0 relayed, 0 expired") {
		t.Fatalf("human status: code=%d out=%q", code, out.String())
	}
}

func TestUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"status", "--help"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "--json") {
		t.Fatalf("status --help: code=%d out=%q", code, out.String())
	}
	if code := run([]string{"bogus"}, &out, &errb); code != exitUsage {
		t.Fatalf("bogus code = %d", code)
	}
	if code := run([]string{"status", "--bogus"}, &out, &errb); code != exitUsage {
		t.Fatalf("bad flag code = %d", code)
	}
	out.Reset()
	if code := run([]string{"--version"}, &out, &errb); code != exitOK || !strings.HasPrefix(out.String(), "agentnet ") {
		t.Fatalf("version: %q", out.String())
	}
}
