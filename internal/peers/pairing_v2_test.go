package peers_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
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
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// ident is a test identity with a signed card and a signed mailbox announcement.
type ident struct {
	priv ed25519.PrivateKey
	key  string
	card json.RawMessage
	mbox []byte
}

func newIdent(t *testing.T, name string) *ident {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c, err := agentcard.New(pub, name, "h", []agentcard.Skill{{ID: "s1", Name: "Skill", Description: "d"}}, time.Now())
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
	mbox, err := mail.SignAnnouncement(pub, func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil }, x.PublicKey().Bytes(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return &ident{priv: priv, key: sc.Card.PublicKey, card: card, mbox: mbox}
}

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// node is one daemon's pairing manager with its own database.
type node struct {
	t      *testing.T
	id     *ident
	m      *peers.Manager
	store  *peers.Store
	db     *store.Store
	audit  *audit.Log
	dbPath string
	logs   *syncBuf
	cfg    func(*peers.Config)
	sender peers.Sender
}

func newNode(t *testing.T, name string, cfg func(*peers.Config)) *node {
	t.Helper()
	dir, err := os.MkdirTemp("", "dn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	n := &node{t: t, id: newIdent(t, name), dbPath: filepath.Join(dir, "t.db"), logs: &syncBuf{}, cfg: cfg}
	n.open()
	t.Cleanup(func() { n.m.Close(); _ = n.db.Close() })
	return n
}

// open (re)opens the database and creates a fresh manager over it, like a daemon restart.
func (n *node) open() {
	n.t.Helper()
	db, err := store.Open(context.Background(), n.dbPath)
	if err != nil {
		n.t.Fatal(err)
	}
	n.db, n.store, n.audit = db, peers.NewStore(db.DB()), audit.New(db.DB())
	// The issuer's ConfirmWait starts at pair_peer, when the redeemer's Argon2id
	// (64 MiB, t=3) may still be running, so it must cover that derivation: about
	// 0.2 s idle, but over 1.5 s on a loaded CI runner (Docs/review/33-pairtag-flake.md).
	// Tests that expect a confirm timeout set a short ConfirmWait on the redeemer.
	cfg := peers.Config{
		Store: n.store, Audit: n.audit, Card: n.id.card, Self: n.id.key,
		Mailbox: func() ([]byte, error) { return n.id.mbox, nil },
		Sender:  n.sender, Wait: 3 * time.Second, ConfirmWait: 10 * time.Second,
		Logger: slog.New(slog.NewTextHandler(n.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if n.cfg != nil {
		n.cfg(&cfg)
	}
	n.m = peers.NewManager(cfg)
}

func (n *node) restart() {
	n.t.Helper()
	n.m.Close()
	_ = n.db.Close()
	n.open()
}

func (n *node) setSender(s peers.Sender) {
	n.sender = s
	n.m.SetSender(s)
}

func (n *node) list() []peers.Peer {
	n.t.Helper()
	l, err := n.m.List(context.Background())
	if err != nil {
		n.t.Fatal(err)
	}
	return l
}

func (n *node) actions() []string {
	n.t.Helper()
	evs, err := n.audit.List(context.Background())
	if err != nil {
		n.t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		out = append(out, e.Action+" "+string(e.Detail))
	}
	return out
}

func (n *node) countAction(action string) int {
	c := 0
	for _, a := range n.actions() {
		if strings.HasPrefix(a, action+" ") {
			c++
		}
	}
	return c
}

func (n *node) mailboxKeys(key string) []json.RawMessage {
	n.t.Helper()
	var raw string
	if err := n.db.DB().QueryRow(`SELECT mailbox_keys FROM peers WHERE public_key = ?`, key).Scan(&raw); err != nil {
		n.t.Fatal(err)
	}
	var out []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		n.t.Fatal(err)
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitState waits for pairing id to reach state. A pairing that ended in the
// other state never changes again, so that fails at once, with its error.
func waitState(t *testing.T, n *node, id, state string) peers.Status {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		st, _ := n.m.Get(id)
		switch {
		case st.State == state:
			return st
		case st.State == peers.StateComplete || st.State == peers.StateFailed:
			t.Fatalf("pairing %s ended %s, want %s (error: %+v)", id, st.State, state, st.Error)
		case time.Now().After(deadline):
			t.Fatalf("timed out waiting for pairing state %s (state %q)", state, st.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func bareCode(formatted string) string { return strings.ReplaceAll(formatted, "-", "") }

// bus is an in-memory relay. With mitm set it puts that identity between the two
// sides: both get the mitm card and key as their peer, and envelopes to it are
// forwarded to the other side as if sent by it.
type bus struct {
	t       *testing.T
	mu      sync.Mutex
	nodes   map[string]*node
	entries map[string]*busEntry
	mitm    *ident
	frames  [][]byte // every frame a daemon sent, as JSON
	envs    []envelope.Envelope
	redeems int
}

type busEntry struct {
	issuer   *node
	ref      string
	card, mb json.RawMessage
}

type busSender struct {
	b    *bus
	self *node
}

func newBus(t *testing.T, mitm *ident, nodes ...*node) *bus {
	b := &bus{t: t, nodes: map[string]*node{}, entries: map[string]*busEntry{}, mitm: mitm}
	for _, n := range nodes {
		b.attach(n)
	}
	return b
}

func (b *bus) attach(n *node) {
	b.mu.Lock()
	b.nodes[n.id.key] = n
	b.mu.Unlock()
	n.setSender(busSender{b: b, self: n})
}

func (b *bus) lookupNode(key string) *node {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nodes[key]
}

func (s busSender) SendControl(_ context.Context, c envelope.Control) error {
	b := s.b
	raw, _ := json.Marshal(c)
	b.mu.Lock()
	b.frames = append(b.frames, raw)
	switch c.Op {
	case envelope.OpPairNew:
		b.entries[c.Lookup] = &busEntry{issuer: s.self, ref: c.Ref, card: c.Card, mb: c.Mbox}
		b.mu.Unlock()
		go s.self.m.HandleControl(envelope.Control{Op: envelope.OpPairCode, Expires: time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339), Ref: c.Ref})
		return nil
	case envelope.OpPairRedeem:
		b.redeems++
		e := b.entries[c.Lookup]
		b.mu.Unlock()
		if e == nil {
			go s.self.m.HandleError(envelope.ErrorFrame{Code: envelope.CodePairInvalid, Message: "invalid", Ref: c.Ref})
			return nil
		}
		toIssuer := envelope.Control{Op: envelope.OpPairPeer, PublicKey: s.self.id.key, Card: c.Card, Mbox: c.Mbox, Ref: e.ref}
		toRedeemer := envelope.Control{Op: envelope.OpPairPeer, PublicKey: e.issuer.id.key, Card: e.card, Mbox: e.mb, Ref: c.Ref}
		if b.mitm != nil {
			toIssuer = envelope.Control{Op: envelope.OpPairPeer, PublicKey: b.mitm.key, Card: b.mitm.card, Mbox: b.mitm.mbox, Ref: e.ref}
			toRedeemer = envelope.Control{Op: envelope.OpPairPeer, PublicKey: b.mitm.key, Card: b.mitm.card, Mbox: b.mitm.mbox, Ref: c.Ref}
		}
		go e.issuer.m.HandleControl(toIssuer)
		go s.self.m.HandleControl(toRedeemer)
		return nil
	case envelope.OpPairCancel:
		delete(b.entries, c.Lookup)
	}
	b.mu.Unlock()
	return nil
}

func (s busSender) Send(_ context.Context, e envelope.Envelope) error {
	b := s.b
	raw, err := e.Marshal()
	if err != nil {
		return err
	}
	parsed, err := envelope.Parse(raw)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.frames = append(b.frames, raw)
	b.envs = append(b.envs, parsed)
	b.mu.Unlock()
	if b.mitm != nil && parsed.To == b.mitm.key {
		// The attacker forwards what it got to the honest issuer, as itself.
		for _, n := range b.nodesExcept(parsed.From) {
			fwd := parsed
			fwd.From, fwd.To = b.mitm.key, n.id.key
			go n.m.HandleEnvelope(fwd)
		}
		return nil
	}
	if n := b.lookupNode(parsed.To); n != nil {
		go n.m.HandleEnvelope(parsed)
	}
	return nil
}

func (b *bus) nodesExcept(key string) []*node {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []*node
	for k, n := range b.nodes {
		if k != key {
			out = append(out, n)
		}
	}
	return out
}

func (b *bus) envsFrom(key string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := 0
	for _, e := range b.envs {
		if e.From == key {
			c++
		}
	}
	return c
}

func (b *bus) redeemFrames() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.redeems
}

func startIssuer(t *testing.T, n *node) (id, code string) {
	t.Helper()
	st, err := n.m.Start(context.Background())
	if err != nil || st.Code == "" {
		t.Fatalf("Start = %+v, %v", st, err)
	}
	return st.ID, bareCode(st.Code)
}

// wrongSecret changes the last character of code.
func wrongSecret(code string) string {
	last := byte('0')
	if code[len(code)-1] == '0' {
		last = '1'
	}
	return code[:len(code)-1] + string(last)
}

// recSender wraps a sender and keeps every frame it sends.
type recSender struct {
	peers.Sender
	mu     sync.Mutex
	frames [][]byte
}

func (r *recSender) SendControl(ctx context.Context, c envelope.Control) error {
	raw, _ := json.Marshal(c)
	r.mu.Lock()
	r.frames = append(r.frames, raw)
	r.mu.Unlock()
	return r.Sender.SendControl(ctx, c)
}

func (r *recSender) Send(ctx context.Context, e envelope.Envelope) error {
	if raw, err := e.Marshal(); err == nil {
		r.mu.Lock()
		r.frames = append(r.frames, raw)
		r.mu.Unlock()
	}
	return r.Sender.Send(ctx, e)
}

func (r *recSender) all() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return bytes.Join(r.frames, []byte("\n"))
}

func TestV2PairsThroughRealRelay(t *testing.T) {
	ts := httptest.NewServer(relay.New(relay.Options{}))
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	issuer, redeemer := newNode(t, "issuer", nil), newNode(t, "redeemer", nil)
	var recs []*recSender
	for _, n := range []*node{issuer, redeemer} {
		n := n
		c, err := relayclient.New(relayclient.Config{
			URL:        "ws" + strings.TrimPrefix(ts.URL, "http"),
			Signer:     relayclient.NewKeySigner(n.id.priv),
			OnControl:  func(c envelope.Control) { n.m.HandleControl(c) },
			OnEnvelope: func(e envelope.Envelope) { n.m.HandleEnvelope(e) },
			OnError:    func(e envelope.ErrorFrame) { n.m.HandleError(e) },
		})
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = c.Run(ctx) }()
		waitFor(t, "relay connection", c.Connected)
		rec := &recSender{Sender: c}
		recs = append(recs, rec)
		n.setSender(rec)
	}

	id, code := startIssuer(t, issuer)
	if len(code) != 15 {
		t.Fatalf("code %q is not 15 characters", code)
	}
	rst, err := redeemer.m.Redeem(ctx, code, false)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, redeemer, rst.ID, peers.StateComplete)
	waitState(t, issuer, id, peers.StateComplete)

	for _, c := range []struct {
		name        string
		self, other *node
	}{{"issuer", issuer, redeemer}, {"redeemer", redeemer, issuer}} {
		l := c.self.list()
		if len(l) != 1 || l[0].PublicKey != c.other.id.key || l[0].Trust != peers.TrustCode {
			t.Fatalf("%s peers = %+v, want %s with trust=code", c.name, l, c.other.id.key)
		}
		keys := c.self.mailboxKeys(c.other.id.key)
		if len(keys) != 1 || string(keys[0]) != string(c.other.id.mbox) {
			t.Fatalf("%s stored mailbox_keys = %s, want the peer's announcement %s", c.name, keys, c.other.id.mbox)
		}
	}
	if st, _ := issuer.m.Get(id); st.Peer == nil || st.Peer.Trust != peers.TrustCode || st.Peer.Fingerprint == "" || st.Code != "" {
		t.Errorf("issuer status = %+v", st)
	}

	// The secret half never appears in what either daemon sent, in its logs or its audit rows.
	secret := code[5:]
	for i, rec := range recs {
		if bytes.Contains(rec.all(), []byte(secret)) || bytes.Contains(rec.all(), []byte(code)) {
			t.Errorf("daemon %d sent the secret to the relay", i)
		}
		if !bytes.Contains(rec.all(), []byte(code[:5])) {
			t.Errorf("daemon %d never sent the lookup (test does not see the frames)", i)
		}
	}
	for _, n := range []*node{issuer, redeemer} {
		blob := n.logs.String() + strings.Join(n.actions(), "\n")
		if strings.Contains(blob, secret) || strings.Contains(blob, code[:5]) {
			t.Errorf("secret or lookup found in logs or audit: %s", blob)
		}
	}
	if got := issuer.countAction(peers.ActionPairComplete); got != 1 {
		t.Errorf("issuer audit pair.complete = %d", got)
	}

	// The code is single use: the relay entry is gone, and this daemon refuses it locally.
	if again, err := redeemer.m.Redeem(ctx, code, false); err != nil || again.State != peers.StateFailed || again.Error.Code != peers.FailCodeUsed {
		t.Errorf("second redeem = %+v, %v", again, err)
	}
}

func TestV2RelayMITMIsDetected(t *testing.T) {
	m := newIdent(t, "mallory")
	issuer := newNode(t, "issuer", nil)
	redeemer := newNode(t, "redeemer", func(c *peers.Config) { c.ConfirmWait = 700 * time.Millisecond })
	b := newBus(t, m, issuer, redeemer)

	id, code := startIssuer(t, issuer)
	rst, err := redeemer.m.Redeem(context.Background(), code, false)
	if err != nil {
		t.Fatal(err)
	}
	final := waitState(t, redeemer, rst.ID, peers.StateFailed)
	if final.Error == nil || final.Error.Code != peers.FailConfirmTimeout {
		t.Fatalf("redeemer status = %+v, want confirm_timeout", final)
	}
	// The issuer got a tag over a different transcript and refuses it.
	waitFor(t, "issuer attempt_fail", func() bool { return issuer.countAction(peers.ActionPairAttemptFail) == 1 })
	if a := strings.Join(issuer.actions(), "\n"); !strings.Contains(a, `"code":"bad_confirm"`) {
		t.Errorf("issuer audit = %s, want bad_confirm", a)
	}
	if st, _ := issuer.m.Get(id); st.State != peers.StatePending {
		t.Errorf("issuer state = %s, want pending (one failed attempt of three)", st.State)
	}
	for _, n := range []*node{issuer, redeemer} {
		if l := n.list(); len(l) != 0 {
			t.Errorf("%s stored %+v after a MITM", n.id.key, l)
		}
	}
	if got := b.envsFrom(issuer.id.key); got != 0 {
		t.Errorf("the issuer sent %d confirmations after a bad tag", got)
	}
}

func TestV2WrongSecretRightLookupFails(t *testing.T) {
	issuer, redeemer := newNode(t, "issuer", nil), newNode(t, "redeemer", func(c *peers.Config) { c.ConfirmWait = 700 * time.Millisecond })
	b := newBus(t, nil, issuer, redeemer)
	_, code := startIssuer(t, issuer)
	rst, err := redeemer.m.Redeem(context.Background(), wrongSecret(code), false)
	if err != nil {
		t.Fatal(err)
	}
	final := waitState(t, redeemer, rst.ID, peers.StateFailed)
	if final.Error.Code != peers.FailConfirmTimeout {
		t.Fatalf("redeemer error = %+v", final.Error)
	}
	waitFor(t, "issuer attempt_fail", func() bool { return issuer.countAction(peers.ActionPairAttemptFail) == 1 })
	if len(issuer.list())+len(redeemer.list()) != 0 {
		t.Fatal("something was stored after a wrong secret")
	}
	if got := b.envsFrom(issuer.id.key); got != 0 {
		t.Errorf("issuer sent %d tags to a peer that had the wrong secret", got)
	}
}

func TestV2CodeUsedTwiceSurvivesRestart(t *testing.T) {
	issuer, redeemer := newNode(t, "issuer", nil), newNode(t, "redeemer", func(c *peers.Config) { c.ConfirmWait = 400 * time.Millisecond })
	b := newBus(t, nil, issuer, redeemer)
	_, code := startIssuer(t, issuer)
	bad := wrongSecret(code)

	first, err := redeemer.m.Redeem(context.Background(), bad, false)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, redeemer, first.ID, peers.StateFailed) // a tag was sent, so the code is used up
	frames := b.redeemFrames()

	check := func(when string) {
		t.Helper()
		st, err := redeemer.m.Redeem(context.Background(), bad, false)
		if err != nil {
			t.Fatalf("%s: %v", when, err)
		}
		if st.State != peers.StateFailed || st.Error == nil || st.Error.Code != peers.FailCodeUsed {
			t.Fatalf("%s: status = %+v, want code_used", when, st)
		}
		if b.redeemFrames() != frames {
			t.Fatalf("%s: a redeem frame was sent for a used code", when)
		}
	}
	check("same daemon")
	redeemer.restart()
	b.attach(redeemer)
	check("after restart")
	// Formatting and case do not matter.
	if st, _ := redeemer.m.Redeem(context.Background(), strings.ToLower(peers.FormatCode(bad)), false); st.Error == nil || st.Error.Code != peers.FailCodeUsed {
		t.Errorf("formatted code status = %+v", st)
	}
}

func TestV2UsedCodesArePrunedAfter24Hours(t *testing.T) {
	e := newEnv(t, time.Millisecond)
	ctx := context.Background()
	h := bytes.Repeat([]byte{9}, 32)
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if fresh, err := e.store.MarkCodeUsed(ctx, h, now); err != nil || !fresh {
		t.Fatalf("first mark = %v, %v", fresh, err)
	}
	if fresh, _ := e.store.MarkCodeUsed(ctx, h, now.Add(time.Hour)); fresh {
		t.Fatal("second mark was fresh")
	}
	if used, _ := e.store.CodeUsed(ctx, h, now.Add(23*time.Hour)); !used {
		t.Fatal("code not used after 23 h")
	}
	if used, _ := e.store.CodeUsed(ctx, h, now.Add(25*time.Hour)); used {
		t.Fatal("code still used after 25 h")
	}
	var n int
	if err := e.db.DB().QueryRow(`SELECT COUNT(*) FROM pair_used_codes`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows left = %d, %v", n, err)
	}
}

// peerFor makes a fake peer and the pair_peer frame the relay would send for it.
func peerFrame(t *testing.T, ref string) (*ident, envelope.Control) {
	t.Helper()
	p := newIdent(t, "peer")
	return p, envelope.Control{Op: envelope.OpPairPeer, PublicKey: p.key, Card: p.card, Mbox: p.mbox, Ref: ref}
}

func confirmEnv(t *testing.T, from *ident, to, lookup string, tag []byte) envelope.Envelope {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"v": 2, "lookup": lookup, "tag": base64.RawURLEncoding.EncodeToString(tag)})
	return envelope.Envelope{From: from.key, To: to, Type: peers.ConfirmType, ID: "pc-" + strings.Repeat("a", 16), TS: time.Now().UTC().Format(time.RFC3339), Payload: payload}
}

func issuerLookup(e *env, t *testing.T) string {
	t.Helper()
	e.send.mu.Lock()
	defer e.send.mu.Unlock()
	for _, c := range e.send.sent {
		if c.Op == envelope.OpPairNew {
			return c.Lookup
		}
	}
	t.Fatal("no pair_new sent")
	return ""
}

func startFake(t *testing.T, e *env) (id, lookup string) {
	t.Helper()
	st, err := e.m.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lookup = issuerLookup(e, t)
	e.m.HandleControl(envelope.Control{Op: envelope.OpPairCode, Expires: time.Now().Add(time.Minute).UTC().Format(time.RFC3339), Ref: st.ID})
	return st.ID, lookup
}

func TestV2ThreeBadTagsAbort(t *testing.T) {
	e := newEnv(t, 50*time.Millisecond)
	id, lookup := startFake(t, e)
	junk := bytes.Repeat([]byte{1}, 32)
	for i := 0; i < 3; i++ {
		p, frame := peerFrame(t, id)
		e.m.HandleControl(frame)
		e.m.HandleEnvelope(confirmEnv(t, p, e.id.key, lookup, junk))
		want := i + 1
		waitFor(t, "attempt_fail", func() bool { return countActions(t, e, peers.ActionPairAttemptFail) == want })
		if st, _ := e.m.Get(id); i < 2 && st.State != peers.StatePending {
			t.Fatalf("pairing ended after %d bad tags: %+v", i+1, st)
		}
	}
	st := waitState2(t, e, id, peers.StateFailed)
	if st.Error.Code != peers.FailBadConfirm || !strings.Contains(st.Error.Message, "too many") {
		t.Fatalf("error = %+v", st.Error)
	}
	waitCancel(t, e, lookup)
	if last := e.send.last(t); last.Op != envelope.OpPairCancel || last.Lookup != lookup {
		t.Errorf("last frame = %+v, want pair_cancel", last)
	}
	e.send.mu.Lock()
	sentEnvs := len(e.send.envs)
	e.send.mu.Unlock()
	if sentEnvs != 0 {
		t.Errorf("issuer sent %d envelopes without a correct tag", sentEnvs)
	}
	if n := len(mustList(t, e)); n != 0 {
		t.Errorf("%d peers stored", n)
	}
	// A pair_peer after the end changes nothing.
	_, frame := peerFrame(t, id)
	e.m.HandleControl(frame)
	if countActions(t, e, peers.ActionPairAttemptFail) != 3 {
		t.Error("a 4th pair_peer was processed")
	}
}

func TestV2ConfirmTimeoutCountsAsFailureAndFourthPeerIsIgnored(t *testing.T) {
	e := newEnvWith(t, 50*time.Millisecond, func(c *peers.Config) { c.ConfirmWait = 150 * time.Millisecond })
	id, _ := startFake(t, e)
	for i := 0; i < 4; i++ { // the 4th is beyond the attempt limit
		_, frame := peerFrame(t, id)
		e.m.HandleControl(frame)
	}
	st := waitState2(t, e, id, peers.StateFailed)
	if st.Error.Code != peers.FailBadConfirm {
		t.Fatalf("error = %+v", st.Error)
	}
	time.Sleep(300 * time.Millisecond)
	if got := countActions(t, e, peers.ActionPairAttemptFail); got != 3 {
		t.Fatalf("attempt_fail rows = %d, want 3", got)
	}
	if !strings.Contains(strings.Join(actionsOf(t, e), "\n"), `"code":"confirm_timeout"`) {
		t.Error("no confirm_timeout attempt failure recorded")
	}
}

func TestV2BadPeerMaterialFailsAttempt(t *testing.T) {
	p, other := newIdent(t, "p"), newIdent(t, "other")
	badMbox := append([]byte(nil), p.mbox...)
	badMbox[len(badMbox)-5] ^= 1
	cases := []struct {
		name string
		mut  func(*envelope.Control)
		want string
	}{
		{"mbox of another identity", func(c *envelope.Control) { c.Mbox = other.mbox }, "bad_mbox"},
		{"tampered mbox", func(c *envelope.Control) { c.Mbox = badMbox }, "bad_mbox"},
		{"no mbox", func(c *envelope.Control) { c.Mbox = nil }, "bad_mbox"},
		{"card of another key", func(c *envelope.Control) { c.Card = other.card }, "bad_card"},
		{"key differs from the card", func(c *envelope.Control) { c.PublicKey = other.key }, "bad_card"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, 50*time.Millisecond)
			id, lookup := startFake(t, e)
			frame := envelope.Control{Op: envelope.OpPairPeer, PublicKey: p.key, Card: p.card, Mbox: p.mbox, Ref: id}
			tc.mut(&frame)
			e.m.HandleControl(frame)
			waitFor(t, "attempt_fail", func() bool { return countActions(t, e, peers.ActionPairAttemptFail) == 1 })
			if a := strings.Join(actionsOf(t, e), "; "); !strings.Contains(a, `"code":"`+tc.want+`"`) {
				t.Errorf("audit = %s, want %s", a, tc.want)
			}
			// A tag from that peer is dropped: the attempt is over.
			e.m.HandleEnvelope(confirmEnv(t, p, e.id.key, lookup, bytes.Repeat([]byte{1}, 32)))
			time.Sleep(50 * time.Millisecond)
			if got := countActions(t, e, peers.ActionPairAttemptFail); got != 1 {
				t.Errorf("attempt_fail rows = %d after a tag for a failed attempt", got)
			}
		})
	}
}

