package session

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/noise"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// lockedBuf is a log sink the timer goroutine and the test share.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

// lines returns the log lines with event=ev.
func (l *lockedBuf) lines(ev string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, ln := range strings.Split(l.b.String(), "\n") {
		if strings.Contains(ln, "event="+ev+" ") {
			out = append(out, ln)
		}
	}
	return out
}

// queueNode is a Manager whose IsPaired records the keys it is asked about
// and waits for gate, so the worker can be held.
type queueNode struct {
	m    *Manager
	log  *audit.Log
	out  *lockedBuf
	mu   sync.Mutex
	seen []string
}

func (q *queueNode) asked() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.seen...)
}

func newQueueNode(t *testing.T, gate <-chan struct{}) *queueNode {
	t.Helper()
	old := dropWindow
	dropWindow = 100 * time.Millisecond
	t.Cleanup(func() { dropWindow = old })
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	st, err := noise.NewStatic(pub, func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil })
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), filepath.Join(testutil.TempDir(t), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	q := &queueNode{log: audit.New(db.DB()), out: &lockedBuf{}}
	q.m = NewManager(Config{
		Static: st, Audit: q.log, Logger: slog.New(slog.NewTextHandler(q.out, nil)),
		IsPaired: func(_ context.Context, k string) (bool, error) {
			q.mu.Lock()
			q.seen = append(q.seen, k)
			q.mu.Unlock()
			<-gate
			return false, nil
		},
	})
	t.Cleanup(q.m.Close)
	return q
}

func dataEnv(from string, n int) envelope.Envelope {
	return envelope.Envelope{From: from, To: "self", Type: TypeData, ID: randHex(8), TS: "2026-09-30T00:00:00Z", Payload: make([]byte, n)}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// R55-F13 (review 55 R55-052): a session.* payload over 65559 bytes is dropped
// before the queue and not audited; 65559 bytes is queued.
func TestSessionOversizePayloadDroppedBeforeQueue(t *testing.T) {
	gate := make(chan struct{})
	close(gate)
	q := newQueueNode(t, gate)
	q.m.HandleEnvelope(dataEnv("oversize-peer", MaxPayload+1))
	q.m.HandleEnvelope(dataEnv("fits-peer", MaxPayload))
	waitUntil(t, "the 65559-byte envelope to reach the worker", func() bool { return len(q.asked()) > 0 })
	waitUntil(t, "the session_drop line", func() bool { return len(q.out.lines("session_drop")) == 1 })
	if got := q.asked(); len(got) != 1 || got[0] != "fits-peer" {
		t.Fatalf("worker saw %v, want only fits-peer", got)
	}
	ln := q.out.lines("session_drop")[0]
	if !strings.Contains(ln, " count=1 ") || !strings.Contains(ln, " oversize=1 ") || !strings.Contains(ln, " bytes=65560") {
		t.Fatalf("drop line %q", ln)
	}
	// The queued one is rejected (unpaired) and audited; the dropped one is not.
	waitUntil(t, "the reject audit row", func() bool {
		evs, err := q.log.List(context.Background())
		return err == nil && len(evs) > 0
	})
	evs, err := q.log.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if strings.Contains(string(e.Detail), "oversize-peer") {
			t.Fatalf("the oversize envelope was audited: %s %s", e.Action, e.Detail)
		}
	}
}

// With the worker held, the inbox stays within 256 envelopes and 16 MiB, and
// the drops of a window make one log line.
func TestSessionQueueByteBound(t *testing.T) {
	gate := make(chan struct{})
	q := newQueueNode(t, gate)
	t.Cleanup(func() { close(gate) }) // runs before Close
	q.m.HandleEnvelope(dataEnv("p", 1))
	waitUntil(t, "the worker to hold", func() bool { return len(q.asked()) == 1 })

	check := func(size, n int) int {
		t.Helper()
		dropped := 0
		for i := 0; i < n; i++ {
			before := q.m.queued.Load()
			q.m.HandleEnvelope(dataEnv("p", size))
			after := q.m.queued.Load()
			if after == before {
				dropped++
			}
			if after > inboxBytes || len(q.m.inbox) > inboxSize {
				t.Fatalf("inbox holds %d bytes in %d envelopes", after, len(q.m.inbox))
			}
		}
		return dropped
	}
	// 300 frames of 64 KiB: 256 fit exactly (16 MiB), the rest is dropped.
	if d := check(64<<10, 300); d != 300-inboxSize {
		t.Fatalf("64 KiB frames: %d dropped, want %d", d, 300-inboxSize)
	}
	waitUntil(t, "the first session_drop line", func() bool { return len(q.out.lines("session_drop")) == 1 })
	if ln := q.out.lines("session_drop")[0]; !strings.Contains(ln, " count=44 ") || !strings.Contains(ln, " queue_full=44 ") {
		t.Fatalf("drop line %q, want the 44 drops in one line", ln)
	}
	if n := q.m.queued.Load(); n != inboxBytes {
		t.Fatalf("inbox holds %d bytes, want %d", n, inboxBytes)
	}
}

// Maximal honest payloads hit the byte bound before the count bound.
func TestSessionQueueByteBoundBeforeCount(t *testing.T) {
	gate := make(chan struct{})
	q := newQueueNode(t, gate)
	t.Cleanup(func() { close(gate) })
	q.m.HandleEnvelope(dataEnv("p", 1))
	waitUntil(t, "the worker to hold", func() bool { return len(q.asked()) == 1 })
	for i := 0; i < 300; i++ {
		q.m.HandleEnvelope(dataEnv("p", MaxPayload))
	}
	if n, c := q.m.queued.Load(), len(q.m.inbox); n > inboxBytes || c != inboxBytes/MaxPayload {
		t.Fatalf("inbox holds %d bytes in %d envelopes, want %d envelopes", n, c, inboxBytes/MaxPayload)
	}
}
