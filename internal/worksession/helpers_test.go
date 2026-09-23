package worksession

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Test harness: two nodes (A the requester, B the worker), each with its own
// database, request.Store and worksession.Store, wired together exactly as
// internal/daemon wires them, with a spy outbox that lets a test hand one
// node's outgoing mail to the other node's Apply/After directly (the same
// pattern as internal/request's deliverMirror, minus real sealing).

var testB64u = base64.RawURLEncoding

func testKey(seed byte) string {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = seed + byte(i)
	}
	priv := ed25519.NewKeyFromSeed(s)
	return testB64u.EncodeToString(priv.Public().(ed25519.PublicKey))
}

const testTeam = "t-fedcba9876543210fedcba9876543210"

var (
	testA = testKey(1) // requester
	testB = testKey(2) // worker
)

// fakePeers pairs every key and gives it one mailbox key.
type fakePeers struct{ pub []byte }

func (p fakePeers) IsPaired(string) bool                        { return true }
func (p fakePeers) MailboxPub(string) ([]byte, bool)            { return p.pub, true }
func (p fakePeers) IsPairedTx(*sql.Tx, string) bool             { return true }
func (p fakePeers) MailboxPubTx(*sql.Tx, string) ([]byte, bool) { return p.pub, true }

type sentMail struct {
	to, kind string
	body     map[string]any
}

// spyOutbox wraps a real *mail.Outbox and records every submitted body, so a
// test can hand it to the peer's Apply as a synthetic mail.Opened.
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
	raw, merr := json.Marshal(body)
	if merr != nil {
		return sub, merr
	}
	v, perr := agentcard.ParseStrict(raw)
	if perr != nil {
		return sub, perr
	}
	o.mu.Lock()
	o.out = append(o.out, sentMail{to: to, kind: kind, body: v.(map[string]any)})
	o.mu.Unlock()
	return sub, nil
}

// last returns and removes the most recently sent mail of kind.
func (o *spyOutbox) last(t *testing.T, kind string) sentMail {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := len(o.out) - 1; i >= 0; i-- {
		if o.out[i].kind == kind {
			m := o.out[i]
			o.out = append(o.out[:i], o.out[i+1:]...)
			return m
		}
	}
	t.Fatalf("no %s mail sent (have %v)", kind, o.out)
	return sentMail{}
}

// sentCount reports how many mails of kind are currently recorded.
func (o *spyOutbox) sentCount(kind string) int {
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

type auditEntry struct {
	action string
	detail string
}

type fakeAudit struct {
	mu      sync.Mutex
	entries []auditEntry
}

func (a *fakeAudit) Append(_ context.Context, _, action string, detail any) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, auditEntry{action, string(b)})
	return nil
}

func (a *fakeAudit) actions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.entries))
	for i, e := range a.entries {
		out[i] = e.action
	}
	return out
}

// node is one daemon: its request.Store and worksession.Store, sharing a
// database, with a fake clock and a spy outbox.
type node struct {
	t     *testing.T
	self  string
	db    *sql.DB
	ob    *spyOutbox
	audit *fakeAudit
	req   *request.Store
	ws    *Store
	clock time.Time
}

func newNode(t *testing.T, self string) *node {
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
	n := &node{t: t, self: self, db: st.DB(), ob: &spyOutbox{Outbox: ob}, audit: &fakeAudit{}, clock: time.Now().UTC().Truncate(time.Second)}
	n.req = &request.Store{
		DB: n.db, Self: self, Outbox: n.ob, Audit: n.audit,
		TeamActive:     func(context.Context, *sql.Tx, string) (bool, error) { return true, nil },
		TeamHasMembers: func(context.Context, *sql.Tx, string, string) (bool, error) { return true, nil },
		Now:            func() time.Time { return n.clock },
	}
	n.ws = &Store{DB: n.db, Self: self, Outbox: n.ob, Audit: n.audit, Requests: n.req, Now: func() time.Time { return n.clock }}
	n.req.Sessions = n.ws
	return n
}