func TestV2RelayV1IsRefused(t *testing.T) {
	e := newEnv(t, 50*time.Millisecond)
	st, err := e.m.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.m.HandleControl(envelope.Control{Op: envelope.OpPairCode, Code: "ABCDE-FGHJK", Ref: st.ID})
	got := waitState2(t, e, st.ID, peers.StateFailed)
	if got.Error.Code != peers.FailRelayV1 || got.Code != "" {
		t.Fatalf("status = %+v", got)
	}
}

func TestV2LookupTakenReissuesNewCodeThreeTimes(t *testing.T) {
	e := newEnv(t, 50*time.Millisecond)
	st, err := e.m.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lookups := []string{issuerLookup(e, t)}
	for i := 0; i < 3; i++ { // pairing.md: a new code at most 3 times, so at most 4 pair_new
		e.m.HandleError(envelope.ErrorFrame{Code: envelope.CodeLookupTaken, Message: "taken", Ref: st.ID})
		want := i + 2
		waitFor(t, "second pair_new", func() bool {
			e.send.mu.Lock()
			defer e.send.mu.Unlock()
			n := 0
			for _, c := range e.send.sent {
				if c.Op == envelope.OpPairNew {
					n++
				}
			}
			return n == want
		})
		e.send.mu.Lock()
		lookups = append(lookups, e.send.sent[len(e.send.sent)-1].Lookup)
		e.send.mu.Unlock()
	}
	if lookups[0] == lookups[1] || lookups[1] == lookups[2] || lookups[2] == lookups[3] {
		t.Errorf("lookups repeated: %v", lookups)
	}
	e.m.HandleError(envelope.ErrorFrame{Code: envelope.CodeLookupTaken, Message: "taken", Ref: st.ID})
	got := waitState2(t, e, st.ID, peers.StateFailed)
	if got.Error.Code != envelope.CodeLookupTaken {
		t.Fatalf("error = %+v", got.Error)
	}
}

