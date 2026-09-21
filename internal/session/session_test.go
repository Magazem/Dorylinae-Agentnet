package session

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/noise"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// fakeRelay routes envelopes between managers in memory. tamper may change or
// drop (return false) an envelope; every envelope is recorded.
type fakeRelay struct {
	mu     sync.Mutex
	nodes  map[string]*Manager
	tamper func(*envelope.Envelope) bool
	seen   []envelope.Envelope
}

type link struct {
	r *fakeRelay
}

func (l link) Connected() bool { return true }

func (l link) Send(_ context.Context, e envelope.Envelope) error {
	l.r.mu.Lock()
	e.Payload = bytes.Clone(e.Payload)
	l.r.seen = append(l.r.seen, e)
	fn, to := l.r.tamper, l.r.nodes[e.To]
	l.r.mu.Unlock()
	if fn != nil && !fn(&e) {
		return nil
	}
	if to != nil {
		to.HandleEnvelope(e)
	}
	return nil
}

type node struct {
	m   *Manager
	key string
	log *audit.Log
}

func newNode(t *testing.T, r *fakeRelay, paired map[string]bool) *node {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	st, err := noise.NewStatic(pub, func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil })
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "sess")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) }) // best effort: Windows may hold the file briefly
	db, err := store.Open(context.Background(), filepath.Join(dir, "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	log := audit.New(db.DB())
	var mu sync.Mutex
	m := NewManager(Config{
		Static: st, Audit: log, Sender: link{r}, PingTimeout: 2 * time.Second,
		IsPaired: func(_ context.Context, k string) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return paired[k], nil
		},
	})
	t.Cleanup(m.Close)
	n := &node{m: m, key: st.Identity(), log: log}
	r.mu.Lock()
	r.nodes[n.key] = m
	r.mu.Unlock()
	return n
}

