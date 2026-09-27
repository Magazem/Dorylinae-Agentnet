package relayclient_test

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// TestProbeReadsChallengeWithoutAuthenticating is the passing case: Probe
// reaches a real relay, reads its challenge (auth versions, expiry) and never
// registers a connection (doctor must never authenticate with the identity
// key, review 50 M4).
func TestProbeReadsChallengeWithoutAuthenticating(t *testing.T) {
	pub, _ := newKey(t)
	key := envelope.KeyString(pub)
	hs := httptest.NewUnstartedServer(nil)
	origin := "ws://" + hs.Listener.Addr().String()
	srv := relay.New(relay.Options{Public: true, Origins: []string{origin}})
	hs.Config.Handler = srv
	hs.Start()
	defer hs.Close()
	defer srv.Close()

	ch, err := relayclient.Probe(context.Background(), "ws"+strings.TrimPrefix(hs.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !contains(ch.Auth, envelope.AuthV2) {
		t.Fatalf("Auth = %v, want it to offer v2", ch.Auth)
	}
	if ch.Expires.IsZero() || time.Until(ch.Expires) <= 0 || time.Until(ch.Expires) > time.Minute {
		t.Fatalf("Expires = %v, want a few seconds in the future", ch.Expires)
	}
	if srv.Connected(key) {
		t.Fatal("Probe registered a connection; it must never authenticate")
	}
}

// TestProbeFailsOnUnreachableRelay is the failing case: no listener at all.
func TestProbeFailsOnUnreachableRelay(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing is listening now

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := relayclient.Probe(ctx, "ws://"+addr, nil); err == nil {
		t.Fatal("Probe succeeded against an unreachable relay")
	}
}

// TestProbeRejectsBadURL is a failing case that never dials anything.
func TestProbeRejectsBadURL(t *testing.T) {
	for _, u := range []string{"", "http://x", "not a url"} {
		if _, err := relayclient.Probe(context.Background(), u, nil); err == nil {
			t.Errorf("Probe(%q) = nil error, want one", u)
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
