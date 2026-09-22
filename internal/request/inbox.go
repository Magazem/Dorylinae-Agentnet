package request

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// Ticket 1.6b: inbox_list (Docs/protocol/request.md §Inbox (1.6),
// Docs/protocol/ipc.md §Requests).

// InboxFilter narrows inbox_list.
type InboxFilter struct {
	Team string // team id, "" for any
	All  bool   // every in row, not just pending and due deferred
}

// InboxList runs inbox_list: pending in rows, plus deferred rows whose
// deferred_until has passed (marked Due), or every in row with All, ordered
// by effective priority descending, then received_at ascending, then peer,
// then id (Docs/protocol/request.md §Inbox (1.6)).
func (s *Store) InboxList(ctx context.Context, f InboxFilter) ([]View, error) {
	now := s.now()
	q := `SELECT ` + requestColumns + ` FROM requests WHERE direction = 'in'`
	var args []any
	if !f.All {
		q += ` AND (state = ? OR (state = ? AND deferred_until <= ?))`
		args = append(args, StatePending, StateDeferred, wireTime(now))
	}
	if f.Team != "" {
		q += ` AND team_id = ?`
		args = append(args, f.Team)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("request: inbox: %w", err)
	}
	var scanned []storedRow
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("request: inbox: scan: %w", err)
		}
		scanned = append(scanned, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	type naCount struct{ n, a int }
	naCache := map[string]naCount{}
	out := make([]View, 0, len(scanned))
	for _, r := range scanned {
		v, err := toView(r)
		if err != nil {
			return nil, err
		}
		na, ok := naCache[r.peer]
		if !ok {
			n, a, err := s.urgentAcceptance(ctx, r.peer, now)
			if err != nil {
				return nil, err
			}
			na = naCount{n: n, a: a}
			naCache[r.peer] = na
		}
		v.Priority = Priority(UrgencyBase(v.Urgency), na.n, na.a)
		if v.State == StateDeferred && !v.DeferredUntil.IsZero() && !v.DeferredUntil.After(now) {
			v.Due = true
		}
		v.UrgencyNote = urgencyNote(v.DowngradedBy, v.UrgencyDeclared)
		out = append(out, v)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		if !out[i].ReceivedAt.Equal(out[j].ReceivedAt) {
			return out[i].ReceivedAt.Before(out[j].ReceivedAt)
		}
		if out[i].Peer != out[j].Peer {
			return out[i].Peer < out[j].Peer
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// urgentAcceptance returns n and a for the effective-priority formula
// (Docs/protocol/request.md §Effective priority): over the last 30 days,
// the sender's in rows with effective urgency high or blocking and a first
// response (n), and how many of those were accepted (a).
func (s *Store) urgentAcceptance(ctx context.Context, peer string, now time.Time) (n, a int, err error) {
	cutoff := storeTime(now.Add(-maxAge))
	row := s.DB.QueryRowContext(ctx, `
		SELECT COUNT(*), SUM(CASE WHEN first_response = 'accept' THEN 1 ELSE 0 END)
		FROM requests
		WHERE direction = 'in' AND peer = ? AND urgency IN ('high', 'blocking')
		  AND received_at >= ? AND first_response IS NOT NULL`, peer, cutoff)
	var aSum sql.NullInt64
	if err := row.Scan(&n, &aSum); err != nil {
		return 0, 0, fmt.Errorf("request: urgent acceptance: %w", err)
	}
	if aSum.Valid {
		a = int(aSum.Int64)
	}
	return n, a, nil
}
