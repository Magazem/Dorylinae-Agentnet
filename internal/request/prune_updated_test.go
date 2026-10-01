package request

// Review 81b M1 (Docs/protocol/retention.md §Approval): the sender mirror
// sets updated to the local time of the change, so an out request sent long
// ago and finished now is not older than a prune's cutoff, nor is its result.

import (
	"context"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/retention"
)

func TestMirrorFinalSetsUpdated(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		extra map[string]any
		state string
	}{
		{KindComplete, nil, StateCompleted},
		{KindDecline, map[string]any{"code": "inbox_full"}, StateDeclined},
		{KindCancelled, nil, StateCancelled},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			s, _, _ := newTestStore(t, testFrom, &policy{})
			ctx := context.Background()
			out, err := s.Submit(ctx, submitParams("", ""))
			if err != nil {
				t.Fatal(err)
			}
			id := out.Request.ID
			// Sent, and last changed locally, 40 days ago.
			old := storeTime(time.Now().Add(-40 * 24 * time.Hour))
			if _, err := s.DB.Exec(`UPDATE requests SET updated = ? WHERE direction = 'out' AND id = ?`, old, id); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			body := map[string]any{"at": wireTime(now), "request": id, "seq": 2}
			for k, v := range tc.extra {
				body[k] = v
			}
			if err := deliverMirror(t, s, tc.kind, testTo, now, body); err != nil {
				t.Fatal(err)
			}
			var state, updated string
			if err := s.DB.QueryRow(`SELECT state, updated FROM requests WHERE direction = 'out' AND id = ?`, id).Scan(&state, &updated); err != nil {
				t.Fatal(err)
			}
			if state != tc.state || updated == old || parseStoreTime(updated).Before(now.Add(-time.Minute)) {
				t.Fatalf("after %s now: state %s, updated %s (was %s); want %s, updated now", tc.kind, state, updated, old, tc.state)
			}
			// A 40-day-old out request finished today is not in a 35 d prune.
			c, err := retention.DryRun(ctx, s.DB, retention.Cutoff(now, retention.MinOlderThan), now)
			if err != nil {
				t.Fatal(err)
			}
			if c.Requests != 0 {
				t.Fatalf("a 35 d prune counts %d requests, want 0", c.Requests)
			}
			tx, err := s.DB.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			removed, _, err := retention.PruneTx(ctx, tx, retention.Cutoff(now, retention.MinOlderThan), now)
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			var n int
			if err := s.DB.QueryRow(`SELECT COUNT(*) FROM requests WHERE direction = 'out' AND id = ?`, id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if removed.Requests != 0 || n != 1 {
				t.Fatalf("a 35 d prune removed %d requests (row left: %d), want the request kept", removed.Requests, n)
			}
		})
	}
}

// SetOutContentTx (A's session close writing its view of the result into a
// completed out row) sets updated too.
func TestSetOutContentSetsUpdated(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	ctx := context.Background()
	out, err := s.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	id := out.Request.ID
	old := storeTime(time.Now().Add(-40 * 24 * time.Hour))
	if _, err := s.DB.Exec(`UPDATE requests SET state = ?, updated = ? WHERE direction = 'out' AND id = ?`, StateCompleted, old, id); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.SetOutContentTx(ctx, tx, testTo, id, "note", nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var updated string
	if err := s.DB.QueryRow(`SELECT updated FROM requests WHERE direction = 'out' AND id = ?`, id).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if updated == old {
		t.Fatalf("updated still %s after SetOutContentTx", updated)
	}
}
