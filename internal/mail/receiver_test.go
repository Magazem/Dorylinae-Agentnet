package mail

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

type peerKeys map[string][]byte

func (p peerKeys) MailboxPub(peer string) ([]byte, bool) { v, ok := p[peer]; return v, ok }

type sentBox struct {
	mu   sync.Mutex
	sent []envelope.Envelope
}

func (s *sentBox) Send(_ context.Context, e envelope.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, e)
	return nil
}

func (s *sentBox) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

type recvFixture struct {
	t      *testing.T
	path   string
	st     *store.Store
	sender party
	recip  party
	rcv    *Receiver
	out    *sentBox
	audits *auditRec
}

func openStore(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func newRecvFixture(t *testing.T) *recvFixture {
	t.Helper()
	sender, recip, o := fixture(t)
	f := &recvFixture{t: t, path: filepath.Join(testutil.TempDir(t), "r.db"), sender: sender, recip: recip, audits: &auditRec{}}
	f.st = openStore(t, f.path)
	t.Cleanup(func() { _ = f.st.Close() })
	f.build(o)
	return f
}

// build makes a Receiver over the current store; a "restart" calls it again.
func (f *recvFixture) build(o *Opener) {
	f.out = &sentBox{}
	recip := f.recip
	f.rcv = &Receiver{
		Opener: o,
		DB:     f.st.DB(),
		Priv:   func() (ed25519.PrivateKey, error) { return append(ed25519.PrivateKey(nil), recip.priv...), nil },
		Peers:  peerKeys{f.sender.key: f.sender.mbox.PublicKey().Bytes()},
		Sender: f.out,
		Audit:  f.audits,
		Kinds:  map[string]Kind{"request": {Inbox: true}},
		Now:    func() time.Time { return vectorNow },
	}
}

func (f *recvFixture) mailEnv(id, kind string, body any) envelope.Envelope {
	sl := sealTo(f.t, f.sender, f.recip, id, kind, body, vectorNow)
	return env(f.sender, f.recip, sl)
}

func (f *recvFixture) count(q string) int {
	f.t.Helper()
	var n int
	if err := f.st.DB().QueryRow(q).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// ackBodies opens every ack the receiver sent, as the original sender would.
func (f *recvFixture) ackBodies() []map[string]any {
	f.t.Helper()
	so := &Opener{
		Self:  f.sender.key,
		Peers: fakePeers{f.recip.key: true},
		Keys:  fakeKeys{KeyIDOf(f.sender.mbox.PublicKey().Bytes()): f.sender.mbox},
		Now:   func() time.Time { return vectorNow },
	}
	var out []map[string]any
	f.out.mu.Lock()
	defer f.out.mu.Unlock()
	for _, e := range f.out.sent {
		if e.Type != "mail" || e.From != f.recip.key || e.To != f.sender.key {
			f.t.Fatalf("bad ack envelope %+v", e)
		}
		op, err := so.Open(e)
		if err != nil {
			f.t.Fatalf("ack does not open: %v", err)
		}
		if op.Msg.Kind != "ack" {
			f.t.Fatalf("kind = %q, want ack", op.Msg.Kind)
		}
		out = append(out, op.Msg.Body)
	}
	return out
}

func idsOf(t *testing.T, body map[string]any, member string) []string {
	t.Helper()
	list, _ := body[member].([]any)
	var out []string
	for _, v := range list {
		out = append(out, v.(string))
	}
	return out
}

const testID = "m-00000000000000000000000000000001"

func TestTripleDeliveryOneRowThreeAcks(t *testing.T) {
	f := newRecvFixture(t)
	e := f.mailEnv(testID, "request", map[string]any{"n": 1})
	for i := 0; i < 3; i++ {
		if err := f.rcv.Handle(context.Background(), e); err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
	}
	if n := f.count(`SELECT COUNT(*) FROM mail_inbox`); n != 1 {
		t.Fatalf("inbox rows = %d, want 1", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM mail_seen`); n != 1 {
		t.Fatalf("mail_seen rows = %d, want 1", n)
	}
	acks := f.ackBodies()
	if len(acks) != 3 {
		t.Fatalf("acks = %d, want 3", len(acks))
	}
	for _, a := range acks {
		if got := idsOf(t, a, "ids"); !reflect.DeepEqual(got, []string{testID}) {
			t.Fatalf("ack ids = %v", got)
		}
	}
	// mail.in is audited once, for the first delivery only.
	if len(f.audits.events) != 1 || f.audits.events[0].action != ActionIn {
		t.Fatalf("audit events = %+v, want one %s", f.audits.events, ActionIn)
	}
	var signed string
	if err := f.st.DB().QueryRow(`SELECT signed FROM mail_inbox`).Scan(&signed); err != nil || signed == "" {
		t.Fatalf("inbox signed = %q, %v", signed, err)
	}
}

func TestReplayAfterRestartStillDeduped(t *testing.T) {
	f := newRecvFixture(t)
	e := f.mailEnv(testID, "request", nil)
	if err := f.rcv.Handle(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err := f.st.Close(); err != nil {
		t.Fatal(err)
	}
	f.st = openStore(t, f.path)
	_, _, o := fixture(t)
	f.build(o)
	if err := f.rcv.Handle(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT COUNT(*) FROM mail_inbox`); n != 1 {
		t.Fatalf("inbox rows after restart replay = %d, want 1", n)
	}
	if got := f.ackBodies(); len(got) != 1 {
		t.Fatalf("replay acks = %d, want 1 (re-ack)", len(got))
	}
}

func TestNoAckBeforeCommit(t *testing.T) {
	f := newRecvFixture(t)
	e := f.mailEnv(testID, "request", nil)
	fail := true
	f.rcv.commit = func(tx *sql.Tx) error {
		if fail {
			return errors.New("injected commit failure")
		}
		return tx.Commit()
	}
	if err := f.rcv.Handle(context.Background(), e); err == nil {
		t.Fatal("commit failure was not reported")
	}
	if f.out.count() != 0 {
		t.Fatal("ack sent although the commit failed")
	}
	if n := f.count(`SELECT COUNT(*) FROM mail_seen`) + f.count(`SELECT COUNT(*) FROM mail_inbox`); n != 0 {
		t.Fatalf("rows left after failed commit: %d", n)
	}
	// The resend is then processed normally, not treated as a duplicate.
	fail = false
	if err := f.rcv.Handle(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if f.count(`SELECT COUNT(*) FROM mail_inbox`) != 1 || f.out.count() != 1 {
		t.Fatal("resend after failed commit was not processed and acked once")
	}
}

func TestUnknownKindAckedUnsupported(t *testing.T) {
	f := newRecvFixture(t)
	e := f.mailEnv(testID, "future.kind", nil)
	for i := 0; i < 2; i++ { // second is a duplicate: still "unsupported"
		if err := f.rcv.Handle(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	if f.count(`SELECT COUNT(*) FROM mail_inbox`) != 0 || f.count(`SELECT COUNT(*) FROM mail_seen`) != 1 {
		t.Fatal("unknown kind must be recorded only in mail_seen")
	}
	acks := f.ackBodies()
	if len(acks) != 2 {
		t.Fatalf("acks = %d, want 2", len(acks))
	}
	for _, a := range acks {
		if _, has := a["ids"]; has || !reflect.DeepEqual(idsOf(t, a, "unsupported"), []string{testID}) {
			t.Fatalf("ack body = %v, want unsupported only", a)
		}
	}
}

func TestAckKindNotStoredNorAcked(t *testing.T) {
	f := newRecvFixture(t)
	var got []string
	f.rcv.OnAck = func(op *Opened) { got = append(got, op.Msg.ID) }
	// Ack mail sent by "sender" to the receiver.
	e := f.mailEnv(testID, "ack", map[string]any{"ids": []string{"m-00000000000000000000000000000009"}})
	for i := 0; i < 2; i++ {
		if err := f.rcv.Handle(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || f.out.count() != 0 || f.count(`SELECT COUNT(*) FROM mail_seen`) != 0 {
		t.Fatalf("ack handling: onAck=%d sent=%d", len(got), f.out.count())
	}
}

func TestRejectedMailNotStoredNorAcked(t *testing.T) {
	f := newRecvFixture(t)
	e := f.mailEnv(testID, "request", nil)
	e.ID = "m-00000000000000000000000000000002" // aad mismatch: step 4
	if err := f.rcv.Handle(context.Background(), e); ReasonOf(err) != ReasonDecrypt {
		t.Fatalf("err = %v, want decrypt reject", err)
	}
	if f.out.count() != 0 || f.count(`SELECT COUNT(*) FROM mail_seen`) != 0 {
		t.Fatal("rejected mail must not be stored or acked")
	}
}

func TestKeysWithoutHandlerNotStoredNorAcked(t *testing.T) {
	f := newRecvFixture(t)
	body := map[string]any{"announcement": signedAnnouncement(t, f.sender.priv, f.sender.mbox.PublicKey().Bytes(),
		"2026-01-02T03:00:00Z", "2026-01-16T03:00:00Z", nil)}
	if err := f.rcv.Handle(context.Background(), f.mailEnv(testID, "keys", body)); !errors.Is(err, ErrNoKindHandler) {
		t.Fatalf("err = %v, want ErrNoKindHandler", err)
	}
	if f.out.count() != 0 || f.count(`SELECT COUNT(*) FROM mail_seen`) != 0 {
		t.Fatal("keys without a handler must not be stored or acked")
	}
}

func TestPrune35Days(t *testing.T) {
	f := newRecvFixture(t)
	db := f.st.DB()
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	ins := func(id string, at time.Time) {
		if _, err := db.Exec(`INSERT INTO mail_seen (from_key, id, received_at) VALUES ('k', ?, ?)`, id, at.Format(StoreTimeFmt)); err != nil {
			t.Fatal(err)
		}
	}
	ins("old", now.Add(-SeenRetention-time.Second))
	ins("edge", now.Add(-SeenRetention+time.Second))
	ins("new", now.Add(-time.Hour))
	n, err := Prune(context.Background(), db, now)
	if err != nil || n != 1 {
		t.Fatalf("Prune = %d, %v; want 1 row", n, err)
	}
	var left []string
	rows, _ := db.Query(`SELECT id FROM mail_seen ORDER BY id`)
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		left = append(left, id)
	}
	_ = rows.Close()
	if !reflect.DeepEqual(left, []string{"edge", "new"}) {
		t.Fatalf("left = %v", left)
	}
	if SeenRetention != 35*24*time.Hour {
		t.Fatal("retention must be 35 days")
	}
}

type auditEvent struct{ action string }

type auditRec struct{ events []auditEvent }

func (a *auditRec) Append(_ context.Context, _, action string, _ any) error {
	a.events = append(a.events, auditEvent{action})
	return nil
}
