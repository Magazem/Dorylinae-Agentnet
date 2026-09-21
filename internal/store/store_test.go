package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenAppliesMigrationsOnce(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "t.db")
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
	path := filepath.Join(t.TempDir(), "t.db")
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

// A database created before the trust column existed keeps its peers, all
// as trust=relay with no mailbox keys (Docs/protocol/pairing.md, migration).
func TestMigrationAddsPeerTrust(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// Rewind to schema version 2 and insert a row as the old binary would.
	for _, q := range []string{
		`DROP TABLE mail_seen`,
		`DROP TABLE mail_inbox`,
		`DROP TABLE peers`,
		`CREATE TABLE peers (public_key TEXT PRIMARY KEY, name TEXT NOT NULL, harness TEXT NOT NULL,
			skills TEXT NOT NULL CHECK (json_valid(skills)), card TEXT NOT NULL CHECK (json_valid(card)),
			paired_at TEXT NOT NULL)`,
		`DROP TABLE pair_used_codes`,
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
