package request

// Ticket 1.4c unit acceptance for the receiving (Apply) and sending (Submit)
// paths: Docs/protocol/request.md §Receiving and §Submitting, and
// Docs/review/11-phase1-tickets.md §1.4c.

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// fakePeers pairs every key and gives it one mailbox key.
type fakePeers struct{ pub []byte }

func (p fakePeers) IsPaired(string) bool                        { return true }
func (p fakePeers) MailboxPub(string) ([]byte, bool)            { return p.pub, true }
func (p fakePeers) IsPairedTx(*sql.Tx, string) bool             { return true }
func (p fakePeers) MailboxPubTx(*sql.Tx, string) ([]byte, bool) { return p.pub, true }

// failAfterSubmit runs the real SubmitTx, then fails: the injected failure
// after the insert of the ticket's auto-decline atomicity test.
type failAfterSubmit struct{ *mail.Outbox }

func (f failAfterSubmit) SubmitTx(ctx context.Context, tx *sql.Tx, to, kind string, body any) (mail.Submitted, error) {
	if _, err := f.Outbox.SubmitTx(ctx, tx, to, kind, body); err != nil {
		return mail.Submitted{}, err
	}
	return mail.Submitted{}, errors.New("injected failure")
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

// policy is the team/D5 answer the test store gives.
type policy struct {
	unverified, inactive, notMember bool
	err                             error
}

func newTestStore(t *testing.T, self string, pol *policy) (*Store, *mail.Outbox, *fakeAudit) {
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
	al := &fakeAudit{}
	s := &Store{
		DB: st.DB(), Self: self, Outbox: ob, Audit: al,
		TeamActive: func(context.Context, *sql.Tx, string) (bool, error) {
			return !pol.inactive, pol.err
		},
		TeamHasMembers: func(context.Context, *sql.Tx, string, string) (bool, error) {
			return !pol.notMember, pol.err
		},
		UnverifiedPeer: func(*sql.Tx, string) (bool, error) { return pol.unverified, pol.err },
	}
	return s, ob, al
}

// deliverRequest runs s's Apply for req as the mail receiver does: inside a
// transaction, rolled back on error, then After on commit.
func deliverRequest(t *testing.T, s *Store, req *Request, msgCreated time.Time) error {
	t.Helper()
	ctx := context.Background()
	raw, err := json.Marshal(WireBody(req))
	if err != nil {
		t.Fatal(err)
	}
	v, err := agentcard.ParseStrict(raw)
	if err != nil {
		t.Fatal(err)
	}
	op := &mail.Opened{Msg: mail.Msg{
		V: 1, ID: "m-" + NewID()[2:], From: req.From, To: s.Self, Created: msgCreated,
		Kind: "request", Body: v.(map[string]any),
	}}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.apply(ctx, tx, op); err != nil {
		_ = tx.Rollback()
		pendingApply.Delete(op)
		return err
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s.after(ctx, op)
	return nil
}

func countRows(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func receivedRequest() *Request {
	r := validRequest()
	r.Created = time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	return r
}

func TestApplyStoresPending(t *testing.T) {
	s, _, al := newTestStore(t, testTo, &policy{})
	req := receivedRequest()
	if err := deliverRequest(t, s, req, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE direction = 'in' AND state = 'pending' AND state_seq = 0`); n != 1 {
		t.Fatalf("pending in rows = %d, want 1", n)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM outbox`); n != 0 {
		t.Errorf("outbox rows = %d, want 0 (nothing to reply)", n)
	}
	if got := al.actions(); len(got) != 1 || got[0] != "request.in" {
		t.Errorf("audit = %v, want [request.in]", got)
	}
}

// TestApplyAutoDecline: each policy code, in the order of §Receiving step 3,
// stores a declined row with last_reply and outboxes the decline to the sender.
func TestApplyAutoDecline(t *testing.T) {
	cases := []struct {
		name string
		pol  policy
		code string
	}{
		{"unverified_peer (D5)", policy{unverified: true}, "unverified_peer"},
		{"unverified before team", policy{unverified: true, inactive: true}, "unverified_peer"},
		{"unknown_team", policy{inactive: true}, "unknown_team"},
		{"unknown_team before membership", policy{inactive: true, notMember: true}, "unknown_team"},
		{"not_team_member", policy{notMember: true}, "not_team_member"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pol := tc.pol
			s, _, al := newTestStore(t, testTo, &pol)
			req := receivedRequest()
			if err := deliverRequest(t, s, req, time.Now()); err != nil {
				t.Fatal(err)
			}
			var state, code, lastReply string
			var seq int
			var first sql.NullString
			if err := s.DB.QueryRow(`SELECT state, decline_code, state_seq, first_response, last_reply FROM requests WHERE direction = 'in'`).
				Scan(&state, &code, &seq, &first, &lastReply); err != nil {
				t.Fatal(err)
			}
			if state != "declined" || code != tc.code || seq != 1 || first.Valid {
				t.Errorf("row = %s %s seq %d first %v, want declined %s seq 1 first NULL", state, code, seq, first, tc.code)
			}
			if !strings.Contains(lastReply, `"kind":"request.decline"`) || !strings.Contains(lastReply, `"code":"`+tc.code+`"`) {
				t.Errorf("last_reply = %s", lastReply)
			}
			if n := countRows(t, s.DB, `SELECT COUNT(*) FROM outbox WHERE kind = 'request.decline' AND to_key = ?`, req.From); n != 1 {
				t.Errorf("decline mails to the sender = %d, want 1", n)
			}
			if got := al.actions(); len(got) != 1 || got[0] != "request.auto_decline" {
				t.Errorf("audit = %v, want [request.auto_decline]", got)
			}
			for _, e := range al.entries {
				if strings.Contains(e.detail, req.Title) || strings.Contains(e.detail, "What:") {
					t.Errorf("audit detail carries content: %s", e.detail)
				}
			}
		})
	}
}

// TestApplyAutoDeclineAtomic: a failure after the decline's outbox insert
// leaves neither the declined row nor the outbox row (ticket 1.4c).
func TestApplyAutoDeclineAtomic(t *testing.T) {
	s, ob, al := newTestStore(t, testTo, &policy{notMember: true})
	s.Outbox = failAfterSubmit{ob}
	err := deliverRequest(t, s, receivedRequest(), time.Now())
	if err == nil || errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("apply = %v, want a retryable (non bad_body) error", err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests`); n != 0 {
		t.Errorf("requests rows = %d, want 0", n)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM outbox`); n != 0 {
		t.Errorf("outbox rows = %d, want 0", n)
	}
	if got := al.actions(); len(got) != 0 {
		t.Errorf("audit = %v, want nothing", got)
	}
}

// TestApplyPolicyReadErrorIsRetryable: a failed team or trust lookup is not
// bad_body (which would ack and drop the request) and never fails open.
func TestApplyPolicyReadErrorIsRetryable(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{err: errors.New("disk")})
	err := deliverRequest(t, s, receivedRequest(), time.Now())
	if err == nil || errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("apply = %v, want a retryable error", err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests`); n != 0 {
		t.Errorf("requests rows = %d, want 0", n)
	}
}

// TestApplyWireIdempotency: (from, id) is the key. The same body is a
// duplicate, a different body keeps the first, and the same id from another
// sender is a separate request.
func TestApplyWireIdempotency(t *testing.T) {
	s, _, al := newTestStore(t, testTo, &policy{})
	req := receivedRequest()
	for range 2 {
		if err := deliverRequest(t, s, req, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	changed := *req
	changed.Title = "a different title"
	if err := deliverRequest(t, s, &changed, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE direction = 'in'`); n != 1 {
		t.Fatalf("in rows = %d, want 1", n)
	}
	canon, err := Canonical(req)
	if err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE body_hash = ?`, BodyHash(canon)); n != 1 {
		t.Error("the first body was not kept")
	}
	want := []string{"request.in", "request.duplicate", "request.conflict"}
	if got := al.actions(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("audit = %v, want %v", got, want)
	}

	other := *req
	other.From = testKey(3)
	if err := deliverRequest(t, s, &other, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = ?`, req.ID); n != 2 {
		t.Errorf("rows for one id from two senders = %d, want 2", n)
	}
}

