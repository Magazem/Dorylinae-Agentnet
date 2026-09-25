package debate

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// Test harness: two nodes (A the initiator, B the respondent), each with its
// own database and request, work-session and debate stores wired as
// internal/daemon wires them, and a spy outbox that lets a test hand one
// node's outgoing mail to the other's Apply/After directly (the pattern of
// internal/worksession's tests, minus real sealing). The e2e test with real
// mail through a relay is in internal/daemon.

func seedKey(start byte) string {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = start + byte(i)
	}
	return base64.RawURLEncoding.EncodeToString(ed25519.NewKeyFromSeed(s).Public().(ed25519.PublicKey))
}

const testTeam = "t-fedcba9876543210fedcba9876543210"

var (
	keyA = seedKey(0x00) // the vector keys of pairing.md
	keyB = seedKey(0x20)
)

type fakePeers struct{ pub []byte }

func (p fakePeers) IsPaired(string) bool                        { return true }
func (p fakePeers) MailboxPub(string) ([]byte, bool)            { return p.pub, true }
func (p fakePeers) IsPairedTx(*sql.Tx, string) bool             { return true }
func (p fakePeers) MailboxPubTx(*sql.Tx, string) ([]byte, bool) { return p.pub, true }

type sentMail struct {
	to, kind string
	body     map[string]any
}

type spyOutbox struct {
	*mail.Outbox
	mu  sync.Mutex
	out []sentMail
}

func (o *spyOutbox) SubmitTx(ctx context.Context, tx *sql.Tx, to, kind string, body any) (mail.Submitted, error) {
	sub, err := o.Outbox.SubmitTx(ctx, tx, to, kind, body)
	if err != nil {
		return sub, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return sub, err
	}
	v, err := agentcard.ParseStrict(raw)
	if err != nil {
		return sub, err
	}
	o.mu.Lock()
	o.out = append(o.out, sentMail{to: to, kind: kind, body: v.(map[string]any)})
	o.mu.Unlock()
	return sub, nil
}

// take removes and returns the oldest recorded mail of kind.
func (o *spyOutbox) take(t *testing.T, kind string) sentMail {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, m := range o.out {
		if m.kind == kind {
			o.out = append(o.out[:i], o.out[i+1:]...)
			return m
		}
	}
	t.Fatalf("no %s mail sent (have %v)", kind, o.kinds())
	return sentMail{}
}

func (o *spyOutbox) kinds() []string {
	out := make([]string, len(o.out))
	for i, m := range o.out {
		out[i] = m.kind
	}
	return out
}

func (o *spyOutbox) count(kind string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, m := range o.out {
		if m.kind == kind {
			n++
		}
	}
	return n
}

type fakeAudit struct {
	mu      sync.Mutex
	entries []string // action + " " + detail JSON
}

func (a *fakeAudit) Append(_ context.Context, _, action string, detail any) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.entries = append(a.entries, action+" "+string(b))
	a.mu.Unlock()
	return nil
}

