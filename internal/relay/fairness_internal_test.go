package relay

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// Memory budgets and fairness (R55-F1), internal acceptance tests 12 and 18
// of Docs/review/56-r55-f1-spec.md.

// testServer returns a Server closed when the test ends, after the
// connections testConns registered (they have no socket) are forgotten.
func testServer(t *testing.T, opts Options) *Server {
	t.Helper()
	s := New(opts)
	t.Cleanup(s.Close)
	t.Cleanup(func() {
		s.mu.Lock()
		clear(s.conns)
		s.mu.Unlock()
	})
	return s
}

// testConns returns connections on s's budgets, registered as connected,
// with direct delivery on.
func testConns(t *testing.T, s *Server, prefixes ...string) []*conn {
	t.Helper()
	var cs []*conn
	for _, p := range prefixes {
		c := newConn(nil, testKey(t), 64, p)
		c.led, c.maxBytes, c.draining, c.ctx = s.led, s.lim.connBuffer, false, context.Background()
		s.mu.Lock()
		s.conns[c.key] = c
		s.mu.Unlock()
		cs = append(cs, c)
	}
	return cs
}

// exactFrame returns a valid envelope of exactly n bytes from `from` to `to`.
func exactFrame(t *testing.T, from, to string, n int) []byte {
	t.Helper()
	for size := range n {
		for idLen := 1; idLen <= 8; idLen++ {
			raw, err := envelope.Envelope{From: from, To: to, Type: "ping", ID: strings.Repeat("i", idLen),
				TS: "2026-01-02T03:04:05Z", Payload: make([]byte, size)}.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) == n {
				return raw
			}
		}
	}
	t.Fatalf("no envelope of exactly %d bytes", n)
	return nil
}

// Acceptance test 12 (R55-034): a frame waiting in an outbound buffer is
// charged exactly the memory it pins. readFrame may leave cap up to about
// twice len; the relay copies such a frame to an exact slice.
func TestFrameChargedAsPinned(t *testing.T) {
	s := testServer(t, Options{})
	cs := testConns(t, s, "10.1.0.0", "10.2.0.0")
	sender, rcpt := cs[0], cs[1]
	frame := exactFrame(t, sender.key, rcpt.key, 4097)
	read := make([]byte, len(frame), 8192) // what readFrame leaves: capacity to spare
	copy(read, frame)
	if !s.route(sender, read) {
		t.Fatal("route refused a valid envelope")
	}
	if n := s.led.used(kindOutbound); n != 4097 {
		t.Fatalf("outbound budget %d after a 4097-byte envelope, want exactly 4097", n)
	}
	got := <-rcpt.out
	if len(got) != 4097 || cap(got) != len(got) {
		t.Fatalf("the waiting frame has len %d, cap %d: it pins more than it is charged", len(got), cap(got))
	}
}

