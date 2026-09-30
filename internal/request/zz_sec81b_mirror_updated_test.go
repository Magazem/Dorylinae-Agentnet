package request

// Review 81b proof: the sender mirror moves an out row to a final state
// without setting updated, so a row sent long ago and completed just now is
// already inside a prune's fixed cutoff, with its fresh result.

import (
	"context"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/retention"
)

func TestSec81bMirrorLeavesUpdatedOld(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	ctx := context.Background()
	out, err := s.Submit(ctx, submitParams("", ""))
	if err != nil {
		t.Fatal(err)
	}
	id := out.Request.ID
	// Sent (and last changed locally) 40 days ago; the peer deferred it.
	old := storeTime(time.Now().Add(-40 * 24 * time.Hour))
	if _, err := s.DB.Exec(`UPDATE requests SET updated = ? WHERE direction = 'out' AND id = ?`, old, id); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	body := map[string]any{"at": wireTime(now), "request": id, "seq": 2}
	if err := deliverMirror(t, s, KindComplete, testTo, now, body); err != nil {
		t.Fatal(err)
	}
	var state, updated string
	if err := s.DB.QueryRow(`SELECT state, updated FROM requests WHERE direction = 'out' AND id = ?`, id).Scan(&state, &updated); err != nil {
		t.Fatal(err)
	}
	t.Logf("after complete just now: state=%s updated=%s", state, updated)
	c, err := retention.DryRun(ctx, s.DB, retention.Cutoff(now, retention.MinOlderThan), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("prune at 35 d would remove %d requests", c.Requests)
	if updated == old || c.Requests != 0 {
		t.Errorf("a request completed just now is in a 35 d prune (updated %s, count %d)", updated, c.Requests)
	}
}
