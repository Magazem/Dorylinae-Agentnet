package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Migration 22 (R55-F18, Docs/protocol/work-session.md §Persistence): a
// fresh database has work_sessions.runner with default 0; upgrading a
// database at schema 21 keeps its rows, with runner = 0; the column takes
// only 0 or 1.
func TestMigration22Runner(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(testutil.TempDir(t), "m22.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var dflt string
	var notNull int
	if err := s.DB().QueryRowContext(ctx, `SELECT dflt_value, "notnull" FROM pragma_table_info('work_sessions') WHERE name = 'runner'`).Scan(&dflt, &notNull); err != nil {
		t.Fatalf("fresh database has no runner column: %v", err)
	}
	if dflt != "0" || notNull != 1 {
		t.Fatalf("runner default %q, not null %d; want 0, 1", dflt, notNull)
	}

	// Back to schema 21 with one row, then reopen.
	for _, q := range []string{
		`DROP INDEX requests_id`,                               // migration 27
		`ALTER TABLE mailbox_keys_own DROP COLUMN key_backend`, // migration 26
		`DROP INDEX mail_inbox_received`, `DROP INDEX requests_introducer_time`, `DROP INDEX requests_introducer_state`,
		`ALTER TABLE requests DROP COLUMN introduced_at`, `ALTER TABLE requests DROP COLUMN introducer`, `DROP INDEX requests_peer_state`, // migration 24
		`ALTER TABLE work_sessions DROP COLUMN runner`, `ALTER TABLE work_sessions DROP COLUMN result_mail`,
		`DELETE FROM migrations WHERE version > 21`,
		`INSERT INTO work_sessions (id, role, peer, request_id, team_id, state, seq, round, opened, state_at, updated, kind)
		 VALUES ('s-00000000000000000000000000000001', 'worker', 'p', 'r-00000000000000000000000000000001', 't', 'open', 1, 1,
		 '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00.000Z', 'work')`,
	} {
		if _, err := s.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = s.Close()
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var runner, n int
	if err := s.DB().QueryRowContext(ctx, `SELECT runner FROM work_sessions WHERE id = 's-00000000000000000000000000000001'`).Scan(&runner); err != nil || runner != 0 {
		t.Fatalf("upgraded row runner = %d, %v; want 0", runner, err)
	}
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM migrations WHERE version = 22`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("migration 22 rows = %d, %v", n, err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE work_sessions SET runner = 2`); err == nil {
		t.Fatal("CHECK accepted runner = 2")
	}
}
