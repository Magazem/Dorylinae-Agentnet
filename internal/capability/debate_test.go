package capability

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Docs/protocol/debate.md §Quarantine interplay: a sensitive grant to a peer
// with a debate in invited, positions, rounds or converge is debate_open (in
// either role); a public one, or a debate closing, closed or broken, is not.
func TestCheckSensitiveGrant(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	db := st.DB()
	for i, tc := range []struct {
		role, phase string
		open        bool
	}{
		{"initiator", "invited", true}, {"respondent", "invited", true},
		{"initiator", "positions", true}, {"respondent", "rounds", true}, {"initiator", "converge", true},
		{"initiator", "closing", false}, {"respondent", "broken", false}, {"initiator", "closed", false},
	} {
		peer := "peer-" + tc.phase + "-" + tc.role
		outcome := any(nil)
		if tc.phase == "closed" {
			outcome = "cancelled"
		}
		if _, err := db.Exec(`INSERT INTO debates (session, role, peer, request_id, rounds_max, turn_timeout_s, commitment, phase, outcome, created, updated)
			VALUES (?, ?, ?, 'r', 2, 3600, 'c', ?, ?, 'x', 'x')`, "s-"+string(rune('a'+i)), tc.role, peer, tc.phase, outcome); err != nil {
			t.Fatal(err)
		}
		err := CheckSensitiveGrant(ctx, db, peer, true)
		if got := errors.Is(err, ErrDebateOpen); got != tc.open || (err != nil && !got) {
			t.Errorf("%s/%s: err = %v, want open %v", tc.role, tc.phase, err, tc.open)
		}
		if err := CheckSensitiveGrant(ctx, db, peer, false); err != nil {
			t.Errorf("%s/%s: a public grant refused: %v", tc.role, tc.phase, err)
		}
	}
	if err := CheckSensitiveGrant(ctx, db, "another-peer", true); err != nil {
		t.Errorf("no debate: %v", err)
	}
}

// PeerQuarantineHoldsTx is rule 2 alone: a sensitive grant to the peer, ever
// active, with exp later than now - 7 d, in any session.
func TestPeerQuarantineHoldsTx(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	s := &Store{DB: st.DB(), Now: func() time.Time { return now }}
	insert := func(peer, state, approval string, sensitive int, exp time.Time) {
		t.Helper()
		if _, err := st.DB().Exec(`INSERT INTO grants (id, direction, peer, session, action, label, sensitive, nbf, exp, token, state, approval, created, updated)
			VALUES (?, 'issued', ?, 's-x', 'fs.read', 'l', ?, ?, ?, '{}', ?, ?, ?, ?)`,
			NewID(), peer, sensitive, fmtTime(now), fmtTime(exp), state, nullIfEmpty(approval), fmtTime(now), fmtTime(now)); err != nil {
			t.Fatal(err)
		}
	}
	insert("p-active", StateActive, "a", 1, now.Add(-6*24*time.Hour))
	insert("p-old", StateActive, "a", 1, now.Add(-8*24*time.Hour))
	insert("p-public", StateActive, "a", 0, now.Add(time.Hour))
	insert("p-never", StatePendingApproval, "", 1, now.Add(time.Hour))
	insert("p-revoked", StateRevoked, "a", 1, now.Add(time.Hour))
	for peer, want := range map[string]bool{"p-active": true, "p-old": false, "p-public": false, "p-never": false, "p-revoked": true, "p-none": false} {
		tx, err := st.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.PeerQuarantineHoldsTx(ctx, tx, peer)
		_ = tx.Rollback()
		if err != nil || got != want {
			t.Errorf("%s: %v (%v), want %v", peer, got, err, want)
		}
	}
}
