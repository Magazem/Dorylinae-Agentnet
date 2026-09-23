package daemon

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
)

// withOutbox gives n a sender outbox that sends through n.Send, routes acks
// and keys retries to it, and understands a "note" kind.
func withOutbox(t *testing.T, n *mnode, clk *testClock) *mail.Outbox {
	t.Helper()
	ob := &mail.Outbox{
		DB: n.db, Peers: n.dir, Sender: n, Now: clk.now, Tick: 2 * time.Millisecond,
		Audit: audit.New(n.db),
		Priv:  func() (ed25519.PrivateKey, error) { return append(ed25519.PrivateKey(nil), n.priv...), nil },
	}
	n.rcv.OnAck = ob.OnAck
	n.rcv.Kinds["keys"] = mail.KeysKind(peers.MergeMailboxKeysTx, ob.Retry)
	n.rcv.Kinds["note"] = mail.Kind{Inbox: true}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); ob.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return ob
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (n *mnode) waitSent(t *testing.T, count int) []envelope.Envelope {
	t.Helper()
	var out []envelope.Envelope
	waitFor(t, "sent envelopes", func() bool {
		out = append(out, n.take()...)
		return len(out) >= count
	})
	return out
}

func rowState(t *testing.T, n *mnode, id string) (state, errText string, plaintextLeft bool) {
	t.Helper()
	var e, s, f *string
	if err := n.db.QueryRow(`SELECT state, error, signed, frame FROM outbox WHERE id = ?`, id).Scan(&state, &e, &s, &f); err != nil {
		t.Fatal(err)
	}
	if e != nil {
		errText = *e
	}
	return state, errText, s != nil || f != nil
}

