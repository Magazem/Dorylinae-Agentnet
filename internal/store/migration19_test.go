package store

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// requestColumns19 is every column of requests in its migration 11 + 12
// physical order (result last).
var requestColumns19 = []string{"direction", "peer", "id", "team_id", "type", "urgency", "urgency_declared",
	"downgraded_by", "body", "body_hash", "state", "state_seq", "state_at", "deferred_until", "decline_code",
	"reason", "note", "first_response", "first_response_at", "created", "received_at", "mail_id",
	"last_reply", "last_reply_sent", "idem_key", "params_hash", "cancel", "cancel_at", "cancel_mail_id",
	"updated", "result"}

var approvalColumns19 = []string{"id", "kind", "subject", "summary", "created", "expires", "attempts", "state", "decided"}

// snapshot returns every row of table as one string per row, each column
// rendered with quote() (so NULL, integers and text stay distinct), keyed by
// the row's primary key text.
func snapshot(t *testing.T, s *Store, table string, cols []string, key string) map[string]string {
	t.Helper()
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = "quote(" + c + ")"
	}
	rows, err := s.DB().Query(`SELECT ` + key + `, ` + strings.Join(parts, " || '|' || ") + ` FROM ` + table)
	if err != nil {
		t.Fatalf("snapshot %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

var spaces = regexp.MustCompile(`\s+`)

// indexes returns the normalised CREATE INDEX text of every index on table.
func indexes(t *testing.T, s *Store, table string) []string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT name, COALESCE(sql, '') FROM sqlite_master WHERE type = 'index' AND tbl_name = ? ORDER BY name`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var name, q string
		if err := rows.Scan(&name, &q); err != nil {
			t.Fatal(err)
		}
		out = append(out, name+": "+strings.TrimSpace(spaces.ReplaceAllString(q, " ")))
	}
	sort.Strings(out)
	return out
}

// Migration 19 rebuilds requests (type debate) and approvals (kind
// debate_constraint) with explicit column lists (review 43 M10): every column
// of every row, in every state, survives, as do the indexes, and the unique
// partial idempotency index still refuses a duplicate.
func TestMigration19RebuildKeepsRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(testutil.TempDir(t), "m19.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// Rewind to schema 18: requests as migrations 11 + 12 left it (result the
	// last physical column), approvals as migration 15, work_sessions without
	// kind, and no debate tables.
	rewind := []string{
		`DROP TABLE requests`,
		`DROP TABLE request_cancels`,
		migrations[10].sql,
		migrations[11].sql,
		`DROP TABLE approvals`,
		`CREATE TABLE approvals (
    id        TEXT PRIMARY KEY,
    kind      TEXT NOT NULL CHECK (kind IN ('grant','grant_policy','release','accept_result','device_link','device_scope')),
    subject   TEXT NOT NULL,
    summary   TEXT NOT NULL,
    created   TEXT NOT NULL,
    expires   TEXT NOT NULL,
    attempts  INTEGER NOT NULL DEFAULT 0,
    state     TEXT NOT NULL CHECK (state IN ('pending','approved','rejected','expired')),
    decided   TEXT
)`,
		`CREATE INDEX approvals_state ON approvals (state, expires)`,
		`DROP TABLE work_sessions`,
		migrations[13].sql,
		`DROP TABLE debates`,
		`DROP TABLE debate_entries`,
		`DROP TABLE debate_constraints`,
		`DROP TABLE decisions`,
		`DELETE FROM migrations WHERE version > 18`,
	}
	for _, q := range rewind {
		if _, err := s.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%.60s: %v", q, err)
		}
	}
	var physical []string
	rows, err := s.DB().Query(`SELECT name FROM pragma_table_info('requests') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		physical = append(physical, n)
	}
	_ = rows.Close()
	if strings.Join(physical, ",") != strings.Join(requestColumns19, ",") {
		t.Fatalf("rewound requests columns = %v", physical)
	}

	// One row per (direction, state), every column filled with a distinct value.
	states := []string{"pending", "accepted", "declined", "deferred", "completed", "cancelled"}
	urg := []string{"low", "normal", "high", "blocking"}
	n := 0
	for _, dir := range []string{"in", "out"} {
		for i, st := range states {
			n++
			var idem, ph any
			if dir == "out" {
				idem, ph = fmt.Sprintf("key-%d", n), fmt.Sprintf("ph-%d", n)
			}
			typ := []string{"review", "task", "question"}[n%3]
			if _, err := s.DB().ExecContext(ctx, `INSERT INTO requests (`+strings.Join(requestColumns19, ", ")+`)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				dir, fmt.Sprintf("peer-%d", n), fmt.Sprintf("r-%032x", n), fmt.Sprintf("t-%032x", n), typ,
				urg[i%4], urg[(i+1)%4], []string{"sender", "receiver"}[n%2],
				fmt.Sprintf(`{"body":%d}`, n), fmt.Sprintf("hash-%d", n), st, 100+n, fmt.Sprintf("state_at-%d", n),
				fmt.Sprintf("until-%d", n), fmt.Sprintf("code-%d", n), fmt.Sprintf("reason-%d", n), fmt.Sprintf("note-%d", n),
				[]string{"accept", "decline", "defer"}[n%3], fmt.Sprintf("fr_at-%d", n), fmt.Sprintf("created-%d", n),
				fmt.Sprintf("recv-%d", n), fmt.Sprintf("m-%d", n), fmt.Sprintf(`{"reply":%d}`, n), fmt.Sprintf("sent-%d", n),
				idem, ph, []string{"requested", "refused"}[n%2], fmt.Sprintf("cancel_at-%d", n), fmt.Sprintf("cm-%d", n),
				fmt.Sprintf("updated-%d", n), fmt.Sprintf(`{"result":%d}`, n)); err != nil {
				t.Fatalf("insert request %d: %v", n, err)
			}
		}
	}
	// A row with every nullable column NULL.
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO requests (direction, peer, id, team_id, type, urgency, urgency_declared,
		body, body_hash, state, created, mail_id, updated) VALUES ('in', 'p0', 'r-0', 't-0', 'task', 'low', 'low', '{}', 'h', 'pending', 'c', 'm', 'u')`); err != nil {
		t.Fatal(err)
	}
	kinds := []string{"grant", "grant_policy", "release", "accept_result", "device_link", "device_scope"}
	for i, st := range []string{"pending", "approved", "rejected", "expired"} {
		for j, k := range kinds {
			id := fmt.Sprintf("a-%d-%d", i, j)
			var decided any
			if st != "pending" {
				decided = "decided-" + id
			}
			if _, err := s.DB().ExecContext(ctx, `INSERT INTO approvals (`+strings.Join(approvalColumns19, ", ")+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id, k, "subj-"+id, "summary-"+id, "created-"+id, "expires-"+id, i*10+j, st, decided); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO work_sessions (id, role, peer, request_id, team_id, state, opened, state_at, updated)
		VALUES ('s-1', 'worker', 'p', 'r-1', 't', 'open', 'o', 's', 'u')`); err != nil {
		t.Fatal(err)
	}
	beforeReq := snapshot(t, s, "requests", requestColumns19, "direction || peer || id")
	beforeAppr := snapshot(t, s, "approvals", approvalColumns19, "id")
	beforeIdx := append(indexes(t, s, "requests"), indexes(t, s, "approvals")...)
	_ = s.Close()

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	afterReq := snapshot(t, s, "requests", requestColumns19, "direction || peer || id")
	afterAppr := snapshot(t, s, "approvals", approvalColumns19, "id")
	if len(beforeReq) != 13 || len(afterReq) != len(beforeReq) {
		t.Fatalf("requests rows before %d after %d", len(beforeReq), len(afterReq))
	}
	for k, v := range beforeReq {
		if afterReq[k] != v {
			t.Errorf("request %s changed:\n before %s\n after  %s", k, v, afterReq[k])
		}
	}
	if len(beforeAppr) != 24 || len(afterAppr) != len(beforeAppr) {
		t.Fatalf("approvals rows before %d after %d", len(beforeAppr), len(afterAppr))
	}
	for k, v := range beforeAppr {
		if afterAppr[k] != v {
			t.Errorf("approval %s changed:\n before %s\n after  %s", k, v, afterAppr[k])
		}
	}
	afterIdx := append(indexes(t, s, "requests"), indexes(t, s, "approvals")...)
	if strings.Join(afterIdx, "\n") != strings.Join(beforeIdx, "\n") {
		t.Errorf("indexes changed:\n before %q\n after  %q", beforeIdx, afterIdx)
	}
	for _, want := range []string{"requests_idem", "requests_state", "requests_peer_time", "approvals_state"} {
		if !strings.Contains(strings.Join(afterIdx, "\n"), want+":") {
			t.Errorf("index %s missing after migration: %q", want, afterIdx)
		}
	}
	var leftovers int
	_ = s.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('requests_new', 'approvals_new')`).Scan(&leftovers)
	if leftovers != 0 {
		t.Error("a _new table was left behind")
	}
	var integrity string
	if err := s.DB().QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check = %q (%v)", integrity, err)
	}
	// The unique partial idempotency index is still enforced.
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO requests (direction, peer, id, team_id, type, urgency, urgency_declared,
		body, body_hash, state, created, mail_id, updated, idem_key) VALUES ('out', 'peer-7', 'r-dup', 't', 'task', 'low', 'low', '{}', 'h', 'pending', 'c', 'm', 'u', 'key-7')`); err == nil ||
		!strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("duplicate idempotency key accepted: %v", err)
	}
	// Only out rows are covered: an in row may repeat it.
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO requests (direction, peer, id, team_id, type, urgency, urgency_declared,
		body, body_hash, state, created, mail_id, updated, idem_key) VALUES ('in', 'peer-7', 'r-dup', 't', 'task', 'low', 'low', '{}', 'h', 'pending', 'c', 'm', 'u', 'key-7')`); err != nil {
		t.Fatalf("in row with a used key refused: %v", err)
	}
	// The new CHECK values are accepted, unknown ones still refused.
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO requests (direction, peer, id, team_id, type, urgency, urgency_declared,
		body, body_hash, state, created, mail_id, updated) VALUES ('out', 'p', 'r-d', 't', 'debate', 'low', 'low', '{}', 'h', 'pending', 'c', 'm', 'u')`); err != nil {
		t.Errorf("type debate refused: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE requests SET type = 'bogus' WHERE id = 'r-d'`); err == nil {
		t.Error("CHECK accepted an unknown request type")
	}
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO approvals (id, kind, subject, summary, created, expires, state) VALUES ('a-dc', 'debate_constraint', 's', 'x', 'c', 'e', 'pending')`); err != nil {
		t.Errorf("kind debate_constraint refused: %v", err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE approvals SET kind = 'bogus' WHERE id = 'a-dc'`); err == nil {
		t.Error("CHECK accepted an unknown approval kind")
	}
	var kind string
	if err := s.DB().QueryRow(`SELECT kind FROM work_sessions WHERE id = 's-1'`).Scan(&kind); err != nil || kind != "work" {
		t.Errorf("existing session kind = %q (%v), want work", kind, err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE work_sessions SET kind = 'bogus'`); err == nil {
		t.Error("CHECK accepted an unknown session kind")
	}
	for _, tbl := range []string{"debates", "debate_entries", "debate_constraints"} {
		var c int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + tbl).Scan(&c); err != nil {
			t.Errorf("%s: %v", tbl, err)
		}
	}
}
