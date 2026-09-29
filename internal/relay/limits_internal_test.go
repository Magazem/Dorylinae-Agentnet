package relay

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// The zero Options give exactly the defaults of Docs/protocol/relay-hosted.md §2.
func TestLimitDefaults(t *testing.T) {
	s := New(Options{})
	defer s.Close()
	l := s.lim
	perMin := func(n float64) float64 { return n / 60 }
	for _, c := range []struct {
		name      string
		got, want float64
	}{
		{"upgrades rate", l.upgrades.rate, perMin(30)}, {"upgrades burst", l.upgrades.burst, 60},
		{"conns per prefix", float64(l.maxConnsPerPrefix), 64},
		{"auth failures", float64(l.authFails.limit), 10}, {"auth failure window (min)", l.authFails.window.Minutes(), 10},
		{"unauthenticated conns", float64(l.maxUnauth), 256},
		{"max conns", float64(l.maxConns), 5000},
		{"keys per prefix", float64(l.maxKeysPerPrefix), 64},
		{"prefix envelopes rate", l.prefixEnvs.rate, perMin(600)}, {"prefix envelopes burst", l.prefixEnvs.burst, 600},
		{"prefix bytes rate", l.prefixBytes.rate, perMin(64 << 20)}, {"prefix bytes burst", l.prefixBytes.burst, 64 << 20},
		{"key envelopes rate", l.keyEnvs.rate, perMin(120)}, {"key envelopes burst", l.keyEnvs.burst, 240},
		{"key bytes rate", l.keyBytes.rate, perMin(32 << 20)}, {"key bytes burst", l.keyBytes.burst, 32 << 20},
		{"control per minute", float64(l.controlPerMin), 60},
		{"reconnects rate", l.reconnects.rate, perMin(20)}, {"reconnects burst", l.reconnects.burst, 20},
		{"conn buffer", float64(l.connBuffer), 4 << 20},
		{"max inflight", float64(s.led.pools[kindOutbound].max), 256 << 20},
		{"read budget", float64(s.led.pools[kindRead].max), 256 << 20},
		{"ephemeral budget", float64(s.led.pools[kindEphemeral].max), 32 << 20},
		{"outbound share", float64(s.led.pools[kindOutbound].share), 32 << 20},
		{"read share", float64(s.led.pools[kindRead].share), 32 << 20},
		{"ephemeral share", float64(s.led.pools[kindEphemeral].share), 4 << 20},
		{"frame read timeout (s)", s.frameTimeout.Seconds(), 30},
		{"queue pair envelopes", float64(s.q.lim.pairCount), 300}, {"queue pair bytes", float64(s.q.lim.pairBytes), 8 << 20},
		{"queue sender envelopes", float64(s.q.lim.senderCount), 2000}, {"queue sender bytes", float64(s.q.lim.senderBytes), 64 << 20},
		{"queue total", float64(s.q.lim.totalBytes), 4 << 30},
		{"queue min free disk", float64(s.q.lim.minFree), 1 << 30},
		{"queue per recipient envelopes", float64(s.q.maxCount), 1000}, {"queue per recipient bytes", float64(s.q.maxBytes), 32 << 20},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if s.q.lim.freeDisk == nil {
		t.Error("no free disk check")
	}

	off := New(Options{MaxConns: -1, KeyEnvelopesPerMinute: -1, QueueMaxTotal: -1})
	defer off.Close()
	if off.lim.maxConns != 0 || off.lim.keyEnvs.rate != 0 || off.q.lim.totalBytes != 0 {
		t.Error("a negative option must turn its limit off")
	}
}

// No cap check scans the queue table (review 50 M3): the query plan of
// every cap-check query searches an index. The per-sender and relay-wide
// totals need no query at all (TestQueueTotalsFollowAddAckSweep).
func TestQueueCapChecksUseIndexes(t *testing.T) {
	q, err := openQueue(filepath.Join(testutil.TempDir(t), "q.db"), time.Hour, 1000, 32<<20, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.close() }()
	// Some rows, and fresh statistics, so the planner has a real choice.
	for i := range 200 {
		h := envelope.Header{To: fmt.Sprintf("to-%d", i%7), From: fmt.Sprintf("from-%d", i%5), ID: fmt.Sprintf("m-%d", i)}
		if err := q.add(h, []byte("frame")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.db.Exec(`ANALYZE`); err != nil {
		t.Fatal(err)
	}
	for _, query := range append(queueCapQueries, `DELETE FROM queue WHERE to_key = ? AND from_key = ? AND id = ?`) {
		args := make([]any, strings.Count(query, "?"))
		for i := range args {
			args[i] = "x"
		}
		rows, err := q.db.Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		_ = rows.Close()
		searched := false
		for _, d := range plan {
			if strings.HasPrefix(d, "SCAN") {
				t.Errorf("%s\nscans: %q", query, d)
			}
			if strings.HasPrefix(d, "SEARCH queue USING") && strings.Contains(d, "INDEX") {
				searched = true
			}
		}
		if !searched {
			t.Errorf("%s\nplan %q does not search an index", query, plan)
		}
	}
}

// The in-memory per-sender and relay-wide totals follow add, ack and sweep,
// and a reopen rebuilds the same totals with one scan.
func TestQueueTotalsFollowAddAckSweep(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clock := func() time.Time { return now }
	path := filepath.Join(testutil.TempDir(t), "q.db")
	q, err := openQueue(path, time.Hour, 1000, 32<<20, clock)
	if err != nil {
		t.Fatal(err)
	}
	add := func(to, from, id string, n int) {
		t.Helper()
		if err := q.add(envelope.Header{To: to, From: from, ID: id}, make([]byte, n)); err != nil {
			t.Fatal(err)
		}
	}
	add("r1", "a", "1", 100)
	add("r2", "a", "2", 50)
	add("r1", "b", "3", 10)
	add("r1", "b", "3", 10) // duplicate: not counted twice
	check := func(q *queue, total int64, a, b usage) {
		t.Helper()
		q.mu.Lock()
		defer q.mu.Unlock()
		if q.total.bytes != total {
			t.Errorf("total %d, want %d", q.total.bytes, total)
		}
		for key, want := range map[string]usage{"a": a, "b": b} {
			got := usage{}
			if u := q.senders[key]; u != nil {
				got = *u
			}
			if got != want {
				t.Errorf("sender %s: %+v, want %+v", key, got, want)
			}
		}
	}
	check(q, 160, usage{2, 150}, usage{1, 10})
	if err := q.ack("r1", "a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := q.ack("r1", "a", "unknown"); err != nil {
		t.Fatal(err)
	}
	check(q, 60, usage{1, 50}, usage{1, 10})
	now = now.Add(2 * time.Hour)
	add("r3", "b", "4", 7)
	if n, err := q.sweep(); err != nil || n != 2 {
		t.Fatalf("sweep %d, %v", n, err)
	}
	check(q, 7, usage{}, usage{1, 7})
	_ = q.close()
	q2, err := openQueue(path, time.Hour, 1000, 32<<20, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q2.close() }()
	check(q2, 7, usage{}, usage{1, 7})
}

// A connection's outbound buffer holds at most ConnBufferBytes of
// envelopes; control frames still pass; release returns every byte to the
// relay-wide budget.
func TestConnBufferByteCap(t *testing.T) {
	b := newLedger(1<<30, 1<<30, 1<<30, time.Now)
	used := func() int64 { return b.used(kindOutbound) + b.used(kindEphemeral) }
	c := newConn(nil, "k", 64, "")
	c.maxBytes, c.led, c.draining = 4<<20, b, false
	frame := make([]byte, 1<<20)
	for i := range 4 {
		if r := c.direct(frame); r != directSent {
			t.Fatalf("frame %d: %v", i, r)
		}
	}
	if r := c.direct(make([]byte, 10)); r != directBusy {
		t.Fatalf("past 4 MiB: %v, want busy", r)
	}
	if !c.send([]byte(`{"op":"queued"}`)) {
		t.Fatal("a control frame was refused")
	}
	if c.buffered() != 4<<20+15 || used() != 4<<20+15 || b.used(kindEphemeral) != 15 {
		t.Fatalf("buffered %d, budgets %d", c.buffered(), used())
	}
	c.written(<-c.out) // the write loop finishes one frame
	if c.buffered() != 3<<20+15 {
		t.Fatalf("after a write: %d", c.buffered())
	}
	if r := c.direct(make([]byte, 100)); r != directSent {
		t.Fatalf("room again: %v", r)
	}
	c.release()
	if used() != 0 || c.send([]byte("x")) || c.direct(frame) != directBusy {
		t.Fatalf("after release: budgets %d", used())
	}
	c.written(frame) // a write that finishes after release is not uncounted twice
	if used() != 0 {
		t.Fatalf("budgets %d after a late write", used())
	}

	// The relay-wide budget: past it, direct sends are refused on every
	// connection (none is stale, so nobody is evicted).
	shared := newLedger(2<<20, 2<<20, 1<<20, time.Now)
	c1, c2 := newConn(nil, "a", 64, "10.0.1.0"), newConn(nil, "b", 64, "10.0.2.0")
	for _, x := range []*conn{c1, c2} {
		x.maxBytes, x.led, x.draining = 4<<20, shared, false
	}
	if c1.direct(frame) != directSent || c2.direct(frame) != directSent {
		t.Fatal("within budget refused")
	}
	if c1.direct(frame) != directBusy || c2.direct(frame) != directBusy {
		t.Fatal("past --max-inflight accepted")
	}
}

// reserve and sendReserved (the queue drain) wait for room instead of
// overfilling, and the reservation is charged to the relay-wide budget.
func TestReserveHonoursByteCap(t *testing.T) {
	c := newConn(nil, "k", 64, "")
	c.maxBytes = 2 << 20
	frame := make([]byte, 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reserved := int64(2 << 20)
	if !c.reserve(ctx, reserved, false) {
		t.Fatal("refused within the cap")
	}
	if c.led.used(kindOutbound) != 2<<20 {
		t.Fatalf("budget %d after reserving 2 MiB", c.led.used(kindOutbound))
	}
	for range 2 {
		if !c.sendReserved(ctx, frame, &reserved) {
			t.Fatal("refused a reserved frame")
		}
	}
	if reserved != 0 || c.buffered() != 2<<20 || c.led.used(kindOutbound) != 2<<20 {
		t.Fatalf("reserved %d, buffered %d, budget %d", reserved, c.buffered(), c.led.used(kindOutbound))
	}
	if c.reserve(ctx, 1<<20, false) {
		t.Fatal("reserve past the byte cap without waiting")
	}
	done := make(chan bool, 1)
	go func() { done <- c.reserve(ctx, 1<<20, true) }()
	select {
	case <-done:
		t.Fatal("reserve overfilled the buffer")
	case <-time.After(100 * time.Millisecond):
	}
	c.written(<-c.out)
	select {
	case ok := <-done:
		if !ok || c.buffered() != 1<<20 || c.led.used(kindOutbound) != 2<<20 {
			t.Fatalf("ok %v, buffered %d, budget %d", ok, c.buffered(), c.led.used(kindOutbound))
		}
	case <-time.After(time.Second):
		t.Fatal("reserve did not resume after a write")
	}
}

// clientAddr trusts the header only from a trusted proxy on a public relay,
// and then only its last entry.
func TestClientAddr(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::/8")}
	for _, c := range []struct {
		public bool
		peer   string
		header []string
		want   string
	}{
		{true, "10.1.2.3:5000", []string{"203.0.113.9"}, "203.0.113.9"},
		{true, "10.1.2.3:5000", []string{"198.51.100.1, 203.0.113.9"}, "203.0.113.9"},
		{true, "10.1.2.3:5000", []string{"198.51.100.1", "203.0.113.7, 203.0.113.9"}, "203.0.113.9"},
		{true, "[fd00::1]:5000", []string{"2001:db8::5"}, "2001:db8::5"},
		{true, "[::ffff:10.1.2.3]:5000", []string{"203.0.113.9"}, "203.0.113.9"},
		{true, "192.0.2.1:5000", []string{"203.0.113.9"}, "192.0.2.1:5000"}, // untrusted peer
		{true, "10.1.2.3:5000", nil, "10.1.2.3:5000"},                       // no header
		{true, "10.1.2.3:5000", []string{"not-an-ip"}, "10.1.2.3:5000"},     // malformed
		{false, "10.1.2.3:5000", []string{"203.0.113.9"}, "10.1.2.3:5000"},  // not public
		{true, "10.1.2.3:5000", []string{"fe80::1%a1"}, "fe80::1"},          // zone dropped (R-4.0 L)
	} {
		s := &Server{public: c.public, ipHeader: "X-Client-IP", trusted: trusted}
		r := &http.Request{RemoteAddr: c.peer, Header: http.Header{}}
		for _, v := range c.header {
			r.Header.Add("X-Client-IP", v)
		}
		if got := s.clientAddr(r); got != c.want {
			t.Errorf("%+v: %q, want %q", c, got, c.want)
		}
	}
	if clientPrefix("203.0.113.9") != "203.0.113.0" || clientPrefix("2001:db8:1:2::5") != "2001:db8:1::" {
		t.Error("clientPrefix of a bare IP")
	}
}

func TestOpenRejectsHeaderWithoutTrustedProxy(t *testing.T) {
	if _, err := Open(Options{ClientIPHeader: "Fly-Client-IP"}); err == nil {
		t.Fatal("a client IP header without a trusted proxy was accepted")
	}
}
