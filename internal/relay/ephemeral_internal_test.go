package relay

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// A presence flood may use at most half of a recipient's send buffer, so mail
// and control frames still get through when the buffer is otherwise busy.
func TestDirectEphemeralLeavesHalfBufferForMail(t *testing.T) {
	// cap=8, half=4: directEphemeral checks occupancy before enqueueing, so it
	// admits while occupancy is at most half (5 sends: 0,1,2,3,4 all <=4), and
	// refuses once occupancy exceeds half (the 6th call, at occupancy 5).
	c := newConn(nil, "k", 8)
	for i := range 5 {
		if !c.directEphemeral([]byte("p")) {
			t.Fatalf("ephemeral frame %d refused with the buffer at most half full", i)
		}
	}
	if c.directEphemeral([]byte("p")) {
		t.Fatal("ephemeral frame accepted with the buffer over half full")
	}
	c.mu.Lock()
	c.draining = false
	c.mu.Unlock()
	if res := c.direct([]byte("mail")); res != directSent {
		t.Fatalf("mail direct result = %v, want directSent while presence is being dropped", res)
	}
}

// Senders racing on the half-full check must not push the buffer past half+1.
func TestDirectEphemeralConcurrentStaysAtHalf(t *testing.T) {
	for range 200 {
		c := newConn(nil, "k", 64)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 64 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				c.directEphemeral([]byte("p"))
			}()
		}
		close(start)
		wg.Wait()
		if n := len(c.out); n > 33 {
			t.Fatalf("occupancy %d after a concurrent flood, want at most 33", n)
		}
	}
}

func testKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return envelope.KeyString(pub)
}

func testFrame(t *testing.T, from, to, typ, id string, payload []byte) []byte {
	t.Helper()
	raw, err := envelope.Envelope{From: from, To: to, Type: typ, ID: id, TS: time.Now().UTC().Format(time.RFC3339Nano), Payload: payload}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Many Sybil keys flood a recipient with presence through the real routing
// path while a paired peer sends it mail. The recipient's writer is stalled,
// so the buffer only fills. Every mail frame must still be forwarded directly:
// none queued, no queued or error frame to anyone.
func TestPresenceFloodDoesNotPushMailToQueue(t *testing.T) {
	s := New(Options{SendQueue: 64, EphemeralPerMinute: 1 << 20})
	t.Cleanup(s.Close)
	rcpt := newConn(nil, testKey(t), 64)
	rcpt.draining = false
	rcpt.ctx = t.Context()
	s.register(rcpt)

	const senders, perSender, mails = 32, 200, 30 // 30 mail <= 64 - 33
	floods := make([]*conn, senders)
	for i := range floods {
		floods[i] = newConn(nil, testKey(t), 16)
		s.register(floods[i])
	}
	mailer := newConn(nil, testKey(t), mails+1)
	s.register(mailer)
	// The fake conns have no WebSocket, so Close must not see them.
	t.Cleanup(func() {
		for _, c := range append(floods, rcpt, mailer) {
			s.unregister(c)
		}
	})

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, f := range floods {
		frames := make([][]byte, perSender)
		for j := range frames {
			frames[j] = testFrame(t, f.key, rcpt.key, envelope.TypePresence, fmt.Sprintf("p-%d-%d", i, j), []byte("hb"))
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for _, fr := range frames {
				s.route(f, fr)
			}
		}()
	}
	mailFrames := make([][]byte, mails)
	for j := range mailFrames {
		mailFrames[j] = testFrame(t, mailer.key, rcpt.key, "mail", fmt.Sprintf("m-%d", j), []byte("sealed"))
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for _, fr := range mailFrames {
			s.route(mailer, fr)
		}
	}()
	close(start)
	wg.Wait()

	if n := len(mailer.out); n != 0 {
		t.Fatalf("mail sender got %d frames (queued or error), want none", n)
	}
	for _, f := range floods {
		if n := len(f.out); n != 0 {
			t.Fatalf("presence sender got %d frames, want none", n)
		}
	}
	if n, err := s.Queued(rcpt.key); err != nil || n != 0 {
		t.Fatalf("Queued = %d, %v; want 0", n, err)
	}
	var gotMail, gotPresence int
	for len(rcpt.out) > 0 {
		h, err := envelope.ParseHeader(<-rcpt.out)
		if err != nil {
			t.Fatal(err)
		}
		if h.Type == "mail" {
			gotMail++
		} else {
			gotPresence++
		}
	}
	if gotMail != mails || gotPresence > 33 {
		t.Fatalf("buffer held %d mail and %d presence, want %d mail and at most 33 presence", gotMail, gotPresence, mails)
	}
}

func TestEphemeralLimiterWindow(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	l := newEphemeralLimiter(3, func() time.Time { return now })
	for i := range 3 {
		if ok, _ := l.allow("a"); !ok {
			t.Fatalf("envelope %d refused", i)
		}
	}
	if ok, _ := l.allow("a"); ok {
		t.Fatal("4th envelope allowed")
	}
	if ok, _ := l.allow("b"); !ok {
		t.Fatal("limit is per key, but b was refused")
	}
	now = now.Add(time.Minute)
	ok, reports := l.allow("a")
	if !ok || len(reports) != 1 || reports[0] != (limitReport{"a", 1}) {
		t.Fatalf("new window: ok=%v reports=%v, want true and a dropped 1", ok, reports)
	}
}

// Limiter state is bounded by the keys active in about the last two minutes,
// and a key that is limited and then goes away is still reported.
func TestEphemeralLimiterEvictsIdleKeys(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	l := newEphemeralLimiter(1, func() time.Time { return now })
	for i := range 5000 {
		l.allow(fmt.Sprintf("sybil-%d", i))
	}
	l.allow("gone")
	if ok, _ := l.allow("gone"); ok {
		t.Fatal("2nd envelope allowed with limit 1")
	}
	now = now.Add(time.Minute)
	_, reports := l.allow("fresh")
	if len(reports) != 1 || reports[0] != (limitReport{"gone", 1}) {
		t.Fatalf("reports = %v, want gone dropped 1", reports)
	}
	if n := len(l.windows); n != 1 {
		t.Fatalf("limiter holds %d keys, want 1", n)
	}
}