func (n *node) events(t *testing.T, action string) []map[string]string {
	t.Helper()
	evs, err := n.log.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]string
	for _, e := range evs {
		if e.Action == action {
			var d map[string]string
			_ = json.Unmarshal(e.Detail, &d)
			out = append(out, d)
		}
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func pair(t *testing.T) (*fakeRelay, *node, *node) {
	r := &fakeRelay{nodes: map[string]*Manager{}}
	pa, pb := map[string]bool{}, map[string]bool{}
	a, b := newNode(t, r, pa), newNode(t, r, pb)
	pa[b.key], pb[a.key] = true, true
	return r, a, b
}

func mustPing(t *testing.T, a, b *node) PingStatus {
	t.Helper()
	st, err := a.m.Ping(context.Background(), PeerRef{PublicKey: b.key, Name: "b"})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestPingOverSession(t *testing.T) {
	r, a, b := pair(t)
	st := mustPing(t, a, b)
	if st.State != StateComplete || st.RTTMillis == nil || !st.Handshake {
		t.Fatalf("first ping = %+v", st)
	}
	st = mustPing(t, a, b)
	if st.State != StateComplete || st.Handshake {
		t.Fatalf("second ping = %+v (should reuse the session)", st)
	}
	if got, ok := a.m.Get(st.ID); !ok || got.State != StateComplete {
		t.Fatalf("Get = %+v %v", got, ok)
	}
	// B can ping back over the same session.
	if st := mustPing(t, b, a); st.State != StateComplete || st.Handshake {
		t.Fatalf("reverse ping = %+v", st)
	}
	if len(a.events(t, ActionOpen)) != 1 || len(b.events(t, ActionOpen)) != 1 {
		t.Fatal("expected exactly one session.open per side")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.seen {
		if bytes.Contains(e.Payload, []byte("ping")) || bytes.Contains(e.Payload, []byte("pong")) {
			t.Fatalf("plaintext in %s payload", e.Type)
		}
	}
}

func TestTamperedAndReplayedDataRejected(t *testing.T) {
	r, a, b := pair(t)
	mustPing(t, a, b) // open the session

	var captured envelope.Envelope
	r.mu.Lock()
	r.tamper = func(e *envelope.Envelope) bool {
		if e.Type == TypeData && e.To == b.key {
			captured = *e
			captured.Payload = bytes.Clone(e.Payload)
			e.Payload[len(e.Payload)-1] ^= 0x01
		}
		return true
	}
	r.mu.Unlock()
	if st := mustPing(t, a, b); st.State != StatePending {
		t.Fatalf("tampered ping = %+v", st)
	}
	waitFor(t, "decrypt reject", func() bool { return len(b.events(t, ActionReject)) == 1 })
	if d := b.events(t, ActionReject)[0]; d["reason"] != ReasonDecrypt || d["peer"] != a.key {
		t.Fatalf("reject detail = %v", d)
	}

	r.mu.Lock()
	r.tamper = nil
	r.mu.Unlock()
	if st := mustPing(t, a, b); st.State != StateComplete || st.Handshake {
		t.Fatalf("ping after tamper = %+v (session should survive)", st)
	}
	// Replay an envelope B already accepted (the untampered copy is older than
	// the last accepted counter, so it counts as a replay too).
	r.mu.Lock()
	var last envelope.Envelope
	for _, e := range r.seen {
		if e.Type == TypeData && e.To == b.key {
			last = e
		}
	}
	r.mu.Unlock()
	b.m.HandleEnvelope(last)
	b.m.HandleEnvelope(captured)
	waitFor(t, "replay rejects", func() bool { return len(b.events(t, ActionReject)) == 3 })
	for _, d := range b.events(t, ActionReject)[1:] {
		if d["reason"] != ReasonReplay {
			t.Fatalf("reject detail = %v", d)
		}
	}
	if st := mustPing(t, a, b); st.State != StateComplete {
		t.Fatalf("ping after replay = %+v", st)
	}
}

func TestUnpairedHandshakeRefused(t *testing.T) {
	r := &fakeRelay{nodes: map[string]*Manager{}}
	pb := map[string]bool{}
	b := newNode(t, r, pb)
	mallory := newNode(t, r, map[string]bool{b.key: true}) // mallory thinks she is paired
	st := mustPing(t, mallory, b)
	if st.State == StateComplete {
		t.Fatal("unpaired ping completed")
	}
	waitFor(t, "unpaired reject", func() bool { return len(b.events(t, ActionReject)) == 1 })
	if d := b.events(t, ActionReject)[0]; d["reason"] != ReasonUnpaired || d["type"] != TypeInit {
		t.Fatalf("reject = %v", d)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.seen {
		if e.From == b.key {
			t.Fatalf("B answered an unpaired key with %s", e.Type)
		}
	}
	if len(b.events(t, ActionOpen)) != 0 {
		t.Fatal("session opened with an unpaired key")
	}
}

func TestPingTimeoutDropsSession(t *testing.T) {
	r, a, b := pair(t)
	mustPing(t, a, b)
	r.mu.Lock()
	r.tamper = func(*envelope.Envelope) bool { return false } // black hole
	r.mu.Unlock()
	st := mustPing(t, a, b)
	waitFor(t, "timeout", func() bool { g, _ := a.m.Get(st.ID); return g.State == StateFailed })
	if g, _ := a.m.Get(st.ID); g.Error == nil || g.Error.Code != FailTimeout {
		t.Fatalf("status = %+v", g)
	}
	r.mu.Lock()
	r.tamper = nil
	r.mu.Unlock()
	if st := mustPing(t, a, b); st.State != StateComplete || !st.Handshake {
		t.Fatalf("after timeout = %+v (want a fresh handshake)", st)
	}
}

func TestRelayErrorFailsPing(t *testing.T) {
	r, a, b := pair(t)
	r.mu.Lock()
	r.tamper = func(e *envelope.Envelope) bool {
		go a.m.HandleError(envelope.ErrorFrame{Code: envelope.CodePeerOffline, Message: "offline", Ref: e.ID})
		return false
	}
	r.mu.Unlock()
	st := mustPing(t, a, b)
	if st.State != StateFailed || st.Error.Code != envelope.CodePeerOffline {
		t.Fatalf("status = %+v", st)
	}
}