// TestApplyAgeBound: an unknown request created more than 30 d ago is
// bad_body; a duplicate of a stored one is still recognised at that age.
func TestApplyAgeBound(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	base := time.Now().UTC().Truncate(time.Second)
	old := receivedRequest()
	old.Created = base.Add(-31 * 24 * time.Hour)
	if err := deliverRequest(t, s, old, base); !errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("31 d old new request: err = %v, want bad_body", err)
	}

	known := receivedRequest()
	known.ID = NewID()
	known.Created = base
	if err := deliverRequest(t, s, known, base); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return base.Add(31 * 24 * time.Hour) }
	if err := deliverRequest(t, s, known, base.Add(31*24*time.Hour)); err != nil {
		t.Fatalf("31 d old duplicate: err = %v, want nil", err)
	}
}

// TestApplyEnvelopeBinding: from, to and created are bound to the mail.
func TestApplyEnvelopeBinding(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	req := receivedRequest()

	wrongTo := *req
	wrongTo.To = testKey(4)
	if err := deliverRequest(t, s, &wrongTo, time.Now()); !errors.Is(err, mail.ErrBadBody) {
		t.Errorf("to != msg.to: err = %v, want bad_body", err)
	}
	if err := deliverRequest(t, s, req, req.Created.Add(-time.Second)); !errors.Is(err, mail.ErrBadBody) {
		t.Errorf("created after msg.created: err = %v, want bad_body", err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests`); n != 0 {
		t.Errorf("requests rows = %d, want 0", n)
	}
}

func submitParams(key, hash string) SubmitParams {
	return SubmitParams{
		From: testFrom, To: testTo, Team: testTeam, Type: TypeTask, Title: "t", Brief: "What: x",
		Urgency: UrgencyNormal, IdempotencyKey: key, ParamsHash: hash,
	}
}

// TestSubmitIdempotencyKey: the same params return the first request as a
// duplicate; different params are ErrIdempotencyConflict; the key is scoped
// per peer.
func TestSubmitIdempotencyKey(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	ctx := context.Background()
	first, err := s.Submit(ctx, submitParams("k1", "h1"))
	if err != nil || first.Duplicate || first.Status != "queued" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	second, err := s.Submit(ctx, submitParams("k1", "h1"))
	if err != nil || !second.Duplicate || second.Request.ID != first.Request.ID || second.MailID != first.MailID || second.Status != "queued" {
		t.Fatalf("second = %+v, %v; want a duplicate of %s", second, err, first.Request.ID)
	}
	if _, err := s.Submit(ctx, submitParams("k1", "h2")); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different params: err = %v, want ErrIdempotencyConflict", err)
	}
	otherPeer := submitParams("k1", "h2")
	otherPeer.To = testKey(5)
	if res, err := s.Submit(ctx, otherPeer); err != nil || res.Duplicate {
		t.Fatalf("same key to another peer = %+v, %v; want a new request", res, err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE direction = 'out'`); n != 2 {
		t.Errorf("out rows = %d, want 2", n)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM outbox WHERE kind = 'request'`); n != 2 {
		t.Errorf("outbox rows = %d, want 2", n)
	}
}

// TestSubmitIdempotencyConcurrent: concurrent submits with one key make one
// row and one mail; every loser gets duplicate: true.
func TestSubmitIdempotencyConcurrent(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	const n = 8
	var wg sync.WaitGroup
	results := make([]SubmitOutcome, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.Submit(context.Background(), submitParams("race", "h"))
		}(i)
	}
	wg.Wait()
	fresh := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("submit %d: %v", i, errs[i])
		}
		if !results[i].Duplicate {
			fresh++
		}
		if results[i].Request.ID != results[0].Request.ID {
			t.Errorf("submit %d returned %s, want %s", i, results[i].Request.ID, results[0].Request.ID)
		}
	}
	if fresh != 1 {
		t.Errorf("non-duplicate results = %d, want 1", fresh)
	}
	if c := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE direction = 'out'`); c != 1 {
		t.Errorf("out rows = %d, want 1", c)
	}
	if c := countRows(t, s.DB, `SELECT COUNT(*) FROM outbox`); c != 1 {
		t.Errorf("outbox rows = %d, want 1", c)
	}
}

// TestSubmitValidatesBeforeStoring: a bad field or an oversized body stores
// nothing.
func TestSubmitValidatesBeforeStoring(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	ctx := context.Background()
	bad := submitParams("", "")
	bad.Title = strings.Repeat("x", 121)
	var fe *FieldError
	if _, err := s.Submit(ctx, bad); !errors.As(err, &fe) || fe.Field != "title" {
		t.Errorf("121-code-point title: err = %v, want a FieldError on title", err)
	}
	big := submitParams("", "")
	big.Brief = strings.Repeat(`"`, 16384)
	big.Artifacts = []Artifact{{Path: strings.Repeat(`"`, 1024)}, {Path: strings.Repeat(`"`, 1024)}}
	for range 18 {
		big.Artifacts = append(big.Artifacts, Artifact{URL: "https://x/" + strings.Repeat("a", 2030)})
	}
	var tl *TooLargeError
	if _, err := s.Submit(ctx, big); !errors.As(err, &tl) {
		t.Errorf("oversized request: err = %v, want TooLargeError", err)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests`) + countRows(t, s.DB, `SELECT COUNT(*) FROM outbox`); n != 0 {
		t.Errorf("rows stored = %d, want 0", n)
	}
}
