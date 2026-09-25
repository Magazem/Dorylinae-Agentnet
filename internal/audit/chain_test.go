package audit

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// migration1 is audit_events as migration 1 creates it (schema before 18).
const migration1 = `
CREATE TABLE audit_events (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	ts     TEXT NOT NULL,
	actor  TEXT NOT NULL,
	action TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(detail))
);
CREATE INDEX audit_events_ts ON audit_events (ts);
CREATE TRIGGER audit_events_no_update BEFORE UPDATE ON audit_events
BEGIN SELECT RAISE(ABORT, 'audit_events is append-only'); END;
CREATE TRIGGER audit_events_no_delete BEFORE DELETE ON audit_events
BEGIN SELECT RAISE(ABORT, 'audit_events is append-only'); END;
`

func openStore(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func exec(t *testing.T, db *sql.DB, qs ...string) {
	t.Helper()
	for _, q := range qs {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// legacyStore creates a database at schema 17 holding the given pre-chain
// rows (as INSERT value lists), then reopens it so migration 18 runs.
func legacyStore(t *testing.T, rows ...string) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(testutil.TempDir(t), "legacy.db")
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, s.DB(), `DROP TABLE audit_events`, migration1, `DELETE FROM migrations WHERE version > 17`)
	for _, r := range rows {
		exec(t, s.DB(), `INSERT INTO audit_events (id, ts, actor, action, detail) VALUES `+r)
	}
	_ = s.Close()
	return openStore(t, path), path
}

func dropTriggers(t *testing.T, db *sql.DB) {
	t.Helper()
	exec(t, db, `DROP TRIGGER audit_events_no_update`, `DROP TRIGGER audit_events_no_delete`, `DROP TRIGGER audit_events_chained`)
}

func mustVerify(t *testing.T, l *Log, anchors ...Anchor) *VerifyResult {
	t.Helper()
	res, err := l.Verify(context.Background(), anchors...)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func wantBroken(t *testing.T, res *VerifyResult, id int64, reason string) {
	t.Helper()
	if res.Status != StatusBroken || res.FirstBad != id || res.Reason != reason {
		t.Fatalf("verify = %+v, want broken at %d (%s)", res, id, reason)
	}
}

func wantOK(t *testing.T, res *VerifyResult) {
	t.Helper()
	if res.Status != StatusOK || res.Reason != "" || res.FirstBad != 0 {
		t.Fatalf("verify = %+v, want ok", res)
	}
}

// appendN appends n rows through Append.
func appendN(t *testing.T, l *Log, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := l.Append(context.Background(), ActorDaemon, "test.row", map[string]int{"n": i}); err != nil {
			t.Fatal(err)
		}
	}
}

// storedHash returns the hash column of row id.
func storedHash(t *testing.T, db *sql.DB, id int64) string {
	t.Helper()
	var h string
	if err := db.QueryRow(`SELECT hash FROM audit_events WHERE id = ?`, id).Scan(&h); err != nil {
		t.Fatal(err)
	}
	return h
}

// The vector of audit.md §Vector, byte for byte, and end to end through a
// migrated database holding exactly those rows.
func TestChainVector(t *testing.T) {
	const (
		row1 = `{"action":"daemon.start","actor":"daemon","detail":"{\"pid\":4242,\"version\":\"0.3.0\"}","id":1,"ts":"2026-10-01T09:00:00.123456789Z"}`
		row2 = `{"action":"audit.chain_start","actor":"daemon","detail":"{\"legacy_last_id\":1,\"legacy_rows\":1}","id":2,"ts":"2026-10-01T09:00:05.5Z"}`
		row3 = `{"action":"peer.verify","actor":"cli","detail":"{\"fingerprint\":\"abcd\",\"peer\":\"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc\"}","id":3,"ts":"2026-10-01T09:00:06Z"}`
		gen  = "fa4302605d936ae73c80ffaba49180f3676a0c659a72f4c06d88fb64c6a979dd"
		h1   = "554876a662aa875734c153e722b7b6acd327058b14b02a3af13bd798d0d03f48"
		h2   = "c2878d547c6df62488566622d96d9c70e5891cffc849a5dc462a5aa56df24d85"
		h3   = "3daa4dae406cb5ab73902738474b6386b3112dd17eec7344978c9a665d88b6a2"
	)
	rows := []row{
		{id: 1, ts: "2026-10-01T09:00:00.123456789Z", actor: "daemon", action: "daemon.start", detail: `{"pid":4242,"version":"0.3.0"}`},
		{id: 2, ts: "2026-10-01T09:00:05.5Z", actor: "daemon", action: "audit.chain_start", detail: `{"legacy_last_id":1,"legacy_rows":1}`},
		{id: 3, ts: "2026-10-01T09:00:06Z", actor: "cli", action: "peer.verify", detail: `{"fingerprint":"abcd","peer":"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc"}`},
	}
	if got := hex.EncodeToString(genesis()); got != gen {
		t.Fatalf("genesis = %s", got)
	}
	prev := genesis()
	for i, want := range []struct{ canon, hash string }{{row1, h1}, {row2, h2}, {row3, h3}} {
		c, err := rowCanonical(rows[i])
		if err != nil || string(c) != want.canon {
			t.Fatalf("row_c(%d) = %s (%v)\nwant %s", i+1, c, err, want.canon)
		}
		h, err := chainHash(prev, rows[i])
		if err != nil || encodeHash(h) != want.hash {
			t.Fatalf("hash(%d) = %x (%v), want %s", i+1, h, err, want.hash)
		}
		prev = h
	}

	s, _ := legacyStore(t, `(1, '2026-10-01T09:00:00.123456789Z', 'daemon', 'daemon.start', '{"pid":4242,"version":"0.3.0"}')`)
	exec(t, s.DB(),
		`INSERT INTO audit_events VALUES (2, '2026-10-01T09:00:05.5Z', 'daemon', 'audit.chain_start', '{"legacy_last_id":1,"legacy_rows":1}', '`+h2+`')`,
		`INSERT INTO audit_events VALUES (3, '2026-10-01T09:00:06Z', 'cli', 'peer.verify', '{"fingerprint":"abcd","peer":"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc"}', '`+h3+`')`)
	res := mustVerify(t, New(s.DB()), Anchor{ID: 3, Hash: h3})
	wantOK(t, res)
	if res.Rows != 3 || res.LegacyRows != 1 || res.ChainedFrom != 2 || res.Head == nil || *res.Head != (Head{ID: 3, Hash: h3, TS: "2026-10-01T09:00:06Z"}) {
		t.Fatalf("verify = %+v head %+v", res, res.Head)
	}
}

// A fresh database: the first append writes audit.chain_start with zero
// legacy rows as row 1.
func TestFreshChainStart(t *testing.T) {
	s := openStore(t, filepath.Join(testutil.TempDir(t), "f.db"))
	l := New(s.DB())
	res := mustVerify(t, l)
	wantOK(t, res)
	if res.Rows != 0 || res.Head != nil {
		t.Fatalf("empty verify = %+v", res)
	}
	appendN(t, l, 3)
	var action, detail string
	if err := s.DB().QueryRow(`SELECT action, detail FROM audit_events WHERE id = 1`).Scan(&action, &detail); err != nil {
		t.Fatal(err)
	}
	if action != ActionChainStart || detail != `{"legacy_last_id":0,"legacy_rows":0}` {
		t.Fatalf("row 1 = %s %s", action, detail)
	}
	res = mustVerify(t, l)
	wantOK(t, res)
	if res.Rows != 4 || res.LegacyRows != 0 || res.ChainedFrom != 1 || res.Head.ID != 4 || res.Head.Hash != storedHash(t, s.DB(), 4) {
		t.Fatalf("verify = %+v head %+v", res, res.Head)
	}
}

// Legacy rows (with a gap in their ids) migrate; the first append writes
// audit.chain_start with the right counts, and Verify is ok.
func TestLegacyRowsMigrate(t *testing.T) {
	s, _ := legacyStore(t,
		`(1, '2026-01-01T00:00:00Z', 'daemon', 'daemon.start', '{"pid":1}')`,
		`(2, '2026-01-01T00:00:01.5Z', 'cli', 'peer.verify', '{"peer":"k"}')`,
		`(5, '2026-01-01T00:00:02Z', 'daemon', 'daemon.stop', '{}')`)
	l := New(s.DB())
	res := mustVerify(t, l) // legacy only, before the first append
	wantOK(t, res)
	if res.Rows != 3 || res.LegacyRows != 3 || res.ChainedFrom != 0 {
		t.Fatalf("legacy-only verify = %+v", res)
	}
	appendN(t, l, 2)
	var action, detail string
	if err := s.DB().QueryRow(`SELECT action, detail FROM audit_events WHERE id = 6`).Scan(&action, &detail); err != nil {
		t.Fatal(err)
	}
	if action != ActionChainStart || detail != `{"legacy_last_id":5,"legacy_rows":3}` {
		t.Fatalf("row 6 = %s %s", action, detail)
	}
	res = mustVerify(t, l)
	wantOK(t, res)
	if res.Rows != 6 || res.LegacyRows != 3 || res.ChainedFrom != 6 || res.Head.ID != 8 {
		t.Fatalf("verify = %+v head %+v", res, res.Head)
	}
	evs, err := l.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 5 || evs[2].ID != 5 || evs[3].ID != 7 {
		t.Fatalf("List = %+v, want the 3 legacy rows and 2 appended ones, without chain_start", evs)
	}
	// An anchor on a legacy row is tampering (D31), not a caller error.
	res = mustVerify(t, l, Anchor{ID: 2, Hash: storedHash(t, s.DB(), 7)})
	if res.Status != StatusBroken || res.Reason != ReasonAnchorMismatch || res.FirstBad != 2 {
		t.Fatalf("legacy anchor: %+v, want broken anchor_mismatch at row 2", res)
	}
}

// Review 44 M1: nulling the hash of an anchored row must be reported as
// tampering, not as the caller's bad anchor.
func TestAnchorOnNulledHashIsBroken(t *testing.T) {
	setup := func(t *testing.T) (*store.Store, *Log, Anchor) {
		s := openStore(t, filepath.Join(testutil.TempDir(t), "t.db"))
		l := New(s.DB())
		appendN(t, l, 5)
		a := Anchor{ID: 4, Hash: storedHash(t, s.DB(), 4)}
		wantOK(t, mustVerify(t, l, a))
		dropTriggers(t, s.DB())
		return s, l, a
	}
	t.Run("one row after the chain start", func(t *testing.T) {
		s, l, a := setup(t)
		exec(t, s.DB(), `UPDATE audit_events SET hash = NULL WHERE id = 4`)
		wantBroken(t, mustVerify(t, l, a), 4, ReasonUnchained)
	})
	t.Run("every row, no chain start left", func(t *testing.T) {
		s, l, a := setup(t)
		exec(t, s.DB(), `UPDATE audit_events SET hash = NULL`)
		wantOK(t, mustVerify(t, l)) // documented limit: a rewrite verifies on its own
		wantBroken(t, mustVerify(t, l, a), 4, ReasonAnchorMismatch)
	})
}

// Plan 3.6: tampering with one field of one row breaks the chain at that row.
func TestVerifyDetectsChangedField(t *testing.T) {
	for _, set := range []string{
		`actor = 'evil'`,
		`action = 'x.other'`,
		`detail = '{"n":99}'`,
		`ts = '2030-01-01T00:00:00Z'`,
		`hash = '` + hex.EncodeToString(make([]byte, 32)) + `'`,
	} {
		t.Run(set, func(t *testing.T) {
			s := openStore(t, filepath.Join(testutil.TempDir(t), "t.db"))
			l := New(s.DB())
			appendN(t, l, 8)
			wantOK(t, mustVerify(t, l))
			dropTriggers(t, s.DB())
			exec(t, s.DB(), `UPDATE audit_events SET `+set+` WHERE id = 5`)
			wantBroken(t, mustVerify(t, l), 5, ReasonHashMismatch)
		})
	}
}

func TestVerifyDetectsStructuralTampering(t *testing.T) {
	cases := []struct {
		name   string
		tamper []string
		id     int64
		reason string
	}{
		{"deleted middle row", []string{`DELETE FROM audit_events WHERE id = 4`}, 5, ReasonGap},
		{"swapped pair", []string{
			`CREATE TEMP TABLE sw AS SELECT * FROM audit_events WHERE id IN (4, 5)`,
			`UPDATE audit_events SET (ts, actor, action, detail, hash) =
				(SELECT ts, actor, action, detail, hash FROM sw WHERE sw.id = 9 - audit_events.id) WHERE id IN (4, 5)`,
		}, 4, ReasonHashMismatch},
		{"malformed ts, rehashed", nil, 0, ""}, // filled in below
	}
	for _, c := range cases[:2] {
		t.Run(c.name, func(t *testing.T) {
			s := openStore(t, filepath.Join(testutil.TempDir(t), "t.db"))
			l := New(s.DB())
			appendN(t, l, 8)
			dropTriggers(t, s.DB())
			exec(t, s.DB(), c.tamper...)
			wantBroken(t, mustVerify(t, l), c.id, c.reason)
		})
	}

	t.Run("malformed ts, rehashed", func(t *testing.T) {
		s := openStore(t, filepath.Join(testutil.TempDir(t), "t.db"))
		l := New(s.DB())
		appendN(t, l, 3)
		dropTriggers(t, s.DB())
		exec(t, s.DB(), `UPDATE audit_events SET ts = 'yesterday' WHERE id = 4`)
		rehash(t, s.DB(), 4)
		wantBroken(t, mustVerify(t, l), 4, ReasonMalformed)
	})

	t.Run("inserted unchained row", func(t *testing.T) {
		s := openStore(t, filepath.Join(testutil.TempDir(t), "t.db"))
		l := New(s.DB())
		appendN(t, l, 3)
		// The trigger normally refuses it.
		if _, err := s.DB().Exec(`INSERT INTO audit_events (ts, actor, action, detail) VALUES ('2026-01-01T00:00:00Z', 'x', 'x.y', '{}')`); err == nil {
			t.Fatal("unchained insert accepted")
		}
		dropTriggers(t, s.DB())
		exec(t, s.DB(), `INSERT INTO audit_events (ts, actor, action, detail) VALUES ('2026-01-01T00:00:00Z', 'x', 'x.y', '{}')`)
		wantBroken(t, mustVerify(t, l), 5, ReasonUnchained)
		// And the chained writer refuses to continue past it.
		if err := l.Append(context.Background(), ActorDaemon, "x.z", nil); err == nil {
			t.Fatal("append after an unchained head succeeded")
		}
	})

	t.Run("truncated tail still verifies", func(t *testing.T) {
		// Documented limit (audit.md §What the chain proves): removing the
		// newest rows leaves a valid, shorter chain. Anchors close it.
		s := openStore(t, filepath.Join(testutil.TempDir(t), "t.db"))
		l := New(s.DB())
		appendN(t, l, 8)
		anchor := Anchor{ID: 9, Hash: storedHash(t, s.DB(), 9)}
		dropTriggers(t, s.DB())
		exec(t, s.DB(), `DELETE FROM audit_events WHERE id > 6`)
		res := mustVerify(t, l)
		wantOK(t, res)
		if res.Head.ID != 6 {
			t.Fatalf("head = %+v", res.Head)
		}
		wantBroken(t, mustVerify(t, l, anchor), 9, ReasonAnchorMissing)
	})
}

// Legacy rows are protected from the migration on: changing, deleting or
// inserting one breaks audit.chain_start.
func TestVerifyDetectsLegacyTampering(t *testing.T) {
	legacy := []string{
		`(1, '2026-01-01T00:00:00Z', 'daemon', 'daemon.start', '{"pid":1}')`,
		`(2, '2026-01-01T00:00:01Z', 'cli', 'peer.verify', '{"peer":"k"}')`,
		`(3, '2026-01-01T00:00:02Z', 'daemon', 'daemon.stop', '{}')`,
	}
	for _, c := range []struct {
		name, tamper, reason string
	}{
		{"changed legacy row", `UPDATE audit_events SET detail = '{"peer":"other"}' WHERE id = 2`, ReasonHashMismatch},
		{"deleted legacy row", `DELETE FROM audit_events WHERE id = 2`, ReasonChainStart},
		{"deleted last legacy row", `DELETE FROM audit_events WHERE id = 3`, ReasonChainStart},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, _ := legacyStore(t, legacy...)
			l := New(s.DB())
			appendN(t, l, 3)
			wantOK(t, mustVerify(t, l))
			dropTriggers(t, s.DB())
			exec(t, s.DB(), c.tamper)
			wantBroken(t, mustVerify(t, l), 4, c.reason)
		})
	}
	t.Run("legacy row without chain_start", func(t *testing.T) {
		s, _ := legacyStore(t, legacy...)
		l := New(s.DB())
		exec(t, s.DB(), `INSERT INTO audit_events VALUES (4, '2026-01-01T00:00:03Z', 'daemon', 'x.y', '{}', '`+hex.EncodeToString(make([]byte, 32))+`')`)
		wantBroken(t, mustVerify(t, l), 4, ReasonChainStart)
	})
}

