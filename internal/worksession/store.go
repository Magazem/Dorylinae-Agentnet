package worksession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// SubmitTx is the outbox capability the store needs: sending ws.* mail
// inside the same transaction as the row it belongs to.
type SubmitTx interface {
	SubmitTx(ctx context.Context, tx *sql.Tx, to, kind string, body any) (mail.Submitted, error)
	Wake()
}

// AuditSink is the part of audit.Log the Store needs.
type AuditSink interface {
	Append(ctx context.Context, actor, action string, detail any) error
}

// Store owns the work_sessions table (migration 14,
// Docs/protocol/work-session.md §Persistence).
type Store struct {
	DB     *sql.DB
	Self   string
	Outbox SubmitTx
	Audit  AuditSink // may be nil

	// Requests lets a session close complete the worker's own request row in
	// the same transaction (Docs/protocol/work-session.md §Closing the
	// request). Required for CompleteShorthand and the mirror's close step.
	Requests *request.Store

	// Quarantine reports whether the quarantine rule
	// (Docs/protocol/work-session.md §Quarantine) holds for a result on
	// session sid from peer at round, evaluated in the same transaction as
	// the result. round 0 means no session row exists yet (an early
	// request.complete that overtook or skipped the accept): only the
	// peer-wide clause can hold. nil means never quarantine, correct before
	// grants exist (2.4 wires the real check).
	Quarantine func(ctx context.Context, tx *sql.Tx, sid, peer string, round int) (bool, error)

	// RevokeGrants, if set, revokes every grant of a session in the same
	// transaction as the session's close (Docs/protocol/grant.md §Session
	// end): "all its grants end in the same transaction (revoked, reason =
	// session_closed), including rows still pending_approval". Called from
	// closeSessionTx. Must touch only tx (Docs/review/27-2.1a-review.md C1).
	RevokeGrants func(ctx context.Context, tx *sql.Tx, sid string, now time.Time) error

	// OnClosed, if set, is called once per closed session after the closing
	// transaction committed, on both roles and on every close path (review 28
	// L8: the daemon rejects the session's pending approvals here, because the
	// approval Store must never be called under a tx, review 26 N4). It runs
	// outside any transaction.
	OnClosed func(ctx context.Context, sid string)

	// OnQuarantined, if set, is called after the commit of a ws.result that
	// entered quarantine (the daemon fires the content-free session.quarantined
	// notification here). It receives ids only, never any result content.
	OnQuarantined func(ctx context.Context, sid, peer, requestID string)

	// OnResult, if set, is called after the commit of a ws.result that was
	// applied without quarantine: a result for the current round now waits for
	// accept-result or request-changes (D25, session.result). Not called for a
	// quarantined result (OnQuarantined covers it) nor after a release (the
	// human who released it already knows). Ids only, never content.
	OnResult func(ctx context.Context, sid, peer, requestID string)

	// OnChanges, if set, is called on the worker after the commit of a ws.state
	// that started a new round with a changes text (D25, session.changes). Ids
	// only: the changes text is never passed.
	OnChanges func(ctx context.Context, sid, peer, requestID string)

	// Debate, when set, is told about the transitions of debate-kind
	// sessions (Docs/protocol/debate.md): opening one, and A closing one on
	// its own cancel, a ws.cancel from B or an early request.complete. nil
	// refuses those transitions on a debate session.
	Debate DebateHooks

	Now func() time.Time
}

