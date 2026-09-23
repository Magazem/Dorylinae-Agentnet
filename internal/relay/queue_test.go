package relay_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// startStoppable is start with an explicit stop, for restarting a relay on the same database.
func startStoppable(t *testing.T, opts relay.Options) (s *relay.Server, url string, stop func()) {
	t.Helper()
	s, err := relay.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	var once sync.Once
	stop = func() {
		once.Do(func() {
			ts.Close()
			s.Close()
		})
	}
	t.Cleanup(stop)
	return s, "ws" + strings.TrimPrefix(ts.URL, "http") + envelope.ConnectPath, stop
}

// sendQueued sends envelope id from p to `to` over c and waits for the relay's "queued" answer.
func sendQueued(t *testing.T, c *websocket.Conn, p peer, to, id string, payload []byte) {
	t.Helper()
	frame, err := p.env(to, id, payload).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx(t), websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	if ctl := readControl(t, c); ctl.Op != envelope.OpQueued || ctl.Ref != id {
		t.Fatalf("got %+v, want queued ref %s", ctl, id)
	}
}

// readID reads one envelope frame from c and returns its id.
func readID(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	_, frame, err := c.Read(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	h, err := envelope.ParseHeader(frame)
	if err != nil {
		t.Fatalf("got %s, want an envelope: %v", frame, err)
	}
	return h.ID
}

func ack(t *testing.T, c *websocket.Conn, from, id string) {
	t.Helper()
	frame := control(envelope.Control{Op: envelope.OpAck, From: from, Ref: id})
	if err := c.Write(ctx(t), websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
}

func ids(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s-%03d", prefix, i)
	}
	return out
}

func waitQueued(t *testing.T, s *relay.Server, key string, want int) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		n, err := s.Queued(key)
		if err != nil {
			t.Fatal(err)
		}
		if n == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue for %s holds %d envelopes, want %d", key[:8], n, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestOfflineEnvelopesArriveOnceAndInOrder is the ticket's integration test,
// with real relayclient daemons on both ends.
func TestOfflineEnvelopesArriveOnceAndInOrder(t *testing.T) {
	s, url := start(t, relay.Options{})
	a, b := newPeer(t), newPeer(t)

	queuedAcks := make(chan string, 16)
	ca, err := relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(a.priv), OnQueued: func(ref string) { queuedAcks <- ref }})
	if err != nil {
		t.Fatal(err)
	}
	actx, stopA := context.WithCancel(context.Background())
	defer stopA()
	go func() { _ = ca.Run(actx) }()

	want := ids("off", 5)
	for _, id := range want {
		deadline := time.Now().Add(wait)
		for ca.Send(ctx(t), a.env(b.key, id, []byte("hello "+id))) != nil { // wait for A to connect
			if time.Now().After(deadline) {
				t.Fatal("A never connected")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	for _, id := range want {
		select {
		case ref := <-queuedAcks:
			if ref != id {
				t.Fatalf("queued ack for %q, want %q", ref, id)
			}
		case <-time.After(wait):
			t.Fatalf("no queued ack for %s", id)
		}
	}
	waitQueued(t, s, b.key, len(want))

	var mu sync.Mutex
	var got []string
	cb, err := relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(b.priv), OnEnvelope: func(e envelope.Envelope) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, e.ID)
	}})
	if err != nil {
		t.Fatal(err)
	}
	bctx, stopB := context.WithCancel(context.Background())
	defer stopB()
	go func() { _ = cb.Run(bctx) }()

	// One more sent after B is back: must follow the backlog.
	waitConnected(t, s, b.key, true)
	if err := ca.Send(ctx(t), a.env(b.key, "live-1", nil)); err != nil {
		t.Fatal(err)
	}
	want = append(want, "live-1")

	deadline := time.Now().Add(wait)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= len(want) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // room for stray duplicates
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("B received %v, want %v", got, want)
	}
	waitQueued(t, s, b.key, 0) // every delivery was acked and deleted
}

func TestQueueSurvivesRelayRestart(t *testing.T) {
	db := filepath.Join(testutil.TempDir(t), "queue.db")
	a, b := newPeer(t), newPeer(t)

	_, url1, stop1 := startStoppable(t, relay.Options{QueuePath: db})
	ca := rawAuthed(t, url1, a)
	want := ids("keep", 4)
	for _, id := range want {
		sendQueued(t, ca, a, b.key, id, []byte("secret "+id))
	}
	stop1()

	s2, url2, _ := startStoppable(t, relay.Options{QueuePath: db})
	waitQueued(t, s2, b.key, len(want))
	cb := rawAuthed(t, url2, b)
	for _, id := range want {
		if got := readID(t, cb); got != id {
			t.Fatalf("got %s, want %s", got, id)
		}
		ack(t, cb, a.key, id)
	}
	waitQueued(t, s2, b.key, 0)
}

