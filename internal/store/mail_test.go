package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func TestMigration5CreatesMailTables(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(testutil.TempDir(t), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for i, m := range migrations {
		if m.version != i+1 {
			t.Fatalf("migration %d has version %d", i+1, m.version)
		}
	}
	for _, q := range []string{
		`INSERT INTO mail_seen (from_key, id, received_at) VALUES ('a', 'm-1', 't')`,
		`INSERT INTO mail_inbox (from_key, id, kind, created, received_at, signed) VALUES ('a', 'm-1', 'k', 'c', 't', '{}')`,
	} {
		if _, err := s.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if _, err := s.DB().ExecContext(ctx, q); err == nil {
			t.Fatalf("duplicate (from_key, id) accepted: %s", q)
		}
	}
}
