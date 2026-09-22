package mail

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Ticket 1.4b: an Apply error wrapping ErrBadBody is recorded in mail_seen only,
// audited bad_body without content, acked as unsupported, and stays deduped.
func TestBadBodyPath(t *testing.T) {
	f := newRecvFixture(t)
	rec := &detailRec{}
	f.rcv.Opener.Audit = NewRejectAudit(rec, nil)
	calls := 0
	secret := "SECRET-BODY-CONTENT"
	f.rcv.Kinds = map[string]Kind{"request": {
		Inbox: true,
		Apply: func(_ context.Context, tx *sql.Tx, _ *Opened) error {
			calls++
			if _, err := tx.ExecContext(context.Background(),
				`INSERT INTO mail_inbox (from_key, id, kind, created, received_at, signed) VALUES ('x','x','request','c','r','s')`); err != nil {
				return err
			}
			return fmt.Errorf("invalid %s: %w", secret, ErrBadBody)
		},
	}}
	ctx := context.Background()
	e := f.mailEnv(testID, "request", map[string]any{"n": secret})
	for i := 0; i < 3; i++ {
		err := f.rcv.Handle(ctx, e)
		if ReasonOf(err) != ReasonBadBody {
			t.Fatalf("delivery %d: err = %v, want bad_body reject", i, err)
		}
	}
	if calls != 1 {
		t.Fatalf("Apply called %d times, want 1 (resends skip it)", calls)
	}
	if n := f.count(`SELECT COUNT(*) FROM mail_inbox`); n != 0 {
		t.Fatalf("inbox rows = %d, want 0 (rolled back)", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM mail_seen`); n != 1 {
		t.Fatalf("mail_seen rows = %d, want 1", n)
	}
	acks := f.ackBodies()
	if len(acks) != 3 {
		t.Fatalf("acks = %d, want 3", len(acks))
	}
	for _, a := range acks {
		if _, has := a["ids"]; has || !reflect.DeepEqual(idsOf(t, a, "unsupported"), []string{testID}) {
			t.Fatalf("ack body = %v, want unsupported only", a)
		}
	}
	want := map[string]string{"action": ActionReject, "peer": f.sender.key, "id": testID, "reason": "bad_body"}
	if len(rec.events) != 1 || !reflect.DeepEqual(rec.events[0], want) {
		t.Fatalf("audit = %v, want [%v]", rec.events, want)
	}
	if strings.Contains(fmt.Sprint(rec.events), secret) {
		t.Fatal("audit leaks body content")
	}
	if len(f.audits.events) != 0 {
		t.Fatalf("mail.in audited for a bad body: %v", f.audits.events)
	}
}

// Any other Apply error keeps the old behaviour: nothing recorded, no ack.
func TestOtherApplyErrorNoAck(t *testing.T) {
	f := newRecvFixture(t)
	f.rcv.Kinds = map[string]Kind{"request": {
		Apply: func(context.Context, *sql.Tx, *Opened) error { return errors.New("db busy") },
	}}
	if err := f.rcv.Handle(context.Background(), f.mailEnv(testID, "request", nil)); err == nil {
		t.Fatal("Apply error was swallowed")
	}
	if f.out.count() != 0 || f.count(`SELECT COUNT(*) FROM mail_seen`) != 0 {
		t.Fatal("other Apply error must leave no mail_seen row and send no ack")
	}
}

// A bad-body row is pruned with the others.
func TestBadBodyRowPruned(t *testing.T) {
	f := newRecvFixture(t)
	f.rcv.Kinds = map[string]Kind{"request": {
		Apply: func(context.Context, *sql.Tx, *Opened) error { return ErrBadBody },
	}}
	_ = f.rcv.Handle(context.Background(), f.mailEnv(testID, "request", nil))
	if n, err := Prune(context.Background(), f.st.DB(), vectorNow.Add(SeenRetention-time.Hour)); err != nil || n != 0 {
		t.Fatalf("Prune before retention = %d, %v; want 0", n, err)
	}
	n, err := Prune(context.Background(), f.st.DB(), vectorNow.Add(SeenRetention+time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("Prune = %d, %v; want 1", n, err)
	}
}

// If another delivery of the same id recorded mail_seen between the two
// bad-body transactions, the stored row decides: no second audit, and the ack
// matches that row.
func TestBadBodyRowRecordedInBetween(t *testing.T) {
	for _, tc := range []struct {
		name, at, member string
		wantErr          bool
	}{
		{"marked", "2026-01-01T00:00:00.000Z" + badBodyMark, "unsupported", true},
		{"accepted", "2026-01-01T00:00:00.000Z", "ids", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecvFixture(t)
			rec := &detailRec{}
			f.rcv.Opener.Audit = NewRejectAudit(rec, nil)
			f.rcv.Kinds = map[string]Kind{"request": {
				Apply: func(context.Context, *sql.Tx, *Opened) error { return ErrBadBody },
			}}
			f.rcv.beforeBad = func() {
				if _, err := f.st.DB().Exec(`INSERT INTO mail_seen (from_key, id, received_at) VALUES (?, ?, ?)`,
					f.sender.key, testID, tc.at); err != nil {
					t.Fatal(err)
				}
			}
			err := f.rcv.Handle(context.Background(), f.mailEnv(testID, "request", nil))
			if (ReasonOf(err) == ReasonBadBody) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			if len(rec.events) != 0 || len(f.audits.events) != 0 {
				t.Fatalf("audited %v %v, want nothing (the other delivery owns the row)", rec.events, f.audits.events)
			}
			acks := f.ackBodies()
			if len(acks) != 1 || !reflect.DeepEqual(idsOf(t, acks[0], tc.member), []string{testID}) {
				t.Fatalf("acks = %v, want %s", acks, tc.member)
			}
			if n := f.count(`SELECT COUNT(*) FROM mail_seen`); n != 1 {
				t.Fatalf("mail_seen rows = %d, want 1", n)
			}
		})
	}
}

type lockedRec struct {
	mu sync.Mutex
	detailRec
}

func (l *lockedRec) Append(ctx context.Context, actor, action string, detail any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.detailRec.Append(ctx, actor, action, detail)
}

// Parallel resends of one bad body: one marked row, one audit, all acked unsupported.
func TestBadBodyParallelResends(t *testing.T) {
	f := newRecvFixture(t)
	rec := &lockedRec{}
	f.rcv.Opener.Audit = NewRejectAudit(rec, nil)
	f.rcv.Audit = nil
	f.rcv.Kinds = map[string]Kind{"request": {
		Apply: func(context.Context, *sql.Tx, *Opened) error { return ErrBadBody },
	}}
	e := f.mailEnv(testID, "request", nil)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = f.rcv.Handle(context.Background(), e)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if ReasonOf(err) != ReasonBadBody {
			t.Fatalf("delivery %d: err = %v, want bad_body", i, err)
		}
	}
	var at string
	if err := f.st.DB().QueryRow(`SELECT received_at FROM mail_seen`).Scan(&at); err != nil || !strings.HasSuffix(at, badBodyMark) {
		t.Fatalf("mail_seen row = %q, %v; want one marked row", at, err)
	}
	if len(rec.events) != 1 {
		t.Fatalf("reject audits = %d, want 1", len(rec.events))
	}
	acks := f.ackBodies()
	if len(acks) != n {
		t.Fatalf("acks = %d, want %d", len(acks), n)
	}
	for _, a := range acks {
		if !reflect.DeepEqual(idsOf(t, a, "unsupported"), []string{testID}) {
			t.Fatalf("ack body = %v, want unsupported", a)
		}
	}
}