func TestExpiredEnvelopesAreNotDelivered(t *testing.T) {
	clock := newClock()
	a, b := newPeer(t), newPeer(t)

	t.Run("unswept", func(t *testing.T) {
		s, url := start(t, relay.Options{Now: clock.Now, QueueTTL: 7 * 24 * time.Hour, SweepInterval: time.Hour})
		ca := rawAuthed(t, url, a)
		sendQueued(t, ca, a, b.key, "old-1", nil)
		clock.Advance(6 * 24 * time.Hour)
		sendQueued(t, ca, a, b.key, "fresh-1", nil)
		clock.Advance(2 * 24 * time.Hour) // old-1 is 8 days old, fresh-1 is 2

		// Expired even though nothing has purged it yet.
		cb := rawAuthed(t, url, b)
		if got := readID(t, cb); got != "fresh-1" {
			t.Fatalf("got %s first, want fresh-1", got)
		}
		waitQueued(t, s, b.key, 1)
	})

	t.Run("sweep", func(t *testing.T) {
		s, url := start(t, relay.Options{Now: clock.Now, SweepInterval: time.Hour})
		ca := rawAuthed(t, url, a)
		sendQueued(t, ca, a, b.key, "sweep-1", nil)
		sendQueued(t, ca, a, b.key, "sweep-2", nil)
		waitQueued(t, s, b.key, 2)
		if n, err := s.Sweep(); err != nil || n != 0 {
			t.Fatalf("Sweep() = %d, %v; nothing has expired yet", n, err)
		}
		clock.Advance(7*24*time.Hour + time.Second)
		if n, err := s.Sweep(); err != nil || n != 2 {
			t.Fatalf("Sweep() = %d, %v, want 2", n, err)
		}
		cb := rawAuthed(t, url, b)
		stillAlive(t, cb, b) // the first frame B sees is its own probe, not a queued envelope
	})
}

func TestReconnectMidFlushRedeliversOnlyUnacked(t *testing.T) {
	s, url := start(t, relay.Options{})
	a, b := newPeer(t), newPeer(t)
	ca := rawAuthed(t, url, a)
	want := ids("mid", 4)
	for _, id := range want {
		sendQueued(t, ca, a, b.key, id, nil)
	}

	cb := rawAuthed(t, url, b)
	for _, id := range want {
		if got := readID(t, cb); got != id {
			t.Fatalf("got %s, want %s", got, id)
		}
	}
	ack(t, cb, a.key, want[0])
	ack(t, cb, a.key, want[1])
	waitQueued(t, s, b.key, 2)
	_ = cb.CloseNow()
	waitConnected(t, s, b.key, false)

	cb = rawAuthed(t, url, b)
	for _, id := range want[2:] {
		if got := readID(t, cb); got != id {
			t.Fatalf("after reconnect got %s, want %s", got, id)
		}
	}
	waitDrained(t, s, b.key)
	stillAlive(t, cb, b) // and nothing else is pending
}

func TestBacklogStaysAheadOfLiveTraffic(t *testing.T) {
	s, url := start(t, relay.Options{SendQueue: 8})
	a, b := newPeer(t), newPeer(t)
	ca := rawAuthed(t, url, a)
	const backlog, live = 150, 150
	want := ids("seq", backlog+live)
	for _, id := range want[:backlog] {
		sendQueued(t, ca, a, b.key, id, nil)
	}

	cb := rawAuthed(t, url, b)
	// A keeps sending while B's backlog is flushing.
	sendErr := make(chan error, 1)
	go func() {
		for _, id := range want[backlog:] {
			frame, err := a.env(b.key, id, nil).Marshal()
			if err == nil {
				err = ca.Write(context.Background(), websocket.MessageText, frame)
			}
			if err != nil {
				sendErr <- err
				return
			}
		}
		sendErr <- nil
	}()
	for i, id := range want {
		if got := readID(t, cb); got != id {
			t.Fatalf("envelope %d: got %s, want %s", i, got, id)
		}
		ack(t, cb, a.key, id)
	}
	if err := <-sendErr; err != nil {
		t.Fatal(err)
	}
	waitQueued(t, s, b.key, 0)
}

func TestQueueLimitsAndDuplicates(t *testing.T) {
	s, url := start(t, relay.Options{QueueMaxEnvelopes: 2})
	a, b := newPeer(t), newPeer(t)
	ca := rawAuthed(t, url, a)
	sendQueued(t, ca, a, b.key, "q-1", nil)
	sendQueued(t, ca, a, b.key, "q-1", nil) // same id again: stored once
	waitQueued(t, s, b.key, 1)
	sendQueued(t, ca, a, b.key, "q-2", nil)

	frame, _ := a.env(b.key, "q-3", nil).Marshal()
	if err := ca.Write(ctx(t), websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	if ctl := readControl(t, ca); ctl.Op != envelope.OpError || ctl.Code != envelope.CodeQueueFull || ctl.Ref != "q-3" {
		t.Fatalf("got %+v, want queue_full ref q-3", ctl)
	}
	waitQueued(t, s, b.key, 2)
	stillAlive(t, ca, a)
}

func TestOnlyTheRecipientCanAckAnEnvelope(t *testing.T) {
	s, url := start(t, relay.Options{})
	a, b, mallory := newPeer(t), newPeer(t), newPeer(t)
	ca := rawAuthed(t, url, a)
	sendQueued(t, ca, a, b.key, "mine-1", nil)

	cm := rawAuthed(t, url, mallory)
	ack(t, cm, a.key, "mine-1")
	stillAlive(t, cm, mallory) // the ack was processed (frames are handled in order) and ignored
	waitQueued(t, s, b.key, 1)
}

func TestQueuedPayloadIsOpaqueAndByteForByte(t *testing.T) {
	_, url := start(t, relay.Options{})
	a, b := newPeer(t), newPeer(t)
	ca := rawAuthed(t, url, a)
	frame := []byte(mustJSON(a.env(b.key, "raw-1", []byte{0, 1, 2, 0xff})))
	frame = append(frame[:len(frame)-1], []byte(`,"unknown":{"x":1}}`)...) // unknown extra field must survive
	if err := ca.Write(ctx(t), websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	readControl(t, ca)

	cb := rawAuthed(t, url, b)
	_, got, err := cb.Read(ctx(t))
	if err != nil || string(got) != string(frame) {
		t.Fatalf("queued frame changed:\n got %s\nwant %s (err %v)", got, frame, err)
	}
}

func control(c envelope.Control) []byte {
	raw, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	return raw
}
