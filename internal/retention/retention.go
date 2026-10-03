// Package retention removes finished items for `agentnet prune`
// (Docs/protocol/retention.md, ticket R55-F13, owner decision D50). The daemon
// never calls it on its own: it runs only for IPC data_prune, one bounded
// transaction per call. It never touches audit_events or decisions.
package retention

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

const (
	// MinOlderThan is the smallest older_than (35 d, retention.md §Why 35 days).
	MinOlderThan = 35 * 24 * time.Hour
	// MaxRequests is the most requests one call removes.
	MaxRequests = 500
	// MaxContentBytes stops adding requests to a call, and stops blanking,
	// once this much content has been taken (8 MiB, retention.md §IPC).
	MaxContentBytes = 8 << 20
	// MaxRows is the most rows of each orphan table, and of mail_inbox, one
	// call removes or blanks.
	MaxRows = 5000
)

// Counts is what a call removed (or, for a dry run, would remove).
type Counts struct {
	Requests          int64 `json:"requests"`
	WorkSessions      int64 `json:"work_sessions"`
	Grants            int64 `json:"grants"`
	Debates           int64 `json:"debates"`
	DebateEntries     int64 `json:"debate_entries"`
	DebateConstraints int64 `json:"debate_constraints"`
	ExperienceRecords int64 `json:"experience_records"`
	MailInbox         int64 `json:"mail_inbox"`
	InboxBlanked      int64 `json:"inbox_blanked"`
}

// Add returns the sum of c and o.
func (c Counts) Add(o Counts) Counts {
	return Counts{
		Requests: c.Requests + o.Requests, WorkSessions: c.WorkSessions + o.WorkSessions,
		Grants: c.Grants + o.Grants, Debates: c.Debates + o.Debates,
		DebateEntries: c.DebateEntries + o.DebateEntries, DebateConstraints: c.DebateConstraints + o.DebateConstraints,
		ExperienceRecords: c.ExperienceRecords + o.ExperienceRecords, MailInbox: c.MailInbox + o.MailInbox,
		InboxBlanked: c.InboxBlanked + o.InboxBlanked,
	}
}

// Sub returns c less o, table by table.
func (c Counts) Sub(o Counts) Counts {
	return Counts{
		Requests: c.Requests - o.Requests, WorkSessions: c.WorkSessions - o.WorkSessions,
		Grants: c.Grants - o.Grants, Debates: c.Debates - o.Debates,
		DebateEntries: c.DebateEntries - o.DebateEntries, DebateConstraints: c.DebateConstraints - o.DebateConstraints,
		ExperienceRecords: c.ExperienceRecords - o.ExperienceRecords, MailInbox: c.MailInbox - o.MailInbox,
		InboxBlanked: c.InboxBlanked - o.InboxBlanked,
	}
}

// Zero reports whether nothing was counted.
func (c Counts) Zero() bool { return c == Counts{} }

const storeTimeFmt = mail.StoreTimeFmt

// Cutoff is now − olderThan, the instant every removed item is older than.
func Cutoff(now time.Time, olderThan time.Duration) time.Time {
	return now.Add(-olderThan).UTC()
}

// queryer is what the planning reads need: the call's transaction.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// reqKey is the primary key of a requests row.
type reqKey struct{ direction, peer, id string }

// plan is the set of rows one call removes or blanks. Every table's rows are
// kept as sets of ids, so a row that two rules select is counted once.
type plan struct {
	requests    []reqKey
	sessions    map[string]bool // work_sessions ids
	debates     map[string]bool // debates sessions
	grants      map[string]bool // grant ids
	experience  map[[2]string]bool
	entries     int64 // debate_entries of the debates (counted, removed by session)
	constraints int64
	inbox       []int64 // mail_inbox rowids to delete
	blank       []int64 // mail_inbox rowids to blank
	more        bool

	// allow is what this call may still remove, table by table (the approved
	// counts less what the earlier calls of the prune removed), or nil for no
	// bound but the per-call ones. used is what the plan holds so far, and
	// capped is set once an item did not fit (retention.md §Approval).
	allow  *Counts
	used   Counts
	capped bool
}

