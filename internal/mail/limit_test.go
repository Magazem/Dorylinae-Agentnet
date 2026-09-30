package mail

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

// R55-F13 (Docs/protocol/request.md §Per-peer caps, test A6 of
// Docs/review/71-r55-f13-spec.md): an Apply error wrapping ErrLimit takes the
// bad-body path with reason limit: only the marked mail_seen row, audited
// mail.reject limit, acked rejected, and a relay replay is re-acked rejected
// without calling Apply again.
func TestLimitPath(t *testing.T) {
	f := newRecvFixture(t)
	rec := &detailRec{}
	f.rcv.Opener.Audit = NewRejectAudit(rec, nil)
	calls := 0
	f.rcv.Kinds = map[string]Kind{"request": {
		Inbox: true,
		Apply: func(context.Context, *sql.Tx, *Opened) error {
			calls++
			return fmt.Errorf("request: sender over the daily cap: %w", ErrLimit)
		},
	}}
	ctx := context.Background()
	e := f.mailEnv(testID, "request", nil)
	for i := 0; i < 3; i++ {
		// The first delivery is refused for the limit; a replay is re-acked
		// from the marked mail_seen row (reported like any marked reject).
		want := ReasonLimit
		if i > 0 {
			want = ReasonBadBody
		}
		if err := f.rcv.Handle(ctx, e); ReasonOf(err) != want {
			t.Fatalf("delivery %d: err = %v, want reject %s", i, err, want)
		}
	}
	if calls != 1 {
		t.Fatalf("Apply called %d times, want 1 (replays are re-acked without it)", calls)
	}
	if n := f.count(`SELECT COUNT(*) FROM mail_inbox`); n != 0 {
		t.Fatalf("inbox rows = %d, want 0", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM mail_seen`); n != 1 {
		t.Fatalf("mail_seen rows = %d, want 1", n)
	}
	acks := f.ackBodies()
	if len(acks) != 3 {
		t.Fatalf("acks = %d, want 3", len(acks))
	}
	for _, a := range acks {
		if len(a) != 1 || !reflect.DeepEqual(idsOf(t, a, AckRejected), []string{testID}) {
			t.Fatalf("ack body = %v, want rejected only", a)
		}
	}
	want := map[string]string{"action": ActionReject, "peer": f.sender.key, "id": testID, "reason": ReasonLimit}
	if len(rec.events) != 1 || !reflect.DeepEqual(rec.events[0], want) {
		t.Fatalf("audit = %v, want [%v]", rec.events, want)
	}
	if len(f.audits.events) != 0 {
		t.Fatalf("mail.in audited for a refused mail: %v", f.audits.events)
	}
}

// Every stored inbox row is blank, whatever the kind (Docs/protocol/mail.md
// §Inbox rows, R55-F13), including a kind whose Apply does not ask to
// withhold its content.
func TestInboxRowAlwaysBlank(t *testing.T) {
	f := newRecvFixture(t)
	f.rcv.Kinds = map[string]Kind{"note": {Inbox: true}}
	if err := f.rcv.Handle(context.Background(), f.mailEnv(testID, "note", map[string]any{"text": "MARKER-PLAINTEXT"})); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT COUNT(*) FROM mail_inbox WHERE signed = ''`); n != 1 {
		t.Fatalf("blank inbox rows = %d, want 1", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM mail_inbox WHERE signed LIKE '%MARKER%'`); n != 0 {
		t.Fatal("the plaintext was stored in mail_inbox")
	}
}
