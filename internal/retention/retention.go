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
	"sort"
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

// Zero reports whether nothing was counted.
func (c Counts) Zero() bool { return c == Counts{} }

const storeTimeFmt = mail.StoreTimeFmt

// Cutoff is now − olderThan, the instant every removed item is older than.
func Cutoff(now time.Time, olderThan time.Duration) time.Time {
	return now.Add(-olderThan).UTC()
}

// queryer is what the planning reads need: a transaction for a real call, a
// read transaction for a dry run.
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
}

// finishedRequests selects the finished requests of retention.md §Finished
// items, oldest updated first, with the byte length of their content.
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

// makePlan reads what one call removes. limited applies the per-call bounds
// of retention.md §IPC; a dry run plans without them.
func makePlan(ctx context.Context, q queryer, cutoff, now time.Time, limited bool) (*plan, error) {
	p := &plan{sessions: map[string]bool{}, debates: map[string]bool{}, grants: map[string]bool{}, experience: map[[2]string]bool{}}
	cut := cutoff.UTC().Format(storeTimeFmt)

	// 1. Finished requests and what belongs to them.
	query := finishedRequests
	if limited {
		query += fmt.Sprintf(" LIMIT %d", MaxRequests+1)
	}
	var total int64
	err := each(ctx, q, query, []any{cut}, func(rows *sql.Rows) error {
		var k reqKey
		var size int64
		if err := rows.Scan(&k.direction, &k.peer, &k.id, &size); err != nil {
			return err
		}
		if limited && (len(p.requests) == MaxRequests || (len(p.requests) > 0 && total >= MaxContentBytes)) {
			p.more = true
			return errStop
		}
		p.requests = append(p.requests, k)
		total += size
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("retention: select requests: %w", err)
	}
	for _, k := range p.requests {
		wsRole, dRole := "worker", "respondent"
		if k.direction == "out" {
			wsRole, dRole = "requester", "initiator"
		}
		if err := collectIDs(ctx, q, `SELECT id FROM work_sessions WHERE peer = ? AND request_id = ? AND role = ?`,
			[]any{k.peer, k.id, wsRole}, p.sessions); err != nil {
			return nil, err
		}
		if err := collectIDs(ctx, q, `SELECT session FROM debates WHERE peer = ? AND request_id = ? AND role = ?`,
			[]any{k.peer, k.id, dRole}, p.debates); err != nil {
			return nil, err
		}
	}

	// 2. Orphans: closed sessions and closed or broken debates whose request
	// is gone, and grants by direction (review 71b F1).
	lim := ""
	if limited {
		lim = fmt.Sprintf(" LIMIT %d", MaxRows+1)
	}
	orphanSessions := map[string]bool{}
	if err := collectIDs(ctx, q, `SELECT w.id FROM work_sessions w
WHERE w.state = 'closed' AND w.updated < ?
  AND NOT EXISTS (SELECT 1 FROM requests r WHERE r.peer = w.peer AND r.id = w.request_id
	AND r.direction = CASE w.role WHEN 'worker' THEN 'in' ELSE 'out' END)
ORDER BY w.updated, w.id`+lim, []any{cut}, orphanSessions); err != nil {
		return nil, err
	}
	p.more = mergeBounded(p.sessions, orphanSessions, limited) || p.more
	orphanDebates := map[string]bool{}
	if err := collectIDs(ctx, q, `SELECT d.session FROM debates d
WHERE d.phase IN ('closed', 'broken') AND d.updated < ?
  AND NOT EXISTS (SELECT 1 FROM requests r WHERE r.peer = d.peer AND r.id = d.request_id
	AND r.direction = CASE d.role WHEN 'respondent' THEN 'in' ELSE 'out' END)
ORDER BY d.updated, d.session`+lim, []any{cut}, orphanDebates); err != nil {
		return nil, err
	}
	p.more = mergeBounded(p.debates, orphanDebates, limited) || p.more
	orphanGrants := map[string]bool{}
	if err := collectIDs(ctx, q, `SELECT g.id FROM grants g
WHERE (g.direction = 'held' AND (g.exp < ?1 OR (g.state = 'revoked' AND g.revoked_at < ?1)))
   OR (g.direction = 'issued' AND g.exp < ?1 AND NOT EXISTS (SELECT 1 FROM work_sessions w WHERE w.id = g.session))
ORDER BY g.exp, g.id`+lim, []any{cut}, orphanGrants); err != nil {
		return nil, err
	}
	p.more = mergeBounded(p.grants, orphanGrants, limited) || p.more

	// Everything in a removed session goes with it.
	for sid := range p.sessions {
		if err := collectIDs(ctx, q, `SELECT id FROM grants WHERE session = ?`, []any{sid}, p.grants); err != nil {
			return nil, err
		}
		if err := collectExperience(ctx, q, `SELECT session, role FROM experience_records WHERE session = ?`, []any{sid}, p.experience); err != nil {
			return nil, err
		}
	}
	for sid := range p.debates {
		var n, c int64
		if err := scanOne(ctx, q, `SELECT (SELECT COUNT(*) FROM debate_entries WHERE session = ?1), (SELECT COUNT(*) FROM debate_constraints WHERE session = ?1)`,
			[]any{sid}, &n, &c); err != nil {
			return nil, err
		}
		p.entries += n
		p.constraints += c
	}
	// Experience records left without a session (their session was removed
	// earlier) go by the same age rule.
	orphanExp := map[[2]string]bool{}
	if err := collectExperience(ctx, q, `SELECT x.session, x.role FROM experience_records x
WHERE x.created < ? AND NOT EXISTS (SELECT 1 FROM work_sessions w WHERE w.id = x.session)
  AND NOT EXISTS (SELECT 1 FROM debates d WHERE d.session = x.session)
ORDER BY x.created, x.session, x.role`+lim, []any{cut}, orphanExp); err != nil {
		return nil, err
	}
	if limited && len(orphanExp) > MaxRows {
		p.more = true
		n := 0
		for k := range orphanExp {
			if n++; n > MaxRows {
				delete(orphanExp, k)
			}
		}
	}
	for k := range orphanExp {
		p.experience[k] = true
	}

	// 3. mail_inbox: rows older than the cutoff whose mail_seen row is gone
	// (after the mail_seen prune), then pre-R55-F13 rows still holding
	// plaintext.
	seenCut := now.Add(-mail.SeenRetention).UTC().Format(storeTimeFmt)
	err = each(ctx, q, `SELECT i.rowid FROM mail_inbox i
WHERE i.received_at < ?1
  AND NOT EXISTS (SELECT 1 FROM mail_seen s WHERE s.from_key = i.from_key AND s.id = i.id AND s.received_at >= ?2)
ORDER BY i.received_at, i.rowid`+lim, []any{cut, seenCut}, func(rows *sql.Rows) error {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if limited && len(p.inbox) == MaxRows {
			p.more = true
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
		if limited && (len(p.blank) == MaxRows || (len(p.blank) > 0 && blanked >= MaxContentBytes)) {
			p.more = true
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

// mergeBounded adds the ids of add to into and reports whether add was cut to
// MaxRows (more remain).
func mergeBounded(into, add map[string]bool, limited bool) bool {
	ids := make([]string, 0, len(add))
	for id := range add {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	more := false
	if limited && len(ids) > MaxRows {
		ids, more = ids[:MaxRows], true
	}
	for _, id := range ids {
		into[id] = true
	}
	return more
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

func collectIDs(ctx context.Context, q queryer, query string, args []any, into map[string]bool) error {
	err := each(ctx, q, query, args, func(rows *sql.Rows) error {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		into[id] = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("retention: select: %w", err)
	}
	return nil
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

func (p *plan) counts() Counts {
	return Counts{
		Requests: int64(len(p.requests)), WorkSessions: int64(len(p.sessions)), Grants: int64(len(p.grants)),
		Debates: int64(len(p.debates)), DebateEntries: p.entries, DebateConstraints: p.constraints,
		ExperienceRecords: int64(len(p.experience)), MailInbox: int64(len(p.inbox)), InboxBlanked: int64(len(p.blank)),
	}
}

// DryRun counts what a full prune at cutoff would remove and blank, without
// the per-call bounds, and changes nothing (retention.md §IPC, dry_run).
func DryRun(ctx context.Context, db *sql.DB, cutoff, now time.Time) (Counts, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Counts{}, fmt.Errorf("retention: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return CountTx(ctx, tx, cutoff, now)
}

// CountTx is DryRun inside tx (the data_prune approval re-counts at confirm).
func CountTx(ctx context.Context, tx *sql.Tx, cutoff, now time.Time) (Counts, error) {
	p, err := makePlan(ctx, tx, cutoff, now, false)
	if err != nil {
		return Counts{}, err
	}
	return p.counts(), nil
}

// PruneTx removes, inside tx, one call's worth of finished items older than
// cutoff (retention.md §IPC, steps 1 to 3) and returns what it removed and
// whether rows remain. The caller commits, together with its audit row.
func PruneTx(ctx context.Context, tx *sql.Tx, cutoff, now time.Time) (Counts, bool, error) {
	if cutoff.After(now.Add(-MinOlderThan)) {
		return Counts{}, false, fmt.Errorf("retention: cutoff %s is less than 35 days old", cutoff.Format(time.RFC3339))
	}
	p, err := makePlan(ctx, tx, cutoff, now, true)
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
	return c, p.more, nil
}
