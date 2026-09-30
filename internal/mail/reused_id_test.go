package mail

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// Review 55 R55-017 (T9-01): a paired peer re-uses the id of one of its own
// mails after mail_seen was pruned (35 d) while mail_inbox still holds it. The
// id is a duplicate: Apply does not run again, each delivery is acked, and
// nothing an Apply would record is left behind.
func TestReusedIDAfterSeenPruneIsDuplicate(t *testing.T) {
	f := newRecvFixture(t)
	var applies, afters int
	var pending sync.Map // what the kinds' Apply → After hand-over would hold
	f.rcv.Kinds["request"] = Kind{
		Inbox: true,
		Apply: func(_ context.Context, _ *sql.Tx, op *Opened) error {
			applies++
			pending.Store(op, struct{}{})
			return nil
		},
		After: func(_ context.Context, op *Opened) { afters++; pending.Delete(op) },
	}
	ctx := context.Background()
	if err := f.rcv.Handle(ctx, f.mailEnv(testID, "request", map[string]any{"text": "first"})); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	// 36 days later: mail_seen is pruned, mail_inbox is not.
	later := vectorNow.Add(36 * 24 * time.Hour)
	if _, err := Prune(ctx, f.st.DB(), later); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT COUNT(*) FROM mail_seen`); n != 0 {
		t.Fatalf("mail_seen rows after prune = %d, want 0", n)
	}
	f.rcv.Now = func() time.Time { return later }
	f.rcv.Opener.Now = func() time.Time { return later }
	acksBefore := f.out.count()
	for i := 0; i < 5; i++ {
		sl := sealTo(t, f.sender, f.recip, testID, "request", map[string]any{"text": "again"}, later)
		if err := f.rcv.Handle(ctx, env(f.sender, f.recip, sl)); err != nil {
			t.Fatalf("re-used id delivery %d: %v", i, err)
		}
	}
	n := 0
	pending.Range(func(_, _ any) bool { n++; return true })
	if applies != 1 || afters != 1 || n != 0 {
		t.Fatalf("applies=%d afters=%d leaked=%d, want 1, 1, 0", applies, afters, n)
	}
	if got := f.out.count() - acksBefore; got != 5 {
		t.Fatalf("acks for the re-used id = %d, want 5 (each re-acked as a duplicate)", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM mail_inbox`); got != 1 {
		t.Fatalf("mail_inbox rows = %d, want 1", got)
	}
}

// Review 55 R55-058: a receive error that is not a rejection (here a failed
// commit) is logged, but only once per (from, id), not on every redelivery.
func TestReceiveFailureLoggedOncePerID(t *testing.T) {
	f := newRecvFixture(t)
	var buf bytes.Buffer
	f.rcv.Log = slog.New(slog.NewTextHandler(&buf, nil))
	f.rcv.commit = func(*sql.Tx) error { return errors.New("injected commit failure") }
	ctx := context.Background()
	const otherID = "m-00000000000000000000000000000002"
	for i := 0; i < 3; i++ {
		if err := f.rcv.Handle(ctx, f.mailEnv(testID, "request", map[string]any{"text": "x"})); err == nil {
			t.Fatal("commit failure was not reported")
		}
	}
	if got := strings.Count(buf.String(), "event=mail_receive_failed"); got != 1 {
		t.Fatalf("log lines after 3 failures of one id = %d, want 1:\n%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "id="+testID) || !strings.Contains(buf.String(), "stage=commit cause=other") ||
		strings.Contains(buf.String(), "injected") {
		t.Fatalf("log line must name the id, step and class, never the error text:\n%s", buf.String())
	}
	if err := f.rcv.Handle(ctx, f.mailEnv(otherID, "request", nil)); err == nil {
		t.Fatal("commit failure was not reported")
	}
	if got := strings.Count(buf.String(), "event=mail_receive_failed"); got != 2 {
		t.Fatalf("log lines after a second id = %d, want 2", got)
	}
	if f.out.count() != 0 {
		t.Fatal("ack sent although the commit failed")
	}
}

// Review 70 L2: the receive-failure log never carries an Apply error's text,
// so body content cannot reach it even if a kind's plain error quoted it.
func TestReceiveFailureLogCarriesNoErrorText(t *testing.T) {
	f := newRecvFixture(t)
	var buf bytes.Buffer
	f.rcv.Log = slog.New(slog.NewTextHandler(&buf, nil))
	const marker = "BODY-MARKER-7f3a"
	f.rcv.Kinds["request"] = Kind{
		Inbox: true,
		Apply: func(_ context.Context, _ *sql.Tx, op *Opened) error {
			text, _ := op.Msg.Body["text"].(string)
			return errors.New("apply quoted the body: " + text) // not ErrBadBody
		},
	}
	err := f.rcv.Handle(context.Background(), f.mailEnv(testID, "request", map[string]any{"text": marker}))
	if err == nil || !strings.Contains(err.Error(), marker) {
		t.Fatalf("Handle error = %v, want the apply error returned to the caller", err)
	}
	if got := buf.String(); strings.Contains(got, marker) || !strings.Contains(got, "stage=apply cause=other") {
		t.Fatalf("log must carry the step and class only:\n%s", got)
	}
}
