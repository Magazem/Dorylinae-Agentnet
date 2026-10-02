package mail

import (
	"context"
	"database/sql"
	"testing"
)

// Review 55 R55-100: shutdown cancelling the receive context right after a mail
// committed must not lose its After effects or its ack; no resend would redo
// them (the mail is already in mail_seen).
func TestAfterAndAckSurviveCancelAfterCommit(t *testing.T) {
	f := newRecvFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var afters int
	var afterErr error
	f.rcv.Kinds["request"] = Kind{
		Inbox: true,
		After: func(c context.Context, _ *Opened) { afters++; afterErr = c.Err() },
	}
	f.rcv.commit = func(tx *sql.Tx) error {
		err := tx.Commit()
		cancel() // shutdown lands between the commit and the After hook
		return err
	}
	acksBefore := f.out.count()
	if err := f.rcv.Handle(ctx, f.mailEnv(testID, "request", map[string]any{"text": "x"})); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if afters != 1 || afterErr != nil {
		t.Fatalf("afters=%d ctx err=%v, want 1 and a live context", afters, afterErr)
	}
	if got := f.out.count() - acksBefore; got != 1 {
		t.Fatalf("acks = %d, want 1", got)
	}
}
