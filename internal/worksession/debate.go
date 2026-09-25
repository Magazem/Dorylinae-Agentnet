package worksession

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Debate sessions (Docs/protocol/debate.md §Model, ticket 3.1a). A debate is
// an ordinary request whose accept opens a work session of kind "debate". The
// session carries no result, grant or change request, is never quarantined,
// and sends no ws.state: internal/debate owns the debate's state and closes
// the session with CloseDebateTx. internal/debate depends on this package, so
// the reverse calls go through DebateHooks.

// DebateHooks is implemented by *debate.Store. Every method runs inside the
// caller's transaction and must touch only tx; a returned after func, if
// non-nil, is called once after that transaction commits.
type DebateHooks interface {
	// OpenedTx is called by OpenSession when a debate-kind session opens (or
	// is opened again: it must be idempotent), on either role.
	OpenedTx(ctx context.Context, tx *sql.Tx, role, peer, requestID, sid string, now time.Time) error
	// CancelTx is A's own ws_cancel on an open debate session: the debate
	// closes cancelled, no Decision.
	CancelTx(ctx context.Context, tx *sql.Tx, sid string, now time.Time) (after func(context.Context), err error)
	// PeerCancelTx applies a ws.cancel from B on A. applied is false when
	// the debate is no longer in positions, rounds or converge (refused).
	PeerCancelTx(ctx context.Context, tx *sql.Tx, sid string, now time.Time) (applied bool, after func(context.Context), err error)
	// EarlyCompleteTx applies a request.complete from B on A while A's
	// session is not closed (review 43 M4): an open debate closes cancelled.
	EarlyCompleteTx(ctx context.Context, tx *sql.Tx, sid string, now time.Time) (after func(context.Context), err error)
}

// debateBadState is the refusal of a work-session transition on a debate
// session (Docs/protocol/debate.md §What a debate session does not do).
func debateBadState(r storedRow, what string) error {
	return &BadStateError{State: r.state, Msg: fmt.Sprintf("%s is a debate session: %s (use agentnet debate)", r.id, what)}
}

// CloseDebateTx closes the debate-kind session sid inside tx with outcome
// (accepted when a Decision exists, cancelled otherwise), without sending a
// ws.state (Docs/protocol/debate.md §Kinds: debate.close closes B's mirror).
// Grants of the session end in the same transaction, as for every close
// (there are none: debates carry no grants). It does nothing if the session
// is already closed. The returned after func audits ws.close and runs
// OnClosed; call it once after tx commits.
func (s *Store) CloseDebateTx(ctx context.Context, tx *sql.Tx, sid, outcome string, now time.Time) (after func(context.Context), err error) {
	r, err := scanByID(ctx, tx, sid)
	if err != nil {
		return nil, err
	}
	if r.kind != SessionKindDebate {
		return nil, fmt.Errorf("worksession: %s is not a debate session", sid)
	}
	if r.state == StateClosed {
		return nil, nil
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE work_sessions SET state = ?, outcome = ?, state_at = ?, closed = ?, cancel = NULL, updated = ? WHERE id = ?`,
		StateClosed, outcome, wireTime(now), wireTime(now), storeTime(now), sid); err != nil {
		return nil, fmt.Errorf("worksession: close debate session: %w", err)
	}
	if s.RevokeGrants != nil {
		if err := s.RevokeGrants(ctx, tx, sid, now); err != nil {
			return nil, err
		}
	}
	return func(ctx context.Context) { s.auditClose(ctx, r, outcome, now) }, nil
}

// requestType reads the type of this daemon's request row for a session of
// role with peer: the "in" row on the worker, the "out" row on the requester.
// ok is false when no row exists.
func requestType(ctx context.Context, tx *sql.Tx, role, peer, requestID string) (typ string, ok bool, err error) {
	dir := "out"
	if role == RoleWorker {
		dir = "in"
	}
	err = tx.QueryRowContext(ctx, `SELECT type FROM requests WHERE direction = ? AND peer = ? AND id = ?`, dir, peer, requestID).Scan(&typ)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("worksession: read request type: %w", err)
	}
	return typ, true, nil
}
