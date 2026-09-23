package worksession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// CheckPhase1Fallback runs Docs/protocol/work-session.md's "Phase 1
// requester" rule for B's (worker-role) session of requestID with peer: when
// a ws.result mail to peer ended failed/unsupported_kind (mail.Outbox.OnAck,
// error "unsupported_kind"), peer is a Phase 1 daemon that does not know
// ws.*. B then, in one transaction, closes its mirror locally (outcome
// cancelled) and completes the request through the Phase 1 path: for a
// failed ws.result, with the D14 part of that result and its notes as the
// note; for a failed ws.cancel, with no result and note "session cancelled".
//
// There is no per-mail linkage from an outbox row back to its session (the
// outbox does not know about sessions), so this looks for any matching
// failed ws.* mail to peer since the session's row was last updated; a
// daemon calls it after an outbox row addressed to a session's peer turns
// failed with unsupported_kind (2.1b wires that trigger; this method is the
// testable unit run directly here).
func (s *Store) CheckPhase1Fallback(ctx context.Context, peer, requestID string) error {
	row, err := findRow(ctx, s.DB, peer, requestID)
	if errors.Is(err, ErrUnknownSession) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.state == StateClosed {
		return nil
	}
	unsupported, err := peerIsUnsupported(ctx, s.DB, peer)
	if err != nil {
		return err
	}
	if !unsupported {
		return nil
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("worksession: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.now()
	if _, err := tx.ExecContext(ctx, `
UPDATE work_sessions SET state = ?, outcome = ?, closed = ?, state_at = ?, updated = ? WHERE id = ?`,
		StateClosed, OutcomeCancelled, wireTime(now), wireTime(now), storeTime(now), row.id); err != nil {
		return fmt.Errorf("worksession: close row (phase 1 fallback): %w", err)
	}

	note := "session cancelled"
	var res *request.Result
	if row.result.Valid && row.result.String != "" {
		stored, derr := decodeStoredResult(row.result.String)
		if derr != nil {
			return fmt.Errorf("worksession: decode stored result: %w", derr)
		}
		res = &request.Result{Status: stored.Status, Summary: stored.Summary, ExitCode: stored.ExitCode, Output: stored.Output, Artifacts: stored.Artifacts}
		note = stored.Notes
	}
	if s.Requests != nil {
		if err := s.Requests.CompleteInTx(ctx, tx, peer, requestID, note, res); err != nil {
			return fmt.Errorf("worksession: complete request (phase 1 fallback): %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("worksession: commit: %w", err)
	}
	if s.Outbox != nil {
		s.Outbox.Wake()
	}
	if s.Audit != nil {
		_ = s.Audit.Append(ctx, "daemon", "ws.close", map[string]any{"session": row.id, "peer": peer, "outcome": OutcomeCancelled})
	}
	return nil
}

// findRow resolves the worker-role row for (peer, requestID) through the
// connection pool, for use outside a mail-apply transaction.
func findRow(ctx context.Context, db *sql.DB, peer, requestID string) (storedRow, error) {
	row := db.QueryRowContext(ctx, `SELECT `+workSessionColumns+` FROM work_sessions WHERE role = ? AND peer = ? AND request_id = ?`, RoleWorker, peer, requestID)
	r, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return storedRow{}, ErrUnknownSession
	}
	if err != nil {
		return storedRow{}, fmt.Errorf("worksession: read row: %w", err)
	}
	return r, nil
}

// peerIsUnsupported reports whether peer's outbox has any ws.* mail that
// ended failed/unsupported_kind (mail.Outbox.OnAck).
func peerIsUnsupported(ctx context.Context, db *sql.DB, peer string) (bool, error) {
	var one int
	err := db.QueryRowContext(ctx, `
SELECT 1 FROM outbox WHERE to_key = ? AND kind IN (?, ?, ?) AND state = 'failed' AND error = 'unsupported_kind' LIMIT 1`,
		peer, KindResult, KindState, KindCancel).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