// rehash recomputes the stored hashes from row `from` on, as an attacker who
// rewrites the chain would.
func rehash(t *testing.T, db *sql.DB, from int64) {
	t.Helper()
	var prevHex string
	if err := db.QueryRow(`SELECT hash FROM audit_events WHERE id = ?`, from-1).Scan(&prevHex); err != nil {
		t.Fatal(err)
	}
	prev, err := decodeHash(prevHex)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := db.Query(`SELECT id, ts, actor, action, detail FROM audit_events WHERE id >= ? ORDER BY id`, from)
	if err != nil {
		t.Fatal(err)
	}
	var all []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.id, &r.ts, &r.actor, &r.action, &r.detail); err != nil {
			t.Fatal(err)
		}
		all = append(all, r)
	}
	_ = rs.Close()
	for _, r := range all {
		h, err := chainHash(prev, r)
		if err != nil {
			t.Fatal(err)
		}
		exec(t, db, fmt.Sprintf(`UPDATE audit_events SET hash = '%s' WHERE id = %d`, encodeHash(h), r.id))
		prev = h
	}
}

// A rewrite of the whole chain verifies on its own (documented limit) but not
// against an anchor recorded before it.
func TestAnchorDetectsRewrittenPrefix(t *testing.T) {
	s := openStore(t, filepath.Join(testutil.TempDir(t), "t.db"))
	l := New(s.DB())
	appendN(t, l, 6)
	anchor, err := ParseAnchor(fmt.Sprintf("6:%s", storedHash(t, s.DB(), 6)))
	if err != nil {
		t.Fatal(err)
	}
	wantOK(t, mustVerify(t, l, anchor))
	appendN(t, l, 2)
	wantOK(t, mustVerify(t, l, anchor))

	dropTriggers(t, s.DB())
	exec(t, s.DB(), `UPDATE audit_events SET detail = '{"n":42}' WHERE id = 3`)
	rehash(t, s.DB(), 3)
	wantOK(t, mustVerify(t, l))
	wantBroken(t, mustVerify(t, l, anchor), 6, ReasonAnchorMismatch)
	wantBroken(t, mustVerify(t, l, Anchor{ID: 50, Hash: anchor.Hash}), 50, ReasonAnchorMissing)

	for _, bad := range []string{"", "6", "x:" + anchor.Hash, "0:" + anchor.Hash, "6:abc", "6:" + string(make([]byte, 64))} {
		if _, err := ParseAnchor(bad); !errors.Is(err, ErrBadAnchor) {
			t.Errorf("ParseAnchor(%q) = %v, want ErrBadAnchor", bad, err)
		}
	}
}