func TestV2CodeExpiresLocally(t *testing.T) {
	e := newEnvWith(t, 50*time.Millisecond, func(c *peers.Config) { c.CodeTTL = 200 * time.Millisecond })
	id, lookup := startFake(t, e)
	got := waitState2(t, e, id, peers.StateFailed)
	if got.Error.Code != peers.FailExpired {
		t.Fatalf("error = %+v", got.Error)
	}
	waitCancel(t, e, lookup)
	if last := e.send.last(t); last.Op != envelope.OpPairCancel || last.Lookup != lookup {
		t.Errorf("last frame = %+v", last)
	}
}

func TestStoreMergesMailboxKeysAndRaisesTrust(t *testing.T) {
	e := newEnv(t, time.Millisecond)
	ctx := context.Background()
	p := newIdent(t, "p")
	sc, err := agentcard.Verify(p.card)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ann := func(created time.Time) []byte {
		x, _ := ecdh.X25519().GenerateKey(rand.Reader)
		b, err := mail.SignAnnouncement(ed25519.PublicKey(mustKey(t, p.key)), func(m []byte) ([]byte, error) { return ed25519.Sign(p.priv, m), nil }, x.PublicKey().Bytes(), created)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	a1, a2, a3, old := ann(now.Add(-3*time.Hour)), ann(now.Add(-2*time.Hour)), ann(now.Add(-time.Hour)), ann(now.Add(-5*time.Hour))
	if err := e.store.Add(ctx, sc, p.card, now); err != nil { // v1: trust relay, no mailbox key
		t.Fatal(err)
	}
	if k := keysOf(t, e, p.key); len(k) != 0 {
		t.Fatalf("v1 pairing stored mailbox keys: %v", k)
	}
	steps := []struct {
		ann  []byte
		want [][]byte
	}{
		{a1, [][]byte{a1}},
		{a1, [][]byte{a1}},      // same key_id: idempotent
		{old, [][]byte{a1}},     // older than the newest: no rollback
		{a2, [][]byte{a2, a1}},  // newer goes first
		{a3, [][]byte{a3, a2}},  // at most two are kept
		{a1, [][]byte{a3, a2}},  // stale again
		{nil, [][]byte{a3, a2}}, // no announcement changes nothing
	}
	for i, s := range steps {
		if err := e.store.AddTrusted(ctx, sc, p.card, now, peers.TrustCode, s.ann); err != nil {
			t.Fatal(err)
		}
		got := keysOf(t, e, p.key)
		if len(got) != len(s.want) {
			t.Fatalf("step %d: %d keys, want %d", i, len(got), len(s.want))
		}
		for j := range got {
			if string(got[j]) != string(s.want[j]) {
				t.Fatalf("step %d: key %d differs", i, j)
			}
		}
	}
	if l, _ := e.store.List(ctx); l[0].Trust != peers.TrustCode {
		t.Fatalf("trust = %s, want code (raised from relay)", l[0].Trust)
	}
	if err := e.store.SetTrust(ctx, p.key, peers.TrustFingerprint); err != nil {
		t.Fatal(err)
	}
	if err := e.store.AddTrusted(ctx, sc, p.card, now, peers.TrustCode, nil); err != nil {
		t.Fatal(err)
	}
	if l, _ := e.store.List(ctx); l[0].Trust != peers.TrustFingerprint {
		t.Fatalf("trust = %s, want fingerprint kept", l[0].Trust)
	}
}

func TestV2ErrorsWithoutRelayOrMailbox(t *testing.T) {
	e := newEnvWith(t, time.Millisecond, func(c *peers.Config) { c.Mailbox = func() ([]byte, error) { return nil, errors.New("no key") } })
	if _, err := e.m.Start(context.Background()); err == nil {
		t.Fatal("Start succeeded without a mailbox key")
	}
	if _, err := e.m.Redeem(context.Background(), "7KQ2M9XHF4TRW8N", false); err == nil {
		t.Fatal("Redeem succeeded without a mailbox key")
	}
}

func mustKey(t *testing.T, s string) []byte {
	t.Helper()
	k, err := envelope.ParseKey(s)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func keysOf(t *testing.T, e *env, key string) [][]byte {
	t.Helper()
	var raw string
	if err := e.db.DB().QueryRow(`SELECT mailbox_keys FROM peers WHERE public_key = ?`, key).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var out []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	var res [][]byte
	for _, o := range out {
		res = append(res, o)
	}
	return res
}

func actionsOf(t *testing.T, e *env) []string {
	t.Helper()
	evs, err := e.audit.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Action+" "+string(ev.Detail))
	}
	return out
}

func countActions(t *testing.T, e *env, action string) int {
	t.Helper()
	c := 0
	for _, a := range actionsOf(t, e) {
		if strings.HasPrefix(a, action+" ") {
			c++
		}
	}
	return c
}

func mustList(t *testing.T, e *env) []peers.Peer {
	t.Helper()
	l, err := e.m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func waitState2(t *testing.T, e *env, id, state string) peers.Status {
	t.Helper()
	var st peers.Status
	waitFor(t, "pairing state "+state, func() bool {
		st, _ = e.m.Get(id)
		return st.State == state
	})
	return st
}

// waitCancel waits for the pair_cancel of lookup. The issuer sends it after
// the pairing has ended, so seeing the failed state does not mean it is out.
func waitCancel(t *testing.T, e *env, lookup string) {
	t.Helper()
	waitFor(t, "pair_cancel", func() bool {
		e.send.mu.Lock()
		defer e.send.mu.Unlock()
		for _, c := range e.send.sent {
			if c.Op == envelope.OpPairCancel && c.Lookup == lookup {
				return true
			}
		}
		return false
	})
}
