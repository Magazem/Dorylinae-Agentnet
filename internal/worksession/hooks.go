package worksession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// OpenSession implements request.SessionOpener: called inside the same
// transaction as an accept (Docs/protocol/work-session.md §Session id, "the
// session row is created in the same transaction as the accept, so Accepted
// is never observable without Open"). Idempotent: a session may already
// exist (a ws.result that overtook the accept created it first, or the
// accept mail is seen twice).
//
// A debate request opens a session of kind debate, and the debate learns of
// it in the same transaction (DebateHooks.OpenedTx, Docs/protocol/debate.md
// §Model).
func (s *Store) OpenSession(ctx context.Context, tx *sql.Tx, actor, role, peer, requestID, teamID string, now time.Time) error {
	sid := DeriveID(idA(role, s.Self, peer), idB(role, s.Self, peer), requestID)
	typ, _, err := requestType(ctx, tx, role, peer, requestID)
	if err != nil {
		return err
	}
	kind := SessionKindWork
	if typ == request.TypeDebate {
		if s.Debate == nil {
			return fmt.Errorf("worksession: debate sessions are not wired")
		}
		kind = SessionKindDebate
	}
	res, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO work_sessions (id, role, peer, request_id, team_id, state, seq, round, opened, state_at, updated, kind)
VALUES (?, ?, ?, ?, ?, ?, 0, 1, ?, ?, ?, ?)`,
		sid, role, peer, requestID, teamID, StateOpen, wireTime(now), wireTime(now), storeTime(now), kind)
	if err != nil {
		return fmt.Errorf("worksession: open session: %w", err)
	}
	// ws.open is written when this call inserted the row (class N: a failed
	// row is logged and the session still opens; R55-121).
	if n, _ := res.RowsAffected(); n == 1 && s.Audit != nil {
		if err := s.auditOpen(ctx, tx, actor, sid, requestID, peer, role); err != nil {
			return err
		}
	}
	if kind == SessionKindDebate {
		return s.Debate.OpenedTx(ctx, tx, role, peer, requestID, sid, now)
	}
	return nil
}

// auditOpen writes the ws.open row of a session row just inserted in tx,
// through a savepoint (class N, audit.md §When the row cannot be written).
func (s *Store) auditOpen(ctx context.Context, tx *sql.Tx, actor, sid, requestID, peer, role string) error {
	return s.Audit.AppendTxSoft(ctx, tx, actor, "ws.open", map[string]string{
		"session": sid, "request": requestID, "peer": peer, "role": role,
	})
}

// idA and idB order (requester-key, worker-key) for DeriveID: the requester
// is A, the worker is B (Docs/protocol/work-session.md §Session id). role is
// this daemon's own role in the session being opened.
func idA(role, self, peer string) string {
	if role == RoleRequester {
		return self
	}
	return peer
}

func idB(role, self, peer string) string {
	if role == RoleRequester {
		return peer
	}
	return self
}

// EarlyComplete implements request.SessionEarlyComplete: called by A's
// applyComplete when a request.complete mail from B is applied, inside the
// mail transaction (Docs/protocol/work-session.md §Early complete and Phase 1
// workers, §Closing the request). note and result are what B sent.
//
//   - open: the session closes cancelled (an early complete); B's content is
//     stored unless the quarantine rule holds (2.4) or the session is past
//     round 1 (B proved Phase 2 there, review 78 S1: A stores "session
//     cancelled").
//   - awaiting_result or quarantined: a result is under review, so B's
//     content is never stored (review 69b F1); A's close writes A's view.
//   - closed: A stores its own view of the close, whatever B sent (R55-022);
//     a difference is audited result_mismatch, only when the mirror applies
//     the mail (review 69b F2).
//
// The returned after func audits the caused close (ws.close, review 27 L2)
// and dropped content (ws.ignored {reason: "early_complete"}, review 27 L3),
// after the caller's transaction commits.
func (s *Store) EarlyComplete(ctx context.Context, tx *sql.Tx, peer, requestID, note string, result *request.Result) (request.CompleteContent, func(context.Context), error) {
	hadResult := result != nil || note != ""
	keep := request.CompleteContent{}
	drop := request.CompleteContent{Override: true, Withhold: true}
	row, err := findRowTx(ctx, tx, RoleRequester, peer, requestID)
	if errors.Is(err, ErrUnknownSession) {
		// A debate has no result: an early complete for a debate request
		// always drops its result and note (Docs/protocol/debate.md §Cancel
		// and abandon, review 43 M4).
		if typ, _, terr := requestType(ctx, tx, RoleRequester, peer, requestID); terr != nil {
			return keep, nil, terr
		} else if typ == request.TypeDebate {
			if hadResult {
				return drop, s.auditEarlyCompleteDropped(DeriveID(s.Self, peer, requestID), peer), nil
			}
			return drop, nil, nil
		}
		// No session (yet): B skipped or overtook the accept. The session is
		// "not closed", so this is still an early complete, and the rule's
		// peer-wide clause (a sensitive grant to this peer in another
		// session, less than 7 d ago) can hold without a row here.
		if hadResult && s.Quarantine != nil {
			sid := DeriveID(s.Self, peer, requestID)
			q, qerr := s.Quarantine(ctx, tx, sid, peer, 0)
			if qerr != nil {
				return keep, nil, qerr
			}
			if q {
				return drop, s.auditEarlyCompleteDropped(sid, peer), nil
			}
		}
		return keep, nil, nil
	}
	if err != nil {
		return keep, nil, err
	}
	if row.kind == SessionKindDebate && row.state != StateClosed {
		return s.earlyCompleteDebate(ctx, tx, row, hadResult)
	}
	quarantined := false
	if hadResult && s.Quarantine != nil {
		q, qerr := s.Quarantine(ctx, tx, row.id, peer, row.round)
		if qerr != nil {
			return keep, nil, qerr
		}
		quarantined = q
	}
	switch {
	case row.state == StateClosed && row.kind != SessionKindDebate:
		// A stores its own view of the close (R55-022). A released and
		// accepted result is A's own reviewed copy, so it is stored even
		// when the quarantine rule holds (OD-F18-8); only the inbox copy
		// follows the rule.
		aNote, aRes, verr := s.requesterView(row, row.outcome.String)
		if verr != nil {
			return keep, nil, verr
		}
		cc := request.CompleteContent{Override: true, Note: aNote, Result: aRes, Withhold: quarantined}
		same, cerr := sameContent(note, result, aNote, aRes)
		if cerr != nil {
			return keep, nil, cerr
		}
		if !same {
			cc.AfterApplied = s.auditResultMismatch(row.id, peer)
		}
		return cc, nil, nil
	case row.state == StateAwaitingResult || row.state == StateQuarantined:
		// Only a misbehaving Phase 2 worker can cause this. The session is
		// left to A, and B's content is not stored whatever the quarantine
		// rule says (review 69b F1): A's close writes A's view.
		if hadResult {
			return drop, s.auditEarlyCompleteDropped(row.id, peer), nil
		}
		return drop, nil, nil
	}
	// open, or a closed debate (whose request.complete is the normal end).
	cc := keep
	// A session past round 1 has had a ws.result from B applied (only a
	// result leaves round 1's open state, and only a request for changes
	// returns to open), so B is Phase 2 here: its early complete is not the
	// Phase 1 path, and its content was never reviewed. A stores its own view
	// of the cancelled close instead (review 78 S1, owner decision). A Phase
	// 1 B never sends ws.result, so it keeps today's behaviour.
	phase2 := row.state == StateOpen && row.kind != SessionKindDebate && (row.round > 1 || row.seq > 0)
	switch {
	case phase2:
		cc = request.CompleteContent{Override: true, Note: "session cancelled", Withhold: true}
	case quarantined:
		cc = drop
	}
	var afterClose func(context.Context)
	if row.state == StateOpen {
		now := s.now()
		expBytes, expTruncated, _, err := s.closeSessionTx(ctx, tx, row, OutcomeCancelled, "", "", RoleWorker, now)
		if err != nil {
			return keep, nil, err
		}
		closedRow := row
		afterClose = func(ctx context.Context) {
			s.auditClose(ctx, closedRow, OutcomeCancelled, now)
			s.auditExperience(ctx, closedRow.id, closedRow.role, expBytes, expTruncated)
		}
	}
	var afterDrop func(context.Context)
	if quarantined || (phase2 && hadResult) {
		afterDrop = s.auditEarlyCompleteDropped(row.id, peer)
	}
	return cc, chainAfter(afterClose, afterDrop), nil
}

// sameContent reports whether B's note/result equal A's view, comparing the
// results as canonical JSON.
func sameContent(note string, result *request.Result, aNote string, aRes *request.Result) (bool, error) {
	if note != aNote || (result == nil) != (aRes == nil) {
		return false, nil
	}
	if result == nil {
		return true, nil
	}
	b, err := request.CanonicalResult(result)
	if err != nil {
		return false, err
	}
	a, err := request.CanonicalResult(aRes)
	if err != nil {
		return false, err
	}
	return string(a) == string(b), nil
}

// auditResultMismatch returns the after-commit callback that audits
// ws.ignored {session, peer, kind: "request.complete", reason:
// "result_mismatch"} (Docs/protocol/work-session.md §Closing the request).
func (s *Store) auditResultMismatch(sid, peer string) func(context.Context) {
	return func(ctx context.Context) {
		if s.Audit == nil {
			return
		}
		_ = s.Audit.Append(ctx, "daemon", "ws.ignored", map[string]any{
			"session": sid, "peer": peer, "kind": "request.complete", "reason": "result_mismatch",
		})
	}
}

// chainAfter runs a, then b; either may be nil.
func chainAfter(a, b func(context.Context)) func(context.Context) {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return func(ctx context.Context) {
		a(ctx)
		b(ctx)
	}
}

// SessionEndedTx implements request.SessionEnder: a late request.decline or
// request.cancelled from B was applied to A's mirror (R55-062,
// Docs/protocol/work-session.md §Early complete, "Late decline or
// cancelled"). An open requester session closes cancelled in the same
// transaction, ending its grants and sending the ws.state; a debate session
// closes through the debate. Any other state is left to A (OD-F18-7).
func (s *Store) SessionEndedTx(ctx context.Context, tx *sql.Tx, peer, requestID string) (func(context.Context), error) {
	row, err := findRowTx(ctx, tx, RoleRequester, peer, requestID)
	if errors.Is(err, ErrUnknownSession) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.state != StateOpen {
		return nil, nil
	}
	if row.kind == SessionKindDebate {
		if s.Debate == nil {
			return nil, fmt.Errorf("worksession: debate sessions are not wired")
		}
		return s.Debate.EarlyCompleteTx(ctx, tx, row.id, s.now())
	}
	now := s.now()
	expBytes, expTruncated, _, err := s.closeSessionTx(ctx, tx, row, OutcomeCancelled, "", "", RoleWorker, now)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) {
		s.auditClose(ctx, row, OutcomeCancelled, now)
		s.auditExperience(ctx, row.id, row.role, expBytes, expTruncated)
	}, nil
}

// MarkRunnerTx implements request.RunMarker: the worker session of a run
// request the helper auto-accepted belongs to the runner
// (Docs/protocol/work-session.md §Run sessions, R55-029). Called in the
// receive transaction that accepted it and opened the session.
func (s *Store) MarkRunnerTx(ctx context.Context, tx *sql.Tx, peer, requestID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE work_sessions SET runner = 1 WHERE role = ? AND peer = ? AND request_id = ?`,
		RoleWorker, peer, requestID); err != nil {
		return fmt.Errorf("worksession: mark run session: %w", err)
	}
	return nil
}