// kindFor returns the mail.Kind n uses to receive kind, exactly as
// internal/daemon/mail.go registers it.
func kindFor(n *node, kind string) mail.Kind {
	switch kind {
	case "request":
		return n.req.Kind()
	case request.KindAccept:
		return n.req.AcceptKind()
	case request.KindDecline:
		return n.req.DeclineKind()
	case request.KindDefer:
		return n.req.DeferKind()
	case request.KindComplete:
		return n.req.CompleteKind()
	case request.KindCancelled:
		return n.req.CancelledKind()
	case request.KindCancel:
		return n.req.CancelKind()
	case KindResult:
		return n.ws.ResultKind()
	case KindState:
		return n.ws.StateKind()
	case KindCancel:
		return n.ws.CancelKind()
	default:
		panic("kindFor: unknown kind " + kind)
	}
}

// deliver feeds sm to n's Apply/After for its kind, exactly as the real mail
// receiver would (mail dedupe transaction, then After on commit). The body
// is round-tripped through JSON + agentcard.ParseStrict first, so a
// hand-built sentMail (a test literal with plain Go ints) decodes the same
// way a spy-captured or wire body would (numbers as json.Number).
func deliver(t *testing.T, n *node, from string, sm sentMail) error {
	t.Helper()
	ctx := context.Background()
	k := kindFor(n, sm.kind)
	raw, err := json.Marshal(sm.body)
	if err != nil {
		t.Fatal(err)
	}
	v, err := agentcard.ParseStrict(raw)
	if err != nil {
		t.Fatal(err)
	}
	op := &mail.Opened{Msg: mail.Msg{V: 1, ID: mail.NewID(), From: from, To: n.self, Created: n.clock, Kind: sm.kind, Body: v.(map[string]any)}}
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Apply(ctx, tx, op); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if k.After != nil {
		k.After(ctx, op)
	}
	return nil
}

// setupAcceptedSession builds a request from A to B, has B accept it (which
// opens B's session row) and delivers the accept to A (which opens A's
// session row), returning both nodes and the shared request/session id.
func setupAcceptedSession(t *testing.T) (a, b *node, reqID, sid string) {
	t.Helper()
	ctx := context.Background()
	a = newNode(t, testA)
	b = newNode(t, testB)

	outcome, err := a.req.Submit(ctx, request.SubmitParams{
		From: testA, To: testB, Team: testTeam, Type: request.TypeTask, Title: "t", Brief: "What: x", Urgency: request.UrgencyNormal,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	reqID = outcome.Request.ID
	if err := deliver(t, b, testA, a.ob.last(t, "request")); err != nil {
		t.Fatalf("deliver request: %v", err)
	}
	if _, err := b.req.Accept(ctx, reqID, testA); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := deliver(t, a, testB, b.ob.last(t, request.KindAccept)); err != nil {
		t.Fatalf("deliver accept: %v", err)
	}
	sid = DeriveID(testA, testB, reqID)
	return a, b, reqID, sid
}

// validResult returns a minimal, entirely valid Result.
func validResult() *Result {
	return &Result{Status: request.ResultPass, Summary: "done", Verification: VerificationNone}
}

// submitAndDeliverResult has B submit result for (a, b, reqID) and delivers
// the ws.result to A, returning A's session view.
func submitAndDeliverResult(t *testing.T, a, b *node, reqID string, result *Result) View {
	t.Helper()
	ok, err := b.ws.SubmitResult(context.Background(), testA, reqID, result)
	if !ok || err != nil {
		t.Fatalf("SubmitResult: ok=%v err=%v", ok, err)
	}
	if err := deliver(t, a, testB, b.ob.last(t, KindResult)); err != nil {
		t.Fatalf("deliver ws.result: %v", err)
	}
	v, err := a.ws.Get(context.Background(), DeriveID(testA, testB, reqID))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return v
}

// deliverState delivers A's last ws.state mail to B.
func deliverState(t *testing.T, a, b *node) {
	t.Helper()
	if err := deliver(t, b, testA, a.ob.last(t, KindState)); err != nil {
		t.Fatalf("deliver ws.state: %v", err)
	}
}

func countWorkSessions(t *testing.T, n *node) int {
	t.Helper()
	var c int
	if err := n.db.QueryRow(`SELECT COUNT(*) FROM work_sessions`).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}
