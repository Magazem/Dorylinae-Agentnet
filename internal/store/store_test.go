package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func TestOpenAppliesMigrationsOnce(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(testutil.TempDir(t), "t.db")
	for i := 0; i < 2; i++ {
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		var n int
		if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM migrations`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != len(migrations) {
			t.Fatalf("migrations rows = %d, want %d", n, len(migrations))
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(testutil.TempDir(t), "t.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO migrations VALUES (999, 'future', 'x')`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := Open(ctx, path); err == nil {
		t.Fatal("expected error for newer schema")
	}
}

// Migration 8 rebuilds peers: every row keeps all columns, at every trust level.
func TestMigration8PreservesPeers(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(testutil.TempDir(t), "m8.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// Rewind to schema version 7 with the pre-8 peers table.
	for _, q := range []string{
		`DROP TABLE peers`,
		`CREATE TABLE peers (public_key TEXT PRIMARY KEY, name TEXT NOT NULL, harness TEXT NOT NULL,
			skills TEXT NOT NULL CHECK (json_valid(skills)), card TEXT NOT NULL CHECK (json_valid(card)),
			paired_at TEXT NOT NULL, trust TEXT NOT NULL DEFAULT 'relay'
			CHECK (trust IN ('relay', 'code', 'fingerprint')),
			mailbox_keys TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(mailbox_keys)))`,
		`DROP TABLE teams`,
		`DROP TABLE team_members`,
		`DROP TABLE team_invites`,
		`DROP TABLE team_pending_joins`,
		`DROP TABLE presence_peers`,
		`DROP TABLE settings`,
		`DROP TABLE requests`,
		`DROP TABLE request_cancels`,
		`DROP TABLE webhook_queue`,
		`DROP TABLE work_sessions`,
		`DROP TABLE approvals`,
		`DROP TABLE grant_policies`,
		`DROP TABLE grants`,
		`DROP TABLE device_links`,
		`DROP TABLE device_scopes`,
		`DROP TABLE device_offers`,
		`DROP TABLE audit_events`, // migration 18 alters it: recreate the migration-1 form
		migrations[0].sql,
		`DROP TABLE debates`,
		`DROP TABLE debate_entries`,
		`DROP TABLE debate_constraints`,
		`DELETE FROM migrations WHERE version > 7`,
		`INSERT INTO peers VALUES ('k1', 'n1', 'h1', '[{"id":"s"}]', '{"a":1}', '2026-01-02T03:04:05Z', 'relay', '[]')`,
		`INSERT INTO peers VALUES ('k2', 'n2', 'h2', '[]', '{"b":2}', '2026-02-02T03:04:05Z', 'code', '[{"x":1}]')`,
		`INSERT INTO peers VALUES ('k3', 'n3', 'h3', '[]', '{}', '2026-03-02T03:04:05Z', 'fingerprint', '[{"y":1},{"z":2}]')`,
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
	db := s.DB()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM peers`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("peer rows = %d (%v), want 3", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'peers_new'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("peers_new left behind (%d, %v)", n, err)
	}
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check = %q (%v)", integrity, err)
	}
	for key, want := range map[string][7]string{
		"k1": {"n1", "h1", `[{"id":"s"}]`, `{"a":1}`, "2026-01-02T03:04:05Z", "relay", "[]"},
		"k2": {"n2", "h2", "[]", `{"b":2}`, "2026-02-02T03:04:05Z", "code", `[{"x":1}]`},
		"k3": {"n3", "h3", "[]", "{}", "2026-03-02T03:04:05Z", "fingerprint", `[{"y":1},{"z":2}]`},
	} {
		var got [7]string
		var by sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT name, harness, skills, card, paired_at, trust, mailbox_keys, introduced_by
			FROM peers WHERE public_key = ?`, key).Scan(&got[0], &got[1], &got[2], &got[3], &got[4], &got[5], &got[6], &by); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if got != want || by.Valid {
			t.Fatalf("%s changed: %q introduced_by=%v, want %q", key, got, by, want)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE peers SET trust = 'team' WHERE public_key = 'k1'`); err != nil {
		t.Errorf("trust 'team' rejected: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE peers SET trust = 'bogus'`); err == nil {
		t.Error("CHECK accepted an unknown trust value")
	}
}

// A database created before the trust column existed keeps its peers, all
// as trust=relay with no mailbox keys (Docs/protocol/pairing.md, migration).
func TestMigrationAddsPeerTrust(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(testutil.TempDir(t), "old.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// Rewind to schema version 2 and insert a row as the old binary would.
	for _, q := range []string{
		`DROP TABLE mail_seen`,
		`DROP TABLE mail_inbox`,
		`DROP TABLE mailbox_keys_own`,
		`DROP TABLE outbox`,
		`DROP TABLE requests`,
		`DROP TABLE request_cancels`,
		`DROP TABLE webhook_queue`,
		`DROP TABLE work_sessions`,
		`DROP TABLE approvals`,
		`DROP TABLE grant_policies`,
		`DROP TABLE grants`,
		`DROP TABLE device_links`,
		`DROP TABLE device_scopes`,
		`DROP TABLE device_offers`,
		`DROP TABLE debates`,
		`DROP TABLE debate_entries`,
		`DROP TABLE debate_constraints`,
		`DROP TABLE peers`,
		`CREATE TABLE peers (public_key TEXT PRIMARY KEY, name TEXT NOT NULL, harness TEXT NOT NULL,
			skills TEXT NOT NULL CHECK (json_valid(skills)), card TEXT NOT NULL CHECK (json_valid(card)),
			paired_at TEXT NOT NULL)`,
		`DROP TABLE pair_used_codes`,
		`DROP TABLE teams`,
		`DROP TABLE team_members`,
		`DROP TABLE team_invites`,
		`DROP TABLE team_pending_joins`,
		`DROP TABLE presence_peers`,
		`DROP TABLE settings`,
		`DROP TABLE audit_events`, // migration 18 alters it: recreate the migration-1 form
		migrations[0].sql,
		`DELETE FROM migrations WHERE version > 2`,
		`INSERT INTO peers VALUES ('k1', 'old', 'h', '[]', '{}', '2026-01-02T03:04:05Z')`,
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
	var trust, mbox string
	if err := s.DB().QueryRowContext(ctx, `SELECT trust, mailbox_keys FROM peers WHERE public_key = 'k1'`).Scan(&trust, &mbox); err != nil {
		t.Fatal(err)
	}
	if trust != "relay" || mbox != "[]" {
		t.Fatalf("trust = %q, mailbox_keys = %q; want relay, []", trust, mbox)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE peers SET trust = 'bogus'`); err == nil {
		t.Error("CHECK constraint accepted an unknown trust value")
	}
}

// Review 43 M9: agentnetd install and the daemon both open the store right
// after an upgrade. Two concurrent Opens at schema 17 must both succeed and
// apply migration 18 once.
func TestConcurrentOpenAppliesMigrationsOnce(t *testing.T) {
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		path := filepath.Join(testutil.TempDir(t), fmt.Sprintf("c%d.db", i))
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{`DROP TABLE audit_events`, migrations[0].sql, `DELETE FROM migrations WHERE version > 17`} {
			if _, err := s.DB().ExecContext(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		_ = s.Close()

		var wg sync.WaitGroup
		errs := make([]error, 2)
		stores := make([]*Store, 2)
		for j := range errs {
			wg.Add(1)
			go func(j int) {
				defer wg.Done()
				stores[j], errs[j] = Open(ctx, path)
			}(j)
		}
		wg.Wait()
		for _, st := range stores {
			if st != nil {
				t.Cleanup(func() { _ = st.Close() })
			}
		}
		for j, err := range errs {
			if err != nil {
				t.Fatalf("round %d: open #%d: %v", i, j, err)
			}
		}
		db := stores[0].DB()
		var n, v18, hashCols, triggers int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE version = 18) FROM migrations`).Scan(&n, &v18); err != nil {
			t.Fatal(err)
		}
		if n != len(migrations) || v18 != 1 {
			t.Fatalf("round %d: migrations rows = %d (v18: %d), want %d (1)", i, n, v18, len(migrations))
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('audit_events') WHERE name = 'hash'`).Scan(&hashCols); err != nil || hashCols != 1 {
			t.Fatalf("round %d: hash columns = %d (%v)", i, hashCols, err)
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = 'audit_events_chained'`).Scan(&triggers); err != nil || triggers != 1 {
			t.Fatalf("round %d: chained triggers = %d (%v)", i, triggers, err)
		}
	}
}

// Migration 18: the trigger refuses an unchained row and the CHECK a
// malformed hash.
func TestMigration18RefusesUnchainedRows(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(testutil.TempDir(t), "m18.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO audit_events (ts, actor, action, detail) VALUES ('t', 'a', 'x', '{}')`); err == nil ||
		!strings.Contains(err.Error(), "must be chained") {
		t.Fatalf("unchained insert: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO audit_events (id, ts, actor, action, detail, hash) VALUES (1, 't', 'a', 'x', '{}', ?)`,
		strings.Repeat("A", 64)); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("uppercase hash: %v", err)
	}

	// Review 44 L1: only the new head may be inserted. INSERT OR REPLACE on
	// an existing id would delete the old row without the delete trigger.
	h := strings.Repeat("a", 64)
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO audit_events (id, ts, actor, action, detail, hash) VALUES (1, 't', 'a', 'x', '{}', ?)`, h); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT OR REPLACE INTO audit_events (id, ts, actor, action, detail, hash) VALUES (1, 't', 'a', 'forged', '{}', ?)`,
		`REPLACE INTO audit_events (id, ts, actor, action, detail, hash) VALUES (1, 't', 'a', 'forged', '{}', ?)`,
		`INSERT INTO audit_events (id, ts, actor, action, detail, hash) VALUES (3, 't', 'a', 'gap', '{}', ?)`,
		`INSERT INTO audit_events (ts, actor, action, detail, hash) VALUES ('t', 'a', 'no_id', '{}', ?)`,
	} {
		if _, err := s.DB().ExecContext(ctx, q, h); err == nil || !strings.Contains(err.Error(), "must be chained") {
			t.Errorf("%s: %v", q, err)
		}
	}
	var action string
	if err := s.DB().QueryRowContext(ctx, `SELECT group_concat(action) FROM audit_events`).Scan(&action); err != nil || action != "x" {
		t.Fatalf("rows = %q (%v), want only the original", action, err)
	}
}
