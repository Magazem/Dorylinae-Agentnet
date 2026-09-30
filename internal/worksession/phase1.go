package worksession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
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
// outbox does not know about sessions, and drops the signed body once a row
// is final), so this looks for a ws.result or ws.cancel to peer, created
// since this session opened, that ended failed/unsupported_kind. Older
// failures (a peer that was Phase 1 and has since upgraded) do not count. A
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
	// A debate session is never swept (R55-060): it ends through the debate.
	if row.state == StateClosed || row.kind == SessionKindDebate {
		return nil
	}
	unsupported, err := peerIsUnsupported(ctx, s.DB, peer, storeTime(parseWireTime(row.opened)))
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

	// Re-read inside the transaction: a ws.state may have been applied since.
	row, err = findRowTx(ctx, tx, RoleWorker, peer, requestID)
	if err != nil {
		return err
	}
	if row.state == StateClosed || row.kind == SessionKindDebate {
		return nil
	}
	now := s.now()
	if _, err := tx.ExecContext(ctx, `
UPDATE work_sessions SET state = ?, outcome = ?, closed = ?, state_at = ?, updated = ? WHERE id = ?`,
		StateClosed, OutcomeCancelled, wireTime(now), wireTime(now), storeTime(now), row.id); err != nil {
		return fmt.Errorf("worksession: close row (phase 1 fallback): %w", err)
	}
	if s.RevokeGrants != nil {
		if err := s.RevokeGrants(ctx, tx, row.id, now); err != nil {
			return err
		}
	}
	expBytes, expTruncated, err := s.writeExperienceTx(ctx, tx, row, OutcomeCancelled, "", "", "timeout", now)
	if err != nil {
		return err
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
	var auditComplete func(context.Context)
	if s.Requests != nil {
		fn, err := s.Requests.CompleteInTx(ctx, tx, peer, requestID, note, res)
		if err != nil {
			return fmt.Errorf("worksession: complete request (phase 1 fallback): %w", err)
		}
		auditComplete = fn
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("worksession: commit: %w", err)
	}
	if s.Outbox != nil {
		s.Outbox.Wake()
	}
	if auditComplete != nil {
		auditComplete(ctx)
	}
	s.auditClose(ctx, row, OutcomeCancelled, now)
	s.auditExperience(ctx, row.id, row.role, expBytes, expTruncated)
	return nil
}

// CheckPhase1FallbackForPeer runs CheckPhase1Fallback for every not-yet-closed
// worker-role session with peer. There is no per-mail linkage from an outbox
// row back to its session (see CheckPhase1Fallback), so the daemon's trigger
// (an outbox row to peer, kind ws.result/ws.cancel, ending
// failed/unsupported_kind) is scoped to the peer, not one session; this
// checks each of that peer's open work sessions in turn; debate sessions are
// skipped (R55-060). It is a best-effort background sweep: an error is
// logged, content-free, once per call, and the sweep moves on (a
// cancelled ctx ends it quietly).
func (s *Store) CheckPhase1FallbackForPeer(ctx context.Context, peer string) {
	rows, err := s.DB.QueryContext(ctx, `SELECT request_id FROM work_sessions WHERE role = ? AND peer = ? AND state != ? AND kind != ?`,
		RoleWorker, peer, StateClosed, SessionKindDebate)
	if err != nil {
		s.logPhase1Error(ctx, "", peer, err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		s.logPhase1Error(ctx, "", peer, err)
		return
	}
	var firstErr error
	var firstSID string
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if err := s.CheckPhase1Fallback(ctx, peer, id); err != nil && firstErr == nil {
			firstErr, firstSID = err, DeriveID(peer, s.Self, id)
		}
	}
	if firstErr != nil {
		s.logPhase1Error(ctx, firstSID, peer, firstErr)
	}
}

// logPhase1Error logs a fallback sweep error: ids and the error only, never
// content. Nothing is logged once ctx is cancelled (daemon shutdown).
func (s *Store) logPhase1Error(ctx context.Context, sid, peer string, err error) {
	if s.Log == nil || ctx.Err() != nil {
		return
	}
	s.Log.Warn("worksession: phase 1 fallback check failed", "session", sid, "peer", peer, "error", err)
}

// PeersWithOpenWorkerSessions lists the peers this daemon has a not-yet-closed
// worker-role work session with (debates excluded), for the start-up rescan of
// the Phase 1 fallback (R55-061): a trigger lost to a crash is found again.
func (s *Store) PeersWithOpenWorkerSessions(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT peer FROM work_sessions WHERE role = ? AND state != ? AND kind != ? ORDER BY peer`,
		RoleWorker, StateClosed, SessionKindDebate)
	if err != nil {
		return nil, fmt.Errorf("worksession: list peers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("worksession: scan peer: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
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

// peerIsUnsupported reports whether peer's outbox has a ws.result or
// ws.cancel (the kinds B sends) created at or after since that ended
// failed/unsupported_kind (mail.Outbox.OnAck). since and outbox.created are
// both mail.StoreTimeFmt, which orders as text.
func peerIsUnsupported(ctx context.Context, db *sql.DB, peer, since string) (bool, error) {
	var one int
	err := db.QueryRowContext(ctx, `
SELECT 1 FROM outbox WHERE to_key = ? AND kind IN (?, ?) AND state = ? AND error = ? AND created >= ? LIMIT 1`,
		peer, KindResult, KindCancel, mail.StateFailed, mail.ErrTextUnsupportedKind, since).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