// earlyCompleteDebate is EarlyComplete on a debate session that is not
// closed (review 43 M4): the result and note are always dropped unread, and a
// debate still in positions, rounds or converge closes cancelled on A (how
// B's abandon reaches A). Once the session is closed (the debate reached
// closing or closed), B's request.complete is the normal end and is kept.
func (s *Store) earlyCompleteDebate(ctx context.Context, tx *sql.Tx, row storedRow, hadResult bool) (request.CompleteContent, func(context.Context), error) {
	drop := request.CompleteContent{Override: true, Withhold: true}
	if s.Debate == nil {
		return drop, nil, fmt.Errorf("worksession: debate sessions are not wired")
	}
	afterClose, err := s.Debate.EarlyCompleteTx(ctx, tx, row.id, s.now())
	if err != nil {
		return drop, nil, err
	}
	var afterDrop func(context.Context)
	if hadResult {
		afterDrop = s.auditEarlyCompleteDropped(row.id, row.peer)
	}
	return drop, chainAfter(afterClose, afterDrop), nil
}

// auditEarlyCompleteDropped returns the after-commit callback that audits
// ws.ignored {session, peer, kind: "request.complete", reason:
// "early_complete"} (Docs/protocol/work-session.md §Quarantine (2.4), review
// 27 L3).
func (s *Store) auditEarlyCompleteDropped(sid, peer string) func(context.Context) {
	return func(ctx context.Context) {
		if s.Audit == nil {
			return
		}
		_ = s.Audit.Append(ctx, "daemon", "ws.ignored", map[string]any{
			"session": sid, "peer": peer, "kind": "request.complete", "reason": "early_complete",
		})
	}
}
