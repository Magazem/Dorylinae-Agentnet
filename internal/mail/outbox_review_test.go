package mail

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 10: a key-miss re-seal must not send a row that became final
// between Retry's read and its update (ack, expiry or peer removal).

type obPeers struct {
	mu   sync.Mutex
	keys map[string][]byte
}

func (p *obPeers) IsPaired(k string) bool { _, ok := p.MailboxPub(k); return ok }

func (p *obPeers) MailboxPub(k string) ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.keys[k]
	return v, ok
}

func (p *obPeers) set(k string, pub []byte) {
	p.mu.Lock()
	p.keys[k] = pub
	p.mu.Unlock()
}

func TestRetryDoesNotSendRowThatBecameFinal(t *testing.T) {
	for _, race := range []bool{false, true} {
		sender, recip, _ := fixture(t)
		st := openStore(t, filepath.Join(testutil.TempDir(t), "o.db"))
		t.Cleanup(func() { _ = st.Close() })
		db := st.DB()
		peers := &obPeers{keys: map[string][]byte{recip.key: recip.mbox.PublicKey().Bytes()}}
		out := &sentBox{}
		var hook func()
		ob := &Outbox{
			DB: db, Peers: peers, Sender: out,
			Priv: func() (ed25519.PrivateKey, error) { return append(ed25519.PrivateKey(nil), sender.priv...), nil },
			Now: func() time.Time {
				if h := hook; h != nil {
					hook = nil
					h()
				}
				return vectorNow
			},
		}
		ctx := context.Background()
		res, err := ob.Submit(ctx, recip.key, "note", map[string]any{"text": "x"})
		if err != nil {
			t.Fatal(err)
		}
		// The recipient announced a new key and reports a key miss for the row.
		fresh, err := ecdh.X25519().NewPrivateKey(seq(0x70))
		if err != nil {
			t.Fatal(err)
		}
		peers.set(recip.key, fresh.PublicKey().Bytes())
		if race {
			// An ack lands while Retry is re-sealing (Retry reads the clock there).
			hook = func() {
				if _, err := db.Exec(`UPDATE outbox SET state = 'delivered', signed = NULL, frame = NULL WHERE id = ?`, res.ID); err != nil {
					t.Error(err)
				}
			}
		}
		ob.Retry(ctx, recip.key, []string{res.ID})
		want := 1
		if race {
			want = 0
		}
		if n := out.count(); n != want {
			t.Fatalf("race=%v: Retry sent %d envelopes, want %d", race, n, want)
		}
		var state string
		var frame *string
		if err := db.QueryRow(`SELECT state, frame FROM outbox WHERE id = ?`, res.ID).Scan(&state, &frame); err != nil {
			t.Fatal(err)
		}
		if race && (state != StateDelivered || frame != nil) {
			t.Fatalf("final row was rewritten: state %s, frame kept %v", state, frame != nil)
		}
	}
}
