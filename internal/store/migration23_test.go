package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Migration 23 widens the approvals kind CHECK to peer_verify and team_invite
// (D48) without losing a row or the index.
func TestMigration23ApprovalKinds(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(testutil.TempDir(t), "m23.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ins := func(id, kind string) error {
		_, err := s.DB().ExecContext(ctx, `INSERT INTO approvals (id, kind, subject, summary, created, expires, state)
			VALUES (?, ?, 's', 'x', '2026-01-01T00:00:00.000Z', '2026-01-01T00:10:00.000Z', 'pending')`, id, kind)
		return err
	}
	for i, kind := range []string{"grant", "debate_constraint", "peer_verify", "team_invite"} {
		if err := ins(string(rune('a'+i)), kind); err != nil {
			t.Errorf("kind %q refused: %v", kind, err)
		}
	}
	if err := ins("z", "bogus"); err == nil {
		t.Error("an unknown approval kind was accepted")
	}
	var n int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'approvals_state'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("approvals_state index count = %d, err %v", n, err)
	}
}
