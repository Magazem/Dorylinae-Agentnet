package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Query limits (Docs/protocol/audit.md §agentnet log).
const (
	DefaultLimit = 1000
	MaxLimit     = 5000

	queryBatch = 1000
)

// ErrBadParams marks list parameters the caller got wrong (IPC bad_request).
var ErrBadParams = errors.New("audit: bad parameters")

// ListParams are the params of the audit_list IPC method. Since and Until are
// RFC 3339 times (the CLI turns "24h" into one); Action is a prefix; Session
// is an s- or r- id.
type ListParams struct {
	Since   string `json:"since,omitempty"`
	Until   string `json:"until,omitempty"`
	Session string `json:"session,omitempty"`
	Action  string `json:"action,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	AfterID int64  `json:"after_id,omitempty"`
}

// Entry is one row as audit_list returns it. Hash is empty for a legacy row.
type Entry struct {
	ID     int64           `json:"id"`
	TS     string          `json:"ts"`
	Actor  string          `json:"actor"`
	Action string          `json:"action"`
	Detail json.RawMessage `json:"detail"`
	Hash   string          `json:"hash,omitempty"`
}

// ListResult is the result of audit_list.
type ListResult struct {
	Events      []Entry `json:"events"`
	NextAfterID int64   `json:"next_after_id,omitempty"`
}

// Anchor is the head as an --anchor argument, "ID:HASH".
func (h Head) Anchor() string { return fmt.Sprintf("%d:%s", h.ID, h.Hash) }

// Head returns the newest row (id, hash, ts), or nil for an empty log.
func (l *Log) Head(ctx context.Context) (*Head, error) {
	var (
		h    Head
		hash sql.NullString
	)
	err := l.db.QueryRowContext(ctx, `SELECT id, ts, hash FROM audit_events ORDER BY id DESC LIMIT 1`).Scan(&h.ID, &h.TS, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("audit: head: %w", err)
	}
	h.Hash = hash.String
	return &h, nil
}

// Query returns the rows matching p, oldest first, at most p.Limit of them (the
// default and maximum are DefaultLimit and MaxLimit). Every row is returned,
// including audit.chain_start. When more rows match, NextAfterID is the id to
// pass as AfterID to continue. It runs in short reads of queryBatch rows, so
// it never holds the daemon's only connection for long.
func (l *Log) Query(ctx context.Context, p ListParams) (*ListResult, error) {
	limit := p.Limit
	switch {
	case limit < 0:
		return nil, fmt.Errorf("%w: limit is negative", ErrBadParams)
	case limit == 0:
		limit = DefaultLimit
	case limit > MaxLimit:
		limit = MaxLimit
	}
	if p.AfterID < 0 {
		return nil, fmt.Errorf("%w: after_id is negative", ErrBadParams)
	}
	var since, until time.Time
	var err error
	if p.Since != "" {
		if since, err = time.Parse(time.RFC3339Nano, p.Since); err != nil {
			return nil, fmt.Errorf("%w: since is not an RFC 3339 time", ErrBadParams)
		}
	}
	if p.Until != "" {
		if until, err = time.Parse(time.RFC3339Nano, p.Until); err != nil {
			return nil, fmt.Errorf("%w: until is not an RFC 3339 time", ErrBadParams)
		}
	}

	var where []string
	var args []any
	if !since.IsZero() {
		where = append(where, `substr(ts, 1, 19) >= ?`)
		args = append(args, since.UTC().Format("2006-01-02T15:04:05"))
	}
	if !until.IsZero() {
		where = append(where, `substr(ts, 1, 19) <= ?`)
		args = append(args, until.UTC().Format("2006-01-02T15:04:05"))
	}
	if p.Action != "" {
		where = append(where, `substr(action, 1, ?) = ?`)
		args = append(args, utf8.RuneCountInString(p.Action), p.Action)
	}
	if p.Session != "" {
		clause, cargs, err := l.sessionClause(ctx, p.Session)
		if err != nil {
			return nil, err
		}
		where = append(where, clause)
		args = append(args, cargs...)
	}

	q := `SELECT id, ts, actor, action, detail, hash FROM audit_events WHERE id > ?`
	for _, w := range where {
		q += ` AND ` + w
	}
	q += ` ORDER BY id LIMIT ?`

	res := &ListResult{Events: []Entry{}}
	after := p.AfterID
	for {
		batchArgs := append([]any{after}, args...)
		batchArgs = append(batchArgs, queryBatch)
		batch, last, err := l.readEntries(ctx, q, batchArgs)
		if err != nil {
			return nil, err
		}
		for _, e := range batch {
			if !inWindow(e.TS, since, until) {
				continue
			}
			if len(res.Events) == limit {
				res.NextAfterID = res.Events[limit-1].ID
				return res, nil
			}
			res.Events = append(res.Events, e)
		}
		if len(batch) < queryBatch {
			return res, nil
		}
		after = last
	}
}

// inWindow is the exact check behind the second-resolution SQL prefilter.
func inWindow(ts string, since, until time.Time) bool {
	if since.IsZero() && until.IsZero() {
		return true
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return false
	}
	return (since.IsZero() || !t.Before(since)) && (until.IsZero() || !t.After(until))
}

// readEntries runs one batch and closes the query before returning. last is
// the id of the last row scanned (before any window filtering).
func (l *Log) readEntries(ctx context.Context, q string, args []any) ([]Entry, int64, error) {
	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("audit: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var (
		out  []Entry
		last int64
	)
	for rows.Next() {
		var (
			e      Entry
			detail string
			hash   sql.NullString
		)
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Action, &detail, &hash); err != nil {
			return nil, 0, fmt.Errorf("audit: list: %w", err)
		}
		e.Hash = hash.String
		e.Detail = rawDetail(detail)
		out = append(out, e)
		last = e.ID
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("audit: list: %w", err)
	}
	return out, last, nil
}

// rawDetail keeps a tampered (invalid JSON) detail from breaking the whole
// response: it is returned as a JSON string, which Verify reports as malformed.
func rawDetail(s string) json.RawMessage {
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	b, _ := json.Marshal(s)
	return b
}

// sessionClause resolves an s- or r- id to the SQL condition selecting the
// rows of that session (audit.md §agentnet log): rows naming the session, its
// request together with its peer, its grants, its approvals (subject = the
// session or one of its grants) and its decision.
func (l *Log) sessionClause(ctx context.Context, id string) (string, []any, error) {
	type reqPeer struct{ request, peer string }
	var (
		sessions []string
		reqs     []reqPeer
	)
	var q string
	switch {
	case strings.HasPrefix(id, "s-"):
		q = `SELECT id, request_id, peer FROM work_sessions WHERE id = ?`
		sessions = append(sessions, id)
	case strings.HasPrefix(id, "r-"):
		q = `SELECT id, request_id, peer FROM work_sessions WHERE request_id = ?`
	default:
		return "", nil, fmt.Errorf("%w: session must be an s- or r- id", ErrBadParams)
	}
	rows, err := l.db.QueryContext(ctx, q, id)
	if err != nil {
		return "", nil, fmt.Errorf("audit: resolve session: %w", err)
	}
	found := false
	for rows.Next() {
		var sid, rid, peer string
		if err := rows.Scan(&sid, &rid, &peer); err != nil {
			_ = rows.Close()
			return "", nil, fmt.Errorf("audit: resolve session: %w", err)
		}
		found = true
		if !strings.HasPrefix(id, "s-") {
			sessions = append(sessions, sid)
		}
		reqs = append(reqs, reqPeer{rid, peer})
	}
	err = rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", nil, fmt.Errorf("audit: resolve session: %w", err)
	}
	if !found && strings.HasPrefix(id, "r-") {
		// A request without a session shows its request rows.
		reqs = append(reqs, reqPeer{id, ""})
	}

	grants, err := l.column(ctx, `SELECT id FROM grants WHERE session IN (`+placeholders(len(sessions))+`)`, sessions)
	if err != nil {
		return "", nil, err
	}
	subjects := append(append([]string{}, sessions...), grants...)
	approvals, err := l.column(ctx, `SELECT id FROM approvals WHERE subject IN (`+placeholders(len(subjects))+`)`, subjects)
	if err != nil {
		return "", nil, err
	}

	var conds []string
	var args []any
	add := func(cond string, vals ...string) {
		if len(vals) == 0 {
			return
		}
		conds = append(conds, strings.ReplaceAll(cond, "(?)", "("+placeholders(len(vals))+")"))
		for _, v := range vals {
			args = append(args, v)
		}
	}
	add(`json_extract(detail, '$.session') IN (?)`, sessions...)
	add(`json_extract(detail, '$.grant') IN (?)`, grants...)
	add(`json_extract(detail, '$.subject') IN (?)`, subjects...)
	var ids []string
	ids = append(ids, approvals...)
	for _, s := range sessions {
		ids = append(ids, DecisionID(s))
	}
	add(`json_extract(detail, '$.id') IN (?)`, ids...)
	for _, r := range reqs {
		if r.peer == "" {
			conds = append(conds, `json_extract(detail, '$.request') = ?`)
			args = append(args, r.request)
			continue
		}
		conds = append(conds, `(json_extract(detail, '$.request') = ? AND json_extract(detail, '$.peer') = ?)`)
		args = append(args, r.request, r.peer)
	}
	if len(conds) == 0 {
		return `0`, nil, nil // an unknown s- id with nothing to match
	}
	// CASE, not AND: json_extract fails on a tampered, invalid detail, and
	// SQLite does not promise to short-circuit.
	return `CASE WHEN json_valid(detail) THEN (` + strings.Join(conds, ` OR `) + `) ELSE 0 END`, args, nil
}

func (l *Log) column(ctx context.Context, q string, args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}
	rows, err := l.db.QueryContext(ctx, q, anyArgs...)
	if err != nil {
		return nil, fmt.Errorf("audit: resolve session: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("audit: resolve session: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func placeholders(n int) string {
	if n == 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// DecisionID derives the d- id of a session's decision
// (Docs/protocol/decision.md §Derivation), so the session view finds its
// decision rows without a decisions table.
func DecisionID(session string) string {
	h := sha256.Sum256([]byte("dorylinae-decision-id-v1\n" + session))
	return "d-" + hex.EncodeToString(h[:16])
}
