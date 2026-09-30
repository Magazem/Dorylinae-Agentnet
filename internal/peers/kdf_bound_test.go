package peers

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// Review 55, R55-031: every v2 pairing used to start an uncancellable 64 MiB
// Argon2id derivation before its first frame was even sent, and a pairing
// that failed at once freed its pending slot while the derivation ran on.

// kdfProbe replaces deriveKFunc with a derivation that blocks until released
// and records how many run at once.
type kdfProbe struct {
	release chan struct{}
	active  atomic.Int32
	max     atomic.Int32
	started atomic.Int32
	done    func() // releases every blocked derivation
}

func newKDFProbe(t *testing.T) *kdfProbe {
	t.Helper()
	p := &kdfProbe{release: make(chan struct{})}
	var once sync.Once
	prev := deriveKFunc
	deriveKFunc = func(string, []byte) []byte {
		p.started.Add(1)
		n := p.active.Add(1)
		for {
			m := p.max.Load()
			if n <= m || p.max.CompareAndSwap(m, n) {
				break
			}
		}
		<-p.release
		p.active.Add(-1)
		return make([]byte, 32)
	}
	t.Cleanup(func() {
		once.Do(func() { close(p.release) })
		deriveKFunc = prev
	})
	p.done = func() { once.Do(func() { close(p.release) }) }
	return p
}

func newRedeemCode(t *testing.T) string {
	t.Helper()
	raw, err := NewCode()
	if err != nil {
		t.Fatal(err)
	}
	code, v2, ok := NormalizeCode(raw)
	if !ok || !v2 {
		t.Fatalf("NewCode gave %q", raw)
	}
	return code
}

// A pairing whose first frame never reached the relay derives nothing.
func TestKDFNotStartedWhenSendFails(t *testing.T) {
	p := newKDFProbe(t)
	failing := &failSender{err: relayclient.ErrNotConnected}
	m, _, _, _ := newRevManager(t, func(c *Config) { c.Sender = failing })
	for i := 0; i < 3*maxPending; i++ {
		if _, err := m.beginRedeemer(context.Background(), newRedeemCode(t), nil); err == nil {
			t.Fatal("redeem succeeded without a relay")
		}
	}
	if _, err := m.beginIssuer(context.Background(), nil); err == nil {
		t.Fatal("issue succeeded without a relay")
	}
	time.Sleep(50 * time.Millisecond) // a wrongly started goroutine would have run by now
	if n := p.started.Load(); n != 0 {
		t.Fatalf("%d derivations started for pairings that never reached the relay", n)
	}
}

// Derivations run at most maxDerivations at once, and a derivation still
// queued when its pairing ends is dropped, so failing pairings cannot pile
// up 64 MiB derivations behind the 16-pending bound.
func TestKDFConcurrencyBounded(t *testing.T) {
	p := newKDFProbe(t)
	m, _, _, _ := newRevManager(t, nil)
	var ids []string
	for i := 0; i < maxPending; i++ {
		s, err := m.beginRedeemer(context.Background(), newRedeemCode(t), nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.st.ID)
	}
	revWait(t, "the first derivations", func() bool { return p.active.Load() == maxDerivations })
	time.Sleep(50 * time.Millisecond) // give any unbounded derivation time to start
	if n := p.max.Load(); n != maxDerivations {
		t.Fatalf("%d derivations ran at once, want at most %d", n, maxDerivations)
	}
	// The relay refuses every redemption: all pairings end, the queued
	// derivations are dropped, and the pending slots are free again.
	for _, id := range ids {
		m.HandleError(envelope.ErrorFrame{Code: "pair_unknown", Ref: id})
	}
	p.done()
	revWait(t, "the running derivations to end", func() bool { return p.active.Load() == 0 })
	for _, id := range ids {
		m.mu.Lock()
		kd := m.sessions[id].kd
		m.mu.Unlock()
		select {
		case <-kd.ready:
		case <-time.After(10 * time.Second):
			t.Fatalf("derivation of %s neither ran nor was dropped", id)
		}
	}
	if n := p.started.Load(); n != maxDerivations {
		t.Fatalf("%d derivations started, want %d (the queued ones belong to ended pairings)", n, maxDerivations)
	}
	if n := p.max.Load(); n > maxDerivations {
		t.Fatalf("%d derivations ran at once, want at most %d", n, maxDerivations)
	}
	// Every slot was released: a new pairing derives again.
	s, err := m.beginRedeemer(context.Background(), newRedeemCode(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	kd := s.kd
	m.mu.Unlock()
	select {
	case <-kd.ready:
	case <-time.After(10 * time.Second):
		t.Fatal("a new pairing's derivation did not run")
	}
	m.mu.Lock()
	gotK := kd.k != nil
	m.mu.Unlock()
	if !gotK {
		t.Fatal("a new pairing got no K")
	}
}

type failSender struct{ err error }

func (f *failSender) SendControl(context.Context, envelope.Control) error { return f.err }
func (f *failSender) Send(context.Context, envelope.Envelope) error       { return f.err }
