package mail

import (
	"context"
	"crypto/ed25519"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// INV-4: a hand-off that fails because the relay is not connected yet must
// not undo the relay-connected hook that ran while it was in flight. Before
// the fix the failed send's backoff (about a minute) overwrote OnReady's
// "send now", so a mail queued at start-up (a restarted helper's ws.cancel)
// waited a minute after the relay was already connected.

// readySender fails every hand-off as not connected; the first one also runs
// onSend first, as a relay connection that becomes ready meanwhile would.
type readySender struct {
	mu     sync.Mutex
	onSend func()
	calls  int
}

func (s *readySender) Send(context.Context, envelope.Envelope) error {
	s.mu.Lock()
	h := s.onSend
	s.onSend = nil
	s.calls++
	s.mu.Unlock()
	if h != nil {
		h()
	}
	return errors.New("relayclient: not connected")
}

func (s *readySender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestFailedSendKeepsConcurrentReady(t *testing.T) {
	for _, ready := range []bool{false, true} {
		sender, recip, _ := fixture(t)
		st := openStore(t, filepath.Join(testutil.TempDir(t), "o.db"))
		t.Cleanup(func() { _ = st.Close() })
		out := &readySender{}
		ob := &Outbox{
			DB: st.DB(), Sender: out,
			Peers: &obPeers{keys: map[string][]byte{recip.key: recip.mbox.PublicKey().Bytes()}},
			Priv:  func() (ed25519.PrivateKey, error) { return append(ed25519.PrivateKey(nil), sender.priv...), nil },
			Now:   func() time.Time { return vectorNow },
		}
		ctx := context.Background()
		if _, err := ob.Submit(ctx, recip.key, "note", map[string]any{"text": "x"}); err != nil {
			t.Fatal(err)
		}
		if ready {
			out.onSend = func() { ob.OnReady(ctx) }
		}
		ob.sendDue(ctx, vectorNow)
		ob.sendDue(ctx, vectorNow)
		want := 1
		if ready {
			want = 2
		}
		if n := out.count(); n != want {
			t.Fatalf("ready=%v: %d hand-offs at the same instant, want %d", ready, n, want)
		}
	}
}

// A failed hand-off must not undo OnPeerOnline of its own peer that ran while
// it was in flight, but must ignore one for another peer.
func TestFailedSendKeepsConcurrentPeerOnline(t *testing.T) {
	for _, own := range []bool{false, true} {
		sender, recip, _ := fixture(t)
		st := openStore(t, filepath.Join(testutil.TempDir(t), "o.db"))
		t.Cleanup(func() { _ = st.Close() })
		out := &readySender{}
		ob := &Outbox{
			DB: st.DB(), Sender: out,
			Peers: &obPeers{keys: map[string][]byte{recip.key: recip.mbox.PublicKey().Bytes()}},
			Priv:  func() (ed25519.PrivateKey, error) { return append(ed25519.PrivateKey(nil), sender.priv...), nil },
			Now:   func() time.Time { return vectorNow },
		}
		ctx := context.Background()
		if _, err := ob.Submit(ctx, recip.key, "note", map[string]any{"text": "x"}); err != nil {
			t.Fatal(err)
		}
		peer := "other"
		if own {
			peer = recip.key
		}
		out.onSend = func() { ob.OnPeerOnline(peer) }
		ob.sendDue(ctx, vectorNow)
		ob.sendDue(ctx, vectorNow)
		want := 1
		if own {
			want = 2
		}
		if n := out.count(); n != want {
			t.Fatalf("own=%v: %d hand-offs at the same instant, want %d", own, n, want)
		}
	}
}
