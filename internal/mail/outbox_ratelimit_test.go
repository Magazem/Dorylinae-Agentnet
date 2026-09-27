package mail

import (
	"context"
	"crypto/ed25519"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *stepClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// End to end through a real relay (ticket 4.0b): a mail the relay refuses
// with rate_limited goes back to queued, is resent after its backoff, and
// reaches the recipient exactly once.
func TestOutboxResendsAfterRateLimitedDeliveredOnce(t *testing.T) {
	sender, recip, recipOpener := fixture(t)
	clk := &stepClock{t: vectorNow}
	// One non-ephemeral envelope per key per minute, burst 1.
	rs := relay.New(relay.Options{Now: clk.now, KeyEnvelopesPerMinute: 1, KeyEnvelopeBurst: 1})
	t.Cleanup(rs.Close)
	ts := httptest.NewServer(rs)
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + envelope.ConnectPath
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Recipient: a relay client and a receiver.
	rst := openStore(t, filepath.Join(testutil.TempDir(t), "r.db"))
	t.Cleanup(func() { _ = rst.Close() })
	var mu sync.Mutex
	arrivals := 0
	var rcv *Receiver
	rc, err := relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(recip.priv), MinBackoff: 10 * time.Millisecond,
		OnEnvelope: func(e envelope.Envelope) {
			if e.Type != MailType {
				return
			}
			mu.Lock()
			arrivals++
			mu.Unlock()
			_ = rcv.Handle(ctx, e)
		}})
	if err != nil {
		t.Fatal(err)
	}
	rcv = &Receiver{
		Opener: recipOpener, DB: rst.DB(), Sender: rc,
		Priv:  func() (ed25519.PrivateKey, error) { return append(ed25519.PrivateKey(nil), recip.priv...), nil },
		Peers: peerKeys{sender.key: sender.mbox.PublicKey().Bytes()},
		Kinds: map[string]Kind{"note": {Inbox: true}},
		Now:   clk.now,
	}

	// Sender: outbox plus a receiver for the ack mail.
	sst := openStore(t, filepath.Join(testutil.TempDir(t), "s.db"))
	t.Cleanup(func() { _ = sst.Close() })
	var sc *relayclient.Client
	var ob *Outbox
	var srcv *Receiver
	codes := make(chan string, 16)
	sc, err = relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(sender.priv), MinBackoff: 10 * time.Millisecond,
		OnError: func(ef envelope.ErrorFrame) {
			ob.HandleError(ef)
			codes <- ef.Code + ":" + ef.Ref
		},
		OnEnvelope: func(e envelope.Envelope) {
			if e.Type == MailType {
				_ = srcv.Handle(ctx, e)
			}
		},
		OnReady: func() { ob.OnReady(ctx) },
	})
	if err != nil {
		t.Fatal(err)
	}
	ob = &Outbox{
		DB: sst.DB(), Sender: sc, Now: clk.now, Tick: 5 * time.Millisecond,
		Peers: &obPeers{keys: map[string][]byte{recip.key: recip.mbox.PublicKey().Bytes()}},
		Priv:  func() (ed25519.PrivateKey, error) { return append(ed25519.PrivateKey(nil), sender.priv...), nil },
	}
	srcv = &Receiver{
		Opener: &Opener{
			Self:  sender.key,
			Peers: fakePeers{recip.key: true},
			Keys:  fakeKeys{KeyIDOf(sender.mbox.PublicKey().Bytes()): sender.mbox},
			Now:   func() time.Time { return vectorNow },
		},
		DB: sst.DB(), Sender: sc,
		Priv:  func() (ed25519.PrivateKey, error) { return append(ed25519.PrivateKey(nil), sender.priv...), nil },
		Peers: peerKeys{recip.key: recip.mbox.PublicKey().Bytes()},
		Kinds: map[string]Kind{}, OnAck: ob.OnAck, Now: clk.now,
	}
	var running sync.WaitGroup
	t.Cleanup(func() { cancel(); running.Wait() }) // before the stores close
	for _, run := range []func(context.Context){func(c context.Context) { _ = rc.Run(c) }, func(c context.Context) { _ = sc.Run(c) }, ob.Run} {
		running.Add(1)
		go func() { defer running.Done(); run(ctx) }()
	}
	waitUntil(t, "both connected", func() bool {
		return rs.Connected(sender.key) && rs.Connected(recip.key) && sc.Connected() && rc.Connected()
	})

	// Spend the sender's one envelope this minute.
	if err := sc.Send(ctx, envelope.Envelope{From: sender.key, To: recip.key, Type: "ping", ID: "spend", TS: vectorNow.Format(time.RFC3339), Payload: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	res, err := ob.Submit(ctx, recip.key, "note", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	ob.Wake()
	select {
	case got := <-codes:
		if got != envelope.CodeRateLimited+":"+res.ID {
			t.Fatalf("relay error %s, want rate_limited for the mail", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the relay never refused the mail")
	}
	state := func() string {
		var s string
		if err := sst.DB().QueryRow(`SELECT state FROM outbox WHERE id = ?`, res.ID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	// HandleError moves a relayed row back to queued; if the error beats
	// send's own update the row stays relayed. Either way it keeps its
	// backoff and is resent when due.
	time.Sleep(100 * time.Millisecond) // the backoff holds it: no resend yet
	if st := state(); st != StateQueued && st != StateRelayed {
		t.Fatalf("state %s after rate_limited", st)
	}
	mu.Lock()
	if arrivals != 0 {
		t.Fatalf("%d arrivals before the resend", arrivals)
	}
	mu.Unlock()

	clk.add(2 * time.Minute) // past the first backoff (1 min + jitter); the relay's bucket refills
	ob.Wake()
	waitUntil(t, "delivered", func() bool { return state() == StateDelivered })
	time.Sleep(200 * time.Millisecond) // room for a stray duplicate
	mu.Lock()
	defer mu.Unlock()
	if arrivals != 1 {
		t.Fatalf("the mail arrived %d times, want once", arrivals)
	}
	var inbox int
	if err := rst.DB().QueryRow(`SELECT COUNT(*) FROM mail_inbox`).Scan(&inbox); err != nil || inbox != 1 {
		t.Fatalf("inbox rows %d, %v", inbox, err)
	}
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
