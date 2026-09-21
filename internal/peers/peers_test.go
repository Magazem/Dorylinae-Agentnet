package peers_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// fakeSender records frames; err makes every send fail.
type fakeSender struct {
	mu   sync.Mutex
	sent []envelope.Control
	err  error
}

func (f *fakeSender) SendControl(_ context.Context, c envelope.Control) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, c)
	return nil
}

func (f *fakeSender) last(t *testing.T) envelope.Control {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatal("nothing was sent")
	}
	return f.sent[len(f.sent)-1]
}

type env struct {
	m      *peers.Manager
	send   *fakeSender
	audit  *audit.Log
	store  *peers.Store
	ownRaw json.RawMessage
}

func newEnv(t *testing.T, wait time.Duration) *env {
	t.Helper()
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "dn") // not t.TempDir: Windows may hold SQLite WAL files briefly after Close
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	st, err := store.Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := &env{send: &fakeSender{}, audit: audit.New(st.DB()), store: peers.NewStore(st.DB())}
	own, _ := signedCard(t, "me")
	e.ownRaw = own
	e.m = peers.NewManager(peers.Config{Store: e.store, Audit: e.audit, Card: own, Sender: e.send, Wait: wait})
	t.Cleanup(e.m.Close)
	return e
}

// signedCard returns a valid signed card envelope and its public key string.
func signedCard(t *testing.T, name string) (json.RawMessage, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c, err := agentcard.New(pub, name, "h", []agentcard.Skill{{ID: "s1", Name: "Skill", Description: "d"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s, err := agentcard.Sign(priv, c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return raw, s.Card.PublicKey
}

func (e *env) actions(t *testing.T) string {
	t.Helper()
	evs, err := e.audit.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var a []string
	for _, ev := range evs {
		a = append(a, ev.Action)
	}
	return strings.Join(a, ",")
}

func (e *env) peerList(t *testing.T) []peers.Peer {
	t.Helper()
	l, err := e.m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestRedeemVerifiesCardBeforeStoring(t *testing.T) {
	good, goodKey := signedCard(t, "good")
	tampered := strings.Replace(string(good), `"name":"good"`, `"name":"evil"`, 1)
	other, _ := signedCard(t, "other")

	cases := []struct {
		name    string
		card    json.RawMessage
		relayer string // key the relay claims authenticated the sender
		want    string // failure code, or "" for success
	}{
		{"valid", good, goodKey, ""},
		{"tampered card", json.RawMessage(tampered), goodKey, peers.FailBadCard},
		{"key differs from authenticated key", good, "some-other-key", peers.FailBadCard},
		{"valid card of another key", other, goodKey, peers.FailBadCard},
		{"not an envelope", json.RawMessage(`{"hello":"world"}`), goodKey, peers.FailBadCard},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, 50*time.Millisecond)
			st, err := e.m.Redeem(context.Background(), "abcde-fghjk")
			if err != nil || st.State != peers.StatePending {
				t.Fatalf("Redeem = %+v, %v", st, err)
			}
			if got := e.send.last(t); got.Op != envelope.OpPairRedeem || got.Code != "ABCDEFGHJK" || got.Ref != st.ID {
				t.Fatalf("sent %+v", got)
			}
			e.m.HandleControl(envelope.Control{Op: envelope.OpPairPeer, PublicKey: tc.relayer, Card: tc.card, Ref: st.ID})
			got, ok := e.m.Get(st.ID)
			if !ok {
				t.Fatal("pairing vanished")
			}
			if tc.want == "" {
				if got.State != peers.StateComplete || got.Peer == nil || got.Peer.Name != "good" || len(e.peerList(t)) != 1 {
					t.Fatalf("status = %+v", got)
				}
				if e.actions(t) != "pair.start,pair.complete" {
					t.Errorf("audit = %s", e.actions(t))
				}
				return
			}
			if got.State != peers.StateFailed || got.Error == nil || got.Error.Code != tc.want {
				t.Fatalf("status = %+v", got)
			}
			if n := len(e.peerList(t)); n != 0 {
				t.Fatalf("%d peers stored after a bad card", n)
			}
			if e.actions(t) != "pair.start,pair.fail" {
				t.Errorf("audit = %s", e.actions(t))
			}
		})
	}
}

func TestPendingWhenRelayStaysSilent(t *testing.T) {
	e := newEnv(t, 50*time.Millisecond)
	start := time.Now()
	st, err := e.m.Start(context.Background())
	if err != nil || st.State != peers.StatePending || st.Code != "" || st.ID == "" {
		t.Fatalf("Start = %+v, %v", st, err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("Start took %v", time.Since(start))
	}
	// The code arrives later and can be polled.
	e.m.HandleControl(envelope.Control{Op: envelope.OpPairCode, Code: "ABCDE-FGHJK", Expires: time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339), Ref: st.ID})
	got, _ := e.m.Get(st.ID)
	if got.State != peers.StatePending || got.Code != "ABCDE-FGHJK" || got.Expires == "" {
		t.Fatalf("polled = %+v", got)
	}
	// A code frame for an unknown ref, or twice, changes nothing.
	e.m.HandleControl(envelope.Control{Op: envelope.OpPairCode, Code: "ZZZZZ-ZZZZZ", Ref: st.ID})
	e.m.HandleControl(envelope.Control{Op: envelope.OpPairCode, Code: "ZZZZZ-ZZZZZ", Ref: "nope"})
	if got2, _ := e.m.Get(st.ID); got2.Code != "ABCDE-FGHJK" {
		t.Fatalf("code replaced: %+v", got2)
	}
}

func TestRelayErrorFailsPairing(t *testing.T) {
	e := newEnv(t, 50*time.Millisecond)
	st, err := e.m.Redeem(context.Background(), "ABCDEFGHJK")
	if err != nil {
		t.Fatal(err)
	}
	e.m.HandleError(envelope.ErrorFrame{Code: envelope.CodePeerOffline, Message: "gone", Ref: "an-envelope-id"}) // unrelated
	if got, _ := e.m.Get(st.ID); got.State != peers.StatePending {
		t.Fatalf("unrelated error changed state: %+v", got)
	}
	e.m.HandleError(envelope.ErrorFrame{Code: envelope.CodePairInvalid, Message: "invalid", Ref: st.ID})
	got, _ := e.m.Get(st.ID)
	if got.State != peers.StateFailed || got.Error == nil || got.Error.Code != envelope.CodePairInvalid {
		t.Fatalf("status = %+v", got)
	}
	// A late peer frame cannot resurrect a failed pairing.
	card, key := signedCard(t, "late")
	e.m.HandleControl(envelope.Control{Op: envelope.OpPairPeer, PublicKey: key, Card: card, Ref: st.ID})
	if n := len(e.peerList(t)); n != 0 {
		t.Fatalf("peer stored after failure")
	}
	if e.actions(t) != "pair.start,pair.fail" {
		t.Errorf("audit = %s", e.actions(t))
	}
}

func TestRedeemReturnsFinalStatusWithinWait(t *testing.T) {
	e := newEnv(t, 2*time.Second)
	card, key := signedCard(t, "quick")
	go func() {
		for i := 0; i < 200; i++ {
			e.send.mu.Lock()
			n := len(e.send.sent)
			var ref string
			if n > 0 {
				ref = e.send.sent[0].Ref
			}
			e.send.mu.Unlock()
			if n > 0 {
				e.m.HandleControl(envelope.Control{Op: envelope.OpPairPeer, PublicKey: key, Card: card, Ref: ref})
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	start := time.Now()
	st, err := e.m.Redeem(context.Background(), "ABCDEFGHJK")
	if err != nil || st.State != peers.StateComplete {
		t.Fatalf("Redeem = %+v, %v", st, err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("did not return as soon as the exchange finished: %v", time.Since(start))
	}
}

func TestSetupErrors(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, 50*time.Millisecond)

	if _, err := e.m.Redeem(ctx, "short"); !errors.Is(err, peers.ErrBadCode) {
		t.Errorf("bad code err = %v", err)
	}
	if e.actions(t) != "" {
		t.Errorf("malformed code was audited: %s", e.actions(t))
	}

	e.send.err = relayclient.ErrNotConnected
	if _, err := e.m.Start(ctx); !errors.Is(err, relayclient.ErrNotConnected) {
		t.Errorf("not connected err = %v", err)
	}
	if e.actions(t) != "pair.start,pair.fail" {
		t.Errorf("audit = %s", e.actions(t))
	}

	noRelay := peers.NewManager(peers.Config{Store: e.store, Audit: e.audit, Card: e.ownRaw})
	if _, err := noRelay.Start(ctx); !errors.Is(err, peers.ErrNoRelay) {
		t.Errorf("no relay err = %v", err)
	}
	if _, ok := e.m.Get("pair-nope"); ok {
		t.Error("unknown id found")
	}
}

func TestTooManyPending(t *testing.T) {
	e := newEnv(t, 10*time.Millisecond)
	var err error
	for i := 0; i < 100 && err == nil; i++ {
		_, err = e.m.Start(context.Background())
	}
	if !errors.Is(err, peers.ErrTooMany) {
		t.Fatalf("err = %v, want ErrTooMany", err)
	}
}

func TestStoreRepairKeepsFirstPairedAt(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, time.Millisecond)
	raw, _ := signedCard(t, "first")
	sc, err := agentcard.Verify(raw)
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := e.store.Add(ctx, sc, raw, t1); err != nil {
		t.Fatal(err)
	}
	sc.Card.Name = "renamed"
	if err := e.store.Add(ctx, sc, raw, t1.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	l := e.peerList(t)
	if len(l) != 1 || l[0].Name != "renamed" || l[0].PairedAt != "2026-01-02T03:04:05Z" || len(l[0].Skills) != 1 {
		t.Fatalf("peers = %+v", l)
	}
}