// within reports whether every count of c is at most the one of a.
func (c Counts) within(a Counts) bool {
	return c.Requests <= a.Requests && c.WorkSessions <= a.WorkSessions && c.Grants <= a.Grants &&
		c.Debates <= a.Debates && c.DebateEntries <= a.DebateEntries && c.DebateConstraints <= a.DebateConstraints &&
		c.ExperienceRecords <= a.ExperienceRecords && c.MailInbox <= a.MailInbox && c.InboxBlanked <= a.InboxBlanked
}

// fits takes add into the plan's running counts if they stay within allow,
// and otherwise marks the plan capped and reports false.
func (p *plan) fits(add Counts) bool {
	u := p.used.Add(add)
	if p.allow != nil && !u.within(*p.allow) {
		p.capped = true
		return false
	}
	p.used = u
	return true
}

// group is one item with everything removed with it, not yet in the plan.
type group struct {
	request     *reqKey
	sessions    map[string]bool
	debates     map[string]bool
	grants      map[string]bool
	experience  map[[2]string]bool
	entries     int64
	constraints int64
}

func newGroup() *group {
	return &group{sessions: map[string]bool{}, debates: map[string]bool{}, grants: map[string]bool{}, experience: map[[2]string]bool{}}
}

func (g *group) counts() Counts {
	c := Counts{WorkSessions: int64(len(g.sessions)), Debates: int64(len(g.debates)), Grants: int64(len(g.grants)),
		ExperienceRecords: int64(len(g.experience)), DebateEntries: g.entries, DebateConstraints: g.constraints}
	if g.request != nil {
		c.Requests = 1
	}
	return c
}

// take adds g to the plan if it fits, and reports whether it did.
func (p *plan) take(g *group) bool {
	if !p.fits(g.counts()) {
		return false
	}
	if g.request != nil {
		p.requests = append(p.requests, *g.request)
	}
	for id := range g.sessions {
		p.sessions[id] = true
	}
	for id := range g.debates {
		p.debates[id] = true
	}
	for id := range g.grants {
		p.grants[id] = true
	}
	for k := range g.experience {
		p.experience[k] = true
	}
	p.entries += g.entries
	p.constraints += g.constraints
	return true
}

// addSession adds a work session to g with its grants and experience
// records, leaving out what the plan already holds.
func (p *plan) addSession(ctx context.Context, q queryer, g *group, sid string) error {
	if p.sessions[sid] || g.sessions[sid] {
		return nil
	}
	g.sessions[sid] = true
	ids, err := listIDs(ctx, q, `SELECT id FROM grants WHERE session = ?`, sid)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !p.grants[id] {
			g.grants[id] = true
		}
	}
	return p.addExperience(ctx, q, g, sid)
}

// addDebate adds a debate to g with its entries, constraints and experience
// records (debate/experience.go writes them under the debate's session,
// review 81b L1).
func (p *plan) addDebate(ctx context.Context, q queryer, g *group, sid string) error {
	if p.debates[sid] || g.debates[sid] {
		return nil
	}
	g.debates[sid] = true
	var n, c int64
	if err := scanOne(ctx, q, `SELECT (SELECT COUNT(*) FROM debate_entries WHERE session = ?1), (SELECT COUNT(*) FROM debate_constraints WHERE session = ?1)`,
		[]any{sid}, &n, &c); err != nil {
		return err
	}
	g.entries += n
	g.constraints += c
	return p.addExperience(ctx, q, g, sid)
}

func (p *plan) addExperience(ctx context.Context, q queryer, g *group, sid string) error {
	recs := map[[2]string]bool{}
	if err := collectExperience(ctx, q, `SELECT session, role FROM experience_records WHERE session = ?`, []any{sid}, recs); err != nil {
		return err
	}
	for k := range recs {
		if !p.experience[k] {
			g.experience[k] = true
		}
	}
	return nil
}

