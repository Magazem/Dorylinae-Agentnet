package worksession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// ws.state: A -> B (Docs/protocol/work-session.md §Mirror (on B)). B does
// not check the transition; A is authoritative and the higher seq wins, except
// that closed is final.

type stateOutcome struct {
	orphan      bool
	duplicate   bool
	ignoredKind bool
	closed      bool // the mirror is already closed: nothing leaves closed
	applied     bool
	sessionID   string
	peer        string
	requestID   string
	state       string
	seq, round  int
	newRound    bool // state open with a changes text (a change request)
	autoCancel  bool // a run session's new round: ws.cancel sent in this transaction
	// auditComplete appends the request.complete audit row of a close,
	// after commit (request.Store.CompleteInTx).
	auditComplete func(context.Context)
	expBytes      int
	expTruncated  bool
}

// StateKind is the receiver Kind for "ws.state", applied on B.
func (s *Store) StateKind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.applyState, After: s.afterState}
}

func (s *Store) applyState(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
	body := op.Msg.Body
	if err := strictMembers(body, "at", "changes", "outcome", "request", "round", "seq", "session", "state", "verification"); err != nil {
		return err
	}
	at, err := decodeTime("at", body["at"])
	if err != nil {
		return err
	}
	reqID, err := decodeString(body, "request", false)
	if err != nil {
		return err
	}
	round, err := decodeInt(body, "round")
	if err != nil {
		return err
	}
	if round < 1 {
		return badBody("round must be >= 1")
	}
	seq, err := decodeInt(body, "seq")
	if err != nil {
		return err
	}
	if seq < 1 {
		return badBody("seq must be >= 1")
	}
	sid, err := decodeString(body, "session", false)
	if err != nil {
		return err
	}
	if sid != DeriveID(op.Msg.From, op.Msg.To, reqID) {
		return badBody("session is not the derived id for (from, to, request)")
	}
	state, err := decodeString(body, "state", false)
	if err != nil {
		return err
	}
	switch state {
	case StateOpen, StateAwaitingResult, StateQuarantined, StateClosed:
	default:
		return badBody("state must be open, awaiting_result, quarantined or closed")
	}
	outcome, err := decodeOptionalNonEmpty(body, "outcome")
	if err != nil {
		return err
	}
	if (outcome != "") != (state == StateClosed) {
		return badBody("outcome is present iff state is closed")
	}
	if outcome != "" && outcome != OutcomeAccepted && outcome != OutcomeCancelled {
		return badBody("outcome must be accepted or cancelled")
	}
	changes, err := decodeOptionalNonEmpty(body, "changes")
	if err != nil {
		return err
	}
	if changes != "" {
		if state != StateOpen || round < 2 {
			return badBody("changes is only present with state open and round >= 2")
		}
		if err := checkCodePoints("changes", changes, 1, 4000, "\n\t"); err != nil {
			return badBody("%s", err.Error())
		}
	}
	verification, err := decodeOptionalNonEmpty(body, "verification")
	if err != nil {
		return err
	}
	if verification != "" {
		if outcome != OutcomeAccepted {
			return badBody("verification is only present with outcome accepted")
		}
		switch verification {
		case VerificationNone, VerificationTestsPassed, VerificationHumanAccepted:
		default:
			return badBody("verification must be none, tests_passed or human_accepted")
		}
	}

	row, err := findRowTx(ctx, tx, RoleWorker, op.Msg.From, reqID)
	if errors.Is(err, ErrUnknownSession) {
		op.Outcome = &stateOutcome{orphan: true, sessionID: sid, requestID: reqID, peer: op.Msg.From}
		return nil
	}
	if err != nil {
		return err
	}
	if row.kind == SessionKindDebate {
		// ws.state is never sent for a debate (Docs/protocol/debate.md
		// §Kinds): debate.close closes B's mirror. One from a misbehaving A
		// changes nothing.
		op.Outcome = &stateOutcome{ignoredKind: true, sessionID: row.id, requestID: reqID, peer: op.Msg.From}
		return nil
	}
	if seq <= row.seq {
		// A's echo of the state B already has (same seq) in awaiting_result
		// or quarantined answers B's pending cancel: A refused it (R55-114).
		// An older seq is a reordered mail and marks nothing (review 69b F4).
		if seq == row.seq && row.cancel.Valid && row.cancel.String == "requested" &&
			(state == StateAwaitingResult || state == StateQuarantined) {
			if _, err := tx.ExecContext(ctx, `UPDATE work_sessions SET cancel = 'refused', updated = ? WHERE id = ?`,
				storeTime(s.now()), row.id); err != nil {
				return fmt.Errorf("worksession: mark cancel refused: %w", err)
			}
		}
		op.Outcome = &stateOutcome{duplicate: true, sessionID: row.id, requestID: reqID, peer: op.Msg.From}
		return nil
	}
	if row.state == StateClosed {
		// Closed is final on B too (review 55 R55-067, O-131): a later
		// ws.state from a misbehaving A cannot reopen the mirror, and a
		// second step into closed would re-insert the experience record and
		// fail the mail transaction on every resend.
		op.Outcome = &stateOutcome{closed: true, sessionID: row.id, requestID: reqID, peer: op.Msg.From}
		return nil
	}

	now := s.now()
	// B's cancel mark (R55-114, Docs/protocol/work-session.md §Mirror step 4):
	// closed or open answers the wish (applied, or a new round started), so
	// it is cleared; awaiting_result or quarantined while requested means A
	// refused it, and B may send again.
	cancelSet := `cancel = NULL`
	if state == StateAwaitingResult || state == StateQuarantined {
		cancelSet = `cancel = CASE WHEN cancel = 'requested' THEN 'refused' ELSE cancel END`
	}
	set := `state = ?, seq = ?, round = ?, state_at = ?, updated = ?, ` + cancelSet
	args := []any{state, seq, round, wireTime(at), storeTime(now)}
	set += `, outcome = ?`
	args = append(args, nullIfEmpty(outcome))
	set += `, changes = ?`
	args = append(args, nullIfEmpty(changes))
	if verification != "" {
		set += `, verification = ?`
		args = append(args, verification)
	}
	if state == StateClosed {
		set += `, closed = ?`
		args = append(args, wireTime(at))
	}
	if state == StateOpen || (state == StateClosed && outcome == OutcomeCancelled) {
		// The round that produced this result is over without being accepted
		// (a new round started, or A discarded/cancelled): B's own bookkeeping
		// copy is stale, matching the "current round" invariant of the result
		// column (Docs/protocol/work-session.md §Persistence).
		set += `, result = NULL, result_round = NULL, result_mail = NULL`
	}
	args = append(args, row.id)
	if _, err := tx.ExecContext(ctx, `UPDATE work_sessions SET `+set+` WHERE id = ?`, args...); err != nil {
		return fmt.Errorf("worksession: apply mirror state: %w", err)
	}

	// A new round on a run session (Docs/protocol/work-session.md §Run
	// sessions, OD-F18-6): the runner acts only on arrival and no agent may
	// answer, so the helper cancels at once, in this transaction, so a crash
	// cannot lose it (review 69b F5).
	autoCancel := false
	if state == StateOpen && round >= 2 && row.runner != 0 {
		if _, err := s.submitCancelTx(ctx, tx, row, "", now); err != nil {
			return err
		}
		autoCancel = true
	}

	// The holder learns of a close from this ws.state and ends its own
	// (held) grants of this session in the same transaction; no separate
	// grant.revoke is sent for this (Docs/protocol/grant.md §Session end).
	if state == StateClosed && row.state != StateClosed && s.RevokeGrants != nil {
		if err := s.RevokeGrants(ctx, tx, row.id, now); err != nil {
			return err
		}
	}

	// The experience record joins the same transaction that closes B's
	// mirror (Docs/protocol/experience.md §When and where).
	var expBytes int
	var expTruncated bool
	if state == StateClosed && row.state != StateClosed {
		verificationBy := verificationActor(verification)
		cancelledBy := ""
		if outcome == OutcomeCancelled {
			cancelledBy = RoleRequester
			if row.cancel.Valid && row.cancel.String == "requested" {
				cancelledBy = RoleWorker
			}
		}
		finalRow := row
		finalRow.round = round
		var werr error
		if expBytes, expTruncated, werr = s.writeExperienceTx(ctx, tx, finalRow, outcome, verification, verificationBy, cancelledBy, now); werr != nil {
			return werr
		}
	}

	// Complete B's request only on the step into closed. A later ws.state
	// (a higher seq from a misbehaving A, "closed" again or after a reopen)
	// must not fail the mail transaction: a non-bad-body error is never
	// acked, so the sender would resend it for 14 days.
	var auditComplete func(context.Context)
	if state == StateClosed && row.state != StateClosed && s.Requests != nil {
		note := ""
		var res *request.Result
		if outcome == OutcomeAccepted {
			if row.result.Valid && row.result.String != "" && row.resultRound.Valid && int(row.resultRound.Int64) == round {
				stored, derr := decodeStoredResult(row.result.String)
				if derr != nil {
					return fmt.Errorf("worksession: decode stored result: %w", derr)
				}
				res = &request.Result{Status: stored.Status, Summary: stored.Summary, ExitCode: stored.ExitCode, Output: stored.Output, Artifacts: stored.Artifacts}
			}
		} else {
			note = "session cancelled"
		}
		fn, err := s.Requests.CompleteInTx(ctx, tx, op.Msg.From, reqID, note, res)
		var bse *request.BadStateError
		switch {
		case errors.As(err, &bse):
			// The request is no longer accepted (already completed, for
			// example by the Phase 1 fallback): nothing left to complete.
		case err != nil:
			return fmt.Errorf("worksession: complete request on close: %w", err)
		default:
			auditComplete = fn
		}
	}

	op.Outcome = &stateOutcome{applied: true, sessionID: row.id, requestID: reqID, peer: op.Msg.From, state: state, seq: seq, round: round, newRound: state == StateOpen && changes != "", autoCancel: autoCancel, auditComplete: auditComplete, expBytes: expBytes, expTruncated: expTruncated}
	return nil
}

