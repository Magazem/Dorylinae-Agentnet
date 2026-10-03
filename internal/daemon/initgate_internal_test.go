package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 79 M1: while invisible the daemon still opens a session for a peer
// that holds a live grant from it or shares an open work session with it, and
// for no other peer.
func TestPeerHasLiveTies(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	db := st.DB()
	const tf = "2006-01-02T15:04:05.000Z"
	now := time.Now().UTC()
	grant := func(id, direction, peer, state string, exp time.Time) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO grants (id, direction, peer, session, action, label, sensitive, nbf, exp, token, state, created, updated)
			VALUES (?, ?, ?, 's-1', 'fs.read', 'l', 0, ?, ?, 'tok', ?, ?, ?)`,
			id, direction, peer, now.Add(-time.Hour).Format(tf), exp.Format(tf), state, now.Format(tf), now.Format(tf)); err != nil {
			t.Fatal(err)
		}
	}
	session := func(id, peer, state string) {
		t.Helper()
		outcome := any(nil)
		if state == "closed" {
			outcome = "cancelled"
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO work_sessions (id, role, peer, request_id, team_id, state, outcome, opened, state_at, updated)
			VALUES (?, 'worker', ?, ?, 't-1', ?, ?, ?, ?, ?)`, id, peer, "r-"+id, state, outcome, now.Format(tf), now.Format(tf), now.Format(tf)); err != nil {
			t.Fatal(err)
		}
	}
	grant("g-live", "issued", "granted", "active", now.Add(time.Hour))
	grant("g-expired", "issued", "expired", "active", now.Add(-time.Minute))
	grant("g-revoked", "issued", "revoked", "revoked", now.Add(time.Hour))
	grant("g-held", "held", "holder-of-nothing", "active", now.Add(time.Hour)) // a grant we hold, not one we gave
	session("s-open", "sessioned", "open")
	session("s-closed", "done", "closed")

	for peer, want := range map[string]bool{
		"granted": true, "sessioned": true,
		"expired": false, "revoked": false, "holder-of-nothing": false, "done": false, "stranger": false,
	} {
		if got := peerHasLiveTies(ctx, db, peer, time.Now()); got != want {
			t.Errorf("peerHasLiveTies(%q) = %v, want %v", peer, got, want)
		}
	}
}
