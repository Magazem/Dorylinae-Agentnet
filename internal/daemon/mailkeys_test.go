package daemon

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mailbox"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func TestDebugNoteKindOnlyWithEnv(t *testing.T) {
	build := func() map[string]mail.Kind {
		rcv, _ := newMailReceiver(nil, nil, nil, make([]byte, ed25519.PublicKeySize), nil, nil, nil)
		return rcv.Kinds
	}
	t.Setenv(mail.DebugEnv, "")
	if _, ok := build()[mail.DebugKind]; ok {
		t.Error("note kind registered without DORYLINAE_DEBUG=1")
	}
	t.Setenv(mail.DebugEnv, "1")
	k, ok := build()[mail.DebugKind]
	if !ok || !k.Inbox || k.Apply != nil || k.After != nil {
		t.Errorf("note kind = %+v, %v; want a bare inbox kind", k, ok)
	}
	if _, ok := build()["keys"]; !ok {
		t.Error("keys kind lost")
	}
}

const annTimeFmt = "2006-01-02T15:04:05Z"

// mnode is one daemon's mail machinery over a real database and mailbox keys.
type mnode struct {
	priv   ed25519.PrivateKey
	key    string
	db     *sql.DB
	keys   *mailbox.Keys
	rcv    *mail.Receiver
	pusher *mail.Pusher
	dir    peerDirectory

	mu   sync.Mutex
	sent []envelope.Envelope
}

func (n *mnode) Send(_ context.Context, e envelope.Envelope) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, e)
	return nil
}

func (n *mnode) take() []envelope.Envelope {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := n.sent
	n.sent = nil
	return out
}

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newMNode(t *testing.T, clk *testClock) *mnode {
	t.Helper()
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testutil.TempDir(t)
	ks := keystore.New(keystore.NewFile(filepath.Join(cfg, "identity.key")))
	if _, _, err := ks.Save(priv.Seed()); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(cfg, "agentnet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	alog := audit.New(st.DB())
	keys := mailbox.New(cfg, "file", pub, func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil }, clk.now)
	if err := keys.Attach(ctx, st.DB(), alog, nil); err != nil {
		t.Fatal(err)
	}
	n := &mnode{priv: priv, key: envelope.KeyString(pub), db: st.DB(), keys: keys, dir: peerDirectory{st.DB()}}
	n.rcv, n.pusher = newMailReceiver(st.DB(), alog, ks, pub, keys, nil, nil)
	n.rcv.Sender, n.pusher.Sender = n, n
	n.rcv.Now, n.rcv.Opener.Now, n.pusher.Now = clk.now, clk.now, clk.now
	keys.OnRotate(func(a []byte) { n.pusher.PushAll(ctx, a) })
	return n
}

// pair stores each side's current announcement, as a v2 pairing does.
func pair(t *testing.T, a, b *mnode) {
	t.Helper()
	for _, p := range [][2]*mnode{{a, b}, {b, a}} {
		self, other := p[0], p[1]
		ann, err := other.keys.Announcement()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := self.db.Exec(`INSERT INTO peers (public_key, name, harness, skills, card, paired_at, trust, mailbox_keys)
VALUES (?, 'n', 'h', '[]', '{}', '2026-01-01T00:00:00Z', 'code', ?)`, other.key, "["+string(ann)+"]"); err != nil {
			t.Fatal(err)
		}
	}
	// Creating the first keys may have pushed them; start each test from silence.
	a.take()
	b.take()
}

