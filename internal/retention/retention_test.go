package retention

// R55-F13 acceptance for agentnet prune (Docs/protocol/retention.md, tests
// A10-A12 of Docs/review/71-r55-f13-spec.md with review 71b's additions).

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

var now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func ago(d int) string { return now.Add(-time.Duration(d) * 24 * time.Hour).Format(storeTimeFmt) }

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(testutil.TempDir(t), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.DB()
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%.80s: %v", q, err)
	}
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%.80s: %v", q, err)
	}
	return n
}

func sid(n int) string { return fmt.Sprintf("s-%032x", n) }
func rid(n int) string { return fmt.Sprintf("r-%032x", n) }

// request inserts a request row; updated and received are days ago.
func request(t *testing.T, db *sql.DB, dir, peer, id, state string, updated int, body string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO requests (direction, peer, id, team_id, type, urgency, urgency_declared, body, body_hash,
		state, created, received_at, mail_id, updated) VALUES (?, ?, ?, 't', 'task', 'normal', 'normal', ?, 'h', ?, ?, ?, 'm', ?)`,
		dir, peer, id, body, state, ago(updated+1), ago(updated+1), ago(updated))
}

// session inserts a work session row (role from the request's direction).
func session(t *testing.T, db *sql.DB, id, dir, peer, reqID, state string, updated int, result string) {
	t.Helper()
	role := "worker"
	if dir == "out" {
		role = "requester"
	}
	var outcome any
	if state == "closed" {
		outcome = "accepted"
	}
	mustExec(t, db, `INSERT INTO work_sessions (id, role, peer, request_id, team_id, state, outcome, result, opened, state_at, updated)
		VALUES (?, ?, ?, ?, 't', ?, ?, ?, ?, ?, ?)`, id, role, peer, reqID, state, outcome, nullIf(result), ago(updated+1), ago(updated), ago(updated))
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func grant(t *testing.T, db *sql.DB, id, dir, sess, state string, exp int, sensitive int) {
	t.Helper()
	var revoked any
	if state == "revoked" {
		revoked = ago(exp + 1)
	}
	mustExec(t, db, `INSERT INTO grants (id, direction, peer, session, action, label, sensitive, nbf, exp, token, state, approval, revoked_at, created, updated)
		VALUES (?, ?, 'P', ?, 'fs.read', 'l', ?, ?, ?, '{}', ?, 'a-1', ?, ?, ?)`,
		id, dir, sess, sensitive, ago(exp+1), ago(exp), state, revoked, ago(exp+1), ago(exp))
}

func debate(t *testing.T, db *sql.DB, sess, dir, peer, reqID, phase string, updated, entries, constraints int) {
	t.Helper()
	role := "respondent"
	if dir == "out" {
		role = "initiator"
	}
	var outcome any
	if phase == "closed" {
		outcome = "agreed"
	}
	mustExec(t, db, `INSERT INTO debates (session, role, peer, request_id, rounds_max, turn_timeout_s, commitment, phase, outcome, created, updated)
		VALUES (?, ?, ?, ?, 2, 3600, 'c', ?, ?, ?, ?)`, sess, role, peer, reqID, phase, outcome, ago(updated+1), ago(updated))
	for i := 0; i < entries; i++ {
		mustExec(t, db, `INSERT INTO debate_entries (session, slot, author, kind, entry, at, state) VALUES (?, ?, 'initiator', 'move', '{"x":1}', 'a', 'applied')`, sess, i)
	}
	for i := 0; i < constraints; i++ {
		mustExec(t, db, `INSERT INTO debate_constraints (session, id, author, text, at, state) VALUES (?, ?, 'initiator', 'text', 'a', 'active')`, sess, fmt.Sprintf("c-%d", i))
	}
}

func experience(t *testing.T, db *sql.DB, sess, role string, created int) {
	t.Helper()
	mustExec(t, db, `INSERT INTO experience_records (session, role, record, created) VALUES (?, ?, '{}', ?)`, sess, role, ago(created))
}

// fixtures builds every table at 34 d and 36 d, finished and not. It returns
// what a prune at 35 d must remove.
func fixtures(t *testing.T, db *sql.DB) Counts {
	t.Helper()
	// 1. A finished `in` request with a closed session (2 grants, 1
	// experience record) and a closed debate (3 entries, 1 constraint), all
	// 36 d old, and its kept Decision.
	request(t, db, "in", "P", rid(1), "completed", 36, `{"b":1}`)
	session(t, db, sid(1), "in", "P", rid(1), "closed", 36, `{"r":1}`)
	grant(t, db, "g-1", "held", sid(1), "active", 50, 1)
	grant(t, db, "g-2", "issued", sid(1), "active", 10, 1) // goes with its session, whatever its exp
	experience(t, db, sid(1), "worker", 36)
	debate(t, db, sid(1), "in", "P", rid(1), "closed", 36, 3, 1)
	mustExec(t, db, `INSERT INTO decisions (id, session, role, peer, decision, hash, state, created, updated)
		VALUES ('d-1', ?, 'respondent', 'P', '{}', 'h', 'signed', ?, ?)`, sid(1), ago(36), ago(36))
	// 2. A finished `out` request, no session.
	request(t, db, "out", "Q", rid(2), "declined", 36, `{"b":2}`)
	// 3. Finished but 34 d old: kept.
	request(t, db, "in", "P", rid(3), "cancelled", 34, `{"b":3}`)
	// 4. Finished 36 d, but its session was updated 34 d ago: kept.
	request(t, db, "in", "P", rid(4), "completed", 36, `{"b":4}`)
	session(t, db, sid(4), "in", "P", rid(4), "closed", 34, "")
	// 5. Finished 36 d, its session still open: kept.
	request(t, db, "out", "P", rid(5), "completed", 36, `{"b":5}`)
	session(t, db, sid(5), "out", "P", rid(5), "open", 36, "")
	// 6. A pending request of 400 days: kept.
	request(t, db, "in", "P", rid(6), "pending", 400, `{"b":6}`)
	// 7. Finished 36 d with a debate still in rounds: kept.
	request(t, db, "out", "P", rid(7), "cancelled", 36, `{"b":7}`)
	debate(t, db, sid(7), "out", "P", rid(7), "rounds", 36, 1, 0)
	// 8. Orphans: a closed session (1 grant, 1 experience record) and a
	// broken debate (2 entries) without their requests, 36 d; and a closed
	// session 34 d: kept.
	session(t, db, sid(8), "in", "P", rid(8), "closed", 36, "")
	grant(t, db, "g-8", "issued", sid(8), "revoked", 40, 1)
	experience(t, db, sid(8), "worker", 36)
	debate(t, db, sid(9), "in", "P", rid(9), "broken", 36, 2, 0)
	session(t, db, sid(10), "in", "P", rid(10), "closed", 34, "")
	// 9. Review 71b F1: in the open session 5, an issued sensitive grant
	// expired 40 d ago stays (the quarantine rule still needs it); a held
	// grant expired 40 d ago goes.
	grant(t, db, "g-5i", "issued", sid(5), "active", 40, 1)
	grant(t, db, "g-5h", "held", sid(5), "active", 40, 1)
	// An issued grant expired 40 d ago in no session goes; one expired 34 d
	// ago stays.
	grant(t, db, "g-11", "issued", "s-gone", "active", 40, 0)
	grant(t, db, "g-12", "issued", "s-gone", "active", 34, 0)
	// A held grant revoked 36 d ago but with exp later: goes (revoked_at).
	mustExec(t, db, `INSERT INTO grants (id, direction, peer, session, action, label, sensitive, nbf, exp, token, state, revoked_at, created, updated)
		VALUES ('g-13', 'held', 'P', 's-x', 'fs.read', 'l', 0, ?, ?, '{}', 'revoked', ?, ?, ?)`, ago(40), ago(-5), ago(36), ago(40), ago(36))
	// 10. mail_inbox: one row 36 d old (goes), one 34 d old (stays), one
	// recent with pre-F13 plaintext (blanked), one 36 d old with plaintext
	// (removed, not blanked).
	mustExec(t, db, `INSERT INTO mail_inbox (from_key, id, kind, created, received_at, signed) VALUES
		('P', 'm-1', 'request', 'c', ?, ''), ('P', 'm-2', 'request', 'c', ?, ''),
		('P', 'm-3', 'request', 'c', ?, '{"plain":"MARKER"}'), ('P', 'm-4', 'request', 'c', ?, '{"old":1}')`,
		ago(36), ago(34), ago(1), ago(36))
	mustExec(t, db, `INSERT INTO mail_seen (from_key, id, received_at) VALUES ('P', 'm-2', ?), ('P', 'm-3', ?), ('P', 'm-1', ?)`, ago(34), ago(1), ago(36))
	return Counts{
		Requests: 2, WorkSessions: 2, Grants: 6, Debates: 2, DebateEntries: 5, DebateConstraints: 1,
		ExperienceRecords: 2, MailInbox: 2, InboxBlanked: 1,
	}
}

// A11 (with A10's blanking): the dry run's counts equal a real run's; only
// finished and older rows go, each request with everything of it; Decisions,
// open items and audit_events stay.
func TestPruneFinishedOnly(t *testing.T) {
	db := openDB(t)
	want := fixtures(t, db)
	ctx := context.Background()
	auditBefore := count(t, db, `SELECT COUNT(*) FROM audit_events`)
	cutoff := Cutoff(now, MinOlderThan)
	dry, err := DryRun(ctx, db, cutoff, now)
	if err != nil {
		t.Fatal(err)
	}
	if dry != want {
		t.Fatalf("dry run = %+v\nwant      %+v", dry, want)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM requests`); n != 7 {
		t.Fatalf("the dry run removed rows: %d requests left", n)
	}
	var total Counts
	for i := 0; ; i++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		c, more, err := PruneTx(ctx, tx, cutoff, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		total = total.Add(c)
		if !more {
			break
		}
		if i > 3 {
			t.Fatal("prune did not finish")
		}
	}
	if total != want {
		t.Fatalf("removed = %+v\nwant      %+v", total, want)
	}
	for _, id := range []int{3, 4, 5, 6, 7} {
		if n := count(t, db, `SELECT COUNT(*) FROM requests WHERE id = ?`, rid(id)); n != 1 {
			t.Errorf("request %d removed, want kept", id)
		}
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM requests WHERE id IN ('` + rid(1) + `', '` + rid(2) + `')`,
		`SELECT COUNT(*) FROM work_sessions WHERE id IN ('` + sid(1) + `', '` + sid(8) + `')`,
		`SELECT COUNT(*) FROM debates WHERE session IN ('` + sid(1) + `', '` + sid(9) + `')`,
		`SELECT COUNT(*) FROM debate_entries WHERE session IN ('` + sid(1) + `', '` + sid(9) + `')`,
		`SELECT COUNT(*) FROM experience_records`,
		`SELECT COUNT(*) FROM grants WHERE id IN ('g-1', 'g-2', 'g-8', 'g-5h', 'g-11', 'g-13')`,
		`SELECT COUNT(*) FROM mail_inbox WHERE id IN ('m-1', 'm-4')`,
		`SELECT COUNT(*) FROM mail_inbox WHERE signed <> ''`,
		`SELECT COUNT(*) FROM mail_seen WHERE id = 'm-1'`,
	} {
		if n := count(t, db, q); n != 0 {
			t.Errorf("%s = %d, want 0", q, n)
		}
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM decisions WHERE id = 'd-1'`,
		`SELECT COUNT(*) FROM work_sessions WHERE id = '` + sid(10) + `'`,
		`SELECT COUNT(*) FROM debates WHERE session = '` + sid(7) + `'`,
		`SELECT COUNT(*) FROM grants WHERE id = 'g-5i'`,
		`SELECT COUNT(*) FROM grants WHERE id = 'g-12'`,
		`SELECT COUNT(*) FROM mail_inbox WHERE id = 'm-2'`,
		`SELECT COUNT(*) FROM mail_inbox WHERE id = 'm-3' AND signed = ''`,
	} {
		if n := count(t, db, q); n != 1 {
			t.Errorf("%s = %d, want 1", q, n)
		}
	}
	if n := count(t, db, `SELECT COUNT(*) FROM audit_events`); n != auditBefore {
		t.Errorf("audit_events changed: %d -> %d (retention itself never writes it)", auditBefore, n)
	}
	// The quarantine rule still holds for a later result in the open
	// session 5 (clause 1: an ever-active sensitive grant in the session).
	cs := &capability.Store{DB: db, Now: func() time.Time { return now }}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if holds, err := cs.QuarantineHolds(ctx, tx, sid(5), "P"); err != nil || !holds {
		t.Fatalf("quarantine after prune = %v, %v; want held", holds, err)
	}
	_ = tx.Rollback()
	// Nothing is left: a second prune removes nothing.
	again, err := DryRun(ctx, db, cutoff, now)
	if err != nil || !again.Zero() {
		t.Fatalf("second dry run = %+v, %v", again, err)
	}
	// A cutoff younger than 35 d is refused.
	tx2, _ := db.Begin()
	defer func() { _ = tx2.Rollback() }()
	if _, _, err := PruneTx(ctx, tx2, Cutoff(now, MinOlderThan-time.Hour), now); err == nil {
		t.Fatal("a 35 d - 1 h cutoff was accepted")
	}
}

