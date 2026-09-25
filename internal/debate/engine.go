package debate

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// advanceTx recomputes the next turn from tr and stores the phase, next_slot
// and turn deadline (Docs/protocol/debate.md §State, §Timeouts: the deadline
// runs from the time the previous slot was applied). After the answer (Done)
// the respondent waits for the close: no deadline.
func (s *Store) advanceTx(ctx context.Context, tx *sql.Tx, r row, tr transcript, now time.Time) (Turn, error) {
	t := Next(tr.metas(turnStates(r.role)...), r.roundsMax)
	phase := t.Phase
	var deadline any
	if t.Done {
		phase = PhaseConverge
	} else {
		deadline = wireTime(now.Add(time.Duration(r.turnTimeoutS) * time.Second))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE debates SET phase = ?, next_slot = ?, turn_deadline = ?, updated = ? WHERE session = ?`,
		phase, t.Slot, deadline, storeTime(now), r.session); err != nil {
		return Turn{}, fmt.Errorf("debate: advance: %w", err)
	}
	return t, nil
}

// insertEntry stores one entry row.
func insertEntry(ctx context.Context, tx *sql.Tx, sid string, slot int, author, kind string, canon []byte, at, state string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO debate_entries (session, slot, author, kind, entry, at, state) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sid, slot, author, kind, string(canon), at, state); err != nil {
		return fmt.Errorf("debate: store entry %d: %w", slot, err)
	}
	return nil
}

// sendLastState submits a mail of kind with body to r's peer and, on the
// initiator, stores it as last_state for the echo (§Submitting entries, "The
// echo").
func (s *Store) sendLastState(ctx context.Context, tx *sql.Tx, r row, kind string, body map[string]any, now time.Time) (string, error) {
	sub, err := s.Outbox.SubmitTx(ctx, tx, r.peer, kind, body)
	if err != nil {
		return "", err
	}
	if r.role == RoleInitiator {
		ls, err := jsonObject(map[string]any{"kind": kind, "body": body})
		if err != nil {
			return "", fmt.Errorf("debate: encode last_state: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE debates SET last_state = ?, last_state_sent = ? WHERE session = ?`,
			ls, storeTime(now), r.session); err != nil {
			return "", fmt.Errorf("debate: store last_state: %w", err)
		}
	}
	return sub.ID, nil
}

// revealTx is A's automatic reveal (§Commit-reveal): in the transaction that
// applied B's slot-1 position, slot 0 becomes applied and debate.reveal
// carries the stored position and nonce, never anything from the agent.
func (s *Store) revealTx(ctx context.Context, tx *sql.Tx, r row, tr transcript, now time.Time) (func(context.Context), error) {
	e, ok := tr[0]
	if !ok || e.state != stateCommitted || !r.nonce.Valid {
		return nil, fmt.Errorf("debate: %s has no committed position to reveal", r.session)
	}
	at := wireTime(now)
	if _, err := tx.ExecContext(ctx, `UPDATE debate_entries SET state = ?, at = ? WHERE session = ? AND slot = 0`,
		stateApplied, at, r.session); err != nil {
		return nil, fmt.Errorf("debate: reveal slot 0: %w", err)
	}
	e.state, e.at = stateApplied, at
	tr[0] = e
	pos, err := entryValue(e.canon)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"at": at, "nonce": r.nonce.String, "position": pos, "request": r.requestID, "session": r.session}
	if _, err := s.sendLastState(ctx, tx, r, MailReveal, body, now); err != nil {
		return nil, err
	}
	return s.audit("daemon", "debate.reveal", map[string]any{"session": r.session, "peer": r.peer}), nil
}

