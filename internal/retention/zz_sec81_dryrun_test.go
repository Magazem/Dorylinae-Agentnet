package retention

// Security review 81 (R55-F13), finding M2: the dry run (run by every
// `prune`, by the approval's creation and again by its Rebuild inside the
// confirm write transaction) is unbounded and issues two queries per
// finished request on the daemon's single connection.

import (
	"context"
	"testing"
	"time"
)

func TestSec81DryRunCost(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	db := openDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	const n = 40000 // 200 a day per peer for 200 days (one flooding peer)
	for i := 0; i < n; i++ {
		if _, err := tx.Exec(`INSERT INTO requests (direction, peer, id, team_id, type, urgency, urgency_declared, body, body_hash,
			state, created, received_at, mail_id, updated) VALUES ('in', 'P', ?, 't', 'task', 'normal', 'normal', '{}', 'h', 'declined', ?, ?, 'm', ?)`,
			rid(i), ago(40), ago(40), ago(40)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	c, err := DryRun(context.Background(), db, now.Add(-35*24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("dry run over %d finished requests: %v (counts %d)", n, time.Since(start), c.Requests)
}
