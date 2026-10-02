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
// accept). actor is the audit actor of the ws.open row: the accept's own
// ("cli", or "daemon" for a device helper's auto-accept) on B, "daemon" on A.
type SessionOpener interface {
	OpenSession(ctx context.Context, tx *sql.Tx, actor, role, peer, requestID, teamID string, now time.Time) error
}

// CompleteContent is what SessionEarlyComplete decides about the content of
// a request.complete from B (Docs/protocol/work-session.md §Closing the
// request, §Early complete and Phase 1 workers, R55-F18).
type CompleteContent struct {
	// Override: store Note and Result below instead of B's note and result.
	// Empty Note and nil Result store no content at all.
	Override bool
	Note     string
	Result   *Result
	// Withhold: store the mail's inbox copy blank (#inbox-copy-d18).
	Withhold bool
	// AfterApplied, if set, runs once after commit, and only when the mirror
	// applied the mail (its seq advanced the row): the result_mismatch audit,
	// which a stale request.complete must not write (review 69b F2).
	AfterApplied func(context.Context)
}

// SessionEarlyComplete is called by applyComplete inside the mail apply
// transaction, for a request.complete mail applied while a session might
// exist for it (Docs/protocol/work-session.md §Early complete and Phase 1
// workers). note and result are what B sent. If the session is (still)
// open, the hook closes it (outcome cancelled). If it is closed, A's own
// view of the close overrides B's content; while a result is under review,
// or when the quarantine rule holds, B's content is not stored. The returned
// after func, if non-nil, is called once by the caller after its transaction
// commits (it may append audit rows: ws.close for the caused close, and
// ws.ignored {reason: "early_complete"} when content was dropped).
type SessionEarlyComplete interface {
	EarlyComplete(ctx context.Context, tx *sql.Tx, peer, requestID, note string, result *Result) (content CompleteContent, after func(context.Context), err error)
}

// SessionEnder is called by the sender mirror inside the mail transaction
// when a late request.decline or request.cancelled from B is applied
// (Docs/protocol/work-session.md §Early complete, "Late decline or
// cancelled", R55-062): an open requester session closes cancelled, like an
// early complete. The after func, if non-nil, runs once after commit.
type SessionEnder interface {
	SessionEndedTx(ctx context.Context, tx *sql.Tx, peer, requestID string) (after func(context.Context), err error)
}

// RunMarker marks the worker session of an auto-accepted run request as a
// run session, in the receive transaction that accepted it
// (Docs/protocol/work-session.md §Run sessions, R55-029).
type RunMarker interface {
	MarkRunnerTx(ctx context.Context, tx *sql.Tx, peer, requestID string) error
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

// DebateHooks wires debates (Docs/protocol/debate.md, ticket 3.1a) into the
// request lifecycle from internal/debate, which depends on this package. Both
// methods run inside the caller's transaction and must touch only tx.
type DebateHooks interface {
	// ReceivedTx stores the respondent's debate row (phase invited, with the
	// commitment) for a new pending debate request, in the transaction that
	// stores the request (Docs/protocol/debate.md §Request type debate).
	ReceivedTx(ctx context.Context, tx *sql.Tx, req *Request, now time.Time) error
	// EndedTx closes the debate row of a debate request that ended before it
	// was accepted (declined or cancelled, either side): "a decline, a
	// request.cancel or an auto-decline closes both rows cancelled".
	// direction is the request row's ("in" or "out").
	EndedTx(ctx context.Context, tx *sql.Tx, direction, peer, id string, now time.Time) error
	// HasDecisionTx reports whether the session derived for a debate request
	// from `from` to `to` with id already has a stored Decision: its request
	// was pruned and the id is being re-used (Docs/protocol/request.md
	// §Receiving step 2, review 71b F8).
	HasDecisionTx(ctx context.Context, tx *sql.Tx, from, to, id string) (bool, error)
}

// SessionHooks is the combination *worksession.Store implements.
type SessionHooks interface {
	SessionOpener
	SessionEarlyComplete
	SessionCompleter
	SessionEnder
	RunMarker
}

// SetOutContentTx replaces the note and result of the out request record
// (peer, id) inside tx, if and only if it is completed, touching no state or
// seq; updated becomes now (review 81b M1). A's session close writes its own view of the close with it
// (Docs/protocol/work-session.md §Closing the request, review 69b F1).
func (s *Store) SetOutContentTx(ctx context.Context, tx *sql.Tx, peer, id, note string, result *Result) error {
	var noteArg, resultArg any
	if note != "" {
		noteArg = note
	}
	if result != nil {
		canon, err := CanonicalResult(result)
		if err != nil {
			return err
		}
		resultArg = string(canon)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE requests SET note = ?, result = ?, updated = ? WHERE direction = 'out' AND peer = ? AND id = ? AND state = ?`,
		noteArg, resultArg, storeTime(s.now()), peer, id, StateCompleted); err != nil {
		return fmt.Errorf("request: set out content: %w", err)
	}
	return nil
}

// CompleteInTx is the tx-scoped counterpart to Complete: it applies
// request.complete's `in`-row update and sends the request.complete mail
// inside tx, without opening or committing a transaction itself. It is used
// only when a work session closes (Docs/protocol/work-session.md §Closing
// the request): the caller commits after this and its own row update both
// succeed, so a crash cannot leave the session closed with the request still
// accepted, or vice versa.
//
// It writes no audit row itself: the audit log shares the daemon's single
// SQLite connection, which tx holds, so an Append here would block until ctx
// ends. The returned function appends the request.complete audit row; the
// caller runs it after tx commits.
func (s *Store) CompleteInTx(ctx context.Context, tx *sql.Tx, peer, id, note string, result *Result) (func(context.Context), error) {
	if err := ValidateComplete(note, result); err != nil {
		return nil, err
	}
	row, err := getRow(ctx, tx, "in", peer, id)
	if err != nil {
		return nil, err
	}
	if row.state != StateAccepted {
		return nil, &BadStateError{State: row.state, Msg: fmt.Sprintf("%s is %s", id, row.state)}
	}
	now := s.now()
	seq := row.stateSeq + 1
	canon, err := completeBodyCanonical(id, seq, now, note, result)
	if err != nil {
		return nil, err
	}
	if err := CheckCompleteSize(canon); err != nil {
		return nil, err
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
			return nil, err
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
		return nil, fmt.Errorf("request: encode last_reply: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE requests SET state = ?, state_seq = ?, state_at = ?, note = ?, result = ?, last_reply = ?, last_reply_sent = ?, updated = ?
WHERE direction = 'in' AND peer = ? AND id = ?`,
		StateCompleted, seq, wireTime(now), noteArg, resultArg, lastReply, storeTime(now), storeTime(now), peer, id); err != nil {
		return nil, fmt.Errorf("request: complete in row (tx): %w", err)
	}
	if _, err := s.Outbox.SubmitTx(ctx, tx, peer, KindComplete, body); err != nil {
		return nil, err
	}
	extra := map[string]any{"request": id, "peer": peer, "team": row.teamID, "type": row.typ, "urgency": row.urgency, "seq": seq, "age_s": ageSeconds(row, now)}
	if result != nil {
		extra["result_bytes"] = ResultBytes(resultCanon)
		extra["output_bytes"] = OutputBytes(result.Output)
		extra["artifacts"] = len(result.Artifacts)
	}
	return func(ctx context.Context) {
		if s.Audit != nil {
			_ = s.Audit.Append(ctx, "daemon", "request.complete", extra)
		}
	}, nil
}
