package team_test

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// testNode is one simulated daemon: its own identity, store, peers and team
// stores, and the mail kinds a real daemon would register.
type testNode struct {
	t     *testing.T
	name  string
	priv  ed25519.PrivateKey
	key   string
	card  []byte // signed card envelope
	mbox  []byte // signed mailbox announcement
	mpriv *ecdh.PrivateKey
	mpub  []byte

	st    *store.Store
	db    *sql.DB
	ps    *peers.Store
	alog  *audit.Log
	ts    *team.Store
	kinds map[string]mail.Kind

	mu  sync.Mutex
	clk time.Time
}

func newTestNode(t *testing.T, name string) *testNode {
	t.Helper()
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c, err := agentcard.New(pub, name, "h", []agentcard.Skill{{ID: "s", Name: "Skill", Description: "d"}}, now)
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
	xk, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mbox, err := mail.SignAnnouncement(pub, func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil }, xk.PublicKey().Bytes(), now)
	if err != nil {
		t.Fatal(err)
	}
	dir := testutil.TempDir(t)
	st, err := store.Open(ctx, filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ps := peers.NewStore(st.DB())
	alog := audit.New(st.DB())
	ps.SetAudit(alog)
	key := envelope.KeyString(pub)
	ts := team.NewStore(st.DB(), ps, key)
	ts.SetAudit(alog)
	ts.OwnCard = func() ([]byte, error) { return card, nil }
	ts.Announcement = func() ([]byte, error) { return mbox, nil }
	ts.Now = func() time.Time { return now }

	n := &testNode{
		t: t, name: name, priv: priv, key: key, card: card, mbox: mbox,
		mpriv: xk, mpub: xk.PublicKey().Bytes(), st: st, db: st.DB(), ps: ps, alog: alog, ts: ts, clk: now,
	}
	n.kinds = map[string]mail.Kind{
		"keys":        mail.KeysKind(peers.MergeMailboxKeysTx, nil),
		"team.roster": ts.RosterKind(),
		"team.join":   ts.JoinKind(),
		"team.leave":  ts.LeaveKind(),
	}
	return n
}

func (n *testNode) now() time.Time {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.clk
}

// pairWith directly pairs n and other at trust (as if pairing v2 had run):
// each stores the other's card and mailbox announcement.
func pairWith(t *testing.T, a, b *testNode, trust string) {
	t.Helper()
	ctx := context.Background()
	sa, err := agentcard.Verify(a.card)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := agentcard.Verify(b.card)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.ps.AddTrusted(ctx, sa, a.card, a.now(), trust, a.mbox); err != nil {
		t.Fatal(err)
	}
	if err := a.ps.AddTrusted(ctx, sb, b.card, b.now(), trust, b.mbox); err != nil {
		t.Fatal(err)
	}
}

// deliver simulates the receipt by "to" of an application mail of kind from
// "from", carrying body (a JSON-marshalable value). It runs the kind's Apply
// inside a transaction (as the mail receiver would), commits, then runs
// After, mirroring internal/mail.Receiver.Handle without the crypto.
func deliver(t *testing.T, from *testNode, to *testNode, kind string, body any) error {
	t.Helper()
	ctx := context.Background()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := agentcard.ParseStrict(raw)
	if err != nil {
		t.Fatal(err)
	}
	genBody, _ := gen.(map[string]any)
	op := &mail.Opened{Msg: mail.Msg{
		V: 1, ID: mail.NewID(), From: from.key, To: to.key, Created: to.now(), Kind: kind, Body: genBody,
	}}
	k, ok := to.kinds[kind]
	if !ok {
		return fmt.Errorf("no handler for kind %s", kind)
	}
	tx, err := to.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if k.Apply != nil {
		if err := k.Apply(ctx, tx, op); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if k.After != nil {
		k.After(ctx, op)
	}
	return nil
}

// routedOutbox implements team.Outbox by delivering directly to the named
// node in reg, simulating the real outbox without any network or crypto.
type routedOutbox struct {
	t    *testing.T
	from *testNode
	reg  map[string]*testNode
}

func (o *routedOutbox) Submit(_ context.Context, to, kind string, body any) (mail.Submitted, error) {
	target, ok := o.reg[to]
	if !ok {
		return mail.Submitted{}, fmt.Errorf("no route to %s", to)
	}
	if err := deliver(o.t, o.from, target, kind, body); err != nil {
		return mail.Submitted{}, err
	}
	return mail.Submitted{ID: mail.NewID(), State: mail.StateDelivered}, nil
}

// network wires every node's Outbox to deliver directly to every other node
// registered, simulating a fully connected mesh of paired daemons.
func newNetwork(t *testing.T, nodes ...*testNode) {
	t.Helper()
	reg := make(map[string]*testNode, len(nodes))
	for _, n := range nodes {
		reg[n.key] = n
	}
	for _, n := range nodes {
		n.ts.Outbox = &routedOutbox{t: t, from: n, reg: reg}
	}
}

// addPendingJoin inserts a team_pending_joins row for the joiner's own
// database, as `team_join` (1.1d) would before sending team.join.
func addPendingJoin(t *testing.T, joiner *testNode, owner, lookup string, now time.Time) {
	t.Helper()
	if _, err := joiner.db.Exec(`INSERT INTO team_pending_joins (owner_key, lookup, created, expires) VALUES (?, ?, ?, ?)`,
		owner, lookup, now.Format("2006-01-02T15:04:05.000Z"), now.Add(24*time.Hour).Format("2006-01-02T15:04:05.000Z")); err != nil {
		t.Fatal(err)
	}
}

func auditActions(t *testing.T, n *testNode) []string {
	t.Helper()
	evs, err := n.alog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Action
	}
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