// has reports whether an audit row with action and every fragment exists.
func (a *fakeAudit) has(action string, fragments ...string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.entries {
		if !strings.HasPrefix(e, action+" ") {
			continue
		}
		ok := true
		for _, f := range fragments {
			if !strings.Contains(e, f) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func (a *fakeAudit) all() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.Join(a.entries, "\n")
}

type events struct {
	mu   sync.Mutex
	list []string
}

func (e *events) add(ev string) {
	e.mu.Lock()
	e.list = append(e.list, ev)
	e.mu.Unlock()
}

func (e *events) count(ev string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, x := range e.list {
		if x == ev {
			n++
		}
	}
	return n
}

// dnode is one daemon.
type dnode struct {
	t      *testing.T
	self   string
	db     *sql.DB
	ob     *spyOutbox
	audit  *fakeAudit
	events *events
	req    *request.Store
	ws     *worksession.Store
	ds     *Store
	caps   *capability.Store
	clock  time.Time
}

func newDNode(t *testing.T, self string) *dnode {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(testutil.TempDir(t), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	xk, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ob := &mail.Outbox{
		DB:    st.DB(),
		Priv:  func() (ed25519.PrivateKey, error) { return append(ed25519.PrivateKey(nil), priv...), nil },
		Peers: fakePeers{pub: xk.PublicKey().Bytes()},
	}
	n := &dnode{t: t, self: self, db: st.DB(), ob: &spyOutbox{Outbox: ob}, audit: &fakeAudit{}, events: &events{},
		clock: time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)}
	now := func() time.Time { return n.clock }
	n.req = &request.Store{
		DB: n.db, Self: self, Outbox: n.ob, Audit: n.audit,
		TeamActive:     func(context.Context, *sql.Tx, string) (bool, error) { return true, nil },
		TeamHasMembers: func(context.Context, *sql.Tx, string, string) (bool, error) { return true, nil },
		Now:            now,
	}
	n.ws = &worksession.Store{DB: n.db, Self: self, Outbox: n.ob, Audit: n.audit, Requests: n.req, Now: now}
	n.caps = &capability.Store{DB: n.db, Now: now}
	n.ds = &Store{
		DB: n.db, Self: self, Outbox: n.ob, Audit: n.audit, Requests: n.req, Sessions: n.ws,
		PeerQuarantine: n.caps.PeerQuarantineHoldsTx, Now: now,
		OnEvent: func(_ context.Context, ev, _, _, _ string) { n.events.add(ev) },
	}
	n.req.Sessions = n.ws
	n.req.Debates = n.ds
	n.ws.Debate = n.ds
	return n
}

func (n *dnode) advance(d time.Duration) { n.clock = n.clock.Add(d) }

func kindOf(n *dnode, kind string) mail.Kind {
	switch kind {
	case "request":
		return n.req.Kind()
	case request.KindAccept:
		return n.req.AcceptKind()
	case request.KindDecline:
		return n.req.DeclineKind()
	case request.KindComplete:
		return n.req.CompleteKind()
	case request.KindCancelled:
		return n.req.CancelledKind()
	case request.KindCancel:
		return n.req.CancelKind()
	case worksession.KindResult:
		return n.ws.ResultKind()
	case worksession.KindState:
		return n.ws.StateKind()
	case worksession.KindCancel:
		return n.ws.CancelKind()
	case MailEntry:
		return n.ds.EntryKind()
	case MailReveal:
		return n.ds.RevealKind()
	case MailClose:
		return n.ds.CloseKind()
	}
	panic("kindOf: unknown kind " + kind)
}

// deliver feeds sm to n as the mail receiver would: Apply in the dedupe
// transaction with the generic inbox copy (blank when withheld), then After.
func deliver(t *testing.T, n *dnode, from string, sm sentMail) error {
	t.Helper()
	ctx := context.Background()
	k := kindOf(n, sm.kind)
	raw, err := json.Marshal(sm.body)
	if err != nil {
		t.Fatal(err)
	}
	v, err := agentcard.ParseStrict(raw)
	if err != nil {
		t.Fatal(err)
	}
	op := &mail.Opened{Msg: mail.Msg{V: 1, ID: mail.NewID(), From: from, To: n.self, Created: n.clock, Kind: sm.kind, Body: v.(map[string]any)},
		Signed: []byte(`{"msg":` + string(raw) + `}`)}
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Apply(ctx, tx, op); err != nil {
		_ = tx.Rollback()
		return err
	}
	if k.Inbox {
		signed := string(op.Signed)
		if op.Withhold {
			signed = ""
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO mail_inbox (from_key, id, kind, created, received_at, signed) VALUES (?, ?, ?, ?, ?, ?)`,
			op.Msg.From, op.Msg.ID, op.Msg.Kind, wireTime(n.clock), storeTime(n.clock), signed); err != nil {
			t.Fatal(err)
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

func mustDeliver(t *testing.T, n *dnode, from string, sm sentMail) {
	t.Helper()
	if err := deliver(t, n, from, sm); err != nil {
		t.Fatalf("deliver %s: %v", sm.kind, err)
	}
}

func wantBadBody(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("err = %v, want bad_body", err)
	}
}

// pass relays every mail of kind from src to dst, oldest first.
func pass(t *testing.T, src, dst *dnode, kind string) {
	t.Helper()
	for src.ob.count(kind) > 0 {
		mustDeliver(t, dst, src.self, src.ob.take(t, kind))
	}
}

// testPosition returns a valid opening position with claim c, one
// assumption and one piece of evidence (so targets assumptions/0 and
// evidence/0 exist).
func testPosition(c string) map[string]any {
	return map[string]any{
		"claim":       c,
		"assumptions": []any{"Load is steady"},
		"evidence":    []any{map[string]any{"kind": "file", "ref": "internal/mail/outbox.go"}},
		"argument":    "Because of " + c + ".\nSee the outbox.",
	}
}

func pass0() map[string]any { return map[string]any{"challenges": []any{}} }

func challenge(targets ...string) map[string]any {
	ts := make([]any, len(targets))
	for i, x := range targets {
		ts[i] = x
	}
	return map[string]any{"challenges": []any{map[string]any{"targets": ts, "argument": "This does not hold."}}}
}

func proposal() map[string]any {
	return map[string]any{"agreement": map[string]any{"decision": "Use capped backoff"}}
}

func answer(accept bool) map[string]any { return map[string]any{"accept": accept} }

// startDebate has A start a debate with B (rounds) and delivers the request
// to B. It returns the request and session ids.
func startDebate(t *testing.T, a, b *dnode, rounds int) (reqID, sid string) {
	t.Helper()
	out, err := a.ds.Start(context.Background(), StartParams{
		Submit: request.SubmitParams{
			From: a.self, To: b.self, Team: testTeam, Type: request.TypeDebate, Title: "Backoff",
			Brief: "How should the outbox retry?", Urgency: request.UrgencyNormal,
		},
		Position: testPosition("A: capped backoff"), Rounds: rounds,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	mustDeliver(t, b, a.self, a.ob.take(t, "request"))
	return out.Request.ID, out.Session
}

// openPositions starts a debate, has B accept with its position in one step
// and delivers the accept and the entry to A (accept first), then the reveal
// to B. The debate is in rounds on both sides.
func openPositions(t *testing.T, rounds int) (a, b *dnode, reqID, sid string) {
	t.Helper()
	a, b = newDNode(t, keyA), newDNode(t, keyB)
	reqID, sid = startDebate(t, a, b, rounds)
	submit(t, b, sid, KindPosition, testPosition("B: fixed retry"))
	pass(t, b, a, request.KindAccept)
	pass(t, b, a, MailEntry)
	pass(t, a, b, MailReveal)
	return a, b, reqID, sid
}

func submit(t *testing.T, n *dnode, id, kind string, entry map[string]any) SubmitResult {
	t.Helper()
	res, err := n.ds.Submit(context.Background(), id, "", kind, entry)
	if err != nil {
		t.Fatalf("%s Submit %s: %v", n.name(), kind, err)
	}
	return res
}

func (n *dnode) name() string {
	if n.self == keyA {
		return "A"
	}
	return "B"
}

func view(t *testing.T, n *dnode, id string) View {
	t.Helper()
	v, err := n.ds.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("%s Get: %v", n.name(), err)
	}
	return v
}

func phaseOf(t *testing.T, n *dnode, sid string) string {
	t.Helper()
	var p string
	if err := n.db.QueryRow(`SELECT phase FROM debates WHERE session = ?`, sid).Scan(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func sessionState(t *testing.T, n *dnode, sid string) (state, outcome string) {
	t.Helper()
	var o sql.NullString
	if err := n.db.QueryRow(`SELECT state, outcome FROM work_sessions WHERE id = ?`, sid).Scan(&state, &o); err != nil {
		t.Fatal(err)
	}
	return state, o.String
}

// seedSensitiveGrant stores an issued, active, sensitive grant to peer with
// the given exp, the rule-2 test's input.
func seedSensitiveGrant(t *testing.T, n *dnode, peer string, exp time.Time) {
	t.Helper()
	now := n.clock.Format("2006-01-02T15:04:05.000Z")
	if _, err := n.db.Exec(`INSERT INTO grants (id, direction, peer, session, action, label, sensitive, nbf, exp, token, state, approval, created, updated)
		VALUES (?, 'issued', ?, 's-00000000000000000000000000000000', 'fs.read', 'x', 1, ?, ?, '{}', 'active', 'a-1', ?, ?)`,
		capability.NewID(), peer, now, exp.UTC().Format("2006-01-02T15:04:05.000Z"), now, now); err != nil {
		t.Fatal(err)
	}
}
