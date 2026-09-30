package relayclient_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// R55-F9 test 10 (review 55 C04-02, review 67b F9R-4/F9R-5): the backoff
// resets only after a connection stayed up stableAfter after ready, and never
// after a close with 1013. Assertions are on gaps, not dial counts: jitter
// (75-125 %) makes counts in a fixed window flaky.

const (
	testStable      = 300 * time.Millisecond
	testFloor       = 50 * time.Millisecond
	testLowBackoff  = 10 * time.Millisecond
	testHighBackoff = 80 * time.Millisecond
	testHoldOpen    = 350 * time.Millisecond
)

// dialLog records, per connection, when the relay accepted it and when it
// started (closing) and finished closing it.
type dialLog struct {
	mu                      sync.Mutex
	accepted, closing, gone []time.Time
}

func (d *dialLog) add(list *[]time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	*list = append(*list, time.Now())
}

func (d *dialLog) snapshot() (accepted, closing, gone []time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]time.Time(nil), d.accepted...), append([]time.Time(nil), d.closing...), append([]time.Time(nil), d.gone...)
}

// backoffClient runs a client with the test backoff timing against a relay
// that authenticates, holds each connection for hold, then closes it with
// status.
func backoffClient(t *testing.T, hold time.Duration, status websocket.StatusCode) (*relayclient.Client, *dialLog) {
	t.Helper()
	d := &dialLog{}
	url := fakeRelay(t, func(ctx context.Context, ws *websocket.Conn) {
		d.add(&d.accepted)
		if !acceptAuth(ctx, ws) {
			return
		}
		go func() { // answer the client's close handshake
			for {
				if _, _, err := ws.Read(ctx); err != nil {
					return
				}
			}
		}()
		time.Sleep(hold)
		d.add(&d.closing)
		_ = ws.Close(status, "")
		d.add(&d.gone)
	})
	_, priv := newKey(t)
	cfg := relayclient.WithBackoffTiming(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv),
		MinBackoff: testLowBackoff, MaxBackoff: testHighBackoff}, testStable, testFloor)
	c, err := relayclient.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return c, d
}

func waitDials(t *testing.T, d *dialLog, n int) []time.Time {
	t.Helper()
	var accepted []time.Time
	deadline := time.Now().Add(20 * time.Second)
	for {
		accepted, _, _ = d.snapshot()
		if len(accepted) >= n {
			return accepted
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d dials, want %d", len(accepted), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A relay that sends ready and closes at once no longer resets the backoff:
// from the 5th gap on every gap is at least 0.75 x 80 ms, minus slack.
func TestBackoffReadyAndCloseKeepsGrowing(t *testing.T) {
	_, d := backoffClient(t, 0, websocket.StatusNormalClosure)
	accepted := waitDials(t, d, 8)
	for i := 5; i < 8; i++ {
		if gap := accepted[i].Sub(accepted[i-1]); gap < 55*time.Millisecond {
			t.Errorf("gap %d = %v, want >= 55ms (the backoff was reset)", i, gap)
		}
	}
}

// A connection that stayed up past stableAfter resets the backoff: the next
// dial follows the close within MinBackoff's jitter.
func TestBackoffResetsAfterStableConnection(t *testing.T) {
	_, d := backoffClient(t, testHoldOpen, websocket.StatusNormalClosure)
	accepted := waitDials(t, d, 4)
	_, closing, _ := d.snapshot()
	for i := 1; i < 4; i++ {
		if gap := accepted[i].Sub(closing[i-1]); gap >= 40*time.Millisecond {
			t.Errorf("gap after close %d = %v, want < 40ms (reset to %v)", i, gap, testLowBackoff)
		}
	}
}

// A close with 1013 never resets the backoff, and the next wait is at least
// jitter(tryAgainFloor).
func TestBackoffTryAgainLaterFloor(t *testing.T) {
	c, d := backoffClient(t, testHoldOpen, websocket.StatusTryAgainLater)
	accepted := waitDials(t, d, 4)
	_, _, gone := d.snapshot()
	for i := 1; i < 4; i++ {
		if gap := accepted[i].Sub(gone[i-1]); gap < 35*time.Millisecond {
			t.Errorf("gap after 1013 close %d = %v, want >= 35ms", i, gap)
		}
	}
	if le := c.State().LastError; le != "closed by relay (status 1013)" {
		t.Errorf("LastError = %q, want %q", le, "closed by relay (status 1013)")
	}
}
