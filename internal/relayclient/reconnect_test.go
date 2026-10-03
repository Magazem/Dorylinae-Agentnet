package relayclient_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// R55-F29 test 7 (R55-071, review 92b F4): Reconnect drops the connection and
// Run dials again after jitter(MinBackoff), with no classification and no
// doubling, logged relay_redial and never relay_disconnect.
func TestReconnectRedialsAtMinBackoff(t *testing.T) {
	var dials atomic.Int32
	url := fakeRelay(t, func(ctx context.Context, ws *websocket.Conn) {
		if dials.Add(1) <= 2 {
			return // two failed dials push the backoff up
		}
		if !acceptAuth(ctx, ws) {
			return
		}
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				return
			}
		}
	})
	_, priv := newKey(t)
	logs := &syncLog{}
	c, err := relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv),
		Logger: newTextLogger(logs), MinBackoff: 10 * time.Millisecond, MaxBackoff: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "the first connection", c.Connected)
	if n := dials.Load(); n != 3 {
		t.Fatalf("dials = %d, want 3 (two failed, one ready)", n)
	}
	before := c.State().Since
	disconnects := strings.Count(logs.String(), "relay_disconnect")
	time.Sleep(5 * time.Millisecond) // a later Since is observable

	start := time.Now()
	c.Reconnect()
	for dials.Load() != 4 || !c.Connected() {
		if time.Since(start) > time.Second {
			t.Fatalf("no ready within 1s of Reconnect (dials %d)", dials.Load())
		}
		time.Sleep(2 * time.Millisecond)
	}
	if after := c.State().Since; !after.After(before) {
		t.Errorf("Since = %v, want after %v", after, before)
	}
	out := logs.String()
	if !strings.Contains(out, "relay_redial") {
		t.Errorf("log has no relay_redial:\n%s", out)
	}
	if n := strings.Count(out, "relay_disconnect"); n != disconnects {
		t.Errorf("relay_disconnect lines %d -> %d, want none for the re-dial:\n%s", disconnects, n, out)
	}
}

// A Reconnect while disconnected changes nothing: no dial, no state change.
func TestReconnectWhileDisconnectedIsNoop(t *testing.T) {
	var dials atomic.Int32
	url := fakeRelay(t, func(context.Context, *websocket.Conn) { dials.Add(1) })
	_, priv := newKey(t)
	c, err := relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv)})
	if err != nil {
		t.Fatal(err)
	}
	st := c.State()
	c.Reconnect()
	time.Sleep(50 * time.Millisecond)
	if n := dials.Load(); n != 0 {
		t.Errorf("dials = %d, want 0", n)
	}
	if got := c.State(); got != st {
		t.Errorf("State = %+v, want %+v", got, st)
	}
}