// closedAfterCommit runs OnClosed. Call it after the commit of any
// transaction that closed session sid.
func (s *Store) closedAfterCommit(ctx context.Context, sid string) {
	if s.OnClosed != nil {
		s.OnClosed(ctx, sid)
	}
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// workSessionColumns is the column list shared by every SELECT against
// work_sessions, in scanRow's order.
const workSessionColumns = `id, role, peer, request_id, team_id, state, outcome, seq, round,
	result, result_round, verification, changes, cancel, released, last_state, last_state_sent,
	opened, state_at, closed, updated, kind`

// storedRow is one work_sessions row.
type storedRow struct {
	id, role, peer, requestID, teamID string
	state                             string
	outcome                           sql.NullString
	seq, round                        int
	result                            sql.NullString
	resultRound                       sql.NullInt64
	verification                      sql.NullString
	changes                           sql.NullString
	cancel                            sql.NullString
	released                          int
	lastState                         sql.NullString
	lastStateSent                     sql.NullString
	opened                            string
	stateAt                           string
	closed                            sql.NullString
	updated                           string
	kind                              string
}

type scanner interface{ Scan(dest ...any) error }

func scanRow(sc scanner) (storedRow, error) {
	var r storedRow
	err := sc.Scan(&r.id, &r.role, &r.peer, &r.requestID, &r.teamID, &r.state, &r.outcome, &r.seq, &r.round,
		&r.result, &r.resultRound, &r.verification, &r.changes, &r.cancel, &r.released, &r.lastState, &r.lastStateSent,
		&r.opened, &r.stateAt, &r.closed, &r.updated, &r.kind)
	return r, err
}

// findRowTx resolves a row by (role, peer, requestID), the unique index
// work_sessions_request, read through tx (the mail dedupe transaction already
// holds the daemon's one SQLite connection: see request.queryer).
func findRowTx(ctx context.Context, tx *sql.Tx, role, peer, requestID string) (storedRow, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+workSessionColumns+` FROM work_sessions WHERE role = ? AND peer = ? AND request_id = ?`, role, peer, requestID)
	r, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return storedRow{}, ErrUnknownSession
	}
	if err != nil {
		return storedRow{}, fmt.Errorf("worksession: read row: %w", err)
	}
	return r, nil
}

// findByID resolves a row by its session id, for A-side CLI-shaped methods
// (a session's id is unique regardless of role, but a given daemon only
// ever holds one role for it).
func findByID(ctx context.Context, q *sql.DB, id string) (storedRow, error) {
	row := q.QueryRowContext(ctx, `SELECT `+workSessionColumns+` FROM work_sessions WHERE id = ?`, id)
	r, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return storedRow{}, ErrUnknownSession
	}
	if err != nil {
		return storedRow{}, fmt.Errorf("worksession: read row: %w", err)
	}
	return r, nil
}

// View is a fully decoded work_sessions row, for future IPC (2.1b).
type View struct {
	ID           string
	Kind         string // SessionKindWork or SessionKindDebate
	Role         string
	Peer         string
	RequestID    string
	TeamID       string
	State        string
	Outcome      string
	Seq          int
	Round        int
	Result       *Result
	ResultBytes  int
	OutputBytes  int
	Artifacts    int    // number of result artifacts, kept while Result is withheld
	ResultStatus string // result status, kept while Result is withheld
	ResultRound  int
	Verification string
	Changes      string
	Cancel       string
	Released     bool
	Opened       time.Time
	StateAt      time.Time
	Closed       time.Time
	Updated      time.Time
}

func toView(r storedRow) (View, error) {
	v := View{
		ID: r.id, Kind: r.kind, Role: r.role, Peer: r.peer, RequestID: r.requestID, TeamID: r.teamID,
		State: r.state, Seq: r.seq, Round: r.round, Released: r.released != 0,
		Opened: parseWireTime(r.opened), StateAt: parseWireTime(r.stateAt), Updated: parseStoreTime(r.updated),
	}
	if r.outcome.Valid {
		v.Outcome = r.outcome.String
	}
	if r.verification.Valid {
		v.Verification = r.verification.String
	}
	if r.changes.Valid {
		v.Changes = r.changes.String
	}
	if r.cancel.Valid {
		v.Cancel = r.cancel.String
	}
	if r.closed.Valid {
		v.Closed = parseWireTime(r.closed.String)
	}
	if r.resultRound.Valid {
		v.ResultRound = int(r.resultRound.Int64)
	}
	if r.result.Valid && r.result.String != "" {
		res, err := decodeStoredResult(r.result.String)
		if err != nil {
			return View{}, err
		}
		v.ResultBytes = len(r.result.String)
		v.OutputBytes = OutputBytes(res.Output)
		v.Artifacts = len(res.Artifacts)
		v.ResultStatus = res.Status
		// A quarantined result is never handed out on the requester's side:
		// only its sizes (Docs/protocol/work-session.md §Quarantine (2.4)).
		// Withholding it here means no view built on View can leak it.
		if r.role != RoleRequester || r.state != StateQuarantined {
			v.Result = res
		}
	}
	return v, nil
}