// afterState audits the outcome, once, after a successful commit.
func (s *Store) afterState(ctx context.Context, op *mail.Opened) {
	out, ok := op.Outcome.(*stateOutcome)
	if !ok {
		return
	}
	if s.Outbox != nil {
		s.Outbox.Wake()
	}
	if out.auditComplete != nil {
		out.auditComplete(ctx)
	}
	if out.applied && out.state == StateClosed {
		s.closedAfterCommit(ctx, out.sessionID)
		s.auditExperience(ctx, out.sessionID, RoleWorker, out.expBytes, out.expTruncated)
	}
	if out.applied && out.newRound && s.OnChanges != nil {
		s.OnChanges(ctx, out.sessionID, out.peer, out.requestID)
	}
	if s.Audit == nil {
		return
	}
	if out.orphan {
		_ = s.Audit.Append(ctx, "daemon", "ws.orphan", map[string]any{"session": out.sessionID, "peer": out.peer, "kind": KindState})
		return
	}
	if out.duplicate {
		return
	}
	if out.ignoredKind || out.closed {
		reason := "kind"
		if out.closed {
			reason = "closed"
		}
		_ = s.Audit.Append(ctx, "daemon", "ws.ignored", map[string]any{
			"session": out.sessionID, "peer": out.peer, "kind": KindState, "reason": reason,
		})
		return
	}
	_ = s.Audit.Append(ctx, "daemon", "ws.state", map[string]any{
		"session": out.sessionID, "peer": out.peer, "state": out.state, "seq": out.seq, "round": out.round,
	})
	if out.autoCancel {
		_ = s.Audit.Append(ctx, "daemon", "ws.cancel", map[string]any{"session": out.sessionID, "peer": out.peer, "role": RoleWorker})
	}
}
