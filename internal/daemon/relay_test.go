package daemon_test

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

func relayTestPaths(t *testing.T) (paths.Paths, daemon.Options) {
	t.Helper()
	dir, err := os.MkdirTemp("", "dn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}
	ks, err := identity.NewKeystore(p.Dir, "file") // never touch the real keychain from tests
	if err != nil {
		t.Fatal(err)
	}
	return p, daemon.Options{Keystore: ks}
}

func TestDaemonConnectsToRelay(t *testing.T) {
	srv := relay.New(relay.Options{})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	p, opts := relayTestPaths(t)
	opts.RelayURL = "ws" + strings.TrimPrefix(ts.URL, "http")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.RunWithOptions(ctx, p, ready, opts) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}

	var id daemon.IdentityResult
	cctx, ccancel := context.WithTimeout(ctx, 2*time.Second)
	defer ccancel()
	if err := ipc.Call(cctx, p.Endpoint, "identity", nil, &id); err != nil {
		t.Fatal(err)
	}
	key := id.Card.PublicKey

	deadline := time.Now().Add(10 * time.Second)
	for !srv.Connected(key) {
		if time.Now().After(deadline) {
			t.Fatal("daemon never appeared in the relay registry")
		}
		time.Sleep(10 * time.Millisecond)
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
	deadline = time.Now().Add(10 * time.Second)
	for srv.Connected(key) {
		if time.Now().After(deadline) {
			t.Fatal("daemon still registered after shutdown")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestStatusReportsRelayState is the passing case (4.4c): once the daemon has
// connected, "status --json" reports the relay's URL, connected=true, a
// "since" timestamp and auth "v2".
func TestStatusReportsRelayState(t *testing.T) {
	srv := relay.New(relay.Options{})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	p, opts := relayTestPaths(t)
	relayURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	opts.RelayURL = relayURL

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.RunWithOptions(ctx, p, ready, opts) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon exited early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}

	var res daemon.StatusResult
	deadline := time.Now().Add(10 * time.Second)
	for {
		cctx, ccancel := context.WithTimeout(ctx, 2*time.Second)
		err := ipc.Call(cctx, p.Endpoint, "status", nil, &res)
		ccancel()
		if err != nil {
			t.Fatal(err)
		}
		if res.Relay != nil && res.Relay.Connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay never reported connected: %+v", res.Relay)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if res.Relay.URL != relayURL {
		t.Errorf("Relay.URL = %q, want %q", res.Relay.URL, relayURL)
	}
	if res.Relay.Auth != "v2" {
		t.Errorf("Relay.Auth = %q, want v2", res.Relay.Auth)
	}
	if res.Relay.Since == nil || *res.Relay.Since == "" {
		t.Error("Relay.Since is empty while connected")
	}
	if res.Relay.LastError != "" {
		t.Errorf("Relay.LastError = %q, want empty on a clean connect", res.Relay.LastError)
	}
}

// TestStatusReportsNoRelayWhenUnconfigured is the failing case (4.4c): with no
// --relay, "status" omits the relay object entirely.
func TestStatusReportsNoRelayWhenUnconfigured(t *testing.T) {
	p, opts := relayTestPaths(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- daemon.RunWithOptions(ctx, p, ready, opts) }()
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
	if res.Relay != nil {
		t.Errorf("Relay = %+v, want nil with no relay configured", res.Relay)
	}
}

func TestDaemonRejectsBadRelayURL(t *testing.T) {
	p, opts := relayTestPaths(t)
	opts.RelayURL = "http://127.0.0.1:1"
	if err := daemon.RunWithOptions(context.Background(), p, nil, opts); err == nil || !strings.Contains(err.Error(), "ws://") {
		t.Fatalf("err = %v, want a URL error", err)
	}
}