func decodeStoredResult(body string) (*Result, error) {
	v, err := agentcard.ParseStrict([]byte(body))
	if err != nil {
		return nil, err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, badBody("stored result is not an object")
	}
	return DecodeResult(obj)
}

// Get resolves ws_show-style lookup by session id.
func (s *Store) Get(ctx context.Context, id string) (View, error) {
	r, err := findByID(ctx, s.DB, id)
	if err != nil {
		return View{}, err
	}
	return toView(r)
}

// GetByRequestID resolves ws_show's r-<id> shorthand
// (Docs/protocol/work-session.md §IPC, "ws_show ... an r- id resolved
// through its session"): the session belonging to a request id. A daemon
// only ever holds one role for a given session, so request_id alone (not
// narrowed by role) is enough in practice.
func (s *Store) GetByRequestID(ctx context.Context, requestID string) (View, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+workSessionColumns+` FROM work_sessions WHERE request_id = ? LIMIT 1`, requestID)
	r, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return View{}, ErrUnknownSession
	}
	if err != nil {
		return View{}, fmt.Errorf("worksession: read row: %w", err)
	}
	return toView(r)
}

// GetTx is Get read through tx, for callers that must check a session's
// state as part of another transaction (for example a grant issuance's
// approval Precondition, which must touch only tx,
// Docs/review/27-2.1a-review.md C1).
func (s *Store) GetTx(ctx context.Context, tx *sql.Tx, id string) (View, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+workSessionColumns+` FROM work_sessions WHERE id = ?`, id)
	r, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return View{}, ErrUnknownSession
	}
	if err != nil {
		return View{}, fmt.Errorf("worksession: read row: %w", err)
	}
	return toView(r)
}

// RefFor resolves the session id/state/round for a request, for the request
// view's "session" member (Docs/protocol/work-session.md §IPC). ok is false
// when no session exists yet.
func (s *Store) RefFor(ctx context.Context, role, peer, requestID string) (id, state string, round int, ok bool) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, state, round FROM work_sessions WHERE role = ? AND peer = ? AND request_id = ?`, role, peer, requestID)
	var i, st string
	var r int
	if err := row.Scan(&i, &st, &r); err != nil {
		return "", "", 0, false
	}
	return i, st, r, true
}

// ListFilter narrows ws_list (Docs/protocol/work-session.md §IPC).
type ListFilter struct {
	State, Role, Peer, TeamID string
}

// List runs ws_list: every session matching the filter, newest state_at
// first.
func (s *Store) List(ctx context.Context, f ListFilter) ([]View, error) {
	q := `SELECT ` + workSessionColumns + ` FROM work_sessions WHERE 1=1`
	var args []any
	if f.State != "" {
		q += ` AND state = ?`
		args = append(args, f.State)
	}
	if f.Role != "" {
		q += ` AND role = ?`
		args = append(args, f.Role)
	}
	if f.Peer != "" {
		q += ` AND peer = ?`
		args = append(args, f.Peer)
	}
	if f.TeamID != "" {
		q += ` AND team_id = ?`
		args = append(args, f.TeamID)
	}
	q += ` ORDER BY state_at DESC, id ASC`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("worksession: list: %w", err)
	}
	var scanned []storedRow
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("worksession: scan: %w", err)
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
	out := make([]View, 0, len(scanned))
	for _, r := range scanned {
		v, err := toView(r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// SelfOf returns the (requester, worker) identity keys of row's session in
// wire form, from this daemon's point of view: role tells which one is
// Store.Self. Used to build the capability.SessionOpen callback without
// exposing storedRow outside the package.
func (v View) SelfOf(self string) (requester, worker string) {
	if v.Role == RoleRequester {
		return self, v.Peer
	}
	return v.Peer, self
}