func inboxRows(t *testing.T, n *mnode) int {
	t.Helper()
	var c int
	if err := n.db.QueryRow(`SELECT COUNT(*) FROM mail_inbox`).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestOutboxSubmitSendAckDelivered(t *testing.T) {
	ctx := context.Background()
	clk := &testClock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	a, b := newMNode(t, clk), newMNode(t, clk)
	pair(t, a, b)
	ob := withOutbox(t, a, clk)
	withOutbox(t, b, clk)

	start := time.Now()
	res, err := ob.Submit(ctx, b.key, "note", map[string]any{"text": "hi"})
	if err != nil || res.State != "queued" || !mail.ValidID(res.ID) {
		t.Fatalf("Submit = %+v, %v", res, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("Submit took over 2 s")
	}
	env := a.waitSent(t, 1)[0]
	if env.ID != res.ID || env.To != b.key || env.Type != "mail" {
		t.Fatalf("sent %+v", env)
	}
	waitFor(t, "relayed", func() bool { s, _, _ := rowState(t, a, res.ID); return s == "relayed" })

	if err := b.rcv.Handle(ctx, env); err != nil {
		t.Fatal(err)
	}
	// The same envelope again (a resend after a lost ack): one inbox row, acked again.
	if err := b.rcv.Handle(ctx, env); err != nil {
		t.Fatal(err)
	}
	if n := inboxRows(t, b); n != 1 {
		t.Fatalf("inbox rows = %d, want 1", n)
	}
	acks := b.take()
	if len(acks) != 2 {
		t.Fatalf("B sent %d acks, want 2 (one per delivery)", len(acks))
	}
	if err := a.rcv.Handle(ctx, acks[0]); err != nil {
		t.Fatal(err)
	}
	state, _, left := rowState(t, a, res.ID)
	if state != "delivered" || left {
		t.Fatalf("after ack: state %s, plaintext left %v", state, left)
	}
	// A second ack for a final row is ignored.
	if err := a.rcv.Handle(ctx, acks[1]); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := rowState(t, a, res.ID); s != "delivered" {
		t.Fatalf("state %s", s)
	}
	c, err := ob.Counts(ctx)
	if err != nil || c.Queued != 0 || c.Relayed != 0 {
		t.Fatalf("Counts = %+v, %v", c, err)
	}
}

func TestOutboxResendKeepsIDAndFrame(t *testing.T) {
	ctx := context.Background()
	clk := &testClock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	a, b := newMNode(t, clk), newMNode(t, clk)
	pair(t, a, b)
	ob := withOutbox(t, a, clk)

	res, err := ob.Submit(ctx, b.key, "note", nil)
	if err != nil {
		t.Fatal(err)
	}
	first := a.waitSent(t, 1)[0]
	clk.add(2 * time.Minute) // past delay(1) = 1 min plus jitter
	second := a.waitSent(t, 1)[0]
	if first.ID != res.ID || second.ID != res.ID || first.TS != second.TS || string(first.Payload) != string(second.Payload) {
		t.Fatal("resend is not the stored frame unchanged")
	}
	if c, _ := ob.Counts(ctx); c.Relayed != 1 {
		t.Fatalf("Counts = %+v, want one relayed", c)
	}
}

func TestOutboxRelayErrorFrames(t *testing.T) {
	ctx := context.Background()
	clk := &testClock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	a, b := newMNode(t, clk), newMNode(t, clk)
	pair(t, a, b)
	ob := withOutbox(t, a, clk)

	r1, _ := ob.Submit(ctx, b.key, "note", nil)
	a.waitSent(t, 1)
	waitFor(t, "relayed", func() bool { s, _, _ := rowState(t, a, r1.ID); return s == "relayed" })
	ob.HandleError(envelope.ErrorFrame{Code: envelope.CodeQueueFull, Ref: r1.ID})
	if s, _, _ := rowState(t, a, r1.ID); s != "queued" {
		t.Errorf("queue_full: state %s, want queued", s)
	}
	ob.HandleError(envelope.ErrorFrame{Code: envelope.CodeBadEnvelope, Ref: r1.ID})
	if s, e, left := rowState(t, a, r1.ID); s != "failed" || e != "bad_envelope" || left {
		t.Errorf("bad_envelope: %s %q plaintext %v", s, e, left)
	}
}

func TestOutboxExpiry(t *testing.T) {
	ctx := context.Background()
	clk := &testClock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	a, b := newMNode(t, clk), newMNode(t, clk)
	pair(t, a, b)
	ob := withOutbox(t, a, clk)

	res, _ := ob.Submit(ctx, b.key, "note", nil)
	a.waitSent(t, 1)
	clk.add(mail.OutboxExpiry)
	waitFor(t, "expired", func() bool { s, _, _ := rowState(t, a, res.ID); return s == "expired" })
	if _, _, left := rowState(t, a, res.ID); left {
		t.Error("expired row keeps its plaintext")
	}
	if c, _ := ob.Counts(ctx); c.Expired != 1 {
		t.Errorf("Counts = %+v", c)
	}
	// The audit row is written just after the state change, so poll for it.
	var n int
	var err error
	waitFor(t, "mail.expired audit row", func() bool {
		err = a.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'mail.expired'`).Scan(&n)
		return err != nil || n >= 1
	})
	if err != nil || n != 1 {
		t.Errorf("mail.expired audit rows = %d, %v", n, err)
	}
}

func TestOutboxUnsupportedKindFails(t *testing.T) {
	ctx := context.Background()
	clk := &testClock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	a, b := newMNode(t, clk), newMNode(t, clk)
	pair(t, a, b)
	ob := withOutbox(t, a, clk)
	withOutbox(t, b, clk)

	res, _ := ob.Submit(ctx, b.key, "mystery", nil) // B does not know it
	env := a.waitSent(t, 1)[0]
	if err := b.rcv.Handle(ctx, env); err != nil {
		t.Fatal(err)
	}
	if err := a.rcv.Handle(ctx, b.waitSent(t, 1)[0]); err != nil {
		t.Fatal(err)
	}
	if s, e, left := rowState(t, a, res.ID); s != "failed" || e != "unsupported_kind" || left {
		t.Fatalf("state %s %q plaintext %v", s, e, left)
	}
}

func TestOutboxRefusals(t *testing.T) {
	ctx := context.Background()
	clk := &testClock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	a, b := newMNode(t, clk), newMNode(t, clk)
	ob := withOutbox(t, a, clk)
	if _, err := ob.Submit(ctx, b.key, "note", nil); !errors.Is(err, mail.ErrUnpaired) {
		t.Errorf("unpaired: %v", err)
	}
	pair(t, a, b)
	if _, err := ob.Submit(ctx, b.key, "ack", nil); !errors.Is(err, mail.ErrKindNotSent) {
		t.Errorf("ack: %v", err)
	}
	if _, err := a.db.Exec(`UPDATE peers SET mailbox_keys = '[]'`); err != nil {
		t.Fatal(err)
	}
	if _, err := ob.Submit(ctx, b.key, "note", nil); !errors.Is(err, mail.ErrNoMailboxKey) {
		t.Errorf("no key: %v", err)
	}
}

func TestOutboxKeyMissResealDelivers(t *testing.T) {
	ctx := context.Background()
	clk := &testClock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	a, b := newMNode(t, clk), newMNode(t, clk)
	pair(t, a, b)
	ob := withOutbox(t, a, clk)
	withOutbox(t, b, clk)

	// A holds an announcement of B whose private key B no longer has. It is
	// older than B's real one, so B's reply can replace it.
	gone, _ := ecdh.X25519().GenerateKey(rand.Reader)
	stale, err := mail.SignAnnouncement(b.priv.Public().(ed25519.PublicKey), func(m []byte) ([]byte, error) { return ed25519.Sign(b.priv, m), nil },
		gone.PublicKey().Bytes(), clk.now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE peers SET mailbox_keys = ? WHERE public_key = ?`, "["+string(stale)+"]", b.key); err != nil {
		t.Fatal(err)
	}

	res, err := ob.Submit(ctx, b.key, "note", map[string]any{"text": "sealed to a dead key"})
	if err != nil {
		t.Fatal(err)
	}
	old := a.waitSent(t, 1)[0]
	if mail.ReasonOf(b.rcv.Handle(ctx, old)) != mail.ReasonKeyMiss {
		t.Fatal("B did not report a key miss")
	}
	reply := b.waitSent(t, 1)[0] // keys mail with the retry list
	if err := a.rcv.Handle(ctx, reply); err != nil {
		t.Fatal(err)
	}
	// A acks the keys mail and re-seals the note (same id, new payload).
	var resealed envelope.Envelope
	for _, e := range a.waitSent(t, 2) {
		if e.ID == res.ID {
			resealed = e
		}
	}
	if resealed.ID != res.ID || string(resealed.Payload) == string(old.Payload) {
		t.Fatal("A did not re-seal the same id to the new key")
	}
	if err := b.rcv.Handle(ctx, resealed); err != nil {
		t.Fatalf("B rejects the re-sealed mail: %v", err)
	}
	if n := inboxRows(t, b); n != 1 {
		t.Fatalf("inbox rows = %d", n)
	}
	if err := a.rcv.Handle(ctx, b.waitSent(t, 1)[0]); err != nil { // ack of the note
		t.Fatal(err)
	}
	if s, _, left := rowState(t, a, res.ID); s != "delivered" || left {
		t.Fatalf("state %s, plaintext left %v", s, left)
	}
}

func TestRotationPushGoesThroughOutbox(t *testing.T) {
	ctx := context.Background()
	clk := &testClock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	a, b := newMNode(t, clk), newMNode(t, clk)
	pair(t, a, b)
	ob := withOutbox(t, a, clk)
	withOutbox(t, b, clk)
	a.pusher.Outbox = func(ctx context.Context, peer string, body map[string]any) error {
		_, err := ob.Submit(ctx, peer, "keys", body)
		return err
	}
	clk.add(7 * 24 * time.Hour)
	if _, err := a.keys.Rotate(ctx); err != nil {
		t.Fatal(err)
	}
	push := a.waitSent(t, 1)[0]
	var id string
	waitFor(t, "outbox row", func() bool {
		return a.db.QueryRow(`SELECT id FROM outbox WHERE kind = 'keys'`).Scan(&id) == nil
	})
	if id != push.ID {
		t.Fatal("the push is not the outbox row")
	}
	if err := b.rcv.Handle(ctx, push); err != nil {
		t.Fatal(err)
	}
	if err := a.rcv.Handle(ctx, b.waitSent(t, 1)[0]); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := rowState(t, a, id); s != "delivered" {
		t.Fatalf("keys push state %s", s)
	}
}

// Review 10: an ack only settles rows addressed to the acking peer. Another
// paired peer (or a relay replaying its acks) cannot mark A's mail to B delivered.
func TestOutboxAckFromOtherPeerIgnored(t *testing.T) {
	ctx := context.Background()
	clk := &testClock{t: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	a, b, c := newMNode(t, clk), newMNode(t, clk), newMNode(t, clk)
	pair(t, a, b)
	pair(t, a, c)
	ob := withOutbox(t, a, clk)

	res, err := ob.Submit(ctx, b.key, "note", map[string]any{"text": "for b only"})
	if err != nil {
		t.Fatal(err)
	}
	env := a.waitSent(t, 1)[0]
	waitFor(t, "relayed", func() bool { s, _, _ := rowState(t, a, res.ID); return s == "relayed" })

	pubA, ok := c.dir.MailboxPub(a.key)
	if !ok {
		t.Fatal("c has no mailbox key for a")
	}
	for _, member := range []string{"ids", "unsupported"} {
		sl, err := mail.Seal(mail.SealInput{Priv: c.priv, To: a.key, MailboxPub: pubA, Kind: "ack",
			Body: map[string]any{member: []string{res.ID}}, Created: clk.now()})
		if err != nil {
			t.Fatal(err)
		}
		if err := a.rcv.Handle(ctx, toEnvelope(c, a.key, sl)); err != nil {
			t.Fatal(err)
		}
		if s, _, left := rowState(t, a, res.ID); s != "relayed" || !left {
			t.Fatalf("after C's %s ack: state %s, plaintext kept %v", member, s, left)
		}
	}

	// B's real ack still settles it.
	withOutbox(t, b, clk)
	if err := b.rcv.Handle(ctx, env); err != nil {
		t.Fatal(err)
	}
	if err := a.rcv.Handle(ctx, b.waitSent(t, 1)[0]); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := rowState(t, a, res.ID); s != "delivered" {
		t.Fatalf("after B's ack: state %s", s)
	}
}
