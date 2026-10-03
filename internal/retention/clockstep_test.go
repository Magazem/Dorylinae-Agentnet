package retention

import (
	"context"
	"testing"
	"time"
)

// R55-F28 (review 55 R55-053): agentnet prune uses the mail_seen cutoff of
// mail.SeenCutoff. Run while the clock is stepped 40 days forward, it removes
// neither the mail_seen row of a 5-day-old mail nor its mail_inbox row, so a
// relay replay after the correction is still a duplicate.
func TestPruneDuringForwardStepKeepsDedupe(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	mustExec(t, db, `INSERT INTO mail_inbox (from_key, id, kind, created, received_at, signed) VALUES ('P', 'm-1', 'request', 'c', ?, '')`, ago(5))
	mustExec(t, db, `INSERT INTO mail_seen (from_key, id, received_at) VALUES ('P', 'm-1', ?)`, ago(5))

	stepped := now.Add(40 * 24 * time.Hour)
	cutoff := Cutoff(stepped, MinOlderThan)
	dry, err := DryRun(ctx, db, cutoff, stepped)
	if err != nil {
		t.Fatal(err)
	}
	if dry.MailInbox != 0 {
		t.Fatalf("dry run counts %d mail_inbox rows, want 0", dry.MailInbox)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PruneTx(ctx, tx, cutoff, stepped); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`SELECT COUNT(*) FROM mail_seen WHERE id = 'm-1'`, `SELECT COUNT(*) FROM mail_inbox WHERE id = 'm-1'`} {
		if n := count(t, db, q); n != 1 {
			t.Errorf("%s = %d, want 1", q, n)
		}
	}
}
