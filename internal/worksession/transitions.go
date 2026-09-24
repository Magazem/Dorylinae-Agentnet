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
// If RevokeGrants is set, every grant of this session ends in the same
// transaction (Docs/protocol/grant.md §Session end).
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
	if s.RevokeGrants != nil {
		if err := s.RevokeGrants(ctx, tx, row.id, now); err != nil {
			return err
		}
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
	s.auditClose(ctx, r, OutcomeAccepted, now)
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
	// OD-P2-6 (c): the quarantined result is deleted unseen below. Its
	// mail_inbox copy needed no separate blanking (#inbox-copy-d18 (1)): a
	// result that entered quarantined was already stored blank at receipt.
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
	// OD-P2-6 (c): the quarantined result is deleted unseen below. Its
	// mail_inbox copy needed no separate blanking (#inbox-copy-d18 (1)): a
	// result that entered quarantined was already stored blank at receipt.
	if _, err := s.sendState(ctx, tx, r, seq, StateClosed, OutcomeCancelled, "", "", now); err != nil {
		return View{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE work_sessions SET state = ?, outcome = ?, seq = ?, result = NULL, result_round = NULL, verification = NULL, closed = ?, state_at = ?, updated = ?
WHERE id = ?`,
		StateClosed, OutcomeCancelled, seq, wireTime(now), wireTime(now), storeTime(now), id); err != nil {
		return View{}, fmt.Errorf("worksession: discard: %w", err)
	}
	if s.RevokeGrants != nil {
		if err := s.RevokeGrants(ctx, tx, id, now); err != nil {
			return View{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return View{}, fmt.Errorf("worksession: commit: %w", err)
	}
	s.Outbox.Wake()
	if s.Audit != nil {
		_ = s.Audit.Append(ctx, "cli", "ws.discard", map[string]any{"session": id, "peer": r.peer, "round": r.round})
	}
	s.auditClose(ctx, r, OutcomeCancelled, now)
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
	s.auditClose(ctx, r, OutcomeCancelled, now)
	newRow, err := findByID(ctx, s.DB, id)
	if err != nil {
		return View{}, err
	}
	return toView(newRow)
}

// ReleaseInTx performs the quarantined -> awaiting_result transition
// (Docs/protocol/work-session.md §Quarantine) inside tx, for use as an
// approval.Action's Perform (kind "release", 2.1b): Confirm runs Precondition
// and Perform in the same transaction as the approval's own state change. It
// writes no audit and does not call s.DB: the caller commits tx itself and
// must audit ws.release (and Outbox.Wake) after commit.
func (s *Store) ReleaseInTx(ctx context.Context, tx *sql.Tx, id string, now time.Time) (peer string, round int, err error) {
	r, err := scanByID(ctx, tx, id)
	if err != nil {
		return "", 0, err
	}
	if r.role != RoleRequester {
		return "", 0, ErrNotRequester
	}
	if r.state != StateQuarantined {
		return "", 0, &BadStateError{State: r.state, Msg: fmt.Sprintf("%s is %s", id, r.state)}
	}
	seq := r.seq + 1
	if _, err := s.sendState(ctx, tx, r, seq, StateAwaitingResult, "", "", "", now); err != nil {
		return "", 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE work_sessions SET state = ?, seq = ?, released = 1, state_at = ?, updated = ? WHERE id = ?`,
		StateAwaitingResult, seq, wireTime(now), storeTime(now), id); err != nil {
		return "", 0, fmt.Errorf("worksession: release: %w", err)
	}
	return r.peer, r.round, nil
}

// ReleaseApproved runs ReleaseInTx in its own transaction, for direct callers
// (tests, and any future non-approval-integrated path). An empty approvalID
// is refused: production callers only reach this once an approval.Action
// (2.2a) confirmed it.
func (s *Store) ReleaseApproved(ctx context.Context, id, approvalID string) (View, error) {
	if approvalID == "" {
		return View{}, errors.New("worksession: release needs a confirmed approval")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return View{}, fmt.Errorf("worksession: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.now()
	peer, round, err := s.ReleaseInTx(ctx, tx, id, now)
	if err != nil {
		return View{}, err
	}
	if err := tx.Commit(); err != nil {
		return View{}, fmt.Errorf("worksession: commit: %w", err)
	}
	s.Outbox.Wake()
	if s.Audit != nil {
		_ = s.Audit.Append(ctx, "cli", "ws.release", map[string]any{"session": id, "peer": peer, "round": round, "approval": approvalID})
	}
	newRow, err := findByID(ctx, s.DB, id)
	if err != nil {
		return View{}, err
	}
	return toView(newRow)
}

// AcceptResultInTx performs the awaiting_result -> closed transition inside
// tx with verification forced to human_accepted (Docs/protocol/work-session.md
// §Accept-result, "--human"), for use as an approval.Action's Perform (kind
// "accept_result", 2.1b). It writes no audit; call AuditAfterHumanAccept with
// the returned peer/round/opened after the caller's transaction commits.
func (s *Store) AcceptResultInTx(ctx context.Context, tx *sql.Tx, id string, now time.Time) (peer string, round int, opened time.Time, err error) {
	r, err := scanByID(ctx, tx, id)
	if err != nil {
		return "", 0, time.Time{}, err
	}
	if r.role != RoleRequester {
		return "", 0, time.Time{}, ErrNotRequester
	}
	if r.state != StateAwaitingResult {
		return "", 0, time.Time{}, &BadStateError{State: r.state, Msg: fmt.Sprintf("%s is %s", id, r.state)}
	}
	if err := s.closeSessionTx(ctx, tx, r, OutcomeAccepted, VerificationHumanAccepted, now); err != nil {
		return "", 0, time.Time{}, err
	}
	return r.peer, r.round, parseWireTime(r.opened), nil
}

// AuditAfterHumanAccept audits ws.accept_result and ws.close for the
// --human ws_accept_result path (2.1b), and wakes the outbox. Call it once,
// after the approval's transaction (which ran AcceptResultInTx) has
// committed: the audit log shares the daemon's single SQLite connection, so
// this must never run inside a transaction (review 27, C1).
func (s *Store) AuditAfterHumanAccept(ctx context.Context, id, peer string, round int, opened, now time.Time) {
	if s.Outbox != nil {
		s.Outbox.Wake()
	}
	s.closedAfterCommit(ctx, id)
	if s.Audit == nil {
		return
	}
	_ = s.Audit.Append(ctx, "cli", "ws.accept_result", map[string]any{
		"session": id, "peer": peer, "round": round, "verification": VerificationHumanAccepted,
	})
	age := 0
	if !opened.IsZero() {
		age = int(now.Sub(opened) / time.Second)
	}
	_ = s.Audit.Append(ctx, "daemon", "ws.close", map[string]any{
		"session": id, "peer": peer, "outcome": OutcomeAccepted, "rounds": round, "age_s": age,
	})
}

// PeekTx reads a session row's role, state and seq inside tx, for an
// approval.Action's Precondition (Docs/protocol/approval.md §Flow): the
// waiting action's preconditions are re-checked immediately before Perform,
// in the same transaction as the approval's own decision. seq moves on every
// transition, so a caller that recorded it when the approval was created can
// refuse an approval that has gone stale (review 35 H1: a release approval
// for round 1 must not release round 2's result).
func (s *Store) PeekTx(ctx context.Context, tx *sql.Tx, id string) (role, state string, seq int, err error) {
	r, err := scanByID(ctx, tx, id)
	if err != nil {
		return "", "", 0, err
	}
	return r.role, r.state, r.seq, nil
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

// auditClose appends ws.close {session, peer, outcome, rounds, age_s}
// (Docs/protocol/work-session.md §Audit; age_s since opened is the
// time-to-result metric). Called after commit.
func (s *Store) auditClose(ctx context.Context, row storedRow, outcome string, now time.Time) {
	s.closedAfterCommit(ctx, row.id)
	if s.Audit == nil {
		return
	}
	age := 0
	if opened := parseWireTime(row.opened); !opened.IsZero() {
		age = int(now.Sub(opened) / time.Second)
	}
	_ = s.Audit.Append(ctx, "daemon", "ws.close", map[string]any{
		"session": row.id, "peer": row.peer, "outcome": outcome, "rounds": row.round, "age_s": age,
	})
}
