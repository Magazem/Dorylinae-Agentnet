package worksession

import (
	"context"
	"errors"
	"fmt"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// SubmitResult runs ws_result (B only, Docs/protocol/ipc.md
// §Methods): B sends result for its (worker-role) session of requestID with
// peer. It also stores the result on B's own row, so B can build the D14
// part of request.complete when the session later closes (Docs/protocol/work-session.md
// §Closing the request). ok reports whether a session exists at all; when
// !ok the caller (e.g. request_complete's shorthand) should fall back to its
// own normal path.
func (s *Store) SubmitResult(ctx context.Context, peer, requestID string, result *Result) (ok bool, mailID string, err error) {
	if err := ValidateResult(result); err != nil {
		return true, "", err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return true, "", fmt.Errorf("worksession: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row, err := findRowTx(ctx, tx, RoleWorker, peer, requestID)
	if errors.Is(err, ErrUnknownSession) {
		return false, "", nil
	}
	if err != nil {
		return true, "", err
	}
	if row.state != StateOpen || (row.cancel.Valid && row.cancel.String == "requested") {
		return true, "", &BadStateError{State: row.state, Msg: fmt.Sprintf("%s is %s", row.id, row.state)}
	}

	now := s.now()
	canon, err := resultBodyCanonical(row.id, requestID, row.round, now, result)
	if err != nil {
		return true, "", err
	}
	if err := CheckResultSize(canon); err != nil {
		return true, "", err
	}
	resultCanon, err := CanonicalResult(result)
	if err != nil {
		return true, "", err
	}
	body := map[string]any{
		"at": wireTime(now), "request": requestID, "result": resultWire(result),
		"round": row.round, "session": row.id,
	}
	if _, err := tx.ExecContext(ctx, `UPDATE work_sessions SET result = ?, result_round = ?, updated = ? WHERE id = ?`,
		string(resultCanon), row.round, storeTime(now), row.id); err != nil {
		return true, "", fmt.Errorf("worksession: store result: %w", err)
	}
	sub, err := s.Outbox.SubmitTx(ctx, tx, peer, KindResult, body)
	if err != nil {
		return true, "", err
	}
	if err := tx.Commit(); err != nil {
		return true, "", fmt.Errorf("worksession: commit: %w", err)
	}
	s.Outbox.Wake()
	if s.Audit != nil {
		resultBytes := ResultBytes(resultCanon)
		outputBytes := OutputBytes(result.Output)
		_ = s.Audit.Append(ctx, "cli", "ws.result", map[string]any{
			"session": row.id, "peer": peer, "round": row.round,
			"result_bytes": resultBytes, "output_bytes": outputBytes,
			"artifacts": len(result.Artifacts), "verification": result.Verification,
		})
	}
	return true, sub.ID, nil
}

// CompleteShorthand implements request.SessionCompleter: request_complete on
// a request whose session exists is a shorthand for ws_result with the given
// note as notes, the D14 result, and verification none (a result without
// status is not possible, so complete without --status submits
// {"status": "n/a"}), Docs/protocol/work-session.md "request_complete while
// a session exists".
func (s *Store) CompleteShorthand(ctx context.Context, peer, requestID, note string, result *request.Result) (ok bool, err error) {
	status := request.ResultNA
	var summary string
	var exitCode *int64
	var output string
	var artifacts []request.Artifact
	if result != nil {
		status = result.Status
		summary, exitCode, output, artifacts = result.Summary, result.ExitCode, result.Output, result.Artifacts
	}
	ws := &Result{
		Status: status, Summary: summary, ExitCode: exitCode, Output: output, Artifacts: artifacts,
		Verification: VerificationNone, Notes: note,
	}
	ok, _, err = s.SubmitResult(ctx, peer, requestID, ws)
	return ok, err
}
