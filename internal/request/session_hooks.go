package request

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Work sessions (2.1a, Docs/protocol/work-session.md) extend the request
// lifecycle from a package this one cannot depend on (internal/worksession
// depends on this package, to reuse ValidateComplete and call CompleteInTx).
// The three hooks below are the reverse direction, satisfied by
// *worksession.Store at daemon wiring time.

// SessionOpener opens a work session inside the same transaction as a
// request.accept, on either side of it (Docs/protocol/work-session.md
// §Session id): role is "worker" when this Store's own accept just ran, or
// "requester" when the mirror just applied a peer's request.accept. It must
// be idempotent: a session may already exist (a ws.result can overtake the
// accept).
type SessionOpener interface {
	OpenSession(ctx context.Context, tx *sql.Tx, role, peer, requestID, teamID string, now time.Time) error
}

// SessionEarlyComplete is called by applyComplete inside the mail apply
// transaction, for a request.complete mail applied while a session might
// exist for it (Docs/protocol/work-session.md §Early complete and Phase 1
// workers). keepContent is false only when the quarantine rule holds for the
// session: then the caller must store no result and no note. If the session
// is (still) open, the hook closes it (outcome cancelled).
type SessionEarlyComplete interface {
	EarlyComplete(ctx context.Context, tx *sql.Tx, peer, requestID string, hadResult bool) (keepContent bool, err error)
}

// SessionCompleter is request_complete's redirect when a session exists for
// the request (Docs/protocol/work-session.md, "request_complete while a
// session exists"): it submits note/result as a ws_result instead of
// completing the request directly. ok is false when no session exists at
// all, in which case the caller proceeds with its normal request.complete
// path; ok is true and err is a *worksession.BadStateError when a session
// exists but is not open.
type SessionCompleter interface {
	CompleteShorthand(ctx context.Context, peer, requestID, note string, result *Result) (ok bool, err error)
}

// SessionHooks is the combination *worksession.Store implements.
type SessionHooks interface {
	SessionOpener
	SessionEarlyComplete
	SessionCompleter
}

// CompleteInTx is the tx-scoped counterpart to Complete: it applies
// request.complete's `in`-row update and sends the request.complete mail
// inside tx, without opening or committing a transaction itself. It is used
// only when a work session closes (Docs/protocol/work-session.md §Closing
// the request): the caller commits after this and its own row update both
// succeed, so a crash cannot leave the session closed with the request still
// accepted, or vice versa.
func (s *Store) CompleteInTx(ctx context.Context, tx *sql.Tx, peer, id, note string, result *Result) error {
	if err := ValidateComplete(note, result); err != nil {
		return err
	}
	row, err := getRow(ctx, tx, "in", peer, id)
	if err != nil {
		return err
	}
	if row.state != StateAccepted {
		return &BadStateError{State: row.state, Msg: fmt.Sprintf("%s is %s", id, row.state)}
	}
	now := s.now()
	seq := row.stateSeq + 1
	canon, err := completeBodyCanonical(id, seq, now, note, result)
	if err != nil {
		return err
	}
	if err := CheckCompleteSize(canon); err != nil {
		return err
	}
	body := map[string]any{"at": wireTime(now), "request": id, "seq": seq}
	var resultCanon []byte
	if note != "" {
		body["note"] = note
	}
	if result != nil {
		body["result"] = resultWire(result)
		resultCanon, err = CanonicalResult(result)
		if err != nil {
			return err
		}
	}
	var noteArg, resultArg any
	if note != "" {
		noteArg = note
	}
	if result != nil {
		resultArg = string(resultCanon)
	}
	lastReply, err := jsonObject(map[string]any{"kind": KindComplete, "body": body})
	if err != nil {
		return fmt.Errorf("request: encode last_reply: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE requests SET state = ?, state_seq = ?, state_at = ?, note = ?, result = ?, last_reply = ?, last_reply_sent = ?, updated = ?
WHERE direction = 'in' AND peer = ? AND id = ?`,
		StateCompleted, seq, wireTime(now), noteArg, resultArg, lastReply, storeTime(now), storeTime(now), peer, id); err != nil {
		return fmt.Errorf("request: complete in row (tx): %w", err)
	}
	if _, err := s.Outbox.SubmitTx(ctx, tx, peer, KindComplete, body); err != nil {
		return err
	}
	if s.Audit != nil {
		extra := map[string]any{"request": id, "peer": peer, "team": row.teamID, "type": row.typ, "urgency": row.urgency, "seq": seq, "age_s": ageSeconds(row, now)}
		if result != nil {
			extra["result_bytes"] = ResultBytes(resultCanon)
			extra["output_bytes"] = OutputBytes(result.Output)
			extra["artifacts"] = len(result.Artifacts)
		}
		_ = s.Audit.Append(ctx, "daemon", "request.complete", extra)
	}
	return nil
}