// Acceptance test 18 (review 56a A3): an evicted connection is uncharged
// once. Its drain reservation, outbound frames, presence and unfinished read
// frame are all returned at eviction; the late returns of its drain, read
// loop, write loop and release change nothing; no budget goes negative; and
// a frame it finishes after the eviction is not routed.
func TestEvictionDoesNotDoubleUncharge(t *testing.T) {
	s := testServer(t, Options{MaxInflight: 16 << 20})
	cs := testConns(t, s, "10.1.0.0", "10.2.0.0", "10.3.0.0")
	h, other, rcpt := cs[0], cs[1], cs[2]
	used := func() [numKinds]int64 {
		return [numKinds]int64{s.led.used(kindOutbound), s.led.used(kindRead), s.led.used(kindEphemeral)}
	}
	// Another connection's charges stay put throughout.
	if !other.sendCapped(make([]byte, 1000)) || !other.send(make([]byte, 100)) {
		t.Fatal("other's charges refused")
	}
	base := used()

	var negative atomic.Bool
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			for _, n := range used() {
				if n < 0 {
					negative.Store(true)
				}
			}
			select {
			case <-stop:
				return
			default:
				time.Sleep(50 * time.Microsecond)
			}
		}
	}()

	// H holds a drain reservation, mail, presence and a read frame.
	reserved := int64(drainReserve)
	if !h.reserve(context.Background(), reserved, false) {
		t.Fatal("reservation refused")
	}
	mail := make([]byte, 64<<10)
	if !h.sendReserved(context.Background(), mail, &reserved) || !h.sendCapped(make([]byte, 32<<10)) {
		t.Fatal("mail refused")
	}
	if !h.send(make([]byte, 5000)) {
		t.Fatal("presence refused")
	}
	if err := s.growRead(h, 20480); err != nil {
		t.Fatal(err)
	}
	if u := used(); u == base {
		t.Fatal("H holds nothing")
	}

	if !h.evict("relay busy; retry later", nil) {
		t.Fatal("evict refused")
	}
	if u := used(); u != base {
		t.Fatalf("after eviction the budgets are %v, want %v", u, base)
	}
	// The late returns of every path that held H's bytes.
	h.unreserve(reserved)      // the drain's defer
	h.doneRead()               // the read loop's done
	h.written(mail)            // the write loop finishing its frame
	h.written(make([]byte, 1)) // and one more
	if h.sendReserved(context.Background(), mail, &reserved) || h.sendCapped(mail) || h.send(mail) {
		t.Fatal("an evicted connection took a frame")
	}
	if err := s.growRead(h, 4096); err == nil {
		t.Fatal("an evicted connection's read was charged")
	}
	h.release() // ServeHTTP's defer
	if h.evict("again", nil) {
		t.Fatal("evicted twice")
	}
	if u := used(); u != base {
		t.Fatalf("after the late returns the budgets are %v, want %v", u, base)
	}

	// A frame H finishes after its eviction is discarded, not routed.
	frame := testFrame(t, h.key, rcpt.key, "ping", "late-1", []byte("x"))
	if s.handleFrame(h, websocket.MessageText, frame) {
		t.Fatal("the evicted connection's read loop goes on")
	}
	if n := len(rcpt.out); n != 0 {
		t.Fatalf("the frame H finished after its eviction was routed (%d frames)", n)
	}

	other.release()
	close(stop)
	<-sampled
	if negative.Load() {
		t.Fatal("a budget went negative")
	}
	if u := used(); u != [numKinds]int64{} {
		t.Fatalf("budgets %v after every connection ended, want 0", u)
	}
}

// Review 63 S-1: an honest recipient draining a mail backlog is not made
// eligible by the few KiB of presence it also holds. Its oldest frame waits
// behind the whole backlog, so an ephemeral charge 5 s later does not evict
// it (5 s < 2 s + 4.2 MiB ÷ 512 KiB/s); only once past that allowance.
func TestEphemeralEvictionCountsQueuedMail(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := newLedger(64<<20, 64<<20, 1<<20, func() time.Time { return now })
	drainer := newConn(nil, testKey(t), 64, "10.1.0.0")
	requester := newConn(nil, testKey(t), 64, "10.2.0.0")
	for range 8 { // a 4 MiB backlog, then 200 KiB of presence behind it
		if ok, _ := l.charge(drainer, kindOutbound, 512<<10, false, true); !ok {
			t.Fatal("mail refused")
		}
	}
	for range 25 {
		if ok, _ := l.charge(drainer, kindEphemeral, 8<<10, false, true); !ok {
			t.Fatal("presence refused")
		}
	}
	l.addUnowned(kindEphemeral, 1<<20-l.used(kindEphemeral)) // the rest of the budget: nobody's

	now = now.Add(5 * time.Second) // past 2 s + 200 KiB ÷ 512 KiB/s, not past the backlog
	if h, _ := l.pick(requester, kindEphemeral, 200, false); h != nil {
		t.Fatal("an ephemeral charge evicted the honest drainer of a 4 MiB backlog")
	}
	now = now.Add(6 * time.Second) // 11 s: past 2 s + 4.2 MiB ÷ 512 KiB/s
	if h, prefix := l.pick(requester, kindEphemeral, 200, false); h != drainer || prefix != "10.1.0.0" {
		t.Fatalf("pick = %v, %q; want the drainer, which no longer keeps up", h, prefix)
	}
}

// Review 63 S-5: frames charged but never refused do not drive a byte
// bucket into debt that would lock the prefix out after they stop.
func TestByteBucketHasNoDebt(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	b := newBucketSet(1000, 1000) // 1000 a second
	for range 100 {
		b.spend("10.1.0.0", now, 1000) // a flood 100 × the burst
	}
	if !b.has("10.1.0.0", now.Add(time.Second), 1000) {
		t.Fatal("a second after the flood the bucket is still empty: it carried a debt")
	}
}