// closeTx is A's close (§Outcome, decision.md §Signing step 1): cancelled
// closes at once (no Decision); agreed and escalated go to closing, where
// 3.3a derives, signs and stores the Decision and B's debate.sign moves the
// debate to closed. The work session closes in the same transaction
// (accepted when a Decision exists, cancelled otherwise), and debate.close
// goes to B. entries counts the slots A applied; constraints lists the ids A
// holds as active. The closing phase keeps outcome NULL (the table's CHECK):
// the decided outcome is in the stored close body (last_state) and reason.
func (s *Store) closeTx(ctx context.Context, tx *sql.Tx, r row, outcome, reason, actor string, now time.Time) (afters, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM debate_entries WHERE session = ? AND state = ?`, r.session, stateApplied).Scan(&n); err != nil {
		return nil, fmt.Errorf("debate: count entries: %w", err)
	}
	ids, err := constraintIDs(ctx, tx, r.session)
	if err != nil {
		return nil, err
	}
	idsAny := make([]any, len(ids))
	for i, id := range ids {
		idsAny[i] = id
	}
	body := map[string]any{
		"at": wireTime(now), "constraints": idsAny, "entries": n, "outcome": outcome, "reason": reason,
		"request": r.requestID, "session": r.session,
	}
	if outcome == OutcomeCancelled {
		if err := setClosed(ctx, tx, r.session, outcome, reason, now); err != nil {
			return nil, err
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE debates SET phase = ?, reason = ?, turn_deadline = NULL, updated = ? WHERE session = ?`,
		PhaseClosing, reason, storeTime(now), r.session); err != nil {
		return nil, fmt.Errorf("debate: closing: %w", err)
	}
	if _, err := s.sendLastState(ctx, tx, r, MailClose, body, now); err != nil {
		return nil, err
	}
	wsOutcome := worksession.OutcomeAccepted
	if outcome == OutcomeCancelled {
		wsOutcome = worksession.OutcomeCancelled
	}
	var out afters
	afterWS, err := s.Sessions.CloseDebateTx(ctx, tx, r.session, wsOutcome, now)
	if err != nil {
		return nil, err
	}
	age := int(now.Sub(parseStoreTime(r.created)) / time.Second)
	if age < 0 {
		age = 0
	}
	out.add(s.audit(actor, "debate.close", map[string]any{
		"session": r.session, "peer": r.peer, "outcome": outcome, "reason": reason,
		"entries": n, "constraints": len(ids), "age_s": age,
	}))
	out.add(afterWS)
	switch outcome {
	case OutcomeAgreed:
		out.add(s.event(EventAgreed, r.session, r.peer, r.requestID))
	case OutcomeEscalated:
		out.add(s.event(EventEscalated, r.session, r.peer, r.requestID))
	}
	return out, nil
}

// CancelTx implements worksession.DebateHooks: A's own cancel after accept
// (§Cancel and abandon) closes the debate cancelled, no Decision.
func (s *Store) CancelTx(ctx context.Context, tx *sql.Tx, sid string, now time.Time) (func(context.Context), error) {
	r, err := getRow(ctx, tx, sid)
	if err != nil {
		return nil, err
	}
	if r.role != RoleInitiator || !r.open() {
		return nil, &BadStateError{Phase: r.phase, Msg: fmt.Sprintf("%s is %s", sid, r.phase)}
	}
	out, err := s.closeTx(ctx, tx, r, OutcomeCancelled, ReasonCancelled, "cli", now)
	if err != nil {
		return nil, err
	}
	return out.run, nil
}

// PeerCancelTx implements worksession.DebateHooks: B's ws.cancel applied on
// A while the debate is open closes it cancelled; after A decided an outcome
// it is refused (applied false).
func (s *Store) PeerCancelTx(ctx context.Context, tx *sql.Tx, sid string, now time.Time) (bool, func(context.Context), error) {
	r, err := getRow(ctx, tx, sid)
	if err != nil {
		return false, nil, err
	}
	if r.role != RoleInitiator || !r.open() {
		return false, nil, nil
	}
	out, err := s.closeTx(ctx, tx, r, OutcomeCancelled, ReasonCancelled, "daemon", now)
	if err != nil {
		return false, nil, err
	}
	return true, out.run, nil
}

// EarlyCompleteTx implements worksession.DebateHooks: a request.complete from
// B while the debate is in positions, rounds or converge closes it cancelled,
// reason cancelled, no Decision (review 43 M4, how B's abandon reaches A). In
// closing it changes nothing.
func (s *Store) EarlyCompleteTx(ctx context.Context, tx *sql.Tx, sid string, now time.Time) (func(context.Context), error) {
	r, err := getRow(ctx, tx, sid)
	if err != nil {
		return nil, err
	}
	if r.role != RoleInitiator || !r.open() {
		return nil, nil
	}
	out, err := s.closeTx(ctx, tx, r, OutcomeCancelled, ReasonCancelled, "daemon", now)
	if err != nil {
		return nil, err
	}
	return out.run, nil
}
