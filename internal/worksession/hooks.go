package worksession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// OpenSession implements request.SessionOpener: called inside the same
// transaction as an accept (Docs/protocol/work-session.md §Session id, "the
// session row is created in the same transaction as the accept, so Accepted
// is never observable without Open"). Idempotent: a session may already
// exist (a ws.result that overtook the accept created it first, or the
// accept mail is seen twice).
func (s *Store) OpenSession(ctx context.Context, tx *sql.Tx, role, peer, requestID, teamID string, now time.Time) error {
	sid := DeriveID(idA(role, s.Self, peer), idB(role, s.Self, peer), requestID)
	if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO work_sessions (id, role, peer, request_id, team_id, state, seq, round, opened, state_at, updated)
VALUES (?, ?, ?, ?, ?, ?, 0, 1, ?, ?, ?)`,
		sid, role, peer, requestID, teamID, StateOpen, wireTime(now), wireTime(now), storeTime(now)); err != nil {
		return fmt.Errorf("worksession: open session: %w", err)
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
		if err := s.closeSessionTx(ctx, tx, row, OutcomeCancelled, "", now); err != nil {
			return keepContent, nil, err
		}
		closedRow := row
		afterClose = func(ctx context.Context) { s.auditClose(ctx, closedRow, OutcomeCancelled, now) }
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
