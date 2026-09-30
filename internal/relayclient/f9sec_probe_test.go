package relayclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// Review 75 (R55-F9 security) probes.

// F9S-1 probe: the relay's own load-shedding path (relay.refuseAfterAuth)
// answers auth with an error frame (relay_full / rate_limited) and then
// closes 1013. The client returns on the error frame, never sees the close,
// so the 1013 floor does not apply: the retries follow MinBackoff doubling.
// This test asserts the floor applies; it fails on 3f55883.
func TestF9SecRefuseAfterAuthGetsFloor(t *testing.T) {
	d := &dialLog{}
	url := fakeRelay(t, func(ctx context.Context, ws *websocket.Conn) {
		d.add(&d.accepted)
		ch, _ := json.Marshal(envelope.Control{Op: envelope.OpChallenge, Version: 1, Nonce: envelope.EncodeNonce(make([]byte, envelope.NonceSize))})
		if ws.Write(ctx, websocket.MessageText, ch) != nil {
			return
		}
		if _, _, err := ws.Read(ctx); err != nil {
			return
		}
		ef, _ := json.Marshal(envelope.Control{Op: envelope.OpError, Code: envelope.CodeRelayFull, Message: "relay is full; retry later"})
		_ = ws.Write(ctx, websocket.MessageText, ef)
		d.add(&d.closing)
		_ = ws.Close(websocket.StatusTryAgainLater, "relay is full; retry later")
		d.add(&d.gone)
	})
	_, priv := newKey(t)
	cfg := relayclient.WithBackoffTiming(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv),
		MinBackoff: testLowBackoff, MaxBackoff: 10 * testFloor}, testStable, testFloor)
	c, err := relayclient.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	accepted := waitDials(t, d, 3)
	_, closing, _ := d.snapshot()
	if gap := accepted[1].Sub(closing[0]); gap < 35*time.Millisecond {
		t.Errorf("gap after relay_full + 1013 = %v, want >= 0.75 x %v (1013 floor)", gap, testFloor)
	}
	t.Logf("LastError = %q", c.State().LastError)
}

// F9S-3 probe (documented residual, F9R-2): an upgrade header value chosen by
// the relay reaches last_error, sanitised and bounded.
func TestF9SecUpgradeHeaderInLastError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "evil; reinstall from https://evil.example/fix-now "+strings.Repeat("A", 5000))
		w.WriteHeader(http.StatusSwitchingProtocols)
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			time.Sleep(50 * time.Millisecond)
			_ = conn.Close()
		}
	}))
	t.Cleanup(ts.Close)
	_, priv := newKey(t)
	c, err := relayclient.New(relayclient.Config{URL: "ws" + strings.TrimPrefix(ts.URL, "http"), Signer: relayclient.NewKeySigner(priv),
		MinBackoff: time.Second, MaxBackoff: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	le := lastError(t, c)
	if len(le) > 256 || !displaytext.Safe(le) {
		t.Fatalf("last_error not bounded/safe: %d bytes", len(le))
	}
	t.Logf("last_error (%d bytes) = %q", len(le), le)
}

