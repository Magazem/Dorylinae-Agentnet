package relay_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

// Memory budgets and fairness, Docs/protocol/relay-hosted.md §2 (R55-F1).
// The acceptance tests of Docs/review/56-r55-f1-spec.md §3; the numbers in
// the comments are the spec's.

// watchMax samples f every millisecond until stop is called, and returns
// the largest value seen.
func watchMax(f func() int64) (stop func() int64) {
	var seen atomic.Int64
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			if n := f(); n > seen.Load() {
				seen.Store(n)
			}
			select {
			case <-quit:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	return func() int64 {
		close(quit)
		<-done
		return seen.Load()
	}
}

// waitUntil polls cond until it holds or d passes.
func waitUntil(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// startFrame begins a text frame on c and writes data, leaving it unfinished.
func startFrame(t *testing.T, c *websocket.Conn, data []byte) io.WriteCloser {
	t.Helper()
	w, err := c.Writer(ctx(t), websocket.MessageText)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	return w
}

// startFrameAsync is startFrame for a connection the relay may close while
// the data is still being written; write errors are ignored.
func startFrameAsync(t *testing.T, c *websocket.Conn, data []byte) {
	t.Helper()
	w, err := c.Writer(ctx(t), websocket.MessageText)
	if err != nil {
		return
	}
	go func() { _, _ = w.Write(data) }()
}

// readEnvID reads the next frame on c within d and returns its envelope id;
// a control frame fails the test.
func readEnvID(t *testing.T, c *websocket.Conn, d time.Duration) string {
	t.Helper()
	rctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_, raw, err := c.Read(rctx)
	if err != nil {
		t.Fatalf("no frame within %v: %v", d, err)
	}
	h, err := envelope.ParseHeader(raw)
	if err != nil {
		t.Fatalf("got %.120s, want an envelope", raw)
	}
	return h.ID
}

// noFrameWithin asserts nothing arrives on c for d. It closes c (a read
// that times out closes a coder/websocket connection), so it goes last.
func noFrameWithin(t *testing.T, c *websocket.Conn, d time.Duration) {
	t.Helper()
	rctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	if _, raw, err := c.Read(rctx); err == nil {
		t.Fatalf("got %.120s, want nothing", raw)
	}
}

// bufCap is what the relay charges to the read budget once n bytes of a
// frame have arrived: readFrame's buffer capacity, grown the same way (a
// power of two only by chance: 4096, 9472, 20480, ... 368640, 901120).
func bufCap(n int) int64 {
	var buf []byte
	for {
		if len(buf) == cap(buf) {
			buf = slices.Grow(buf, max(1, min(max(cap(buf), 4<<10), envelope.MaxFrameBytes+1-len(buf))))
		}
		if len(buf) >= n {
			return int64(cap(buf))
		}
		buf = buf[:min(cap(buf), n)]
	}
}

func presence(p peer, to, id string, body []byte) []byte {
	return p.typed(envelope.TypePresence, to, id, body)
}

// Converted reviewer tests.

// Acceptance test 1 (review 55 C01-01, zz_review55_C01-01_test.go):
// presence to the attacker's own non-reading recipients was charged to the
// outbound budget, so every direct mail on the relay took the queue path.
// Presence now has a budget of its own and never touches the mail budget.
func TestLimitPresenceFloodKeepsMailDirect(t *testing.T) {
	presenceFloodKeepsMailDirect(t, relay.Options{MaxInflight: 512 << 10, EphemeralPerMinute: 1 << 20, MaxInflightEphemeral: 128 << 10}, 128<<10)
}

// Acceptance test 2 (C01-01v): the same with the default 600/min ephemeral
// rate and the default ephemeral budget, max(512 KiB / 8, 1 MiB).
func TestLimitPresenceFloodDefaultRate(t *testing.T) {
	presenceFloodKeepsMailDirect(t, relay.Options{MaxInflight: 512 << 10}, 1<<20)
}

func presenceFloodKeepsMailDirect(t *testing.T, opts relay.Options, ephBudget int64) {
	e := newLimitEnv(t, opts)
	// Victims: two honest peers on another network, both reading.
	va, vb := newPeer(t), newPeer(t)
	cva := e.authed(va, "10.9.0.1")
	cvb := e.authed(vb, "10.9.0.2")
	waitDrained(t, e.s, va.key)
	waitDrained(t, e.s, vb.key)
	// Attacker: 4 recipient keys that never read, one sender key.
	atk := newPeer(t)
	ca := e.authed(atk, "10.1.0.1")
	var sinks []peer
	for i := range 4 {
		r := newPeer(t)
		e.authed(r, fmt.Sprintf("10.1.0.%d", i+2)) // never read from
		waitDrained(t, e.s, r.key)
		sinks = append(sinks, r)
	}
	waitUntil(t, wait, "no drain reservation left", func() bool { return e.s.Inflight() == 0 })
	stopOut := watchMax(e.s.Inflight)
	stopEph := watchMax(e.s.EphemeralInflight)
	body := payload(5500) // frame stays under the 8 KiB ephemeral cap
	for i := range 4 * 40 {
		writeFrame(t, ca, presence(atk, sinks[i%len(sinks)].key, fmt.Sprintf("p%d", i), body))
		if i%64 == 63 {
			time.Sleep(5 * time.Millisecond)
		}
	}
	time.Sleep(200 * time.Millisecond) // let the relay route the flood
	if n := stopOut(); n != 0 {
		t.Fatalf("presence reached the outbound (mail) budget: %d bytes", n)
	}
	if n := stopEph(); n > ephBudget {
		t.Fatalf("ephemeral budget held %d bytes, budget %d", n, ephBudget)
	}

	// An honest small mail between the two reading victims is forwarded
	// directly: delivered, and no queued reply.
	writeFrame(t, cva, []byte(mustJSON(va.env(vb.key, "honest-1", payload(200)))))
	if id := readEnvID(t, cvb, 2*time.Second); id != "honest-1" {
		t.Fatalf("vb got %q, want honest-1", id)
	}
	noFrameWithin(t, cva, time.Second)
}

// Acceptance test 3 (review 55 C01-02, zz_review55_C01-02_test.go):
// unfinished frames held the read budget and every other connection was
// closed 1013 on its next frame. Now the honest frame evicts the oldest
// of the heaviest holders instead.
func TestLimitUnfinishedFramesEvictHeaviest(t *testing.T) {
	// About 64 KiB: exactly 7 unfinished frames of 9000 bytes, so the
	// attackers fill the budget to the byte.
	budget := 7 * bufCap(9000)
	e := newLimitEnv(t, relay.Options{MaxInflight: budget})
	victim, other := newPeer(t), newPeer(t)
	cv := e.authed(victim, "10.9.0.1")
	co := e.authed(other, "10.9.0.2")
	waitDrained(t, e.s, other.key)

	// Attackers in separate /24s each start a frame of at least 8 KiB and
	// never finish it, until the read budget is full.
	var atk []*websocket.Conn
	for n := range 7 {
		a := newPeer(t)
		ca := e.authed(a, fmt.Sprintf("10.1.%d.1", n+1))
		startFrame(t, ca, payload(9000))
		prefix := fmt.Sprintf("10.1.%d.0", n+1)
		waitUntil(t, wait, "the attacker's frame to be charged", func() bool { return e.s.ReadingFor(prefix) == bufCap(9000) })
		atk = append(atk, ca)
		time.Sleep(5 * time.Millisecond) // distinct start times
	}
	if e.s.Reading() != budget {
		t.Fatalf("attackers hold %d of %d read-budget bytes", e.s.Reading(), budget)
	}

	// The victim's 200-byte mail is delivered, and the victim stays open.
	writeFrame(t, cv, []byte(mustJSON(victim.env(other.key, "honest-1", payload(200)))))
	if id := readEnvID(t, co, wait); id != "honest-1" {
		t.Fatalf("other got %q", id)
	}
	// Exactly one attacker connection, the oldest, is closed 1013.
	expectClose(t, atk[0], websocket.StatusTryAgainLater)
	for i := 1; i < len(atk); i++ {
		if e.s.ReadingFor(fmt.Sprintf("10.1.%d.0", i+1)) == 0 {
			t.Fatalf("attacker %d was evicted too", i)
		}
	}
	// A second honest mail is delivered too.
	writeFrame(t, cv, []byte(mustJSON(victim.env(other.key, "honest-2", payload(200)))))
	if id := readEnvID(t, co, wait); id != "honest-2" {
		t.Fatalf("other got %q", id)
	}
	if !e.s.Connected(victim.key) {
		t.Fatal("the victim was closed")
	}
	e.logged("evict_read", "prefix=10.1.1.0")
}

// Acceptance test 4 (C01-02v): at the early relay's size (48 MiB), one /24
// held the whole read budget. One prefix now holds at most its share, 6 MiB.
func TestLimitUnfinishedFramesPrefixShare(t *testing.T) {
	const budget, share = 48 << 20, 6 << 20
	e := newLimitEnv(t, relay.Options{MaxInflight: budget})
	victim, other := newPeer(t), newPeer(t)
	cv := e.authed(victim, "10.9.0.1")
	co := e.authed(other, "10.9.0.2")
	waitDrained(t, e.s, other.key)

	held := func() int64 { return e.s.ReadingFor("10.1.0.0/24") }
	stop := watchMax(held)
	for n := range 24 {
		a := newPeer(t)
		ca := e.authed(a, fmt.Sprintf("10.1.0.%d", n+1)) // all in 10.1.0.0/24
		// Just under a whole frame: charges the full ~1 MiB buffer. An
		// evicted attacker's write may fail; that is tolerated.
		startFrameAsync(t, ca, payload(1<<20-1))
		time.Sleep(30 * time.Millisecond)
		if got := held(); got > share {
			t.Fatalf("after attacker %d the prefix holds %d bytes of the read budget, share %d", n, got, share)
		}
	}
	if got := stop(); got > share {
		t.Fatalf("the prefix held up to %d bytes, share %d", got, share)
	}
	if got := e.s.Reading(); got > share {
		t.Fatalf("frames being read hold %d bytes; one prefix may hold %d", got, share)
	}

	writeFrame(t, cv, []byte(mustJSON(victim.env(other.key, "honest-1", payload(200)))))
	if id := readEnvID(t, co, wait); id != "honest-1" {
		t.Fatalf("other got %q", id)
	}
	if !e.s.Connected(victim.key) {
		t.Fatal("the victim was closed")
	}
}

// New tests.

// Acceptance test 5: a frame must be read completely within
// --frame-read-timeout of its first byte; a slow but steady one passes.
func TestLimitFrameReadDeadline(t *testing.T) {
	e := newLimitEnv(t, relay.Options{FrameReadTimeout: 300 * time.Millisecond})
	a, b, r := newPeer(t), newPeer(t), newPeer(t)
	ca := e.authed(a, "10.1.0.1")
	cb := e.authed(b, "10.2.0.1")
	cr := e.authed(r, "10.3.0.1")
	cr.SetReadLimit(2 << 20)
	waitDrained(t, e.s, r.key)

	start := time.Now()
	startFrame(t, ca, payload(5000)) // never finished
	expectClose(t, ca, websocket.StatusTryAgainLater)
	if el := time.Since(start); el > time.Second {
		t.Fatalf("closed after %v, want within 1 s", el)
	}
	waitUntil(t, time.Second, "the timed-out frame to be uncharged", func() bool { return e.s.Reading() == 0 })
	e.logged("frame_read_timeout", "peer="+a.key[:8])

	// A 256 KiB frame written in fragments over 200 ms is delivered.
	frame := []byte(mustJSON(b.env(r.key, "slow-1", payload(190_000))))
	w, err := cb.Writer(ctx(t), websocket.MessageText)
	if err != nil {
		t.Fatal(err)
	}
	chunk := len(frame)/8 + 1
	for i := 0; i < len(frame); i += chunk {
		if _, err := w.Write(frame[i:min(i+chunk, len(frame))]); err != nil {
			t.Fatal(err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if id := readEnvID(t, cr, wait); id != "slow-1" {
		t.Fatalf("got %q", id)
	}
}

// partFrame is how much of a ~347 KB frame the read-eviction tests send
// before stopping. Its buffer is then charged bufCap(partFrame), which also
// holds the rest of the frame.
const partFrame = 300_000

// holdFrame starts a frame from p (at ip) to `to` that stops after
// partFrame bytes, and waits until the relay has charged it.
func holdFrame(t *testing.T, e *limitEnv, p peer, ip, to, id string) (*websocket.Conn, io.WriteCloser, []byte) {
	t.Helper()
	c := e.authed(p, ip)
	before := e.s.Reading()
	frame := []byte(mustJSON(p.env(to, id, payload(260_000))))
	if int64(len(frame)) > bufCap(partFrame) {
		t.Fatalf("a %d-byte frame does not fit its first %d bytes' buffer", len(frame), bufCap(partFrame))
	}
	w := startFrame(t, c, frame[:partFrame])
	waitUntil(t, wait, "the frame to be charged", func() bool { return e.s.Reading() == before+bufCap(partFrame) })
	time.Sleep(5 * time.Millisecond) // distinct start times
	return c, w, frame
}

// Acceptance test 6: read eviction takes the heaviest prefix, oldest first.
func TestLimitReadEvictionHeaviestPrefixOldestFirst(t *testing.T) {
	budget := 4 * bufCap(partFrame) // ~1.4 MiB, filled exactly below; the share is the whole budget
	e := newLimitEnv(t, relay.Options{MaxInflight: budget})
	r := newPeer(t)
	cr := e.authed(r, "10.4.0.1")
	cr.SetReadLimit(2 << 20)
	waitDrained(t, e.s, r.key)
	var as []*websocket.Conn
	for i := range 3 { // prefix A: 3 unfinished frames
		c, _, _ := holdFrame(t, e, newPeer(t), fmt.Sprintf("10.1.0.%d", i+1), r.key, fmt.Sprintf("a-%d", i))
		as = append(as, c)
	}
	b := newPeer(t) // prefix B: one
	_, wb, frameB := holdFrame(t, e, b, "10.2.0.1", r.key, "b-1")
	if n := e.s.Reading(); n != budget {
		t.Fatalf("read budget holds %d, want it full (%d)", n, budget)
	}
	// A small frame from prefix C evicts A's oldest frame.
	c := newPeer(t)
	cc := e.authed(c, "10.3.0.1")
	writeFrame(t, cc, []byte(mustJSON(c.env(r.key, "c-1", payload(200)))))
	if id := readEnvID(t, cr, wait); id != "c-1" {
		t.Fatalf("got %q", id)
	}
	expectClose(t, as[0], websocket.StatusTryAgainLater)
	// B's frame then completes and is delivered.
	if _, err := wb.Write(frameB[partFrame:]); err != nil {
		t.Fatal(err)
	}
	if err := wb.Close(); err != nil {
		t.Fatal(err)
	}
	if id := readEnvID(t, cr, wait); id != "b-1" {
		t.Fatalf("got %q", id)
	}
	if n := e.s.ReadingFor("10.1.0.0"); n != 2*bufCap(partFrame) {
		t.Fatalf("prefix A holds %d; only its oldest frame should be gone", n)
	}
	e.logged("evict_read", "prefix=10.1.0.0")
}

// Acceptance test 7: the requester's own prefix pays when it is at its
// share; if the requester is its prefix's only holder, it is closed.
func TestLimitReadEvictionOwnPrefixPays(t *testing.T) {
	e := newLimitEnv(t, relay.Options{MaxInflight: 16 << 20}) // read share 2 MiB
	r := newPeer(t)
	cr := e.authed(r, "10.4.0.1")
	cr.SetReadLimit(2 << 20)
	waitDrained(t, e.s, r.key)
	holdFrame(t, e, newPeer(t), "10.2.0.2", r.key, "b-1") // another prefix, older than all of A
	// Prefix A holds as many frames as its 2 MiB share takes.
	k := int((2 << 20) / bufCap(partFrame))
	var as []*websocket.Conn
	for i := range k {
		c, _, _ := holdFrame(t, e, newPeer(t), fmt.Sprintf("10.1.0.%d", i+1), r.key, fmt.Sprintf("a-%d", i))
		as = append(as, c)
	}
	// A's next frame passes the share: A's oldest holder pays.
	a := newPeer(t)
	ca := e.authed(a, "10.1.0.99")
	frame := []byte(mustJSON(a.env(r.key, "a-next", payload(260_000))))
	w := startFrame(t, ca, frame[:partFrame])
	expectClose(t, as[0], websocket.StatusTryAgainLater)
	waitUntil(t, wait, "A's next frame to be charged", func() bool { return e.s.ReadingFor("10.1.0.0") == int64(k)*bufCap(partFrame) })
	if n := e.s.ReadingFor("10.2.0.0"); n != bufCap(partFrame) {
		t.Fatalf("the other prefix's frame was touched: it holds %d", n)
	}
	if _, err := w.Write(frame[partFrame:]); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if id := readEnvID(t, cr, wait); id != "a-next" {
		t.Fatalf("got %q", id)
	}
	e.logged("evict_read", "prefix=10.1.0.0")

	// Fallback: a requester that is its prefix's only holder is closed.
	e2 := newLimitEnv(t, relay.Options{MaxInflight: 512 << 10}) // the share is the whole 512 KiB
	d := newPeer(t)
	cd := e2.authed(d, "10.5.0.1")
	startFrameAsync(t, cd, payload(700_000))
	expectClose(t, cd, websocket.StatusTryAgainLater)
	e2.logged("max_inflight_read", "relay=all")
}

// fillOutbound sends mail from senders to the non-reading sinks until the
// outbound budget cannot take another frame, even once the sinks' socket
// buffers are full, and returns the frame size.
func fillOutbound(t *testing.T, e *limitEnv, senders []peer, conns []*websocket.Conn, sinks []peer, body []byte, budget int64) int64 {
	t.Helper()
	var size int64
	deadline := time.Now().Add(8 * time.Second)
	for i := 0; ; i++ {
		if size > 0 && e.s.Inflight()+size > budget {
			settle(t, e.s.Inflight)
			if e.s.Inflight()+size > budget {
				return size
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the outbound budget did not fill: %d of %d", e.s.Inflight(), budget)
		}
		s := i % len(senders)
		frame := []byte(mustJSON(senders[s].env(sinks[i%len(sinks)].key, fmt.Sprintf("fill-%d", i), body)))
		size = int64(len(frame))
		writeFrame(t, conns[s], frame)
		time.Sleep(3 * time.Millisecond)
	}
}

// settle waits until f has not changed for 200 ms: the sinks' socket
// buffers are full and nothing more is written.
func settle(t *testing.T, f func() int64) {
	t.Helper()
	last, since := f(), time.Now()
	deadline := time.Now().Add(wait)
	for time.Since(since) < 200*time.Millisecond {
		if time.Now().After(deadline) {
			t.Fatal("the budgets did not settle")
		}
		time.Sleep(5 * time.Millisecond)
		if n := f(); n != last {
			last, since = n, time.Now()
		}
	}
}

// Acceptance test 9: outbound eviction (the mail variant of R55-002).
// Once the fake clock is past evictStale, an honest direct mail evicts the
// stalest sink of the heaviest prefix instead of taking the queue path.
func TestLimitOutboundEvictsStaleSink(t *testing.T) {
	const budget = 1 << 20
	e := newLimitEnv(t, relay.Options{MaxInflight: budget})
	h1, h2 := newPeer(t), newPeer(t)
	ch1 := e.authed(h1, "10.9.0.1")
	ch2 := e.authed(h2, "10.9.0.2")
	waitDrained(t, e.s, h2.key)
	var sinks []peer
	for i := range 8 { // 8 prefixes, never read
		r := newPeer(t)
		e.authed(r, fmt.Sprintf("10.1.%d.1", i+1))
		waitDrained(t, e.s, r.key)
		sinks = append(sinks, r)
	}
	senders := []peer{newPeer(t), newPeer(t)}
	conns := []*websocket.Conn{e.authed(senders[0], "10.8.0.1"), e.authed(senders[1], "10.7.0.1")}
	waitUntil(t, wait, "no drain reservation left", func() bool { return e.s.Inflight() == 0 })
	stop := watchMax(e.s.Inflight)
	fillOutbound(t, e, senders, conns, sinks, payload(48_000), budget)
	e.s.ChargeInflight(budget - e.s.Inflight()) // exactly full; this rest is nobody's

	e.clock.Advance(30 * time.Second) // past 2 s + 1 MiB ÷ 512 KiB/s
	writeFrame(t, ch1, []byte(mustJSON(h1.env(h2.key, "honest-1", payload(200)))))
	if id := readEnvID(t, ch2, wait); id != "honest-1" {
		t.Fatalf("got %q", id)
	}
	// One sink is closed; the others stay.
	gone := func() int {
		n := 0
		for _, s := range sinks {
			if !e.s.Connected(s.key) {
				n++
			}
		}
		return n
	}
	waitUntil(t, 2*time.Second, "one sink to be evicted", func() bool { return gone() >= 1 })
	time.Sleep(100 * time.Millisecond)
	if n := gone(); n != 1 {
		t.Fatalf("%d sinks closed, want exactly 1", n)
	}
	if n := stop(); n > budget {
		t.Fatalf("outbound budget reached %d, max %d", n, budget)
	}
	e.logged("evict_outbound", "prefix=10.1.")
	noFrameWithin(t, ch1, 300*time.Millisecond) // no queued reply
}

// Acceptance test 9, second part: with no stale holder the honest mail
// takes the queue path (fallback); once the clock passes evictStale, the
// drain's next recheck evicts a stale sink and the mail is delivered.
//
// The spec's 1 MiB budget cannot show this part: a drain reserves 2 MiB, and
// a prefix pays only if it holds at least what the requester's prefix would
// after the charge (step 1), which no 1 MiB-budget sink does. At 16 MiB each
// sink holds up to its 4 MiB buffer.
func TestLimitOutboundFallbackThenDrainEvicts(t *testing.T) {
	const budget = 16 << 20
	e := newLimitEnv(t, relay.Options{MaxInflight: budget})
	h1, h2 := newPeer(t), newPeer(t)
	ch1 := e.authed(h1, "10.9.0.1")
	ch2 := e.authed(h2, "10.9.0.2")
	waitDrained(t, e.s, h2.key)
	var sinks []peer
	for i := range 5 {
		r := newPeer(t)
		e.authed(r, fmt.Sprintf("10.1.%d.1", i+1))
		waitDrained(t, e.s, r.key)
		sinks = append(sinks, r)
	}
	senders := []peer{newPeer(t), newPeer(t), newPeer(t)}
	conns := []*websocket.Conn{e.authed(senders[0], "10.8.0.1"), e.authed(senders[1], "10.7.0.1"), e.authed(senders[2], "10.6.0.1")}
	fillOutbound(t, e, senders, conns, sinks, payload(500_000), budget)
	e.s.ChargeInflight(budget - e.s.Inflight()) // exactly full; this rest is nobody's

	expectQueued(t, e.send(ch1, h1, h2.key, "honest-1", payload(200)), "honest-1")
	time.Sleep(300 * time.Millisecond) // the drain rechecks, but nobody is stale yet
	if n, _ := e.s.Queued(h2.key); n != 1 {
		t.Fatalf("queued for h2: %d, want the honest mail still waiting", n)
	}
	e.clock.Advance(30 * time.Second)
	if id := readEnvID(t, ch2, wait); id != "honest-1" {
		t.Fatalf("got %q", id)
	}
	e.logged("evict_outbound", "prefix=10.1.")
}

// Acceptance test 10: non-reading sinks in one prefix never hold more than
// the prefix's share of the outbound budget; past it mail to them is queued.
func TestLimitOutboundPrefixShare(t *testing.T) {
	const budget, share = 16 << 20, 6 << 20
	e := newLimitEnv(t, relay.Options{MaxInflight: budget})
	var sinks []peer
	for i := range 3 {
		r := newPeer(t)
		e.authed(r, fmt.Sprintf("10.1.0.%d", i+2))
		waitDrained(t, e.s, r.key)
		sinks = append(sinks, r)
	}
	senders := []peer{newPeer(t), newPeer(t)}
	conns := []*websocket.Conn{e.authed(senders[0], "10.8.0.1"), e.authed(senders[1], "10.7.0.1")}
	waitUntil(t, wait, "no drain reservation left", func() bool { return e.s.Inflight() == 0 })
	stop := watchMax(func() int64 { return e.s.InflightFor("10.1.0.0/24") })
	queued := func() bool {
		for _, s := range sinks {
			if n, _ := e.s.Queued(s.key); n > 0 {
				return true
			}
		}
		return false
	}
	body := payload(500_000)
	for i := 0; i < 28 && !queued(); i++ {
		s := i % len(senders)
		writeFrame(t, conns[s], []byte(mustJSON(senders[s].env(sinks[i%len(sinks)].key, fmt.Sprintf("m-%d", i), body))))
		time.Sleep(5 * time.Millisecond)
	}
	waitUntil(t, wait, "mail to the prefix to take the queue path", queued)
	if n := stop(); n > share {
		t.Fatalf("one prefix held %d bytes of the outbound budget, share %d", n, share)
	}
	e.logged("max_inflight_prefix", "prefix=10.1.0.0")
}

// smallSocket is the socket buffer size asked for at both ends of every
// connection of newSmallSocketEnv (the kernel may round it up).
const smallSocket = 4 << 10

// shrinkSocket makes c's kernel buffers small, so a peer that never reads
// stalls the relay's write loop after a few KiB instead of after the
// megabytes loopback autotuning allows on Linux and macOS (review 63 S-3).
func shrinkSocket(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetReadBuffer(smallSocket)
		_ = tc.SetWriteBuffer(smallSocket)
	}
}

type shrinkListener struct{ net.Listener }

func (l shrinkListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		shrinkSocket(c)
	}
	return c, err
}

// newSmallSocketEnv is newLimitEnv with small socket buffers on both the
// relay's and the clients' side of every connection.
func newSmallSocketEnv(t *testing.T, opts relay.Options) *limitEnv {
	t.Helper()
	e := newLimitEnvWith(t, opts, func(s *relay.Server) string {
		ts := httptest.NewUnstartedServer(s)
		ts.Listener = shrinkListener{ts.Listener}
		ts.Start()
		t.Cleanup(ts.Close)
		return "ws" + strings.TrimPrefix(ts.URL, "http") + envelope.ConnectPath
	})
	e.client = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err == nil {
				shrinkSocket(c)
			}
			return c, err
		},
	}}
	return e
}

// Acceptance test 11: ephemeral eviction and control frames.
func TestLimitEphemeralEvictionAndControlFrames(t *testing.T) {
	const eph = 128 << 10
	e := newSmallSocketEnv(t, relay.Options{MaxInflightEphemeral: eph, EphemeralPerMinute: 1 << 20})
	h1, h2, h3 := newPeer(t), newPeer(t), newPeer(t)
	ch1 := e.authed(h1, "10.9.0.1")
	ch2 := e.authed(h2, "10.9.0.2")
	ch3 := e.authed(h3, "10.9.0.3")
	atk := newPeer(t)
	ca := e.authed(atk, "10.1.0.1")
	var sinks []peer
	for i := range 4 {
		r := newPeer(t)
		e.authed(r, fmt.Sprintf("10.1.0.%d", i+2))
		sinks = append(sinks, r)
	}
	// Presence to the sinks, which never read, until the ephemeral budget
	// cannot take another frame even once their socket buffers are full.
	body := payload(5500)
	deadline := time.Now().Add(8 * time.Second)
	for i, size := 0, int64(0); ; i++ {
		if size > 0 && e.s.EphemeralInflight()+size > eph {
			settle(t, e.s.EphemeralInflight)
			if e.s.EphemeralInflight()+size > eph {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ephemeral budget did not fill: %d of %d", e.s.EphemeralInflight(), eph)
		}
		frame := presence(atk, sinks[i%len(sinks)].key, fmt.Sprintf("p%d", i), body)
		size = int64(len(frame))
		writeFrame(t, ca, frame)
		time.Sleep(time.Millisecond)
	}
	if n := e.s.EphemeralFor("10.1.0.0"); n < eph/2 {
		t.Fatalf("the sinks hold %d bytes of the %d-byte ephemeral budget", n, eph)
	}
	// Top the budget up, so that it is exactly full.
	e.s.ChargeEphemeral(eph - e.s.EphemeralInflight())

	// No holder is stale: a queued reply that does not fit is dropped and
	// counted in the log (OD-R55F1-4 (a)). The fill may have logged drops
	// too, so the reply's drop is the count going up.
	before := e.count("max_inflight_ephemeral", "relay=all")
	offline := newPeer(t)
	writeFrame(t, ch3, []byte(mustJSON(h3.env(offline.key, "q-1", payload(200)))))
	waitUntil(t, wait, "the mail to be queued", func() bool { n, _ := e.s.Queued(offline.key); return n == 1 })
	waitUntil(t, wait, "the dropped reply to be logged", func() bool { return e.count("max_inflight_ephemeral", "relay=all") > before })
	noFrameWithin(t, ch3, 300*time.Millisecond)
	e.logged("max_inflight_ephemeral", "relay=all")
	if n := e.count("evict_ephemeral", ""); n != 0 {
		t.Fatalf("%d evictions before any sink was stale", n)
	}
	// A new connection still gets ready.
	e.authed(newPeer(t), "10.9.0.9")

	// Once the sinks are stale, presence between two reading peers evicts one.
	e.clock.Advance(5 * time.Second) // past 2 s + 128 KiB ÷ 512 KiB/s
	writeFrame(t, ch1, presence(h1, h2.key, "pres-1", payload(200)))
	if id := readEnvID(t, ch2, wait); id != "pres-1" {
		t.Fatalf("got %q", id)
	}
	e.logged("evict_ephemeral", "prefix=10.1.0.0")
	waitUntil(t, 2*time.Second, "a sink to be evicted", func() bool {
		for _, s := range sinks {
			if !e.s.Connected(s.key) {
				return true
			}
		}
		return false
	})
}

// Acceptance test 13 (R55-035): bytes are charged when a frame is read,
// before it is parsed; past the key's byte bucket frames are dropped
// unparsed with an empty ref, valid mail included.
func TestLimitBytesChargedAtRead(t *testing.T) {
	e := newLimitEnv(t, relay.Options{})
	a, victim := newPeer(t), newPeer(t)
	ca := e.authed(a, "10.1.0.1")
	junk := bytes.Repeat([]byte("x"), envelope.MaxFrameBytes-100) // not an envelope
	for range 40 {
		writeFrame(t, ca, junk)
	}
	bad, limited := 0, 0
	for range 40 {
		got := readControl(t, ca)
		switch {
		case got.Code == envelope.CodeBadEnvelope:
			bad++ // parsed
		case got.Code == envelope.CodeRateLimited && got.Ref == "":
			limited++ // not parsed
		default:
			t.Fatalf("got %+v", got)
		}
	}
	if bad > 32 || limited < 8 {
		t.Fatalf("%d frames parsed and %d refused unparsed; the 32 MiB/min bucket allows 32 parses", bad, limited)
	}
	// The key's valid mail in the same minute is refused too: one bucket.
	expectCode(t, e.send(ca, a, victim.key, "m-1", payload(5000)), envelope.CodeRateLimited, "")
	if n, _ := e.s.Queued(victim.key); n != 0 {
		t.Fatalf("%d envelopes queued past the byte bucket", n)
	}
	e.logged("key_bytes", "peer="+a.key[:8])
}

// Acceptance test 14 (R55-144): a panic while routing still releases every
// budget the connection holds; release runs in a defer.
func TestLimitBudgetsReleasedOnPanic(t *testing.T) {
	e := newLimitEnv(t, relay.Options{})
	p := newPeer(t)
	e.s.SetRouteHookForTest(func(key string) {
		if key == p.key {
			panic("R55-144 test: a panic while routing")
		}
	})
	cp := e.authed(p, "10.1.0.1")
	writeFrame(t, cp, []byte(mustJSON(p.env(newPeer(t).key, "boom", payload(3000)))))
	waitUntil(t, wait, "the connection to end and release its budgets", func() bool {
		return !e.s.Connected(p.key) && e.s.Reading() == 0 && e.s.Inflight() == 0 && e.s.EphemeralInflight() == 0
	})
}

// Acceptance test 17: cross-prefix read eviction ignores age. An honest
// frame that started first is not closed when a strictly heavier prefix
// holds the budget with younger frames (review 56a A2).
func TestLimitCrossPrefixReadEvictionIgnoresAge(t *testing.T) {
	budget := bufCap(200_000) + 2*bufCap(400_000) // ~2.1 MiB
	e := newLimitEnv(t, relay.Options{MaxInflight: budget})
	r := newPeer(t)
	cr := e.authed(r, "10.4.0.1")
	cr.SetReadLimit(2 << 20)
	waitDrained(t, e.s, r.key)
	// R starts first: 200 KB of a ~800 KB frame. Then H, a heavier prefix,
	// starts 2 larger frames; together they fill the budget exactly.
	pr := newPeer(t)
	c := e.authed(pr, "10.2.0.1")
	frameR := []byte(mustJSON(pr.env(r.key, "r-1", payload(600_000))))
	if int64(len(frameR)) > bufCap(400_000) {
		t.Fatalf("R's %d-byte frame would grow past %d", len(frameR), bufCap(400_000))
	}
	wr := startFrame(t, c, frameR[:200_000])
	waitUntil(t, wait, "R's frame to be charged", func() bool { return e.s.ReadingFor("10.2.0.0") == bufCap(200_000) })
	time.Sleep(5 * time.Millisecond)
	var hs []*websocket.Conn
	for i := range 2 {
		ph := newPeer(t)
		h := e.authed(ph, fmt.Sprintf("10.1.0.%d", i+1))
		startFrame(t, h, payload(400_000))
		want := int64(i+1) * bufCap(400_000)
		waitUntil(t, wait, "H's frame to be charged", func() bool { return e.s.ReadingFor("10.1.0.0") == want })
		hs = append(hs, h)
	}
	if n := e.s.Reading(); n != budget {
		t.Fatalf("read budget holds %d, want it full (%d)", n, budget)
	}
	// R's next growth does not fit: H pays, although its frames are younger.
	if _, err := wr.Write(frameR[150_000:]); err != nil {
		t.Fatal(err)
	}
	if err := wr.Close(); err != nil {
		t.Fatal(err)
	}
	if id := readEnvID(t, cr, wait); id != "r-1" {
		t.Fatalf("got %q", id)
	}
	expectClose(t, hs[0], websocket.StatusTryAgainLater)
	e.logged("evict_read", "prefix=10.1.0.0")
}

// Acceptance test 19: an honest recipient draining a backlog is not evicted
// while it keeps up (5 s < 2 s + its holding ÷ 512 KiB/s), only later.
func TestLimitHonestDrainNotEvicted(t *testing.T) {
	const budget = 8 << 20
	e := newLimitEnv(t, relay.Options{MaxInflight: budget})
	r := newPeer(t)
	s1, s2 := newPeer(t), newPeer(t)
	c1, c2 := e.authed(s1, "10.8.0.1"), e.authed(s2, "10.7.0.1")
	for i := range 11 { // ~15 MiB of backlog while r is offline
		for j, s := range []struct {
			p peer
			c *websocket.Conn
		}{{s1, c1}, {s2, c2}} {
			id := fmt.Sprintf("b-%d-%d", j, i)
			expectQueued(t, e.send(s.c, s.p, r.key, id, payload(500_000)), id)
		}
	}
	e.authed(r, "10.1.0.1") // connects, reads nothing: its drain fills its buffer
	var last int64
	waitUntil(t, wait, "r's buffer to fill", func() bool {
		n := e.s.InflightFor("10.1.0.0")
		full := n > 2<<20 && n == last
		last = n
		time.Sleep(50 * time.Millisecond)
		return full
	})
	held := e.s.InflightFor("10.1.0.0")
	e.s.ChargeInflight(budget - e.s.Inflight()) // the rest of the budget: nobody's
	h, h3, h4 := newPeer(t), newPeer(t), newPeer(t)
	ch := e.authed(h, "10.2.0.1")
	ch3 := e.authed(h3, "10.3.0.1") // its drain cannot reserve room: mail to it is queued either way

	e.clock.Advance(5 * time.Second) // 5 s < 2 s + held ÷ 512 KiB/s
	// The requester is h3's queue drain: it finds the budget full and, on
	// its rechecks, looks for a holder to evict. (Mail to h3 is queued
	// either way while it drains.) Several rechecks pass, and r keeps what
	// it holds: an eviction would uncharge it at once, before r's socket
	// is closed.
	expectQueued(t, e.send(ch, h, h3.key, "q-1", payload(200)), "q-1")
	time.Sleep(time.Second)
	if n := e.s.InflightFor("10.1.0.0"); n != held {
		t.Fatalf("the honest drain held %d bytes and now %d: it was evicted", held, n)
	}
	if n := e.count("evict_outbound", ""); n != 0 {
		t.Fatalf("%d evictions while the honest drain kept up", n)
	}
	_ = ch3.CloseNow() // its drain would compete for room below
	waitConnected(t, e.s, h3.key, false)

	e.clock.Advance(10 * time.Second) // 15 s: past 2 s + 6 MiB ÷ 512 KiB/s
	ch4 := e.authed(h4, "10.4.0.1")
	waitDrained(t, e.s, h4.key)
	writeFrame(t, ch, []byte(mustJSON(h.env(h4.key, "d-1", payload(200)))))
	if id := readEnvID(t, ch4, wait); id != "d-1" {
		t.Fatalf("got %q", id)
	}
	waitUntil(t, 2*time.Second, "the stale drain to be evicted", func() bool { return !e.s.Connected(r.key) })
	e.logged("evict_outbound", "prefix=10.1.0.0")
}

// Acceptance test 20: eviction never blocks the requester, even when the
// evicted peer never answers the close; a timed-out frame is uncharged at
// once, not after the close handshake.
func TestLimitEvictionNeverBlocks(t *testing.T) {
	const budget = 1 << 20
	e := newLimitEnv(t, relay.Options{MaxInflight: budget})
	h1, h2, sink, s := newPeer(t), newPeer(t), newPeer(t), newPeer(t)
	ch1 := e.authed(h1, "10.9.0.1")
	ch2 := e.authed(h2, "10.9.0.2")
	waitDrained(t, e.s, h2.key)
	e.authed(sink, "10.1.0.1") // never reads, so never answers a close
	waitDrained(t, e.s, sink.key)
	cs := e.authed(s, "10.8.0.1")
	fillOutbound(t, e, []peer{s}, []*websocket.Conn{cs}, []peer{sink}, payload(48_000), budget)
	e.s.ChargeInflight(budget - e.s.Inflight())
	e.clock.Advance(30 * time.Second)

	start := time.Now()
	writeFrame(t, ch1, []byte(mustJSON(h1.env(h2.key, "fast-1", payload(200)))))
	if id := readEnvID(t, ch2, wait); id != "fast-1" {
		t.Fatalf("got %q", id)
	}
	if el := time.Since(start); el > 100*time.Millisecond {
		t.Fatalf("the send that evicted took %v", el)
	}
	e.logged("evict_outbound", "prefix=10.1.0.0")
	waitUntil(t, 1500*time.Millisecond, "the evicted connection to be gone", func() bool { return !e.s.Connected(sink.key) })

	// The frame deadline: the charge is 0 right after the timer fires.
	e2 := newLimitEnv(t, relay.Options{FrameReadTimeout: 300 * time.Millisecond})
	a := newPeer(t)
	ca := e2.authed(a, "10.1.0.1")
	start = time.Now()
	startFrame(t, ca, payload(5000)) // never finished; ca never reads either
	waitUntil(t, wait, "the frame to be charged", func() bool { return e2.s.Reading() > 0 })
	waitUntil(t, time.Second, "the timed-out frame to be uncharged", func() bool { return e2.s.Reading() == 0 })
	if el := time.Since(start); el > 600*time.Millisecond {
		t.Fatalf("uncharged after %v; the timer fires at 300 ms", el)
	}
}

// Acceptance test 21: a frame of at most 1 KiB (an ack) passes a spent byte
// bucket; a larger one is refused unparsed.
func TestLimitSmallFramesPassSpentByteBucket(t *testing.T) {
	e := newLimitEnv(t, relay.Options{KeyBytesPerMinute: 100 << 10})
	k, s := newPeer(t), newPeer(t)
	cs := e.authed(s, "10.8.0.1")
	expectQueued(t, e.send(cs, s, k.key, "q-1", payload(200)), "q-1")
	ck := e.authed(k, "10.1.0.1")
	if id := readEnvID(t, ck, wait); id != "q-1" {
		t.Fatalf("got %q", id)
	}
	// Spend k's byte bucket: 3 × 33 KiB of its 100 KiB leave 1 KiB.
	junk := bytes.Repeat([]byte("x"), 33<<10)
	for range 3 {
		expectCode(t, readControlAfter(t, ck, junk), envelope.CodeBadEnvelope, "")
	}
	expectCode(t, readControlAfter(t, ck, junk), envelope.CodeRateLimited, "")
	// The ack is processed: the queued envelope is deleted.
	writeFrame(t, ck, []byte(`{"op":"ack","ref":"q-1","from":"`+s.key+`"}`))
	waitUntil(t, wait, "the ack to delete the queued envelope", func() bool { n, _ := e.s.Queued(k.key); return n == 0 })
	// A 2 KiB invalid frame is refused unparsed.
	expectCode(t, readControlAfter(t, ck, bytes.Repeat([]byte("y"), 2<<10)), envelope.CodeRateLimited, "")
}

// readControlAfter writes frame on c and returns the relay's next control frame.
func readControlAfter(t *testing.T, c *websocket.Conn, frame []byte) envelope.Control {
	t.Helper()
	writeFrame(t, c, frame)
	return readControl(t, c)
}
