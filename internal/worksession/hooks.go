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
func (s *Store) OpenSession(ctx context.Context, tx *sql.Tx, role, peer, requestID, teamID string, now time.Time) error {
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
	if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO work_sessions (id, role, peer, request_id, team_id, state, seq, round, opened, state_at, updated, kind)
VALUES (?, ?, ?, ?, ?, ?, 0, 1, ?, ?, ?, ?)`,
		sid, role, peer, requestID, teamID, StateOpen, wireTime(now), wireTime(now), storeTime(now), kind); err != nil {
		return fmt.Errorf("worksession: open session: %w", err)
	}
	if kind == SessionKindDebate {
		return s.Debate.OpenedTx(ctx, tx, role, peer, requestID, sid, now)
	}
	return nil
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
// applyComplete when a request.complete mail is applied while A's session
// for the request might still be open (Docs/protocol/work-session.md §Early
// complete and Phase 1 workers). keepContent is false only when the
// quarantine rule holds (2.4 wires Quarantine; before that it never holds).
// The returned after func audits the caused close (ws.close, review 27 L2)
// and the dropped content (ws.ignored {reason: "early_complete"}, review 27
// L3), after the caller's transaction commits.
func (s *Store) EarlyComplete(ctx context.Context, tx *sql.Tx, peer, requestID string, hadResult bool) (keepContent bool, after func(context.Context), err error) {
	row, err := findRowTx(ctx, tx, RoleRequester, peer, requestID)
	if errors.Is(err, ErrUnknownSession) {
		// A debate has no result: an early complete for a debate request
		// always drops its result and note (Docs/protocol/debate.md §Cancel
		// and abandon, review 43 M4).
		if typ, _, terr := requestType(ctx, tx, RoleRequester, peer, requestID); terr != nil {
			return true, nil, terr
		} else if typ == request.TypeDebate {
			if hadResult {
				return false, s.auditEarlyCompleteDropped(DeriveID(s.Self, peer, requestID), peer), nil
			}
			return false, nil, nil
		}
		// No session (yet): B skipped or overtook the accept. The session is
		// "not closed", so this is still an early complete, and the rule's
		// peer-wide clause (a sensitive grant to this peer in another
		// session, less than 7 d ago) can hold without a row here.
		if hadResult && s.Quarantine != nil {
			sid := DeriveID(s.Self, peer, requestID)
			q, qerr := s.Quarantine(ctx, tx, sid, peer, 0)
			if qerr != nil {
				return true, nil, qerr
			}
			if q {
				return false, s.auditEarlyCompleteDropped(sid, peer), nil
			}
		}
		return true, nil, nil
	}
	if err != nil {
		return true, nil, err
	}
	if row.kind == SessionKindDebate && row.state != StateClosed {
		return s.earlyCompleteDebate(ctx, tx, row, hadResult)
	}
	keepContent = true
	if hadResult && s.Quarantine != nil {
		q, qerr := s.Quarantine(ctx, tx, row.id, peer, row.round)
		if qerr != nil {
			return true, nil, qerr
		}
		keepContent = !q
	}
	var afterClose func(context.Context)
	if row.state == StateOpen {
		now := s.now()
		expBytes, expTruncated, err := s.closeSessionTx(ctx, tx, row, OutcomeCancelled, "", "", RoleWorker, now)
		if err != nil {
			return keepContent, nil, err
		}
		closedRow := row
		afterClose = func(ctx context.Context) {
			s.auditClose(ctx, closedRow, OutcomeCancelled, now)
			s.auditExperience(ctx, closedRow.id, closedRow.role, expBytes, expTruncated)
		}
	}
	// awaiting_result or quarantined: only a misbehaving Phase 2 worker can
	// cause this; the session is left to A (Docs/protocol/work-session.md).
	var afterDrop func(context.Context)
	if !keepContent {
		afterDrop = s.auditEarlyCompleteDropped(row.id, peer)
	}
	if afterClose == nil && afterDrop == nil {
		return keepContent, nil, nil
	}
	return keepContent, func(ctx context.Context) {
		if afterClose != nil {
			afterClose(ctx)
		}
		if afterDrop != nil {
			afterDrop(ctx)
		}
	}, nil
}

// earlyCompleteDebate is EarlyComplete on a debate session that is not
// closed (review 43 M4): the result and note are always dropped unread, and a
// debate still in positions, rounds or converge closes cancelled on A (how
// B's abandon reaches A). Once the session is closed (the debate reached
// closing or closed), B's request.complete is the normal end and is kept.
func (s *Store) earlyCompleteDebate(ctx context.Context, tx *sql.Tx, row storedRow, hadResult bool) (bool, func(context.Context), error) {
	if s.Debate == nil {
		return false, nil, fmt.Errorf("worksession: debate sessions are not wired")
	}
	var afterClose, afterDrop func(context.Context)
	fn, err := s.Debate.EarlyCompleteTx(ctx, tx, row.id, s.now())
	if err != nil {
		return false, nil, err
	}
	afterClose = fn
	if hadResult {
		afterDrop = s.auditEarlyCompleteDropped(row.id, row.peer)
	}
	if afterClose == nil && afterDrop == nil {
		return false, nil, nil
	}
	return false, func(ctx context.Context) {
		if afterClose != nil {
			afterClose(ctx)
		}
		if afterDrop != nil {
			afterDrop(ctx)
		}
	}, nil
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