// finishedRequests selects the finished requests of retention.md §Finished
// items, oldest updated first, with the byte length of their content.
// countAll repeats its rules, and makePlan's below, for the dry run: change
// both together (TestPruneFinishedOnly compares them).
const finishedRequests = `
SELECT r.direction, r.peer, r.id,
	length(r.body) + COALESCE(length(r.result), 0) + COALESCE(length(r.last_reply), 0)
	+ COALESCE((SELECT SUM(COALESCE(length(w.result), 0) + COALESCE(length(w.changes), 0)) FROM work_sessions w
		WHERE w.peer = r.peer AND w.request_id = r.id
		  AND w.role = CASE r.direction WHEN 'in' THEN 'worker' ELSE 'requester' END), 0)
	+ COALESCE((SELECT SUM(length(e.entry)) FROM debate_entries e JOIN debates d ON d.session = e.session
		WHERE d.peer = r.peer AND d.request_id = r.id
		  AND d.role = CASE r.direction WHEN 'in' THEN 'respondent' ELSE 'initiator' END), 0)
FROM requests r
WHERE r.state IN ('declined', 'completed', 'cancelled') AND r.updated < ?1
  AND NOT EXISTS (SELECT 1 FROM work_sessions w
	WHERE w.peer = r.peer AND w.request_id = r.id
	  AND w.role = CASE r.direction WHEN 'in' THEN 'worker' ELSE 'requester' END
	  AND (w.state <> 'closed' OR w.updated >= ?1))
  AND NOT EXISTS (SELECT 1 FROM debates d
	WHERE d.peer = r.peer AND d.request_id = r.id
	  AND d.role = CASE r.direction WHEN 'in' THEN 'respondent' ELSE 'initiator' END
	  AND (d.phase NOT IN ('closed', 'broken') OR d.updated >= ?1))
ORDER BY r.updated, r.direction, r.peer, r.id`

