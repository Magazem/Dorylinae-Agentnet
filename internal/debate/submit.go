package debate

import (
	"context"
	"errors"
	"fmt"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// SubmitResult is the outcome of Submit: the debate's session and the id of
// the debate.entry mail.
type SubmitResult struct {
	Session string
	MailID  string
}

// Submit runs debate_submit (Docs/protocol/debate.md §Submitting entries):
// id is the debate's session (s-) or request (r-) id, kind the entry kind and
// entry its generic JSON value. from narrows the request lookup of a one-step
// accept (as request_accept's from). In one transaction it stores the entry
// at its slot (applied on A, sent on B), advances the turn and queues the
// debate.entry mail.
//
// On B, a position for a debate request that is pending or deferred accepts
// it first, in the same transaction (the one-step accept + position): the
// accept opens the session and is refused with ErrQuarantineActive while the
// quarantine clause holds from B's side.
//
// Errors: ErrUnknownDebate; *BadStateError (bad_state); *NotYourTurnError
// (not_your_turn); *FieldError (bad_request) and *TooLargeError
// (entry_too_large) from validation; the request errors of the accept.
func (s *Store) Submit(ctx context.Context, id, from, kind string, entry any) (SubmitResult, error) {
	if !ValidKind(kind) {
		return SubmitResult{}, fieldErr("kind", "must be position, move, proposal or answer")
	}
	if entry == nil {
		return SubmitResult{}, fieldErr("entry", "is required")
	}
	// A enforces its deadlines on every debate IPC call (§Timeouts).
	if _, err := s.SweepOne(ctx, id); err != nil && !errors.Is(err, ErrUnknownDebate) {
		return SubmitResult{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("debate: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	r, err := resolve(ctx, tx, id)
	if err != nil {
		return SubmitResult{}, err
	}
	now := s.now()
	var done afters
	if r.phase == PhaseInvited {
		if r.role != RoleRespondent || kind != KindPosition {
			return SubmitResult{}, &BadStateError{Phase: r.phase, Msg: fmt.Sprintf("%s is invited: the respondent accepts it with its position first", r.session)}
		}
		if from == "" {
			from = r.peer
		}
		_, afterAccept, err := s.Requests.AcceptInTx(ctx, tx, r.requestID, from, request.TypeDebate)
		if err != nil {
			return SubmitResult{}, err
		}
		done.add(afterAccept)
		if r, err = getRow(ctx, tx, r.session); err != nil {
			return SubmitResult{}, err
		}
	}
	if !r.open() {
		return SubmitResult{}, &BadStateError{Phase: r.phase, Msg: fmt.Sprintf("%s is %s", r.session, r.phase)}
	}
	tr, err := loadTranscript(ctx, tx, r.session)
	if err != nil {
		return SubmitResult{}, err
	}
	states := turnStates(r.role)
	t := Next(tr.metas(states...), r.roundsMax)
	if t.Done {
		return SubmitResult{}, &BadStateError{Phase: r.phase, Msg: fmt.Sprintf("%s has its answer: waiting for the close", r.session)}
	}
	if t.Author != r.role || t.Slot == 0 {
		return SubmitResult{}, &NotYourTurnError{Slot: t.Slot, Expect: t.Kind, Author: t.Author}
	}
	if kind != t.Kind {
		if kind == KindMove && t.Phase == PhaseConverge {
			// §Turns: a move once rule 1 or 2 fired is bad_state.
			return SubmitResult{}, &BadStateError{Phase: r.phase, Msg: fmt.Sprintf("%s is converging: slot %d expects a %s", r.session, t.Slot, t.Kind)}
		}
		return SubmitResult{}, &NotYourTurnError{Slot: t.Slot, Expect: t.Kind, Author: t.Author}
	}
	e, canon, err := DecodeEntry(kind, entry)
	if err != nil {
		return SubmitResult{}, fieldPrefix(err, "entry")
	}
	if err := checkEntryTargets(tr, e, r.role, t.Slot, states...); err != nil {
		return SubmitResult{}, fieldPrefix(err, "entry")
	}
	if held, ok := tr[t.Slot]; ok && held.state == stateEarly {
		// A held entry from the peer that claims this caller's slot: drop it.
		if _, err := tx.ExecContext(ctx, `DELETE FROM debate_entries WHERE session = ? AND slot = ? AND state = ?`, r.session, t.Slot, stateEarly); err != nil {
			return SubmitResult{}, fmt.Errorf("debate: drop held entry: %w", err)
		}
		done.add(s.audit("daemon", "debate.ignored", map[string]any{"session": r.session, "peer": r.peer, "kind": MailEntry, "reason": "turn"}))
	}
	at := wireTime(now)
	state := stateApplied
	if r.role == RoleRespondent {
		state = stateSent
	}
	if err := insertEntry(ctx, tx, r.session, t.Slot, r.role, kind, canon, at, state); err != nil {
		return SubmitResult{}, err
	}
	tr[t.Slot] = stored{slot: t.Slot, author: r.role, kind: kind, at: at, state: state, canon: canon, entry: e}
	ev, err := entryValue(canon)
	if err != nil {
		return SubmitResult{}, err
	}
	body := map[string]any{"at": at, "entry": ev, "kind": kind, "request": r.requestID, "session": r.session, "slot": t.Slot}
	mailID, err := s.sendLastState(ctx, tx, r, MailEntry, body, now)
	if err != nil {
		return SubmitResult{}, err
	}
	if _, err := s.advanceTx(ctx, tx, r, tr, now); err != nil {
		return SubmitResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubmitResult{}, fmt.Errorf("debate: commit: %w", err)
	}
	s.Outbox.Wake()
	done.add(s.audit("cli", "debate.entry", entryAudit(r.session, r.peer, t.Slot, kind, canon, e)))
	done.run(ctx)
	return SubmitResult{Session: r.session, MailID: mailID}, nil
}
