package presence

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

type party struct {
	priv ed25519.PrivateKey
	key  string
	mbox *ecdh.PrivateKey
}

func seedBytes(b byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = b + byte(i)
	}
	return out
}

func newParty(t *testing.T, idSeed, mboxSeed byte) party {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(seedBytes(idSeed))
	mb, err := ecdh.X25519().NewPrivateKey(seedBytes(mboxSeed))
	if err != nil {
		t.Fatal(err)
	}
	return party{priv: priv, key: envelope.KeyString(priv.Public().(ed25519.PublicKey)), mbox: mb}
}

type fakePeers map[string]bool

func (p fakePeers) IsPaired(k string) bool { return p[k] }

type fakeKeys map[mail.KeyID]*ecdh.PrivateKey

func (k fakeKeys) MailboxKey(id mail.KeyID) (*ecdh.PrivateKey, bool) {
	v, ok := k[id]
	return v, ok
}

type fixture struct {
	t      *testing.T
	sender party
	recip  party
	rcv    *Receiver
	clock  time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	sender := newParty(t, 0x00, 0x40)
	recip := newParty(t, 0x20, 0x60)
	kid := mail.KeyIDOf(recip.mbox.PublicKey().Bytes())
	f := &fixture{t: t, sender: sender, recip: recip, clock: testNow}
	o := &mail.Opener{
		Self:  recip.key,
		Peers: fakePeers{sender.key: true},
		Keys:  fakeKeys{kid: recip.mbox},
		Now:   func() time.Time { return f.clock },
	}
	path := filepath.Join(testutil.TempDir(t), "p.db")
	st, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f.rcv = &Receiver{Opener: o, Store: NewStore(st.DB()), Now: func() time.Time { return f.clock }}
	return f
}

func (f *fixture) seal(body Body, created time.Time) mail.Sealed {
	f.t.Helper()
	sl, err := Seal(SealInput{Priv: f.sender.priv, To: f.recip.key, MailboxPub: f.recip.mbox.PublicKey().Bytes(), Created: created, Body: body})
	if err != nil {
		f.t.Fatal(err)
	}
	return sl
}

func (f *fixture) env(sl mail.Sealed) envelope.Envelope {
	return envelope.Envelope{From: f.sender.key, To: f.recip.key, Type: "presence", ID: sl.ID, TS: f.clock.UTC().Format(time.RFC3339), Payload: sl.Payload}
}

func onlineBody(boot string, seq int64) Body {
	return Body{State: "online", Agent: 1, Human: 1, Boot: boot, Seq: seq, Interval: 30, Epochs: map[string]int64{}}
}

func TestReceiverAcceptsFirstMessage(t *testing.T) {
	f := newFixture(t)
	sl := f.seal(onlineBody("0123456789abcdef", 1), f.clock)
	accepted, edge, reason, err := f.rcv.Handle(context.Background(), f.env(sl))
	if err != nil || !accepted || !edge || reason != "" {
		t.Fatalf("accepted=%v edge=%v reason=%q err=%v", accepted, edge, reason, err)
	}
}

func TestReceiverReplaySameBootSeqDropped(t *testing.T) {
	f := newFixture(t)
	first := f.seal(onlineBody("0123456789abcdef", 5), f.clock)
	if accepted, _, _, err := f.rcv.Handle(context.Background(), f.env(first)); err != nil || !accepted {
		t.Fatalf("first message: accepted=%v err=%v", accepted, err)
	}
	replay := f.seal(onlineBody("0123456789abcdef", 5), f.clock)
	accepted, _, reason, err := f.rcv.Handle(context.Background(), f.env(replay))
	if err != nil || accepted || reason != ReasonReplay {
		t.Fatalf("replay: accepted=%v reason=%q err=%v", accepted, reason, err)
	}
	lower := f.seal(onlineBody("0123456789abcdef", 4), f.clock)
	accepted, _, reason, err = f.rcv.Handle(context.Background(), f.env(lower))
	if err != nil || accepted || reason != ReasonReplay {
		t.Fatalf("lower seq: accepted=%v reason=%q err=%v", accepted, reason, err)
	}
}

