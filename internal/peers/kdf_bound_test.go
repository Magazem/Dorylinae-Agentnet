package peers

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// Review 55, R55-031 and review 77, M3: every v2 pairing used to start an
// uncancellable 64 MiB Argon2id derivation before its first frame was even
// sent, and a pairing that failed at once freed its pending slot while the
// derivation ran on. A derivation now starts only when a pair_peer arrives,
// and at most maxDerivations run at once.

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
	p.done = func() { once.Do(func() { close(p.release) }) }
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
		p.done()
		deriveKFunc = prev
	})
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

// unlimitedStarts lifts the pairing-start bucket for tests that start many.
func unlimitedStarts(m *Manager) {
	m.mu.Lock()
	m.startTokens = 1e9
	m.mu.Unlock()
}

// A pairing whose code the relay does not know (it answers an error, never
// pair_peer), or whose first frame never reached the relay, derives nothing.
func TestKDFNotStartedWithoutPeer(t *testing.T) {
	p := newKDFProbe(t)
	m, _, _, _ := newRevManager(t, nil)
	unlimitedStarts(m)
	for i := 0; i < 3*maxPending; i++ {
		s, err := m.beginRedeemer(context.Background(), newRedeemCode(t), nil)
		if err != nil {
			t.Fatal(err)
		}
		m.HandleError(envelope.ErrorFrame{Code: "pair_unknown", Ref: s.st.ID})
	}
	s, _ := startRevIssuer(t, m)
	m.HandleError(envelope.ErrorFrame{Code: "pair_unknown", Ref: s.st.ID})

	failing, _, _, _ := newRevManager(t, func(c *Config) { c.Sender = &failSender{err: relayclient.ErrNotConnected} })
	for i := 0; i < startBurst; i++ {
		if _, err := failing.beginRedeemer(context.Background(), newRedeemCode(t), nil); err == nil {
			t.Fatal("redeem succeeded without a relay")
		}
	}
	time.Sleep(50 * time.Millisecond) // a wrongly started goroutine would have run by now
	if n := p.started.Load(); n != 0 {
		t.Fatalf("%d derivations started for pairings that never got a pair_peer", n)
	}
}

// peerFor delivers a valid pair_peer for the pairing id.
func peerFor(t *testing.T, m *Manager, id string) {
	t.Helper()
	p := newRevIdent(t, "peer")
	m.HandleControl(envelope.Control{Op: envelope.OpPairPeer, PublicKey: p.key, Card: p.card, Mbox: p.mbox, Ref: id})
}

// Derivations run at most maxDerivations at once, and a derivation still
// queued when its pairing ends is dropped, so failing pairings cannot pile
// up 64 MiB derivations behind the 16-pending bound.
func TestKDFConcurrencyBounded(t *testing.T) {
	p := newKDFProbe(t)
	m, _, _, _ := newRevManager(t, nil)
	unlimitedStarts(m)
	var ids []string
	for i := 0; i < maxPending; i++ {
		s, err := m.beginRedeemer(context.Background(), newRedeemCode(t), nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.st.ID)
		peerFor(t, m, s.st.ID)
	}
	revWait(t, "the first derivations", func() bool { return p.active.Load() == maxDerivations })
	time.Sleep(50 * time.Millisecond) // give any unbounded derivation time to start
	if n := p.max.Load(); n != maxDerivations {
		t.Fatalf("%d derivations ran at once, want at most %d", n, maxDerivations)
	}
	// Every pairing fails: the queued derivations are dropped and the
	// pending slots are free again.
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
	peerFor(t, m, s.st.ID)
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

// Local pairing starts are limited to startBurst at once and startBurst per
// startRefill after that (review 77, M3).
func TestPairingStartsRateLimited(t *testing.T) {
	var mu sync.Mutex
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	m, _, _, _ := newRevManager(t, func(c *Config) { c.Now = clock })
	start := func() error {
		s, err := m.beginRedeemer(context.Background(), newRedeemCode(t), nil)
		if err == nil {
			m.HandleError(envelope.ErrorFrame{Code: "pair_unknown", Ref: s.st.ID}) // keep pending low
		}
		return err
	}
	for i := 0; i < startBurst; i++ {
		if err := start(); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	err := start()
	if !errors.Is(err, ErrTooManyStarts) || !errors.Is(err, ErrTooMany) {
		t.Fatalf("start past the burst: %v, want ErrTooManyStarts", err)
	}
	advance(startRefill / startBurst)
	if err := start(); err != nil {
		t.Fatalf("start after one refill interval: %v", err)
	}
	if err := start(); !errors.Is(err, ErrTooManyStarts) {
		t.Fatalf("second start after one interval: %v, want ErrTooManyStarts", err)
	}
	advance(time.Hour)
	for i := 0; i < startBurst; i++ {
		if err := start(); err != nil {
			t.Fatalf("start %d after a full refill: %v", i, err)
		}
	}
}

// failSender fails every send. Unless down is set it reports itself
// connected, so a start passes precheck and fails at the send, as when the
// relay connection drops between the two.
type failSender struct {
	err  error
	down bool
}

func (f *failSender) SendControl(context.Context, envelope.Control) error { return f.err }
func (f *failSender) Send(context.Context, envelope.Envelope) error       { return f.err }
func (f *failSender) Connected() bool                                     { return !f.down }
