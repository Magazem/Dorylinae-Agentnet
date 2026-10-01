package mail

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// R55-F13 (review 55 C07-01, R55-020): a paired peer that announces a fresh
// key before every keys mail must not make Retry re-seal and re-send every
// open row every time. A row is re-sealed at most once per hour
// (Docs/protocol/mail.md §Key-miss recovery step 3).
func TestRetryResealsARowAtMostOncePerHour(t *testing.T) {
	sender, recip, _ := fixture(t)
	st := openStore(t, filepath.Join(testutil.TempDir(t), "o.db"))
	t.Cleanup(func() { _ = st.Close() })
	peers := &obPeers{keys: map[string][]byte{recip.key: recip.mbox.PublicKey().Bytes()}}
	out := &sentBox{}
	now := vectorNow
	ob := &Outbox{
		DB: st.DB(), Peers: peers, Sender: out,
		Priv: func() (ed25519.PrivateKey, error) { return append(ed25519.PrivateKey(nil), sender.priv...), nil },
		Now:  func() time.Time { return now },
	}
	ctx := context.Background()
	var ids []string
	for i := 0; i < 3; i++ {
		res, err := ob.Submit(ctx, recip.key, "note", map[string]any{"text": "x"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.ID)
	}
	announce := func(b byte) {
		t.Helper()
		k, err := ecdh.X25519().NewPrivateKey(seq(b))
		if err != nil {
			t.Fatal(err)
		}
		peers.set(recip.key, k.PublicKey().Bytes()) // merge of a newer announcement
	}
	const rounds = 5
	for r := 0; r < rounds; r++ {
		announce(byte(0x80 + r))
		ob.Retry(ctx, recip.key, ids)
		now = now.Add(10 * time.Minute) // all five within the hour
	}
	if n := out.count(); n != len(ids) {
		t.Fatalf("%d rows x %d announcements within an hour: sent %d envelopes, want %d", len(ids), rounds, n, len(ids))
	}

	// One hour after the first re-seal, a new key miss re-seals again.
	now = vectorNow.Add(ResealInterval)
	announce(0x90)
	ob.Retry(ctx, recip.key, ids)
	if n := out.count(); n != 2*len(ids) {
		t.Fatalf("after an hour: sent %d envelopes in total, want %d", n, 2*len(ids))
	}
	for _, id := range ids {
		var keyID string
		if err := st.DB().QueryRow(`SELECT key_id FROM outbox WHERE id = ?`, id).Scan(&keyID); err != nil {
			t.Fatal(err)
		}
		if pub, _ := peers.MailboxPub(recip.key); keyID != KeyIDOf(pub).String() {
			t.Fatalf("row %s sealed to %s, not the newest key", id, keyID)
		}
	}

	// A row that becomes final leaves the in-memory map.
	if !ob.finish(ctx, ids[0], "", StateDelivered, "") {
		t.Fatal("finish changed no row")
	}
	ob.resealMu.Lock()
	_, kept := ob.resealed[ids[0]]
	left := len(ob.resealed)
	ob.resealMu.Unlock()
	if kept || left != len(ids)-1 {
		t.Fatalf("after a final state: entry kept %v, %d entries left", kept, left)
	}
	// Entries an hour old are dropped at the next re-seal.
	now = now.Add(ResealInterval)
	announce(0x91)
	ob.Retry(ctx, recip.key, ids[1:2])
	ob.resealMu.Lock()
	left = len(ob.resealed)
	ob.resealMu.Unlock()
	if left != 1 {
		t.Fatalf("%d entries after an hour, want 1", left)
	}
}
