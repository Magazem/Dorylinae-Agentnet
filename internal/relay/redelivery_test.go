package relay_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

// Redelivery budget, R55-F2 (Docs/protocol/relay-hosted.md §2 "Offline
// queue delivery and expiry", Docs/review/66-r55-f2-spec.md §4).

const redeliverSize = 700 << 10 // payload bytes; the frame is about 934 KiB

// received is what one connection got: frames, their bytes and ids in order.
type received struct {
	frames, bytes int
	ids           []string
}

// readQuiet reads envelopes on c in the background and returns, closing c,
// once the relay has handed key its whole backlog (not draining, nothing
// buffered) and c has read every frame the relay wrote to it, the ready
// frame that authed read included. No quiet window: it waits for the count,
// with a deadline, however loaded the machine. Control frames are not
// returned.
func readQuiet(t *testing.T, s *relay.Server, c *websocket.Conn, key string) received {
	t.Helper()
	c.SetReadLimit(2 << 20)
	var mu sync.Mutex
	var got received
	var read atomic.Int64
	read.Store(1) // the ready frame
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, raw, err := c.Read(context.Background())
			if err != nil {
				return
			}
			read.Add(1)
			h, err := envelope.ParseHeader(raw)
			if err != nil {
				continue // a control frame
			}
			mu.Lock()
			got.frames++
			got.bytes += len(raw)
			got.ids = append(got.ids, h.ID)
			mu.Unlock()
		}
	}()
	deadline := time.Now().Add(wait)
	for s.Connected(key) && (s.Draining(key) || s.Buffered(key) > 0 || read.Load() < s.Written(key)) {
		if time.Now().After(deadline) {
			t.Fatalf("backlog of %s never settled", key[:8])
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = c.CloseNow()
	<-done
	waitConnected(t, s, key, false)
	mu.Lock()
	defer mu.Unlock()
	return got
}

// queueFor queues n envelopes of redeliverSize from snd (connected as cs) to
// key, ids prefix-0..n-1, and returns the bytes of the frames.
func queueFor(t *testing.T, e *limitEnv, cs *websocket.Conn, snd peer, key, prefix string, n int) int {
	t.Helper()
	total := 0
	for i := range n {
		id := fmt.Sprintf("%s-%d", prefix, i)
		expectQueued(t, e.send(cs, snd, key, id, payload(redeliverSize)), id)
		total += len(mustJSON(snd.env(key, id, payload(redeliverSize))))
	}
	return total
}

// frameLen is the length of one queued frame of redeliverSize.
func frameLen(snd, rcv peer) int {
	return len(mustJSON(snd.env(rcv.key, "q-0", payload(redeliverSize))))
}

// Test 1 (T10-02 converted): a key that never acks gets its queue once as
// first deliveries, then only its redelivery budget, however often it
// reconnects; the budget refills.
func TestQueueRedeliveryBudgetPerKey(t *testing.T) {
	const keyBudget = 2 << 20
	e := newLimitEnv(t, relay.Options{QueueRedeliverPerKey: keyBudget, QueueRedeliverPerPrefix: 1 << 30})
	snd, rcv := newPeer(t), newPeer(t)
	cs := e.authed(snd, "10.1.0.1")
	const n = 8
	uploaded := queueFor(t, e, cs, snd, rcv.key, "q", n)

	downloaded := 0
	for minute := range 2 {
		for i := range 20 { // the per-key reconnect limit
			got := readQuiet(t, e.s, e.authed(rcv, "10.2.0.1"), rcv.key)
			if minute == 0 && i == 0 && got.frames != n {
				t.Fatalf("first connection got %d frames, want all %d (first delivery is not budgeted)", got.frames, n)
			}
			downloaded += got.bytes
		}
		e.clock.Advance(time.Minute)
	}
	bound := uploaded + keyBudget + 2*keyBudget/60 + frameLen(snd, rcv)
	t.Logf("uploaded %d KiB; downloaded %d KiB over 40 reconnects in 2 fake minutes; bound %d KiB", uploaded>>10, downloaded>>10, bound>>10)
	if downloaded > bound {
		t.Fatalf("downloaded %d bytes, want at most %d (upload + key budget + refill + one frame)", downloaded, bound)
	}
	if q, err := e.s.Queued(rcv.key); err != nil || q != n {
		t.Fatalf("Queued = %d, %v; want %d", q, err, n)
	}
	e.logged("queue_redeliver_key", rcv.key[:8])

	// An hour later the budget has refilled: redeliveries resume.
	e.clock.Advance(time.Hour)
	before := e.s.RedeliveredBytes()
	got := readQuiet(t, e.s, e.authed(rcv, "10.2.0.1"), rcv.key)
	if got.frames < 2 || e.s.RedeliveredBytes() <= before {
		t.Fatalf("after the refill: %d frames, redelivered %d -> %d; want redeliveries again", got.frames, before, e.s.RedeliveredBytes())
	}
}

// Test 1, variant (review 66b H1): a connection that closes after reading one
// frame of its batch does not get the rest of the batch as first deliveries
// again on its next connection: the batch was claimed before it was sent.
func TestQueueRedeliveryBudgetCloseMidBatch(t *testing.T) {
	const keyBudget = 2 << 20
	e := newLimitEnv(t, relay.Options{QueueRedeliverPerKey: keyBudget, QueueRedeliverPerPrefix: 1 << 30})
	snd, rcv := newPeer(t), newPeer(t)
	cs := e.authed(snd, "10.1.0.1")
	const n = 8
	uploaded := queueFor(t, e, cs, snd, rcv.key, "q", n)

	downloaded := 0
	for range 2 {
		for range 20 {
			c := e.authed(rcv, "10.2.0.1")
			c.SetReadLimit(2 << 20)
			rctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			if _, frame, err := c.Read(rctx); err == nil {
				downloaded += len(frame)
			}
			cancel()
			_ = c.CloseNow()
			waitConnected(t, e.s, rcv.key, false)
		}
		e.clock.Advance(time.Minute)
	}
	bound := uploaded + keyBudget + 2*keyBudget/60 + frameLen(snd, rcv)
	t.Logf("uploaded %d KiB; read %d KiB, one frame per connection, over 40 reconnects; bound %d KiB", uploaded>>10, downloaded>>10, bound>>10)
	if downloaded > bound {
		t.Fatalf("read %d bytes, want at most %d: closing mid-batch replays first deliveries", downloaded, bound)
	}
	if q, err := e.s.Queued(rcv.key); err != nil || q != n {
		t.Fatalf("Queued = %d, %v; want %d", q, err, n)
	}
}

// Test 3: four recipient keys of one prefix share its redelivery budget; a
// recipient in another prefix, in the same minute, gets all of its own.
func TestQueueRedeliveryBudgetPerPrefix(t *testing.T) {
	const prefixBudget = 4 << 20
	e := newLimitEnv(t, relay.Options{QueueRedeliverPerKey: 1 << 30, QueueRedeliverPerPrefix: prefixBudget})
	snd := newPeer(t)
	cs := e.authed(snd, "10.1.0.1")
	rcvs := []peer{newPeer(t), newPeer(t), newPeer(t), newPeer(t)}
	other := newPeer(t)
	const n = 4
	for i, r := range append(rcvs, other) {
		queueFor(t, e, cs, snd, r.key, fmt.Sprintf("r%d", i), n)
	}
	for i, r := range rcvs { // first deliveries, never acked
		if got := readQuiet(t, e.s, e.authed(r, fmt.Sprintf("10.2.0.%d", i+1)), r.key); got.frames != n {
			t.Fatalf("first connection of recipient %d got %d frames, want %d", i, got.frames, n)
		}
	}
	if got := readQuiet(t, e.s, e.authed(other, "10.3.0.1"), other.key); got.frames != n {
		t.Fatalf("first connection of the other recipient got %d frames", got.frames)
	}

	redelivered := 0
	for i, r := range rcvs {
		redelivered += readQuiet(t, e.s, e.authed(r, fmt.Sprintf("10.2.0.%d", i+1)), r.key).bytes
	}
	if bound := prefixBudget + frameLen(snd, rcvs[0]); redelivered > bound {
		t.Fatalf("the prefix's keys got %d bytes redelivered together, want at most %d", redelivered, bound)
	}
	if got := readQuiet(t, e.s, e.authed(other, "10.3.0.1"), other.key); got.frames != n {
		t.Fatalf("a recipient in another prefix got %d of its %d redeliveries", got.frames, n)
	}
	e.logged("queue_redeliver_prefix", "10.2.0.0")
}

// Test 4: an honest daemon acks as it receives; after a dropped connection it
// gets what was in flight at once, and nothing is skipped.
func TestQueueHonestReconnectNothingSkipped(t *testing.T) {
	e := newLimitEnv(t, relay.Options{})
	snd, rcv := newPeer(t), newPeer(t)
	cs := e.authed(snd, "10.1.0.1")
	const n = 8
	queueFor(t, e, cs, snd, rcv.key, "q", n)

	c := e.authed(rcv, "10.2.0.1")
	c.SetReadLimit(2 << 20)
	for i := range 3 {
		id := readEnvID(t, c, wait)
		if want := fmt.Sprintf("q-%d", i); id != want {
			t.Fatalf("got %s, want %s", id, want)
		}
		ack(t, c, snd.key, id)
	}
	waitQueued(t, e.s, rcv.key, n-3)
	_ = c.CloseNow() // 5 in flight
	waitConnected(t, e.s, rcv.key, false)

	got := readQuiet(t, e.s, e.authed(rcv, "10.2.0.1"), rcv.key)
	if want := "q-3 q-4 q-5 q-6 q-7"; strings.Join(got.ids, " ") != want {
		t.Fatalf("after the reconnect got %v, want %s", got.ids, want)
	}
	if e.s.RedeliverySkips() != 0 || e.count("queue_redeliver_key", "") != 0 || e.count("queue_redeliver_prefix", "") != 0 {
		t.Fatalf("an honest reconnect was skipped:\n%s", e.logs.String())
	}
}

// Test 5: a key whose budget is spent still gets new mail at once; after the
// refill the retry sends the skipped rows, oldest first, and charges them.
func TestQueueSkipNewMailThenRetry(t *testing.T) {
	e := newLimitEnv(t, relay.Options{QueueRedeliverPerKey: 2 << 20, QueueRedeliverPerPrefix: 1 << 30})
	snd, rcv := newPeer(t), newPeer(t)
	cs := e.authed(snd, "10.1.0.1")
	queueFor(t, e, cs, snd, rcv.key, "q", 4)
	if got := readQuiet(t, e.s, e.authed(rcv, "10.2.0.1"), rcv.key); got.frames != 4 {
		t.Fatalf("first connection got %d frames", got.frames)
	}

	c := e.authed(rcv, "10.2.0.1")
	c.SetReadLimit(2 << 20)
	for _, want := range []string{"q-0", "q-1"} { // 2 MiB: two frames, then skipped
		if id := readEnvID(t, c, wait); id != want {
			t.Fatalf("redelivered %s, want %s", id, want)
		}
	}
	waitDrained(t, e.s, rcv.key)
	if e.s.RedeliverySkips() == 0 {
		t.Fatal("no redelivery was skipped")
	}
	// New mail is delivered while the old rows wait.
	frame := mustJSON(snd.env(rcv.key, "new", []byte("fresh")))
	writeFrame(t, cs, []byte(frame))
	if id := readEnvID(t, c, wait); id != "new" {
		t.Fatalf("got %s, want the new envelope", id)
	}

	e.s.RetrySkipped() // not refilled yet: nothing
	e.clock.Advance(time.Hour)
	before := e.s.RedeliveredBytes()
	e.s.RetrySkipped()
	var retried int64
	for _, want := range []string{"q-2", "q-3"} {
		rctx, cancel := context.WithTimeout(context.Background(), wait)
		_, raw, err := c.Read(rctx)
		cancel()
		if err != nil {
			t.Fatalf("no retried frame: %v", err)
		}
		if h, err := envelope.ParseHeader(raw); err != nil || h.ID != want {
			t.Fatalf("retried %.80s, want %s", raw, want)
		}
		retried += int64(len(raw))
	}
	if got := e.s.RedeliveredBytes() - before; got != retried {
		t.Fatalf("the retry charged %d bytes, want the %d it sent", got, retried)
	}
}

// Test 18 (review 66b H2): in a shared prefix, a waiting honest key is served
// before attacker keys that reconnect all the time, and a closed connection
// leaves the wait list.
func TestQueueRedeliveryFairOrderInPrefix(t *testing.T) {
	const prefixBudget = 4 << 20
	e := newLimitEnv(t, relay.Options{QueueRedeliverPerKey: 1 << 30, QueueRedeliverPerPrefix: prefixBudget})
	snd := newPeer(t)
	cs := e.authed(snd, "10.1.0.1")
	attackers := []peer{newPeer(t), newPeer(t), newPeer(t), newPeer(t)}
	v := newPeer(t)
	const n = 4 // about 3.6 MiB each
	for i, p := range append(attackers, v) {
		queueFor(t, e, cs, snd, p.key, fmt.Sprintf("k%d", i), n)
	}
	ip := func(i int) string { return fmt.Sprintf("10.2.0.%d", i+1) }
	for i, p := range append(attackers, v) { // first deliveries, never acked
		if got := readQuiet(t, e.s, e.authed(p, ip(i)), p.key); got.frames != n {
			t.Fatalf("first connection %d got %d frames", i, got.frames)
		}
	}
	// A1 spends the prefix's burst; A2 is refused and waits, then closes:
	// a closed connection leaves the list.
	if got := readQuiet(t, e.s, e.authed(attackers[0], ip(0)), attackers[0].key); got.frames != n {
		t.Fatalf("A1 got %d redeliveries, want its %d", got.frames, n)
	}
	if got := readQuiet(t, e.s, e.authed(attackers[1], ip(1)), attackers[1].key); got.frames != 0 {
		t.Fatalf("A2 got %d redeliveries past the prefix budget", got.frames)
	}

	// V's connection drops once: it reconnects, is skipped, and waits.
	vc := e.authed(v, ip(4))
	vc.SetReadLimit(2 << 20)
	var vmu sync.Mutex
	var vIDs []string
	var vRead atomic.Int64 // frames V has read, control frames included
	vRead.Store(1)         // the ready frame
	vDone := make(chan struct{})
	go func() {
		defer close(vDone)
		for {
			_, raw, err := vc.Read(context.Background())
			if err != nil {
				return
			}
			if h, err := envelope.ParseHeader(raw); err == nil {
				vmu.Lock()
				vIDs = append(vIDs, h.ID)
				vmu.Unlock()
			}
			vRead.Add(1) // after its id is recorded
		}
	}()
	vGot := func() int {
		vmu.Lock()
		defer vmu.Unlock()
		return len(vIDs)
	}
	waitDrained(t, e.s, v.key)
	skipAt := time.Duration(0)
	if vGot() != 0 {
		t.Fatalf("V got %d redeliveries while the prefix budget was spent", vGot())
	}

	// Three fake hours: the attackers reconnect every step, the retry runs.
	const step = 5 * time.Minute
	attackerBytes, doneAt := 0, time.Duration(-1)
	for elapsed := step; elapsed <= 3*time.Hour; elapsed += step {
		e.clock.Advance(step)
		for i, a := range attackers {
			got := readQuiet(t, e.s, e.authed(a, ip(i)), a.key)
			if doneAt < 0 {
				attackerBytes += got.bytes
			}
		}
		e.s.RetrySkipped()
		// Settled, and V has read every frame the relay wrote to it: once
		// the relay is done with V, the attackers may be served, so a frame
		// V has not read yet must not count as one V still waits for.
		waitUntil(t, wait, "V's retry to settle", func() bool {
			return !e.s.Draining(v.key) && e.s.Buffered(v.key) == 0 && vRead.Load() >= e.s.Written(v.key)
		})
		if doneAt < 0 && vGot() == n {
			doneAt = elapsed
		}
	}
	_ = vc.CloseNow()
	<-vDone
	if doneAt < 0 {
		t.Fatalf("V got %d of its %d redeliveries in 3 fake hours", vGot(), n)
	}
	if limit := time.Duration(float64(n*prefixBudget+prefixBudget) / float64(prefixBudget) * float64(time.Hour)); doneAt-skipAt > limit {
		t.Fatalf("V waited %v, want at most %v", doneAt-skipAt, limit)
	}
	if attackerBytes != 0 {
		t.Fatalf("attacker keys got %d bytes redelivered while V waited ahead of them", attackerBytes)
	}
	t.Logf("V redelivered in full after %v", doneAt)
}

// Test 10 (R55-010): envelopes whose payload the recipient cannot parse get
// bad_envelope with their id, and are neither queued nor forwarded; valid
// payloads are forwarded byte for byte.
func TestRelayRefusesBadPayload(t *testing.T) {
	e := newLimitEnv(t, relay.Options{})
	a, b, off := newPeer(t), newPeer(t), newPeer(t)
	ca := e.authed(a, "10.1.0.1")
	cb := e.authed(b, "10.2.0.1")
	cb.SetReadLimit(2 << 20)
	waitDrained(t, e.s, b.key)

	head := func(to, typ, id string) string {
		return `"from":"` + a.key + `","to":"` + to + `","team":"t","type":"` + typ + `","id":"` + id + `","ts":"2026-01-02T03:04:05Z"`
	}
	escape := string(rune(92)) + "u0051UFB" // a JSON escape inside the string
	bad := []struct{ name, members string }{
		{"number", `,"payload":1`},
		{"object", `,"payload":{}`},
		{"array", `,"payload":[]`},
		{"null", `,"payload":null`},
		{"missing", ``},
		{"not base64", `,"payload":"not base64!"`},
		{"unpadded", `,"payload":"QQ"`},
		{"padding inside", `,"payload":"QQ==QUFB"`},
		{"escape", `,"payload":"` + escape + `"`},
		{"good then number", `,"payload":"QQ==","payload":1`},
		{"good then case variant", `,"payload":"QQ==","PAYLOAD":1`},
		// Spec delta (review 66 test 10 listed it as accepted, "last wins"):
		// encoding/json keeps the first type error, so the recipient's Parse
		// refuses this frame; the relay refuses it too.
		{"number then good", `,"payload":1,"payload":"QQ=="`},
	}
	for i, tc := range bad {
		for _, to := range []string{b.key, off.key} {
			id := fmt.Sprintf("bad-%d", i)
			got := readControlAfter(t, ca, []byte("{"+head(to, "ping", id)+tc.members+"}"))
			if got.Op != envelope.OpError || got.Code != envelope.CodeBadEnvelope || got.Ref != id {
				t.Errorf("%s to %s: got %+v, want bad_envelope ref %s", tc.name, to[:8], got, id)
			}
		}
	}
	if q, err := e.s.Queued(off.key); err != nil || q != 0 {
		t.Fatalf("refused envelopes were queued: %d, %v", q, err)
	}
	// Presence gets the same check.
	if got := readControlAfter(t, ca, []byte("{"+head(b.key, "presence.heartbeat", "p-1")+`,"payload":1}`)); got.Code != envelope.CodeBadEnvelope || got.Ref != "p-1" {
		t.Fatalf("presence with a bad payload: %+v, want bad_envelope", got)
	}

	big := `"` + strings.Repeat("QUFB", (redeliverSize)/4) + `"`
	for i, members := range []string{`,"payload":""`, `,"payload":"QQ=="`, `,"payload":"QUFB"`, `,"payload":` + big} {
		frame := "{" + head(b.key, "ping", fmt.Sprintf("good-%d", i)) + members + "}"
		writeFrame(t, ca, []byte(frame))
		rctx, cancel := context.WithTimeout(context.Background(), wait)
		_, raw, err := cb.Read(rctx)
		cancel()
		if err != nil || string(raw) != frame {
			t.Fatalf("good payload %d: got %.80s, %v; want it byte for byte", i, raw, err)
		}
	}
	// Nothing refused above reached b: its next frame is this one.
	stillAlive(t, cb, b)
}
