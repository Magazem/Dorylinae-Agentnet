package worksession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// SubmitResult runs ws_result (B only, Docs/protocol/ipc.md
// §Methods): B sends result for its (worker-role) session of requestID with
// peer. It also stores the result on B's own row, so B can build the D14
// part of request.complete when the session later closes (Docs/protocol/work-session.md
// §Closing the request). ok reports whether a session exists at all; when
// !ok the caller (e.g. request_complete's shorthand) should fall back to its
// own normal path. by names the submitter: on a run session only ByRunner is
// allowed (Docs/protocol/work-session.md §Run sessions).
func (s *Store) SubmitResult(ctx context.Context, peer, requestID string, result *Result, by Submitter) (ok bool, mailID string, err error) {
	if err := ValidateResult(result); err != nil {
		return true, "", err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return true, "", fmt.Errorf("worksession: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	ok, mailID, after, err := s.submitResultTx(ctx, tx, peer, requestID, result, by)
	if !ok || err != nil {
		return ok, "", err
	}
	if err := tx.Commit(); err != nil {
		return true, "", fmt.Errorf("worksession: commit: %w", err)
	}
	s.Outbox.Wake()
	after(ctx)
	return true, mailID, nil
}

// submitResultTx is SubmitResult inside the caller's transaction (it neither
// commits nor wakes the outbox). after audits ws.result and must be called
// once, after the caller's commit: the audit log shares the daemon's one
// SQLite connection, which tx holds.
func (s *Store) submitResultTx(ctx context.Context, tx *sql.Tx, peer, requestID string, result *Result, by Submitter) (ok bool, mailID string, after func(context.Context), err error) {
	row, err := findRowTx(ctx, tx, RoleWorker, peer, requestID)
	if errors.Is(err, ErrUnknownSession) {
		return false, "", nil, nil
	}
	if err != nil {
		return true, "", nil, err
	}
	if row.kind == SessionKindDebate {
		// Also request_complete's shorthand on a debate (review 43 M4): B's
		// daemon completes a debate request itself when the mirror closes.
		return true, "", nil, debateBadState(row, "debates have no result")
	}
	if row.runner != 0 && by != ByRunner {
		return true, "", nil, errRunnerSession(row)
	}
	if row.state != StateOpen || (row.cancel.Valid && row.cancel.String == "requested") {
		return true, "", nil, &BadStateError{State: row.state, Msg: fmt.Sprintf("%s is %s", row.id, row.state)}
	}
	// One result per round (R55-F18, review 55 R55-022): B cannot know
	// whether A applied an earlier one, so a revised answer goes through
	// cancel or A's request for changes. The mirror clears result on a new
	// round, which allows the next one.
	if row.result.Valid && row.resultRound.Valid && row.resultRound.Int64 == int64(row.round) {
		return true, "", nil, &BadStateError{State: row.state, Msg: fmt.Sprintf("a result for round %d was already submitted; wait for the requester or cancel", row.round)}
	}

	now := s.now()
	canon, err := resultBodyCanonical(row.id, requestID, row.round, now, result)
	if err != nil {
		return true, "", nil, err
	}
	if err := CheckResultSize(canon); err != nil {
		return true, "", nil, err
	}
	resultCanon, err := CanonicalResult(result)
	if err != nil {
		return true, "", nil, err
	}
	body := map[string]any{
		"at": wireTime(now), "request": requestID, "result": resultWire(result),
		"round": row.round, "session": row.id,
	}
	sub, err := s.Outbox.SubmitTx(ctx, tx, peer, KindResult, body)
	if err != nil {
		return true, "", nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE work_sessions SET result = ?, result_round = ?, result_mail = ?, updated = ? WHERE id = ?`,
		string(resultCanon), row.round, sub.ID, storeTime(now), row.id); err != nil {
		return true, "", nil, fmt.Errorf("worksession: store result: %w", err)
	}
	after = func(ctx context.Context) {
		if s.Audit != nil {
			_ = s.Audit.Append(ctx, "cli", "ws.result", map[string]any{
				"session": row.id, "peer": peer, "round": row.round,
				"result_bytes": ResultBytes(resultCanon), "output_bytes": OutputBytes(result.Output),
				"artifacts": len(result.Artifacts), "verification": result.Verification,
			})
		}
	}
	return true, sub.ID, after, nil
}

// CancelUndeliveredRunResult is the runner's answer to its ws.result mail
// mailID ending failed (other than unsupported_kind, which the Phase 1
// fallback handles) or expired (review 78 S2): the run session cannot get
// another result in this round and no agent may cancel it, so the runner
// sends ws.cancel (ByRunner) to end it. It does nothing unless mailID is the
// current round's result of a run session that is still open with no cancel
// requested. It reports whether a cancel was sent.
func (s *Store) CancelUndeliveredRunResult(ctx context.Context, mailID string) (bool, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM work_sessions WHERE role = ? AND runner = 1 AND result_mail = ? AND state = ? AND cancel IS NULL`,
		RoleWorker, mailID, StateOpen).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("worksession: find run session: %w", err)
	}
	if _, _, _, err := s.SubmitCancel(ctx, id, "", ByRunner); err != nil {
		return false, err
	}
	return true, nil
}

// UndeliveredRunResults lists the ws.result mail ids of open run sessions
// (no cancel requested) whose result ended failed (other than
// unsupported_kind) or expired, for the daemon's start-up rescan: a trigger
// lost to a crash is found again (review 78 S2).
func (s *Store) UndeliveredRunResults(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT w.result_mail FROM work_sessions w JOIN outbox o ON o.id = w.result_mail
WHERE w.role = ? AND w.runner = 1 AND w.state = ? AND w.cancel IS NULL
  AND (o.state = ? OR (o.state = ? AND o.error IS NOT ?))`,
		RoleWorker, StateOpen, mail.StateExpired, mail.StateFailed, mail.ErrTextUnsupportedKind)
	if err != nil {
		return nil, fmt.Errorf("worksession: list undelivered run results: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("worksession: scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AnswerQuestion is the one-step answer of a consult
// (Docs/protocol/consult.md §Answering): for a pending or deferred question
// request it accepts the request, opens the session and submits the result
// (round 1) in ONE transaction, so a failure of any step (a result over the
// size cap, say) leaves the question exactly as it was. from narrows the
// request lookup when the id is ambiguous. It returns the session id and the
// ws.result mail id.
func (s *Store) AnswerQuestion(ctx context.Context, requestID, from string, result *Result) (sid, mailID string, err error) {
	if err := ValidateResult(result); err != nil {
		return "", "", err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", "", fmt.Errorf("worksession: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	peer, afterAccept, err := s.Requests.AcceptInTx(ctx, tx, requestID, from, request.TypeQuestion)
	if err != nil {
		return "", "", err
	}
	ok, mailID, afterResult, err := s.submitResultTx(ctx, tx, peer, requestID, result, ByAgent)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", errors.New("worksession: session missing right after accept")
	}
	if err := tx.Commit(); err != nil {
		return "", "", fmt.Errorf("worksession: commit: %w", err)
	}
	s.Outbox.Wake()
	afterAccept(ctx)
	afterResult(ctx)
	return DeriveID(peer, s.Self, requestID), mailID, nil
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
	// An agent's request_complete: refused on a run session like ws_result.
	ok, _, err = s.SubmitResult(ctx, peer, requestID, ws, ByAgent)
	return ok, err
}