func TestReceiverOlderBootOlderCreatedDropped(t *testing.T) {
	f := newFixture(t)
	first := f.seal(onlineBody("0123456789abcdef", 1), f.clock)
	if accepted, _, _, err := f.rcv.Handle(context.Background(), f.env(first)); err != nil || !accepted {
		t.Fatalf("first: accepted=%v err=%v", accepted, err)
	}
	olderBoot := f.seal(onlineBody("fedcba9876543210", 1), f.clock.Add(-time.Minute))
	accepted, _, reason, err := f.rcv.Handle(context.Background(), f.env(olderBoot))
	if err != nil || accepted || reason != ReasonReplay {
		t.Fatalf("older boot, older created: accepted=%v reason=%q err=%v", accepted, reason, err)
	}
}

func TestReceiverNewBootAccepted(t *testing.T) {
	f := newFixture(t)
	first := f.seal(onlineBody("0123456789abcdef", 5), f.clock)
	if accepted, _, _, err := f.rcv.Handle(context.Background(), f.env(first)); err != nil || !accepted {
		t.Fatalf("first: accepted=%v err=%v", accepted, err)
	}
	newBoot := f.seal(onlineBody("fedcba9876543210", 1), f.clock.Add(time.Second))
	accepted, _, reason, err := f.rcv.Handle(context.Background(), f.env(newBoot))
	if err != nil || !accepted || reason != "" {
		t.Fatalf("new boot: accepted=%v reason=%q err=%v", accepted, reason, err)
	}
}

func TestReceiverCreatedWindow(t *testing.T) {
	f := newFixture(t)
	old := f.seal(onlineBody("0123456789abcdef", 1), f.clock.Add(-11*time.Minute))
	accepted, _, reason, err := f.rcv.Handle(context.Background(), f.env(old))
	if err != nil || accepted || reason != ReasonStale {
		t.Fatalf("11 min old: accepted=%v reason=%q err=%v", accepted, reason, err)
	}

	future := f.seal(onlineBody("0123456789abcdef", 1), f.clock.Add(11*time.Minute))
	accepted, _, reason, err = f.rcv.Handle(context.Background(), f.env(future))
	if err != nil || accepted || reason != ReasonStale {
		t.Fatalf("11 min future: accepted=%v reason=%q err=%v", accepted, reason, err)
	}

	edgeOld := f.seal(onlineBody("0123456789abcdef", 2), f.clock.Add(-10*time.Minute))
	accepted, _, reason, err = f.rcv.Handle(context.Background(), f.env(edgeOld))
	if err != nil || !accepted || reason != "" {
		t.Fatalf("10 min old (edge, accepted): accepted=%v reason=%q err=%v", accepted, reason, err)
	}
}

// TestReceiverOfflineEdgeNotOnline checks that a goodbye is not treated as an
// online edge, and that a subsequent online message after a goodbye is.
func TestReceiverOnlineEdgeAfterGoodbye(t *testing.T) {
	f := newFixture(t)
	hello := f.seal(onlineBody("0123456789abcdef", 1), f.clock)
	if accepted, edge, _, err := f.rcv.Handle(context.Background(), f.env(hello)); err != nil || !accepted || !edge {
		t.Fatalf("hello: accepted=%v edge=%v err=%v", accepted, edge, err)
	}
	goodbye := f.seal(Body{State: "offline", Agent: 0, Human: 2, Boot: "0123456789abcdef", Seq: 2, Interval: 30, Epochs: map[string]int64{}}, f.clock)
	if accepted, edge, _, err := f.rcv.Handle(context.Background(), f.env(goodbye)); err != nil || !accepted || edge {
		t.Fatalf("goodbye: accepted=%v edge=%v err=%v", accepted, edge, err)
	}
	helloAgain := f.seal(onlineBody("0123456789abcdef", 3), f.clock)
	if accepted, edge, _, err := f.rcv.Handle(context.Background(), f.env(helloAgain)); err != nil || !accepted || !edge {
		t.Fatalf("hello again: accepted=%v edge=%v err=%v", accepted, edge, err)
	}
}

func TestReceiverWrongKindRejected(t *testing.T) {
	f := newFixture(t)
	sl, err := mail.SealPresence(mail.SealInput{
		Priv: f.sender.priv, To: f.recip.key, MailboxPub: f.recip.mbox.PublicKey().Bytes(),
		Kind: "presence", Body: map[string]any{"state": "online"}, Created: f.clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Valid presence envelope/id/signature, but a body that is not the strict
	// presence shape: Parse must reject it.
	accepted, _, reason, err := f.rcv.Handle(context.Background(), f.env(sl))
	if err != nil || accepted || reason != ReasonBadBody {
		t.Fatalf("accepted=%v reason=%q err=%v", accepted, reason, err)
	}
}
