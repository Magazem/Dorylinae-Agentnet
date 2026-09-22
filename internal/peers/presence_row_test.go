package peers_test

import (
	"context"
	"testing"
	"time"
)

func insertPresenceRow(t *testing.T, e *env, key string) {
	t.Helper()
	if _, err := e.db.DB().Exec(`INSERT INTO presence_peers (key, boot, seq, created, state, agent, human, interval, last_rx)
VALUES (?, '0123456789abcdef', 1, '2026-01-02T03:04:05.000Z', 'online', 1, 1, 30, '2026-01-02T03:04:05.000Z')`, key); err != nil {
		t.Fatal(err)
	}
}

func presenceRows(t *testing.T, e *env, key string) int {
	t.Helper()
	var n int
	if err := e.db.DB().QueryRow(`SELECT COUNT(*) FROM presence_peers WHERE key = ?`, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestRemoveDeletesPresenceRow checks presence.md §Tables: "Rows of peers that
// are removed are deleted with the peer", for both Remove and GCIntroduced.
func TestRemoveDeletesPresenceRow(t *testing.T) {
	e := newEnv(t, time.Millisecond)

	a, keyA := memberOf(t, "a")
	introduce(t, e, a, "owner-key")
	insertPresenceRow(t, e, keyA)
	if err := e.store.Remove(context.Background(), keyA); err != nil {
		t.Fatal(err)
	}
	if n := presenceRows(t, e, keyA); n != 0 {
		t.Fatalf("Remove left %d presence rows", n)
	}

	b, keyB := memberOf(t, "b")
	introduce(t, e, b, "owner-key")
	insertPresenceRow(t, e, keyB)
	if removed := gc(t, e); len(removed) != 1 {
		t.Fatalf("gc removed %d, want 1", len(removed))
	}
	if n := presenceRows(t, e, keyB); n != 0 {
		t.Fatalf("GCIntroduced left %d presence rows", n)
	}
}
