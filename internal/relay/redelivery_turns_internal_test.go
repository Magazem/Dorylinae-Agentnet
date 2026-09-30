package relay

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"
)

// Review 74 (security review of R55-F2): turns in a prefix's wait list (M-1,
// L-3) and reads bounded by the budget (M-2).

// testClock is a fake clock safe for the relay's goroutines.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// M-1 (the reviewer's probe, inverted): a served connection that stops
// reading does not keep its prefix's honest waiter from being served.
func TestRedeliverSlowReaderDoesNotHoldTurn(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := testServer(t, noBufferLimits(Options{Now: clock.Now, QueueRedeliverPerKey: -1, QueueRedeliverPerPrefix: 64 << 10}))
	const prefix = "10.2.0.0"
	a, v := testKey(t), testKey(t)
	queueRows(t, s.q, a, "a", 60, 100)
	queueRows(t, s.q, v, "v", 5, 100)
	if n := drainAll(s, keyConn(t, s, a, prefix)); n != 60 {
		t.Fatalf("a first delivery %d", n)
	}
	if n := drainAll(s, keyConn(t, s, v, prefix)); n != 5 {
		t.Fatalf("v first delivery %d", n)
	}
	s.lim.mu.Lock()
	s.lim.redeliverPrefix.take(prefix, clock.Now(), 64<<10) // the prefix budget is spent
	s.lim.mu.Unlock()

	// A reconnects with a tiny buffer and never reads; it is skipped first.
	ca := newConn(nil, a, 4, prefix)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ca.led, ca.maxBytes, ca.ctx = s.led, s.lim.connBuffer, ctx
	s.mu.Lock()
	s.conns[a] = ca
	s.mu.Unlock()
	s.drainStep(ca, false)
	// V reconnects (an honest drop) and waits behind A.
	cv := keyConn(t, s, v, prefix)
	if n := drainAll(s, cv); n != 0 {
		t.Fatalf("v got %d before its turn", n)
	}
	clock.Advance(time.Hour)
	s.retrySkipped() // A is served and blocks on its full buffer
	for range 3 {
		clock.Advance(time.Hour)
		s.retrySkipped()
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(cv.out) != 5 {
		if time.Now().After(deadline) {
			t.Fatalf("V got %d of its 5 redeliveries while A held a turn without reading", len(cv.out))
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel() // ends A's blocked retry before the server closes
}

// M-1 and L-3, the wait list itself: every waiter whose row the prefix can
// still pay is served in the same tick, in order; a served connection its
// prefix runs short for goes back to the head; a newcomer goes to the tail;
// a turn older than redeliverTurn ends, and that connection then waits at
// the tail.
func TestRedeliverTurnsHeadSliceAndSeveralPerTick(t *testing.T) {
	clock := &testClock{now: time.Now()}
	l := newLimits(Options{QueueRedeliverPerKey: -1, QueueRedeliverPerPrefix: 3600}, clock.Now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	const prefix = "p"
	conns := map[string]*conn{}
	for _, name := range []string{"a", "b", "c", "n"} {
		conns[name] = newConn(nil, name, 4, prefix)
	}
	a, b, c, n := conns["a"], conns["b"], conns["c"], conns["n"]
	order := func() string {
		l.mu.Lock()
		defer l.mu.Unlock()
		var out string
		for _, x := range l.redeliverWait[prefix] {
			out += x.key
		}
		return out
	}
	l.mu.Lock()
	l.redeliverPrefix.take(prefix, clock.Now(), 3600)
	l.mu.Unlock()
	for _, x := range []*conn{a, b, c} {
		if got := l.redeliverAllowed(x, 1500); got != limitRedeliverPrefix {
			t.Fatalf("%s: %q with the prefix budget spent", x.key, got)
		}
		x.skipSize = 1500
	}
	if got := order(); got != "abc" {
		t.Fatalf("wait list %q, want abc", got)
	}
	skipped := func(x *conn) int64 { return x.skipSize }
	started := map[*conn]bool{}
	start := func(x *conn) bool { started[x] = true; return true }

	clock.Advance(time.Hour) // 3600 bytes: a and b fit (3000), c does not
	picked := l.nextServed(skipped, start)
	if len(picked) != 2 || picked[0] != a || picked[1] != b || order() != "c" {
		t.Fatalf("picked %d, list %q; want a and b served in one tick, c waiting", len(picked), order())
	}
	// A newcomer waits behind the list.
	if got := l.redeliverAllowed(n, 10); got != limitRedeliverPrefix || order() != "cn" {
		t.Fatalf("newcomer: %q, list %q; want refused at the tail", got, order())
	}
	// a is paid once, then the prefix runs short: it goes back to the head.
	if got := l.redeliverAllowed(a, 1500); got != "" {
		t.Fatalf("served a refused: %q", got)
	}
	if got := l.redeliverAllowed(a, 2500); got != limitRedeliverPrefix || order() != "acn" || l.waitingOrServed(a) != true {
		t.Fatalf("a short of prefix budget: %q, list %q; want it at the head", got, order())
	}
	// b's turn is still on after 30 s, and ends after redeliverTurn.
	clock.Advance(30 * time.Second)
	l.nextServed(func(*conn) int64 { return 0 }, start)
	if _, ok := l.redeliverServed[prefix][b]; !ok {
		t.Fatal("b's turn ended within its slice")
	}
	clock.Advance(redeliverTurn)
	l.nextServed(func(*conn) int64 { return 0 }, start)
	if _, ok := l.redeliverServed[prefix][b]; ok {
		t.Fatal("b's turn outlived redeliverTurn")
	}
	if got := l.redeliverAllowed(b, 10); got != limitRedeliverPrefix || order() != "acnb" {
		t.Fatalf("b after its turn: %q, list %q; want it at the tail", got, order())
	}
	if !slices.Equal([]bool{started[a], started[b], started[c]}, []bool{true, true, false}) {
		t.Fatalf("started %v", started)
	}
}

// M-2 (the reviewer's probe, inverted): a tiny first old row passes the probe
// on every reconnect, but only what the budget pays is read from the
// database, not a whole batch.
func TestRedeliverReadsOnlyWhatBudgetPays(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := testServer(t, noBufferLimits(Options{Now: clock.Now, QueueRedeliverPerKey: 64 << 10, QueueRedeliverPerPrefix: -1}))
	key := testKey(t)
	queueRows(t, s.q, key, "tiny", 1, 100)
	queueRows(t, s.q, key, "big", 63, 16<<10)
	if n := drainAll(s, keyConn(t, s, key, "10.2.0.0")); n != 64 {
		t.Fatalf("first delivery %d", n)
	}
	drainAll(s, keyConn(t, s, key, "10.2.0.0")) // spends the burst
	var mu sync.Mutex
	var readBytes int64
	s.q.mu.Lock()
	s.q.readHook = func(_ string, rows []queued) {
		mu.Lock()
		defer mu.Unlock()
		for _, r := range rows {
			readBytes += int64(len(r.frame))
		}
	}
	s.q.mu.Unlock()
	sentBefore := s.redeliveredBytes.Load()
	for range 20 {
		clock.Advance(8 * time.Second) // 20 reconnects in under 3 minutes; the tiny row fits each time
		drainAll(s, keyConn(t, s, key, "10.2.0.0"))
	}
	sent := s.redeliveredBytes.Load() - sentBefore
	mu.Lock()
	defer mu.Unlock()
	t.Logf("20 reconnects: read %d bytes of old frames, redelivered %d", readBytes, sent)
	if sent == 0 || readBytes > sent {
		t.Fatalf("read %d bytes of old frames for %d redelivered: reads not bounded by the budget", readBytes, sent)
	}
}
