package peers

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// Regression tests from Docs/review/09-pairing-daemon-review.md.

type revSender struct {
	mu   sync.Mutex
	ctl  []envelope.Control
	envs []envelope.Envelope
}

func (r *revSender) SendControl(_ context.Context, c envelope.Control) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ctl = append(r.ctl, c)
	return nil
}

func (r *revSender) Send(_ context.Context, e envelope.Envelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.envs = append(r.envs, e)
	return nil
}

type revIdent struct {
	key  string
	card []byte
	mbox []byte
}

func newRevIdent(t *testing.T, name string) revIdent {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c, err := agentcard.New(pub, name, "h", nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sc, err := agentcard.Sign(priv, c)
	if err != nil {
		t.Fatal(err)
	}
	card, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	x, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mbox, err := mail.SignAnnouncement(pub, func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil },
		x.PublicKey().Bytes(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return revIdent{key: sc.Card.PublicKey, card: card, mbox: mbox}
}

func newRevManager(t *testing.T, mut func(*Config)) (*Manager, *revSender, *audit.Log, *Store) {
	t.Helper()
	dir, err := os.MkdirTemp("", "dn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	st, err := store.Open(context.Background(), filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	me := newRevIdent(t, "me")
	snd := &revSender{}
	cfg := Config{
		Store: NewStore(st.DB()), Audit: audit.New(st.DB()), Card: me.card, Self: me.key,
		Mailbox: func() ([]byte, error) { return me.mbox, nil }, Sender: snd, Wait: 2 * time.Second,
	}
	if mut != nil {
		mut(&cfg)
	}
	m := NewManager(cfg)
	t.Cleanup(m.Close)
	return m, snd, cfg.Audit, cfg.Store
}

func revWait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// startRevIssuer issues a code and acknowledges it as the relay would.
func startRevIssuer(t *testing.T, m *Manager) (*session, string) {
	t.Helper()
	st, err := m.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m.HandleControl(envelope.Control{Op: envelope.OpPairCode, Ref: st.ID})
	m.mu.Lock()
	s := m.sessions[st.ID]
	m.mu.Unlock()
	return s, st.ID
}

// A correct tag_R that arrives inside the confirm wait completes the pairing
// even if K is only ready after the wait ended (it used to be failed as
// confirm_timeout and its tag dropped).
func TestReviewTagInTimeWithSlowKCompletes(t *testing.T) {
	m, snd, _, store := newRevManager(t, func(c *Config) { c.ConfirmWait = 40 * time.Millisecond })
	s, id := startRevIssuer(t, m)
	<-s.kd.ready
	m.mu.Lock()
	k := bytes.Clone(s.kd.k)
	gate := &kderiv{ready: make(chan struct{})} // K "still computing"
	s.kd = gate
	lookup, ownMbox := s.lookup, s.ownMbox
	m.mu.Unlock()

	p := newRevIdent(t, "peer")
	m.HandleControl(envelope.Control{Op: envelope.OpPairPeer, PublicKey: p.key, Card: p.card, Mbox: p.mbox, Ref: id})
	revWait(t, "attempt waiting", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(s.attempts) == 1 && s.attempts[0].waiting
	})
	cardR, err := canonicalPart(p.card, "card")
	if err != nil {
		t.Fatal(err)
	}
	tr := transcript(lookup, m.ownCard, cardR, ownMbox, p.mbox)
	m.HandleEnvelope(envelope.Envelope{From: p.key, Type: ConfirmType, Payload: confirmPayload(lookup, redeemerTag(k, tr))})

	time.Sleep(150 * time.Millisecond) // well past the confirm wait
	m.mu.Lock()
	gate.k = k
	m.mu.Unlock()
	close(gate.ready)

	revWait(t, "complete", func() bool { st, _ := m.Get(id); return st.State != StatePending })
	if st, _ := m.Get(id); st.State != StateComplete {
		t.Fatalf("status = %+v, want complete", st)
	}
	snd.mu.Lock()
	n := len(snd.envs)
	snd.mu.Unlock()
	if n != 1 {
		t.Errorf("issuer sent %d envelopes, want tag_I only", n)
	}
	list, err := store.List(context.Background())
	if err != nil || len(list) != 1 || list[0].Trust != TrustCode {
		t.Fatalf("peers = %+v, %v", list, err)
	}
	// The stored card is the canonical one the tags covered.
	var card string
	if err := store.db.QueryRow(`SELECT card FROM peers`).Scan(&card); err != nil || card != string(cardR) {
		t.Errorf("stored card = %q, %v", card, err)
	}
}

// Once a verified tag is being stored, timers and relay errors cannot end the
// pairing as failed.
func TestReviewCompletingPairingIgnoresFailures(t *testing.T) {
	m, _, _, _ := newRevManager(t, nil)
	s, id := startRevIssuer(t, m)
	m.mu.Lock()
	s.completing = true
	m.mu.Unlock()
	m.HandleError(envelope.ErrorFrame{Code: "pair_invalid", Ref: id})
	m.finish(id, StateFailed, nil, &Failure{Code: FailExpired})
	if st, _ := m.Get(id); st.State != StatePending {
		t.Fatalf("status = %+v, want still pending", st)
	}
	m.end(id, StateFailed, nil, &Failure{Code: FailStore}, true)
	if st, _ := m.Get(id); st.State != StateFailed || st.Error.Code != FailStore {
		t.Fatalf("status = %+v", st)
	}
}

// Relay-chosen strings are bounded before they reach the audit log.
func TestReviewRelayStringsAreBounded(t *testing.T) {
	m, _, log, _ := newRevManager(t, nil)
	s, id := startRevIssuer(t, m)
	huge := strings.Repeat("A", 100000)
	m.HandleControl(envelope.Control{Op: envelope.OpPairPeer, PublicKey: huge, Card: []byte(`{}`), Mbox: []byte(`{}`), Ref: id})
	revWait(t, "attempt failure", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return s.failures == 1
	})
	m.HandleError(envelope.ErrorFrame{Code: huge, Message: huge, Ref: id})
	st, _ := m.Get(id)
	if st.State != StateFailed || len(st.Error.Code) > maxCodeLen || len(st.Error.Message) > maxReasonLen {
		t.Fatalf("status error = %d/%d bytes", len(st.Error.Code), len(st.Error.Message))
	}
	evs, err := log.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if b, _ := json.Marshal(ev); len(b) > 2000 {
			t.Errorf("%s audit row is %d bytes", ev.Action, len(b))
		}
	}
}
