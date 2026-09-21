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

func TestDaemonRejectsBadRelayURL(t *testing.T) {
	p, opts := relayTestPaths(t)
	opts.RelayURL = "http://127.0.0.1:1"
	if err := daemon.RunWithOptions(context.Background(), p, nil, opts); err == nil || !strings.Contains(err.Error(), "ws://") {
		t.Fatalf("err = %v, want a URL error", err)
	}
}
