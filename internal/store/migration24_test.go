package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Migration 24 (R55-F13, Docs/protocol/retention.md §Migration) adds the cap
// and prune indexes and requests.introducer and introduced_at, and rewrites no data: a
// mail_inbox row written before it keeps its plaintext (agentnet prune
// blanks it, review 71b F4). Opening again is a no-op. Migration 25 lets an
// approval of kind data_prune be stored (test A10 of
// Docs/review/71-r55-f13-spec.md).
func TestMigration24IndexesNoRewrite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(testutil.TempDir(t), "m24.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// Back to schema 23 with a pre-F13 inbox row that holds plaintext.
	for _, q := range []string{
		`DROP INDEX requests_id`,                               // migration 27
		`ALTER TABLE mailbox_keys_own DROP COLUMN key_backend`, // migration 26
		`DROP INDEX mail_inbox_received`, `DROP INDEX requests_introducer_time`, `DROP INDEX requests_introducer_state`,
		`ALTER TABLE requests DROP COLUMN introduced_at`, `ALTER TABLE requests DROP COLUMN introducer`, `DROP INDEX requests_peer_state`,
		`DELETE FROM migrations WHERE version > 23`,
		`INSERT INTO mail_inbox (from_key, id, kind, created, received_at, signed)
		 VALUES ('k', 'm-1', 'request', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00.000Z', '{"secret":"PLAINTEXT"}')`,
	} {
		if _, err := s.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = s.Close()

	for i := 0; i < 2; i++ {
		s, err = Open(ctx, path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		db := s.DB()
		for _, idx := range []string{"requests_peer_state", "requests_introducer_state", "requests_introducer_time", "mail_inbox_received", "requests_id"} {
			var n int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&n); err != nil || n != 1 {
				t.Fatalf("index %s: %d, %v", idx, n, err)
			}
		}
		for _, col := range []string{"introducer", "introduced_at"} {
			var n int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('requests') WHERE name = ?`, col).Scan(&n); err != nil || n != 1 {
				t.Fatalf("requests.%s: %d, %v", col, n, err)
			}
		}
		var signed string
		if err := db.QueryRowContext(ctx, `SELECT signed FROM mail_inbox WHERE id = 'm-1'`).Scan(&signed); err != nil || signed != `{"secret":"PLAINTEXT"}` {
			t.Fatalf("pre-F13 inbox row = %q, %v; want its plaintext kept", signed, err)
		}
		var v int
		if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM migrations`).Scan(&v); err != nil || v != len(migrations) {
			t.Fatalf("schema version %d, %v; want %d", v, err, len(migrations))
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO approvals (id, kind, subject, summary, created, expires, state)
			VALUES (?, 'data_prune', 'n-1', 's', 'c', 'e', 'pending')`, "a-"+string(rune('0'+i))); err != nil {
			t.Fatalf("data_prune approval refused by the kind CHECK: %v", err)
		}
		_ = s.Close()
	}
}
