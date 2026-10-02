package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 79: migration 23 rebuilds approvals from a schema-22 table that
// already holds rows; every column of every row survives, and so does the
// index.
func TestReview79Migration23RebuildKeepsRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(testutil.TempDir(t), "m23r.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	rewind := []string{
		`DROP TABLE approvals`,
		`CREATE TABLE approvals (
    id        TEXT PRIMARY KEY,
    kind      TEXT NOT NULL CHECK (kind IN ('grant','grant_policy','release','accept_result','device_link','device_scope','debate_constraint')),
    subject   TEXT NOT NULL,
    summary   TEXT NOT NULL,
    created   TEXT NOT NULL,
    expires   TEXT NOT NULL,
    attempts  INTEGER NOT NULL DEFAULT 0,
    state     TEXT NOT NULL CHECK (state IN ('pending','approved','rejected','expired')),
    decided   TEXT
)`,
		`CREATE INDEX approvals_state ON approvals (state, expires)`,
		`DROP INDEX requests_id`, // migration 27
		`DROP INDEX mail_inbox_received`, `DROP INDEX requests_introducer_time`, `DROP INDEX requests_introducer_state`,
		`ALTER TABLE requests DROP COLUMN introduced_at`, `ALTER TABLE requests DROP COLUMN introducer`, `DROP INDEX requests_peer_state`, // migration 24
		`DELETE FROM migrations WHERE version > 22`,
	}
	for _, q := range rewind {
		if _, err := s.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%.60s: %v", q, err)
		}
	}
	kinds := []string{"grant", "grant_policy", "release", "accept_result", "device_link", "device_scope", "debate_constraint"}
	states := []string{"pending", "approved", "rejected", "expired"}
	want := map[string]string{}
	for i, k := range kinds {
		id := fmt.Sprintf("a-%02d", i)
		var decided any
		if i%2 == 0 {
			decided = fmt.Sprintf("decided-%d", i)
		}
		if _, err := s.DB().ExecContext(ctx, `INSERT INTO approvals (id, kind, subject, summary, created, expires, attempts, state, decided)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, k, "subj-"+id, "sum-"+id, "c-"+id, "e-"+id, i%4, states[i%4], decided); err != nil {
			t.Fatal(err)
		}
		want[id] = fmt.Sprintf("%s|subj-%s|sum-%s|c-%s|e-%s|%d|%s|%v", k, id, id, id, id, i%4, states[i%4], decided)
	}
	_ = s.Close()

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	rows, err := s.DB().QueryContext(ctx, `SELECT id, kind, subject, summary, created, expires, attempts, state, decided FROM approvals`)
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	for rows.Next() {
		var id, k, subj, sum, c, e, st string
		var att int
		var dec *string
		if err := rows.Scan(&id, &k, &subj, &sum, &c, &e, &att, &st, &dec); err != nil {
			t.Fatal(err)
		}
		var decided any
		if dec != nil {
			decided = *dec
		}
		if line := fmt.Sprintf("%s|%s|%s|%s|%s|%d|%s|%v", k, subj, sum, c, e, att, st, decided); line != want[id] {
			t.Errorf("row %s = %s, want %s", id, line, want[id])
		}
		got++
	}
	_ = rows.Close()
	if got != len(kinds) {
		t.Fatalf("%d rows after migration 23, want %d", got, len(kinds))
	}
	var n int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'approvals_state'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("approvals_state index count = %d, err %v", n, err)
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO approvals (id, kind, subject, summary, created, expires, state) VALUES ('new', 'team_invite', 's', 'x', 'c', 'e', 'pending')`); err != nil {
		t.Fatalf("team_invite refused after the rebuild: %v", err)
	}
}