// storedKeys returns the announcements stored for peer, newest first.
func (n *mnode) storedKeys(t *testing.T, peer string) []json.RawMessage {
	t.Helper()
	var raw string
	if err := n.db.QueryRow(`SELECT mailbox_keys FROM peers WHERE public_key = ?`, peer).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var out []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func toEnvelope(from *mnode, to string, sl mail.Sealed) envelope.Envelope {
	return envelope.Envelope{From: from.key, To: to, Type: "mail", ID: sl.ID, TS: "2026-05-01T12:00:00Z", Payload: sl.Payload}
}

func TestRotationIsPushedAndPeerCanSealToNewKey(t *testing.T) {
	ctx := context.Background()
	clk := &testClock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	a, b := newMNode(t, clk), newMNode(t, clk)
	pair(t, a, b)

	// After pairing both sides hold the other's verified announcement.
	for _, p := range [][2]*mnode{{a, b}, {b, a}} {
		want, _ := p[1].keys.Announcement()
		got := p[0].storedKeys(t, p[1].key)
		if len(got) != 1 || string(got[0]) != string(want) {
			t.Fatal("peer does not hold the other side's announcement")
		}
	}

	clk.add(7 * 24 * time.Hour)
	ann, err := a.keys.Rotate(ctx)
	if err != nil || ann == nil {
		t.Fatalf("rotate: %v", err)
	}
	out := a.take()
	if len(out) != 1 || out[0].To != b.key {
		t.Fatalf("rotation pushed %d envelopes, want 1 to B", len(out))
	}
	if err := b.rcv.Handle(ctx, out[0]); err != nil {
		t.Fatalf("B rejected the keys mail: %v", err)
	}
	if got := b.storedKeys(t, a.key); len(got) != 2 || string(got[0]) != string(ann) {
		t.Fatalf("B stores %d announcements, newest is not the rotated one", len(got))
	}
	if acks := b.take(); len(acks) != 1 || acks[0].To != a.key {
		t.Fatal("B did not ack the keys mail")
	}
	// Idempotent: the same mail again is a duplicate, acked, and changes nothing.
	if err := b.rcv.Handle(ctx, out[0]); err != nil || len(b.storedKeys(t, a.key)) != 2 {
		t.Fatalf("repeat: %v", err)
	}

	// B seals to the rotated key and A opens it with it.
	pub, _ := b.dir.MailboxPub(a.key)
	sl, err := mail.Seal(mail.SealInput{Priv: b.priv, To: a.key, MailboxPub: pub, Kind: "x.test", Created: clk.now()})
	if err != nil {
		t.Fatal(err)
	}
	op, err := a.rcv.Opener.Open(toEnvelope(b, a.key, sl))
	if err != nil {
		t.Fatalf("A cannot open mail sealed to its rotated key: %v", err)
	}
	if op.KeyID != mail.KeyIDOf(pub) {
		t.Error("mail was not opened with the new key")
	}
}

func TestForgedKeysAnnouncementRejected(t *testing.T) {
	ctx := context.Background()
	clk := &testClock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	a, b := newMNode(t, clk), newMNode(t, clk)
	pair(t, a, b)
	before := b.storedKeys(t, a.key)

	mallory, mpriv, _ := ed25519.GenerateKey(rand.Reader)
	x, _ := ecdh.X25519().GenerateKey(rand.Reader)
	wrongOwner, err := mail.SignAnnouncement(mallory, func(m []byte) ([]byte, error) { return ed25519.Sign(mpriv, m), nil }, x.PublicKey().Bytes(), clk.now())
	if err != nil {
		t.Fatal(err)
	}
	good, _ := a.keys.Announcement()
	created := clk.now().Format(annTimeFmt)
	tampered := strings.Replace(string(good), `"created":"`+created, `"created":"`+clk.now().Add(-time.Hour).Format(annTimeFmt), 1)
	if tampered == string(good) {
		t.Fatal("test did not alter the announcement")
	}
	for name, ann := range map[string][]byte{"wrong owner": wrongOwner, "bad signature": []byte(tampered)} {
		pub, _ := a.dir.MailboxPub(b.key)
		sl, err := mail.Seal(mail.SealInput{
			Priv: a.priv, To: b.key, MailboxPub: pub, Kind: "keys", Created: clk.now(),
			Body: map[string]any{"announcement": json.RawMessage(ann)},
		})
		if err != nil {
			t.Fatal(err)
		}
		err = b.rcv.Handle(ctx, toEnvelope(a, b.key, sl))
		if mail.ReasonOf(err) != mail.ReasonBadKeys {
			t.Errorf("%s: err = %v, want reason bad_keys", name, err)
		}
	}
	after := b.storedKeys(t, a.key)
	if len(after) != len(before) || string(after[0]) != string(before[0]) {
		t.Error("a forged announcement changed the stored keys")
	}
	if len(b.take()) != 0 {
		t.Error("a rejected keys mail was acked")
	}
}

func TestKeyMissTriggersKeysReplyWithRetryList(t *testing.T) {
	ctx := context.Background()
	clk := &testClock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	a, b := newMNode(t, clk), newMNode(t, clk)
	pair(t, a, b)

	// B seals to a mailbox key that A does not have (deleted or never existed).
	gone, _ := ecdh.X25519().GenerateKey(rand.Reader)
	sl, err := mail.Seal(mail.SealInput{Priv: b.priv, To: a.key, MailboxPub: gone.PublicKey().Bytes(), Kind: "x.test", Created: clk.now()})
	if err != nil {
		t.Fatal(err)
	}
	err = a.rcv.Handle(ctx, toEnvelope(b, a.key, sl))
	if mail.ReasonOf(err) != mail.ReasonKeyMiss {
		t.Fatalf("err = %v, want key_miss", err)
	}
	// A malformed envelope id is ignored: no reply of its own.
	bad := toEnvelope(b, a.key, sl)
	bad.ID = "not-an-id"
	_ = a.rcv.Handle(ctx, bad)
	out := a.take()
	if len(out) != 1 || out[0].To != b.key {
		t.Fatalf("A sent %d envelopes, want exactly one keys reply", len(out))
	}

	// B receives the reply: the announcement is merged and the retry ids are handed on.
	var gotPeer string
	var gotIDs []string
	b.rcv.Kinds["keys"] = mail.KeysKind(peers.MergeMailboxKeysTx, func(_ context.Context, peer string, ids []string) {
		gotPeer, gotIDs = peer, ids
	})
	op, err := b.rcv.Opener.Open(out[0])
	if err != nil || op.Msg.Kind != "keys" {
		t.Fatalf("B cannot open the reply: %v", err)
	}
	if err := b.rcv.Handle(ctx, out[0]); err != nil {
		t.Fatal(err)
	}
	if gotPeer != a.key || len(gotIDs) != 1 || gotIDs[0] != sl.ID {
		t.Errorf("retry hook got %q %v, want A and [%s]", gotPeer, gotIDs, sl.ID)
	}
}

func TestKeyMissReplyIsRateLimitedPerPeer(t *testing.T) {
	ctx := context.Background()
	clk := &testClock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	a, b := newMNode(t, clk), newMNode(t, clk)
	pair(t, a, b)
	var fire func()
	var wait time.Duration
	km := &mail.KeyMiss{
		Pusher: a.pusher, Announcement: a.keys.Announcement, Now: clk.now,
		After: func(d time.Duration, f func()) { wait, fire = d, f },
	}

	id1, id2, id3 := mail.NewID(), mail.NewID(), mail.NewID()
	km.Note(ctx, b.key, id1)
	if len(a.take()) != 1 {
		t.Fatal("first miss was not answered at once")
	}
	clk.add(time.Minute)
	km.Note(ctx, b.key, id2)
	km.Note(ctx, b.key, id3)
	km.Note(ctx, b.key, "bad")
	if len(a.take()) != 0 || fire == nil || wait != 9*time.Minute {
		t.Fatalf("second miss inside the window was not held (wait %v)", wait)
	}
	clk.add(9 * time.Minute)
	fire()
	out := a.take()
	if len(out) != 1 {
		t.Fatalf("held set was not sent when the window ended (%d)", len(out))
	}
	op, err := b.rcv.Opener.Open(out[0])
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := op.Msg.Body["retry"].([]any); len(list) != 2 || list[0] != id2 || list[1] != id3 {
		t.Errorf("retry = %v, want [%s %s]", list, id2, id3)
	}
}
