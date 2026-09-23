package worksession

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// A-side transitions (Docs/protocol/work-session.md §Transitions
// (authoritative, on A)): accept-result, request-changes, discard, release
// and A's own cancel. Each runs in one transaction: update the row, store
// the ws.state mail as last_state and submit it, matching the pattern of
// internal/request's lifecycle transitions.

// sendState builds and sends one ws.state mail inside tx, and stores it as
// last_state (Docs/protocol/work-session.md, "For each transition, in one
// transaction on A: update the row ..., store the ws.state mail as
// last_state, and Outbox.SubmitTx it to B").
func (s *Store) sendState(ctx context.Context, tx *sql.Tx, row storedRow, seq int, state, outcome, verification, changes string, now time.Time) (map[string]any, error) {
	body := map[string]any{
		"at": wireTime(now), "request": row.requestID, "round": row.round, "seq": seq, "session": row.id, "state": state,
	}
	if outcome != "" {
		body["outcome"] = outcome
	}
	if verification != "" {
		body["verification"] = verification
	}
	if changes != "" {
		body["changes"] = changes
	}
	lastState, err := jsonObject(map[string]any{"kind": KindState, "body": body})
	if err != nil {
		return nil, fmt.Errorf("worksession: encode last_state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE work_sessions SET last_state = ?, last_state_sent = ? WHERE id = ?`,
		lastState, storeTime(now), row.id); err != nil {
		return nil, fmt.Errorf("worksession: store last_state: %w", err)
	}
	if _, err := s.Outbox.SubmitTx(ctx, tx, row.peer, KindState, body); err != nil {
		return nil, err
	}
	return body, nil
}

// closeSessionTx closes row (state=closed) inside tx and sends the closing
// ws.state. It does not touch the result column: callers that must delete a
// stored result (Discard, RequestChanges from quarantined) do so themselves.
// Grant revocation on close (Docs/protocol/work-session.md §Persistence) is
// added by 2.2c; there is nothing to revoke before grants exist.
func (s *Store) closeSessionTx(ctx context.Context, tx *sql.Tx, row storedRow, outcome, verification string, now time.Time) error {
	seq := row.seq + 1
	if _, err := s.sendState(ctx, tx, row, seq, StateClosed, outcome, verification, "", now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE work_sessions SET state = ?, outcome = ?, seq = ?, verification = ?, state_at = ?, closed = ?, updated = ?
WHERE id = ?`,
		StateClosed, outcome, seq, nullIfEmpty(verification), wireTime(now), wireTime(now), storeTime(now), row.id); err != nil {
		return fmt.Errorf("worksession: close row: %w", err)
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// AcceptResult runs ws_accept_result without --human (A only,
// Docs/protocol/work-session.md §Accept-result): awaiting_result -> closed,
// outcome accepted, the claimed verification unchanged. The --human path
// needs a human approval (2.2a) and is added by that ticket.
func (s *Store) AcceptResult(ctx context.Context, id string) (View, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return View{}, fmt.Errorf("worksession: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	r, err := scanByID(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	if r.role != RoleRequester {
		return View{}, ErrNotRequester
	}
	if r.state != StateAwaitingResult {
		return View{}, &BadStateError{State: r.state, Msg: fmt.Sprintf("%s is %s", id, r.state)}
	}
	now := s.now()
	verification := ""
	if r.verification.Valid {
		verification = r.verification.String
	}
	if err := s.closeSessionTx(ctx, tx, r, OutcomeAccepted, verification, now); err != nil {
		return View{}, err
	}
	if err := tx.Commit(); err != nil {
		return View{}, fmt.Errorf("worksession: commit: %w", err)
	}
	s.Outbox.Wake()
	if s.Audit != nil {
		_ = s.Audit.Append(ctx, "cli", "ws.accept_result", map[string]any{"session": id, "peer": r.peer, "round": r.round, "verification": verification})
	}
	newRow, err := findByID(ctx, s.DB, id)
	if err != nil {
		return View{}, err
	}
	return toView(newRow)
}

// RequestChanges runs ws_request_changes (A only,
// Docs/protocol/work-session.md §Request changes): awaiting_result -> open,
// or quarantined -> open without a release (OD-P2-6 (c)), round += 1. The
// stored result (and result_round) are always cleared: it belonged to the
// round that just ended.
func (s *Store) RequestChanges(ctx context.Context, id, changes string) (View, error) {
	if err := checkCodePoints("changes", changes, 1, 4000, "\n\t"); err != nil {
		return View{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return View{}, fmt.Errorf("worksession: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	r, err := scanByID(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	if r.role != RoleRequester {
		return View{}, ErrNotRequester
	}
	fromQuarantine := r.state == StateQuarantined
	if r.state != StateAwaitingResult && !fromQuarantine {
		return View{}, &BadStateError{State: r.state, Msg: fmt.Sprintf("%s is %s", id, r.state)}
	}
	now := s.now()
	newRound := r.round + 1
	seq := r.seq + 1
	sendRow := r
	sendRow.round = newRound
	if _, err := s.sendState(ctx, tx, sendRow, seq, StateOpen, "", "", changes, now); err != nil {
		return View{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE work_sessions SET state = ?, outcome = NULL, seq = ?, round = ?, result = NULL, result_round = NULL,
	verification = NULL, changes = ?, released = 0, state_at = ?, updated = ?
WHERE id = ?`,
		StateOpen, seq, newRound, changes, wireTime(now), storeTime(now), id); err != nil {
		return View{}, fmt.Errorf("worksession: request changes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return View{}, fmt.Errorf("worksession: commit: %w", err)
	}
	s.Outbox.Wake()
	if s.Audit != nil {
		detail := map[string]any{"session": id, "peer": r.peer, "round": newRound}
		if fromQuarantine {
			detail["from"] = "quarantined"
		}
		_ = s.Audit.Append(ctx, "cli", "ws.request_changes", detail)
	}
	newRow, err := findByID(ctx, s.DB, id)
	if err != nil {
		return View{}, err
	}
	return toView(newRow)
}

// Discard runs ws_discard (A only, Docs/protocol/work-session.md §Discard,
// OD-P2-6 (c)): quarantined -> closed, outcome cancelled, no approval. The
// stored result is deleted unseen, never shown to A's IPC or agent, and B is
// told only closed/cancelled.
func (s *Store) Discard(ctx context.Context, id string) (View, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return View{}, fmt.Errorf("worksession: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	r, err := scanByID(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	if r.role != RoleRequester {
		return View{}, ErrNotRequester
	}
	if r.state != StateQuarantined {
		return View{}, &BadStateError{State: r.state, Msg: fmt.Sprintf("%s is %s", id, r.state)}
	}
	now := s.now()
	seq := r.seq + 1
	if _, err := s.sendState(ctx, tx, r, seq, StateClosed, OutcomeCancelled, "", "", now); err != nil {
		return View{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE work_sessions SET state = ?, outcome = ?, seq = ?, result = NULL, result_round = NULL, closed = ?, state_at = ?, updated = ?
WHERE id = ?`,
		StateClosed, OutcomeCancelled, seq, wireTime(now), wireTime(now), storeTime(now), id); err != nil {
		return View{}, fmt.Errorf("worksession: discard: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return View{}, fmt.Errorf("worksession: commit: %w", err)
	}
	s.Outbox.Wake()
	if s.Audit != nil {
		_ = s.Audit.Append(ctx, "cli", "ws.discard", map[string]any{"session": id, "peer": r.peer, "round": r.round})
	}
	newRow, err := findByID(ctx, s.DB, id)
	if err != nil {
		return View{}, err
	}
	return toView(newRow)
}

// Cancel runs ws_cancel for A (Docs/protocol/work-session.md §Cancel): open
// -> closed, outcome cancelled. reason is validated (1-500 code points) but
// never stored or transmitted: the ws.state kind has no place for it, and it
// is content like every other free-text field in this protocol.
func (s *Store) Cancel(ctx context.Context, id, reason string) (View, error) {
	if reason != "" {
		if err := checkCodePoints("reason", reason, 1, 500, ""); err != nil {
			return View{}, err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return View{}, fmt.Errorf("worksession: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	r, err := scanByID(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	if r.role != RoleRequester {
		return View{}, ErrNotRequester
	}
	if r.state != StateOpen {
		return View{}, &BadStateError{State: r.state, Msg: fmt.Sprintf("%s is %s", id, r.state)}
	}
	now := s.now()
	if err := s.closeSessionTx(ctx, tx, r, OutcomeCancelled, "", now); err != nil {
		return View{}, err
	}
	if err := tx.Commit(); err != nil {
		return View{}, fmt.Errorf("worksession: commit: %w", err)
	}
	s.Outbox.Wake()
	if s.Audit != nil {
		_ = s.Audit.Append(ctx, "cli", "ws.cancel", map[string]any{"session": id, "peer": r.peer, "role": RoleRequester})
	}
	newRow, err := findByID(ctx, s.DB, id)
	if err != nil {
		return View{}, err
	}
	return toView(newRow)
}

// Release runs the DB transition of ws_release once its approval is
// confirmed (Docs/protocol/work-session.md §Quarantine): quarantined ->
// awaiting_result, released = 1. The quarantine rule is not re-evaluated
// after a release for this round. The approval gate itself (kind "release")
// is added by 2.2a/2.4; this method performs only the state change.
func (s *Store) Release(ctx context.Context, id string) (View, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return View{}, fmt.Errorf("worksession: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	r, err := scanByID(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	if r.role != RoleRequester {
		return View{}, ErrNotRequester
	}
	if r.state != StateQuarantined {
		return View{}, &BadStateError{State: r.state, Msg: fmt.Sprintf("%s is %s", id, r.state)}
	}
	now := s.now()
	seq := r.seq + 1
	if _, err := s.sendState(ctx, tx, r, seq, StateAwaitingResult, "", "", "", now); err != nil {
		return View{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE work_sessions SET state = ?, seq = ?, released = 1, state_at = ?, updated = ? WHERE id = ?`,
		StateAwaitingResult, seq, wireTime(now), storeTime(now), id); err != nil {
		return View{}, fmt.Errorf("worksession: release: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return View{}, fmt.Errorf("worksession: commit: %w", err)
	}
	s.Outbox.Wake()
	if s.Audit != nil {
		_ = s.Audit.Append(ctx, "cli", "ws.release", map[string]any{"session": id, "peer": r.peer, "round": r.round})
	}
	newRow, err := findByID(ctx, s.DB, id)
	if err != nil {
		return View{}, err
	}
	return toView(newRow)
}

func scanByID(ctx context.Context, tx *sql.Tx, id string) (storedRow, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+workSessionColumns+` FROM work_sessions WHERE id = ?`, id)
	r, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return storedRow{}, ErrUnknownSession
	}
	if err != nil {
		return storedRow{}, fmt.Errorf("worksession: read row: %w", err)
	}
	return r, nil
}

func jsonObject(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
