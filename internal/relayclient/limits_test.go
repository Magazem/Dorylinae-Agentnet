package relayclient_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// relay_full (and rate_limited after auth) arrive instead of ready: the
// client treats them as a failed connection and keeps retrying with its
// normal backoff, and connects once the relay has room (ticket 4.0b).
func TestRelayFullIsRetriedLater(t *testing.T) {
	s := relay.New(relay.Options{MaxConns: 1})
	t.Cleanup(s.Close)
	url := serve(t, s)
	_, privA := newKey(t)
	pubB, privB := newKey(t)

	actx, stopA := context.WithCancel(context.Background())
	a, err := relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(privA)})
	if err != nil {
		t.Fatal(err)
	}
	aDone := make(chan struct{})
	go func() { defer close(aDone); _ = a.Run(actx) }()
	eventually(t, "A connected", a.Connected)

	b, err := relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(privB),
		MinBackoff: 20 * time.Millisecond, MaxBackoff: 40 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	bctx, stopB := context.WithCancel(context.Background())
	defer stopB()
	go func() { _ = b.Run(bctx) }()
	time.Sleep(300 * time.Millisecond) // several refused attempts
	if b.Connected() || s.Connected(envelope.KeyString(pubB)) {
		t.Fatal("B connected past --max-conns")
	}
	stopA()
	<-aDone
	eventually(t, "B connected once the relay had room", b.Connected)
}

func serve(t *testing.T, s *relay.Server) string {
	t.Helper()
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return "ws" + strings.TrimPrefix(ts.URL, "http") + envelope.ConnectPath
}