// A rolled-back AppendTx leaves no row and no gap, also when it would have
// written audit.chain_start.
func TestAppendTxRollbackLeavesNoGap(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, filepath.Join(testutil.TempDir(t), "t.db"))
	l := New(s.DB())
	for i := 0; i < 2; i++ {
		tx, err := s.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := AppendTx(ctx, tx, ActorDaemon, "x.rolled_back", nil); err != nil {
			t.Fatal(err)
		}
		_ = tx.Rollback()
		var n int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action = 'x.rolled_back'`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("rolled-back rows = %d (%v)", n, err)
		}
		appendN(t, l, 1)
	}
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := AppendTx(ctx, tx, ActorDaemon, "x.committed", nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	res := mustVerify(t, l)
	wantOK(t, res)
	if res.Rows != 4 || res.Head.ID != 4 {
		t.Fatalf("verify = %+v head %+v", res, res.Head)
	}
}

// agentnetd install appends from a second process (a second sql.DB on the same
// file). While the daemon's handle holds a write transaction the append
// waits; it succeeds once the lock is released and the chain stays valid.
func TestAppendFromSecondProcess(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(testutil.TempDir(t), "t.db")
	daemon := openStore(t, path)
	install := openStore(t, path)
	dl, il := New(daemon.DB()), New(install.DB())
	appendN(t, dl, 2)

	tx, err := daemon.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := AppendTx(ctx, tx, ActorDaemon, "x.held", nil); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- il.Append(ctx, ActorCLI, ActionServiceInstall, map[string]string{"platform": "test"}) }()
	select {
	case err := <-done:
		t.Fatalf("second-process append finished while the write lock was held: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second-process append: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second-process append did not finish after the lock was released")
	}
	var id int64
	if err := daemon.DB().QueryRow(`SELECT id FROM audit_events WHERE action = ?`, ActionServiceInstall).Scan(&id); err != nil || id != 5 {
		t.Fatalf("install row id = %d (%v), want 5", id, err)
	}

	// Both processes appending at once still produce one valid chain.
	var wg sync.WaitGroup
	var failed atomic.Int32
	for _, l := range []*Log{dl, il} {
		wg.Add(1)
		go func(l *Log) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				if err := l.Append(ctx, ActorDaemon, "x.concurrent", nil); err != nil {
					t.Error(err)
					failed.Add(1)
					return
				}
			}
		}(l)
	}
	wg.Wait()
	if failed.Load() != 0 {
		t.FailNow()
	}
	res := mustVerify(t, dl)
	wantOK(t, res)
	if res.Rows != 85 {
		t.Fatalf("rows = %d, want 85", res.Rows)
	}
}

// Review 43 M8: Verify walks in pages and releases the daemon's single
// connection between them, so other work completes while 10^5 rows are
// checked.
func TestVerifyPagesReleaseTheConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("builds 10^5 rows")
	}
	ctx := context.Background()
	s := openStore(t, filepath.Join(testutil.TempDir(t), "big.db"))
	l := New(s.DB())
	const n = 100_000
	tx, err := s.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := AppendTx(ctx, tx, ActorDaemon, "x.bulk", map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var (
		pages     atomic.Int32
		completed atomic.Int32
	)
	afterPage = func() {
		if pages.Add(1) != 2 {
			return
		}
		// Mid-walk: an append (a daemon action) and a mail apply's write
		// must get the connection and finish.
		ok := make(chan error, 1)
		go func() {
			if err := l.Append(ctx, ActorDaemon, "x.during_verify", nil); err != nil {
				ok <- err
				return
			}
			_, err := s.DB().ExecContext(ctx, `INSERT INTO mail_seen (from_key, id, received_at) VALUES ('k', 'm', '2026-01-01T00:00:00Z')`)
			ok <- err
		}()
		select {
		case err := <-ok:
			if err == nil {
				completed.Add(1)
			}
		case <-time.After(10 * time.Second):
		}
	}
	t.Cleanup(func() { afterPage = nil })

	res := mustVerify(t, l)
	wantOK(t, res)
	if res.Rows != n+1 || res.Head.ID != n+1 {
		t.Fatalf("verify = %+v head %+v; rows appended during the walk must not be counted", res, res.Head)
	}
	if completed.Load() != 1 {
		t.Fatal("work on the store did not complete while Verify was walking")
	}
	if p := pages.Load(); p != (n+1)/verifyPage+1 {
		t.Fatalf("pages = %d", p)
	}
	afterPage = nil
	res = mustVerify(t, l)
	wantOK(t, res)
	if res.Rows != n+2 {
		t.Fatalf("second verify rows = %d", res.Rows)
	}
}