// A12: 1,200 finished requests take 3 calls (more true, true, false), each
// under 2 s with its dependents; 20 requests of 1 MiB take several calls,
// none over 8 MiB plus one request.
func TestPruneBatches(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1200; i++ {
		id := rid(i)
		if _, err := tx.Exec(`INSERT INTO requests (direction, peer, id, team_id, type, urgency, urgency_declared, body, body_hash,
			state, created, received_at, mail_id, updated) VALUES ('in', 'P', ?, 't', 'task', 'normal', 'normal', '{"b":1}', 'h', 'completed', ?, ?, 'm', ?)`,
			id, ago(40), ago(40), ago(40)); err != nil {
			t.Fatal(err)
		}
		if i%4 == 0 { // 300 sessions with 4 grants and 1 experience record each, plus 300 debates with 8 entries
			s := sid(i)
			if _, err := tx.Exec(`INSERT INTO work_sessions (id, role, peer, request_id, team_id, state, outcome, opened, state_at, updated)
				VALUES (?, 'worker', 'P', ?, 't', 'closed', 'accepted', ?, ?, ?)`, s, id, ago(40), ago(40), ago(40)); err != nil {
				t.Fatal(err)
			}
			for g := 0; g < 4; g++ {
				if _, err := tx.Exec(`INSERT INTO grants (id, direction, peer, session, action, label, sensitive, nbf, exp, token, state, created, updated)
					VALUES (?, 'issued', 'P', ?, 'fs.read', 'l', 1, ?, ?, '{}', 'active', ?, ?)`, fmt.Sprintf("g-%d-%d", i, g), s, ago(41), ago(40), ago(41), ago(40)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tx.Exec(`INSERT INTO experience_records (session, role, record, created) VALUES (?, 'worker', '{}', ?)`, s, ago(40)); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`INSERT INTO debates (session, role, peer, request_id, rounds_max, turn_timeout_s, commitment, phase, outcome, created, updated)
				VALUES (?, 'respondent', 'P', ?, 2, 3600, 'c', 'closed', 'agreed', ?, ?)`, s, id, ago(40), ago(40)); err != nil {
				t.Fatal(err)
			}
			for e := 0; e < 8; e++ {
				if _, err := tx.Exec(`INSERT INTO debate_entries (session, slot, author, kind, entry, at, state) VALUES (?, ?, 'initiator', 'move', '{"x":1}', 'a', 'applied')`, s, e); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	cutoff := Cutoff(now, MinOlderThan)
	dry, err := DryRun(ctx, db, cutoff, now)
	if err != nil {
		t.Fatal(err)
	}
	var mores []bool
	var total Counts
	for len(mores) < 10 {
		start := time.Now()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		c, more, err := PruneTx(ctx, tx, cutoff, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("call %d took %v, want under 2 s", len(mores)+1, d)
		}
		if c.Requests > MaxRequests {
			t.Fatalf("call removed %d requests", c.Requests)
		}
		total = total.Add(c)
		mores = append(mores, more)
		if !more {
			break
		}
	}
	if fmt.Sprint(mores) != "[true true false]" {
		t.Fatalf("more = %v, want [true true false]", mores)
	}
	if total != dry || total.Requests != 1200 || total.Grants != 1200 || total.DebateEntries != 2400 {
		t.Fatalf("total %+v, dry %+v", total, dry)
	}

	// 20 requests of 1 MiB of content each: several calls, none over 8 MiB
	// plus one request.
	db2 := openDB(t)
	big := `{"b":"` + strings.Repeat("x", 1<<20) + `"}`
	for i := 0; i < 20; i++ {
		request(t, db2, "in", "P", rid(i), "completed", 40, big)
	}
	calls := 0
	for {
		tx, err := db2.Begin()
		if err != nil {
			t.Fatal(err)
		}
		c, more, err := PruneTx(ctx, tx, cutoff, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		calls++
		if int(c.Requests)*len(big) > MaxContentBytes+len(big) {
			t.Fatalf("call %d took %d requests of 1 MiB", calls, c.Requests)
		}
		if !more {
			break
		}
		if calls > 20 {
			t.Fatal("no progress")
		}
	}
	if calls < 3 || count(t, db2, `SELECT COUNT(*) FROM requests`) != 0 {
		t.Fatalf("calls = %d, rows left %d", calls, count(t, db2, `SELECT COUNT(*) FROM requests`))
	}
}

// Review 81b L1: a debate's experience records (debate/experience.go writes
// them under the debate's session) are counted with the debate and removed
// with it, not left to a later batch as orphans.
func TestPruneDebateExperienceWithDebate(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	// The oldest finished request, with a closed debate and its experience
	// record; enough newer finished requests that the first call has more.
	request(t, db, "in", "P", rid(1), "completed", 40, `{"b":1}`)
	debate(t, db, sid(1), "in", "P", rid(1), "closed", 40, 1, 0)
	experience(t, db, sid(1), "respondent", 40)
	for i := 2; i <= MaxRequests+1; i++ {
		request(t, db, "in", "P", rid(i), "completed", 36, `{}`)
	}
	cutoff := Cutoff(now, MinOlderThan)
	dry, err := DryRun(ctx, db, cutoff, now)
	if err != nil {
		t.Fatal(err)
	}
	if dry.ExperienceRecords != 1 {
		t.Fatalf("dry run experience_records = %d, want 1 (the debate's)", dry.ExperienceRecords)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	first, more, err := PruneTx(ctx, tx, cutoff, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if !more || first.Debates != 1 || first.ExperienceRecords != 1 {
		t.Fatalf("first call = %+v (more %v), want the debate and its record together", first, more)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM experience_records`); n != 0 {
		t.Fatalf("%d experience records left after their debate went", n)
	}
}

// Retention.md §Approval: PruneTxWithin never removes more of a table than
// allowed, even when an item joined the set after counting; an item that
// does not fit ends the prune (more false).
func TestPruneWithinApproved(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	fixtures(t, db)
	cutoff := Cutoff(now, MinOlderThan)
	approved, err := DryRun(ctx, db, cutoff, now)
	if err != nil {
		t.Fatal(err)
	}
	// After counting: a finished request (its updated before the cutoff)
	// and an old inbox row join.
	request(t, db, "in", "Z", rid(20), "completed", 36, `{"b":20}`)
	mustExec(t, db, `INSERT INTO mail_inbox (from_key, id, kind, created, received_at, signed) VALUES ('P', 'm-9', 'request', 'c', ?, '')`, ago(37))
	var total Counts
	for i := 0; ; i++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		c, more, err := PruneTxWithin(ctx, tx, cutoff, now, approved.Sub(total))
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		total = total.Add(c)
		if !more {
			break
		}
		if i > 3 {
			t.Fatal("prune did not finish")
		}
	}
	if !total.within(approved) {
		t.Fatalf("removed %+v, more than approved %+v", total, approved)
	}
	if total.Requests != approved.Requests || total.MailInbox != approved.MailInbox {
		t.Fatalf("removed %+v, want the approved number of requests and inbox rows %+v", total, approved)
	}
	left, err := DryRun(ctx, db, cutoff, now)
	if err != nil {
		t.Fatal(err)
	}
	if left.Requests != 1 || left.MailInbox != 1 {
		t.Fatalf("left %+v, want one request and one inbox row for a new approval", left)
	}
	// Nothing allowed: nothing removed, and the prune ends.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	c, more, err := PruneTxWithin(ctx, tx, cutoff, now, Counts{})
	if err != nil || !c.Zero() || more {
		t.Fatalf("with nothing allowed: %+v, more %v, %v", c, more, err)
	}
}