// makePlan reads what one call removes, within the per-call bounds of
// retention.md §IPC and, when allow is not nil, within allow table by table
// (retention.md §Approval). A dry run counts with countAll instead.
func makePlan(ctx context.Context, q queryer, cutoff, now time.Time, allow *Counts) (*plan, error) {
	p := &plan{sessions: map[string]bool{}, debates: map[string]bool{}, grants: map[string]bool{}, experience: map[[2]string]bool{}, allow: allow}
	cut := cutoff.UTC().Format(storeTimeFmt)

	// 1. Finished requests and what belongs to them.
	query := finishedRequests + fmt.Sprintf(" LIMIT %d", MaxRequests+1)
	var total int64
	var reqs []reqKey
	err := each(ctx, q, query, []any{cut}, func(rows *sql.Rows) error {
		var k reqKey
		var size int64
		if err := rows.Scan(&k.direction, &k.peer, &k.id, &size); err != nil {
			return err
		}
		if len(reqs) == MaxRequests || (len(reqs) > 0 && total >= MaxContentBytes) {
			p.more = true
			return errStop
		}
		reqs = append(reqs, k)
		total += size
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("retention: select requests: %w", err)
	}
	for i := range reqs {
		k := reqs[i]
		wsRole, dRole := "worker", "respondent"
		if k.direction == "out" {
			wsRole, dRole = "requester", "initiator"
		}
		g := newGroup()
		g.request = &k
		sessions, err := listIDs(ctx, q, `SELECT id FROM work_sessions WHERE peer = ? AND request_id = ? AND role = ?`, k.peer, k.id, wsRole)
		if err != nil {
			return nil, err
		}
		for _, sid := range sessions {
			if err := p.addSession(ctx, q, g, sid); err != nil {
				return nil, err
			}
		}
		debates, err := listIDs(ctx, q, `SELECT session FROM debates WHERE peer = ? AND request_id = ? AND role = ?`, k.peer, k.id, dRole)
		if err != nil {
			return nil, err
		}
		for _, sid := range debates {
			if err := p.addDebate(ctx, q, g, sid); err != nil {
				return nil, err
			}
		}
		if !p.take(g) {
			break
		}
	}

	// 2. Orphans: closed sessions and closed or broken debates whose request
	// is gone (each with what belongs to it), and grants by direction
	// (review 71b F1).
	lim := fmt.Sprintf(" LIMIT %d", MaxRows+1)
	orphanSessions, err := listIDs(ctx, q, `SELECT w.id FROM work_sessions w
WHERE w.state = 'closed' AND w.updated < ?
  AND NOT EXISTS (SELECT 1 FROM requests r WHERE r.peer = w.peer AND r.id = w.request_id
	AND r.direction = CASE w.role WHEN 'worker' THEN 'in' ELSE 'out' END)
ORDER BY w.updated, w.id`+lim, cut)
	if err != nil {
		return nil, err
	}
	for i, sid := range orphanSessions {
		if i == MaxRows {
			p.more = true
			break
		}
		g := newGroup()
		if err := p.addSession(ctx, q, g, sid); err != nil {
			return nil, err
		}
		if !p.take(g) {
			break
		}
	}
	orphanDebates, err := listIDs(ctx, q, `SELECT d.session FROM debates d
WHERE d.phase IN ('closed', 'broken') AND d.updated < ?
  AND NOT EXISTS (SELECT 1 FROM requests r WHERE r.peer = d.peer AND r.id = d.request_id
	AND r.direction = CASE d.role WHEN 'respondent' THEN 'in' ELSE 'out' END)
ORDER BY d.updated, d.session`+lim, cut)
	if err != nil {
		return nil, err
	}
	for i, sid := range orphanDebates {
		if i == MaxRows {
			p.more = true
			break
		}
		g := newGroup()
		if err := p.addDebate(ctx, q, g, sid); err != nil {
			return nil, err
		}
		if !p.take(g) {
			break
		}
	}
	orphanGrants, err := listIDs(ctx, q, `SELECT g.id FROM grants g
WHERE (g.direction = 'held' AND (g.exp < ?1 OR (g.state = 'revoked' AND g.revoked_at < ?1)))
   OR (g.direction = 'issued' AND g.exp < ?1 AND NOT EXISTS (SELECT 1 FROM work_sessions w WHERE w.id = g.session))
ORDER BY g.exp, g.id`+lim, cut)
	if err != nil {
		return nil, err
	}
	for i, id := range orphanGrants {
		if i == MaxRows {
			p.more = true
			break
		}
		if p.grants[id] {
			continue
		}
		if !p.fits(Counts{Grants: 1}) {
			break
		}
		p.grants[id] = true
	}
	// Experience records left without a session or debate (theirs was
	// removed earlier) go by the same age rule.
	var orphanExp [][2]string
	err = each(ctx, q, `SELECT x.session, x.role FROM experience_records x
WHERE x.created < ? AND NOT EXISTS (SELECT 1 FROM work_sessions w WHERE w.id = x.session)
  AND NOT EXISTS (SELECT 1 FROM debates d WHERE d.session = x.session)
ORDER BY x.created, x.session, x.role`+lim, []any{cut}, func(rows *sql.Rows) error {
		var k [2]string
		if err := rows.Scan(&k[0], &k[1]); err != nil {
			return err
		}
		orphanExp = append(orphanExp, k)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("retention: select experience: %w", err)
	}
	for i, k := range orphanExp {
		if i == MaxRows {
			p.more = true
			break
		}
		if p.experience[k] {
			continue
		}
		if !p.fits(Counts{ExperienceRecords: 1}) {
			break
		}
		p.experience[k] = true
	}

	// 3. mail_inbox: rows older than the cutoff whose mail_seen row is gone
	// (after the mail_seen prune), then pre-R55-F13 rows still holding
	// plaintext.
	seenCut, err := mail.SeenCutoff(ctx, q, now)
	if err != nil {
		return nil, err
	}
	err = each(ctx, q, `SELECT i.rowid FROM mail_inbox i
WHERE i.received_at < ?1
  AND NOT EXISTS (SELECT 1 FROM mail_seen s WHERE s.from_key = i.from_key AND s.id = i.id AND s.received_at >= ?2)
ORDER BY i.received_at, i.rowid`+lim, []any{cut, seenCut}, func(rows *sql.Rows) error {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if len(p.inbox) == MaxRows {
			p.more = true
			return errStop
		}
		if !p.fits(Counts{MailInbox: 1}) {
			return errStop
		}
		p.inbox = append(p.inbox, id)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("retention: select inbox: %w", err)
	}
	// A row that this prune deletes (now or in a later call) is not
	// blanked first, so a dry run's counts equal the sum of the calls'.
	var blanked int64
	err = each(ctx, q, `SELECT i.rowid, length(i.signed) FROM mail_inbox i
WHERE i.signed <> ''
  AND (i.received_at >= ?1
	OR EXISTS (SELECT 1 FROM mail_seen s WHERE s.from_key = i.from_key AND s.id = i.id AND s.received_at >= ?2))
ORDER BY i.rowid`, []any{cut, seenCut}, func(rows *sql.Rows) error {
		var id, size int64
		if err := rows.Scan(&id, &size); err != nil {
			return err
		}
		if len(p.blank) == MaxRows || (len(p.blank) > 0 && blanked >= MaxContentBytes) {
			p.more = true
			return errStop
		}
		if !p.fits(Counts{InboxBlanked: 1}) {
			return errStop
		}
		p.blank = append(p.blank, id)
		blanked += size
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("retention: select inbox copies: %w", err)
	}
	return p, nil
}

var errStop = errors.New("retention: stop")

func each(ctx context.Context, q queryer, query string, args []any, fn func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := fn(rows); err != nil {
			if errors.Is(err, errStop) {
				return nil
			}
			return err
		}
	}
	return rows.Err()
}

func scanOne(ctx context.Context, q queryer, query string, args []any, dest ...any) error {
	return each(ctx, q, query, args, func(rows *sql.Rows) error { return rows.Scan(dest...) })
}

// listIDs returns the one-column result of query, in its order.
func listIDs(ctx context.Context, q queryer, query string, args ...any) ([]string, error) {
	var ids []string
	err := each(ctx, q, query, args, func(rows *sql.Rows) error {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("retention: select: %w", err)
	}
	return ids, nil
}

func collectExperience(ctx context.Context, q queryer, query string, args []any, into map[[2]string]bool) error {
	err := each(ctx, q, query, args, func(rows *sql.Rows) error {
		var k [2]string
		if err := rows.Scan(&k[0], &k[1]); err != nil {
			return err
		}
		into[k] = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("retention: select experience: %w", err)
	}
	return nil
}

// countAll counts, in one statement, the rows makePlan selects with no
// per-call bounds (review 81 L1): the same rules as sets of ids, so a row two
// rules select is counted once, without reading a row out per item.
// ?1 is the cutoff, ?2 the mail_seen cutoff.
const countAll = `
WITH fin(direction, peer, id) AS (
	SELECT r.direction, r.peer, r.id FROM requests r
	WHERE r.state IN ('declined', 'completed', 'cancelled') AND r.updated < ?1
	  AND NOT EXISTS (SELECT 1 FROM work_sessions w
		WHERE w.peer = r.peer AND w.request_id = r.id
		  AND w.role = CASE r.direction WHEN 'in' THEN 'worker' ELSE 'requester' END
		  AND (w.state <> 'closed' OR w.updated >= ?1))
	  AND NOT EXISTS (SELECT 1 FROM debates d
		WHERE d.peer = r.peer AND d.request_id = r.id
		  AND d.role = CASE r.direction WHEN 'in' THEN 'respondent' ELSE 'initiator' END
		  AND (d.phase NOT IN ('closed', 'broken') OR d.updated >= ?1))
),
ws(id) AS (
	SELECT w.id FROM work_sessions w JOIN fin r ON w.peer = r.peer AND w.request_id = r.id
		AND w.role = CASE r.direction WHEN 'in' THEN 'worker' ELSE 'requester' END
	UNION
	SELECT w.id FROM work_sessions w
	WHERE w.state = 'closed' AND w.updated < ?1
	  AND NOT EXISTS (SELECT 1 FROM requests r WHERE r.peer = w.peer AND r.id = w.request_id
		AND r.direction = CASE w.role WHEN 'worker' THEN 'in' ELSE 'out' END)
),
db(session) AS (
	SELECT d.session FROM debates d JOIN fin r ON d.peer = r.peer AND d.request_id = r.id
		AND d.role = CASE r.direction WHEN 'in' THEN 'respondent' ELSE 'initiator' END
	UNION
	SELECT d.session FROM debates d
	WHERE d.phase IN ('closed', 'broken') AND d.updated < ?1
	  AND NOT EXISTS (SELECT 1 FROM requests r WHERE r.peer = d.peer AND r.id = d.request_id
		AND r.direction = CASE d.role WHEN 'respondent' THEN 'in' ELSE 'out' END)
),
gr(id) AS (
	SELECT g.id FROM grants g
	WHERE (g.direction = 'held' AND (g.exp < ?1 OR (g.state = 'revoked' AND g.revoked_at < ?1)))
	   OR (g.direction = 'issued' AND g.exp < ?1 AND NOT EXISTS (SELECT 1 FROM work_sessions w WHERE w.id = g.session))
	UNION
	SELECT g.id FROM grants g JOIN ws ON g.session = ws.id
),
ex(session, role) AS (
	SELECT x.session, x.role FROM experience_records x JOIN ws ON x.session = ws.id
	UNION
	SELECT x.session, x.role FROM experience_records x JOIN db ON x.session = db.session
	UNION
	SELECT x.session, x.role FROM experience_records x
	WHERE x.created < ?1 AND NOT EXISTS (SELECT 1 FROM work_sessions w WHERE w.id = x.session)
	  AND NOT EXISTS (SELECT 1 FROM debates d WHERE d.session = x.session)
)
SELECT (SELECT COUNT(*) FROM fin), (SELECT COUNT(*) FROM ws), (SELECT COUNT(*) FROM gr), (SELECT COUNT(*) FROM db),
	(SELECT COUNT(*) FROM debate_entries WHERE session IN (SELECT session FROM db)),
	(SELECT COUNT(*) FROM debate_constraints WHERE session IN (SELECT session FROM db)),
	(SELECT COUNT(*) FROM ex),
	(SELECT COUNT(*) FROM mail_inbox i WHERE i.received_at < ?1
	  AND NOT EXISTS (SELECT 1 FROM mail_seen s WHERE s.from_key = i.from_key AND s.id = i.id AND s.received_at >= ?2)),
	(SELECT COUNT(*) FROM mail_inbox i WHERE i.signed <> ''
	  AND (i.received_at >= ?1
		OR EXISTS (SELECT 1 FROM mail_seen s WHERE s.from_key = i.from_key AND s.id = i.id AND s.received_at >= ?2)))`

// DryRun counts what a full prune at cutoff would remove and blank, without
// the per-call bounds, and changes nothing (retention.md §IPC, dry_run). It
// is one read-only statement (review 81 L1), run outside any write
// transaction.
func DryRun(ctx context.Context, db *sql.DB, cutoff, now time.Time) (Counts, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Counts{}, fmt.Errorf("retention: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var c Counts
	cut := cutoff.UTC().Format(storeTimeFmt)
	seenCut, err := mail.SeenCutoff(ctx, tx, now)
	if err != nil {
		return Counts{}, err
	}
	if err := tx.QueryRowContext(ctx, countAll, cut, seenCut).Scan(&c.Requests, &c.WorkSessions, &c.Grants, &c.Debates,
		&c.DebateEntries, &c.DebateConstraints, &c.ExperienceRecords, &c.MailInbox, &c.InboxBlanked); err != nil {
		return Counts{}, fmt.Errorf("retention: count: %w", err)
	}
	return c, nil
}

// PruneTx removes, inside tx, one call's worth of finished items older than
// cutoff (retention.md §IPC, steps 1 to 3) and returns what it removed and
// whether rows remain. The caller commits, together with its audit row.
func PruneTx(ctx context.Context, tx *sql.Tx, cutoff, now time.Time) (Counts, bool, error) {
	return pruneTx(ctx, tx, cutoff, now, nil)
}

// PruneTxWithin is PruneTx that removes no more than allow of each table
// (the approved counts less what the prune's earlier calls removed,
// retention.md §Approval). An item that does not fit ends the prune: it and
// everything after it are left, and more is false.
func PruneTxWithin(ctx context.Context, tx *sql.Tx, cutoff, now time.Time, allow Counts) (Counts, bool, error) {
	return pruneTx(ctx, tx, cutoff, now, &allow)
}

func pruneTx(ctx context.Context, tx *sql.Tx, cutoff, now time.Time, allow *Counts) (Counts, bool, error) {
	if cutoff.After(now.Add(-MinOlderThan)) {
		return Counts{}, false, fmt.Errorf("retention: cutoff %s is less than 35 days old", cutoff.Format(time.RFC3339))
	}
	p, err := makePlan(ctx, tx, cutoff, now, allow)
	if err != nil {
		return Counts{}, false, err
	}
	var c Counts
	exec := func(n *int64, query string, args ...any) error {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("retention: %s: %w", strings.Fields(query)[0], err)
		}
		k, _ := res.RowsAffected()
		if n != nil {
			*n += k
		}
		return nil
	}
	for _, k := range p.requests {
		if err := exec(&c.Requests, `DELETE FROM requests WHERE direction = ? AND peer = ? AND id = ?`, k.direction, k.peer, k.id); err != nil {
			return Counts{}, false, err
		}
	}
	for id := range p.grants {
		if err := exec(&c.Grants, `DELETE FROM grants WHERE id = ?`, id); err != nil {
			return Counts{}, false, err
		}
	}
	for k := range p.experience {
		if err := exec(&c.ExperienceRecords, `DELETE FROM experience_records WHERE session = ? AND role = ?`, k[0], k[1]); err != nil {
			return Counts{}, false, err
		}
	}
	for sid := range p.debates {
		if err := exec(&c.DebateEntries, `DELETE FROM debate_entries WHERE session = ?`, sid); err != nil {
			return Counts{}, false, err
		}
		if err := exec(&c.DebateConstraints, `DELETE FROM debate_constraints WHERE session = ?`, sid); err != nil {
			return Counts{}, false, err
		}
		if err := exec(&c.Debates, `DELETE FROM debates WHERE session = ?`, sid); err != nil {
			return Counts{}, false, err
		}
	}
	for sid := range p.sessions {
		if err := exec(&c.WorkSessions, `DELETE FROM work_sessions WHERE id = ?`, sid); err != nil {
			return Counts{}, false, err
		}
	}
	// mail_seen first, in this transaction, so an id never leaves mail_inbox
	// while mail_seen still holds it (mail.md §Dedupe and inbox).
	if _, err := mail.PruneSeenTx(ctx, tx, now); err != nil {
		return Counts{}, false, err
	}
	for _, id := range p.inbox {
		if err := exec(&c.MailInbox, `DELETE FROM mail_inbox WHERE rowid = ?`, id); err != nil {
			return Counts{}, false, err
		}
	}
	for _, id := range p.blank {
		if err := exec(&c.InboxBlanked, `UPDATE mail_inbox SET signed = '' WHERE rowid = ? AND signed <> ''`, id); err != nil {
			return Counts{}, false, err
		}
	}
	return c, p.more && !p.capped, nil
}
