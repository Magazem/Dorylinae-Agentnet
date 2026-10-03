package relayclient_test

import (
	"context"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// failedDials is how many dials fail before the first ready in the re-dial
// test. With MinBackoff 10 ms the normal path's next wait is then
// jitter(1280 ms) >= 960 ms, far from the re-dial's jitter(10 ms) <= 12.5 ms.
const failedDials = 7

var redialRetryIn = regexp.MustCompile(`event=relay_redial retry_in=(\S+)`)

// R55-F29 test 7 (R55-071, review 92b F4, review 101 L5): Reconnect drops the
// connection and Run dials again after jitter(MinBackoff), with no
// classification and no doubling, logged relay_redial and never
// relay_disconnect.
func TestReconnectRedialsAtMinBackoff(t *testing.T) {
	var dials atomic.Int32
	url := fakeRelay(t, func(ctx context.Context, ws *websocket.Conn) {
		if dials.Add(1) <= failedDials {
			return // failed dials push the backoff up
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
	const minBackoff = 10 * time.Millisecond
	c, err := relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv),
		Logger: newTextLogger(logs), MinBackoff: minBackoff, MaxBackoff: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "the first connection", c.Connected)
	if n := dials.Load(); n != failedDials+1 {
		t.Fatalf("dials = %d, want %d (%d failed, one ready)", n, failedDials+1, failedDials)
	}
	before := c.State().Since
	disconnects := strings.Count(logs.String(), "relay_disconnect")
	time.Sleep(5 * time.Millisecond) // a later Since is observable

	c.Reconnect()
	eventually(t, "the re-dial", func() bool { return dials.Load() == failedDials+2 && c.Connected() })
	if after := c.State().Since; !after.After(before) {
		t.Errorf("Since = %v, want after %v", after, before)
	}
	out := logs.String()
	m := redialRetryIn.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("log has no relay_redial with retry_in:\n%s", out)
	}
	wait, err := time.ParseDuration(m[1])
	if err != nil {
		t.Fatalf("retry_in %q: %v", m[1], err)
	}
	// The doubled or reclassified delay would be at least 960 ms here.
	if wait > 2*minBackoff {
		t.Errorf("re-dial waited %v, want jitter(MinBackoff) <= %v", wait, 2*minBackoff)
	}
	if n := strings.Count(out, "relay_disconnect"); n != disconnects {
		t.Errorf("relay_disconnect lines %d -> %d, want none for the re-dial:\n%s", disconnects, n, out)
	}
}

// A Reconnect while disconnected changes nothing: Run keeps its own cadence
// against a relay that never sends ready, no relay_redial is logged and no
// extra dial happens (review 101 L5).
func TestReconnectWhileDisconnectedIsNoop(t *testing.T) {
	var dials atomic.Int32
	url := fakeRelay(t, func(context.Context, *websocket.Conn) { dials.Add(1) })
	_, priv := newKey(t)
	logs := &syncLog{}
	c, err := relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv),
		Logger: newTextLogger(logs), MinBackoff: time.Second, MaxBackoff: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "the first failed dial", func() bool { return dials.Load() == 1 })
	// Run now waits jitter(1 s) >= 750 ms before the next dial.
	for i := 0; i < 5; i++ {
		c.Reconnect()
		time.Sleep(20 * time.Millisecond)
	}
	if n := dials.Load(); n != 1 {
		t.Errorf("dials = %d after Reconnect while disconnected, want 1", n)
	}
	if c.Connected() {
		t.Error("connected to a relay that never sends ready")
	}
	if strings.Contains(logs.String(), "relay_redial") {
		t.Errorf("relay_redial logged while disconnected:\n%s", logs.String())
	}
}

// Review 101 L3: a session the relay ends with 1013 keeps the 1013 floor even
// when a stale redial flag is set: relay_disconnect, never relay_redial.
func TestStaleRedialKeepsTryAgainFloor(t *testing.T) {
	var dials atomic.Int32
	closeNow := make(chan struct{})
	var secondDial atomic.Int64
	url := fakeRelay(t, func(ctx context.Context, ws *websocket.Conn) {
		if dials.Add(1) > 1 {
			secondDial.CompareAndSwap(0, time.Now().UnixNano())
			<-ctx.Done()
			return
		}
		if !acceptAuth(ctx, ws) {
			return
		}
		select {
		case <-closeNow:
		case <-ctx.Done():
			return
		}
		_ = ws.Close(websocket.StatusTryAgainLater, "retry later")
	})
	_, priv := newKey(t)
	logs := &syncLog{}
	const floor = 400 * time.Millisecond
	c, err := relayclient.New(relayclient.WithBackoffTiming(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv),
		Logger: newTextLogger(logs), MinBackoff: 10 * time.Millisecond, MaxBackoff: 5 * time.Second}, time.Hour, floor))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	eventually(t, "the first connection", c.Connected)
	relayclient.MarkRedial(c)
	closed := time.Now()
	close(closeNow)
	eventually(t, "the second dial", func() bool { return secondDial.Load() != 0 })
	// jitter(floor) >= 300 ms; the re-dial path would wait about 10 ms.
	if gap := time.Unix(0, secondDial.Load()).Sub(closed); gap < floor*3/4-50*time.Millisecond {
		t.Errorf("second dial %v after the 1013 close, want the floor (>= ~%v)", gap, floor*3/4)
	}
	out := logs.String()
	if strings.Contains(out, "relay_redial") {
		t.Errorf("a relay 1013 close with a stale redial flag was logged relay_redial:\n%s", out)
	}
	if !strings.Contains(out, "relay_disconnect") {
		t.Errorf("no relay_disconnect for the 1013 close:\n%s", out)
	}
}
