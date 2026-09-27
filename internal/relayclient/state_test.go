package relayclient_test

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// TestStateTracksConnectAndDisconnect is the passing case: State().Connected
// follows the real connection, "Since" moves at each transition, and a
// failure after a successful connection is recorded in LastError (the
// failing case is the disconnect half of the same run).
func TestStateTracksConnectAndDisconnect(t *testing.T) {
	_, priv := newKey(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	c, err := relayclient.New(relayclient.Config{
		URL:        "ws://" + addr,
		Signer:     relayclient.NewKeySigner(priv),
		MinBackoff: 20 * time.Millisecond,
		MaxBackoff: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	before := c.State()
	if before.Connected {
		t.Fatal("new client reports Connected")
	}
	firstSince := before.Since
	if firstSince.IsZero() {
		t.Fatal("Since is zero before any connection")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after cancel")
		}
	}()

	srv := relay.New(relay.Options{})
	hs := &http.Server{Handler: srv, ReadHeaderTimeout: time.Second}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = hs.Serve(l) }()

	eventually(t, "client to connect", func() bool { return c.State().Connected })
	connectedState := c.State()
	if !connectedState.Since.After(firstSince) {
		t.Fatalf("Since did not advance on connect: %v -> %v", firstSince, connectedState.Since)
	}

	_ = hs.Close()
	srv.Close()
	eventually(t, "client to notice the relay is gone", func() bool { return !c.State().Connected })
	after := c.State()
	if !after.Since.After(connectedState.Since) {
		t.Fatalf("Since did not advance on disconnect: %v -> %v", connectedState.Since, after.Since)
	}
	if after.LastError == "" {
		t.Fatal("LastError is empty after a disconnect")
	}
}
