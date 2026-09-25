package worksession

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// ws.result: B -> A (Docs/protocol/work-session.md §Kinds, "ws.result
// receive steps on A"). Applied inside the mail dedupe transaction.

// resultOutcome is stashed between Apply and After, keyed by the *mail.Opened
// pointer (mirroring internal/request's pendingApply).
type resultOutcome struct {
	orphan      bool
	ignored     string // "" (applied), "state" or "round"
	sessionID   string
	peer        string
	requestID   string
	round       int
	seq         int
	quarantined bool
	resultBytes int
	outputBytes int
	artifacts   int
}

var pendingResult sync.Map // map[*mail.Opened]*resultOutcome

// ResultKind is the receiver Kind for "ws.result", applied on A.
func (s *Store) ResultKind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.applyResult, After: s.afterResult}
}

func (s *Store) applyResult(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
	body := op.Msg.Body
	if err := strictMembers(body, "at", "request", "result", "round", "session"); err != nil {
		return err
	}
	at, err := decodeTime("at", body["at"])
	if err != nil {
		return err
	}
	reqID, err := decodeString(body, "request", false)
	if err != nil {
		return err
	}
	round, err := decodeInt(body, "round")
	if err != nil {
		return err
	}
	if round < 1 {
		return badBody("round must be >= 1")
	}
	sid, err := decodeString(body, "session", false)
	if err != nil {
		return err
	}
	if sid != DeriveID(op.Msg.To, op.Msg.From, reqID) {
		return badBody("session is not the derived id for (to, from, request)")
	}
	rawResult, ok := body["result"].(map[string]any)
	if !ok {
		return badBody("result must be an object")
	}
	result, err := DecodeResult(rawResult)
	if err != nil {
		return badBody("%s", err.Error())
	}
	if err := ValidateResult(result); err != nil {
		return badBody("%s", err.Error())
	}
	canon, err := resultBodyCanonical(sid, reqID, round, at, result)
	if err != nil {
		return badBody("%s", err.Error())
	}
	if err := CheckResultSize(canon); err != nil {
		return badBody("%s", err.Error())
	}

	// Step 2: find the out request row (peer = msg.from, id = request).
	teamID, requestState, requestType, terr := requestOutState(ctx, tx, op.Msg.From, reqID)
	if errors.Is(terr, sql.ErrNoRows) {
		// An orphan result is applied nowhere either, like an ignored one
		// (#inbox-copy-d18 (2)); keeping its plaintext would let B park
		// content in A's database past the peer-wide clause (review 35 H2).
		op.Withhold = true
		pendingResult.Store(op, &resultOutcome{orphan: true, requestID: reqID, peer: op.Msg.From})
		return nil
	}
	if terr != nil {
		return fmt.Errorf("worksession: read out request: %w", terr)
	}
	if requestType == request.TypeDebate {
		// Docs/protocol/debate.md §What a debate session does not do: a
		// ws.result for a debate is ignored, its inbox copy stored blank.
		op.Withhold = true
		pendingResult.Store(op, &resultOutcome{ignored: "kind", sessionID: sid, requestID: reqID, peer: op.Msg.From})
		return nil
	}
	switch requestState {
	case "declined", "cancelled", "completed":
		// Docs/protocol/work-session.md #inbox-copy-d18 (2): an ignored
		// ws.result is applied nowhere, so its inbox copy is withheld too.
		op.Withhold = true
		pendingResult.Store(op, &resultOutcome{ignored: "state", requestID: reqID, peer: op.Msg.From})
		return nil
	}

	// Step 3: find or create the session row.
	row, err := findRowTx(ctx, tx, RoleRequester, op.Msg.From, reqID)
	if errors.Is(err, ErrUnknownSession) {
		now := s.now()
		if _, err := tx.ExecContext(ctx, `
INSERT INTO work_sessions (id, role, peer, request_id, team_id, state, seq, round, opened, state_at, updated)
VALUES (?, ?, ?, ?, ?, ?, 0, 1, ?, ?, ?)`,
			sid, RoleRequester, op.Msg.From, reqID, teamID, StateOpen, wireTime(now), wireTime(now), storeTime(now)); err != nil {
			return fmt.Errorf("worksession: create session row: %w", err)
		}
		row, err = findRowTx(ctx, tx, RoleRequester, op.Msg.From, reqID)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// Step 4: state must be open and round must equal the row's round.
	if row.state != StateOpen {
		op.Withhold = true // #inbox-copy-d18 (2)
		pendingResult.Store(op, &resultOutcome{ignored: "state", sessionID: row.id, requestID: reqID, peer: op.Msg.From})
		return nil
	}
	if round != row.round {
		op.Withhold = true // #inbox-copy-d18 (2)
		pendingResult.Store(op, &resultOutcome{ignored: "round", sessionID: row.id, requestID: reqID, peer: op.Msg.From})
		return nil
	}

	// Step 5: apply the transition.
	quarantined := false
	if s.Quarantine != nil {
		q, qerr := s.Quarantine(ctx, tx, row.id, op.Msg.From, round)
		if qerr != nil {
			return fmt.Errorf("worksession: quarantine check: %w", qerr)
		}
		quarantined = q
	}
	newState := StateAwaitingResult
	if quarantined {
		newState = StateQuarantined
		// Docs/protocol/work-session.md #inbox-copy-d18 (1): the result's
		// content lives in work_sessions.result (visible again after
		// release); the redundant mail_inbox copy is withheld immediately.
		op.Withhold = true
	}
	now := s.now()
	seq := row.seq + 1
	if _, err := s.sendState(ctx, tx, row, seq, newState, "", "", "", now); err != nil {
		return err
	}
	resultCanon, err := CanonicalResult(result)
	if err != nil {
		return badBody("%s", err.Error())
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE work_sessions SET state = ?, seq = ?, result = ?, result_round = ?, verification = ?, state_at = ?, updated = ?
WHERE id = ?`,
		newState, seq, string(resultCanon), round, result.Verification, wireTime(now), storeTime(now), row.id); err != nil {
		return fmt.Errorf("worksession: store result: %w", err)
	}

	pendingResult.Store(op, &resultOutcome{
		sessionID: row.id, peer: op.Msg.From, requestID: reqID, round: round, seq: seq, quarantined: quarantined,
		resultBytes: ResultBytes(resultCanon), outputBytes: OutputBytes(result.Output), artifacts: len(result.Artifacts),
	})
	return nil
}

// requestOutState reads the state and team_id of the `out` request row
// (direction = 'out', peer, id) directly: internal/request owns that table
// but exposes no tx-scoped lookup, and this runs inside the caller's mail
// dedupe transaction (Docs/protocol/work-session.md "Find the out request row").
func requestOutState(ctx context.Context, tx *sql.Tx, peer, id string) (teamID, state, typ string, err error) {
	err = tx.QueryRowContext(ctx, `SELECT team_id, state, type FROM requests WHERE direction = 'out' AND peer = ? AND id = ?`, peer, id).
		Scan(&teamID, &state, &typ)
	return teamID, state, typ, err
}

// afterResult audits the outcome and re-sends last_state for an ignored
// wrong-state/round result (10-minute rule), after commit
// (Docs/protocol/work-session.md §Kinds, "ws.result receive steps on A").
func (s *Store) afterResult(ctx context.Context, op *mail.Opened) {
	v, ok := pendingResult.LoadAndDelete(op)
	if !ok {
		return
	}
	out := v.(*resultOutcome)
	if s.Outbox != nil {
		s.Outbox.Wake()
	}
	if out.ignored != "" && out.ignored != "kind" {
		s.resendLastState(ctx, out.sessionID, s.now())
	}
	if out.quarantined && out.ignored == "" && !out.orphan && s.OnQuarantined != nil {
		s.OnQuarantined(ctx, out.sessionID, out.peer, out.requestID)
	}
	if !out.quarantined && out.ignored == "" && !out.orphan && s.OnResult != nil {
		s.OnResult(ctx, out.sessionID, out.peer, out.requestID)
	}
	if s.Audit == nil {
		return
	}
	switch {
	case out.orphan:
		_ = s.Audit.Append(ctx, "daemon", "ws.orphan", map[string]any{"peer": out.peer, "kind": KindResult})
	case out.ignored != "":
		_ = s.Audit.Append(ctx, "daemon", "ws.ignored", map[string]any{
			"session": out.sessionID, "peer": out.peer, "kind": KindResult, "reason": out.ignored,
		})
	default:
		_ = s.Audit.Append(ctx, "daemon", "ws.result_in", map[string]any{
			"session": out.sessionID, "peer": out.peer, "round": out.round,
			"result_bytes": out.resultBytes, "output_bytes": out.outputBytes,
			"artifacts": out.artifacts, "quarantined": out.quarantined,
		})
	}
}

// resendLastState re-sends sid's stored last_state as a new mail, if it was
// last sent over 10 minutes ago (Docs/protocol/work-session.md, the
// 10-minute rule shared with request.md's receiving step 2). Errors are
// swallowed: this is a best-effort echo run after the mail transaction has
// already committed.
func (s *Store) resendLastState(ctx context.Context, sid string, now time.Time) {
	if sid == "" {
		return
	}
	row, err := findByID(ctx, s.DB, sid)
	if err != nil {
		return
	}
	if !row.lastState.Valid || row.lastState.String == "" {
		return
	}
	if row.lastStateSent.Valid {
		if now.Sub(parseStoreTime(row.lastStateSent.String)) < 10*time.Minute {
			return
		}
	}
	var payload struct {
		Kind string         `json:"kind"`
		Body map[string]any `json:"body"`
	}
	if err := json.Unmarshal([]byte(row.lastState.String), &payload); err != nil {
		return
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := s.Outbox.SubmitTx(ctx, tx, row.peer, payload.Kind, payload.Body); err != nil {
		return
	}
	if _, err := tx.ExecContext(ctx, `UPDATE work_sessions SET last_state_sent = ? WHERE id = ?`, storeTime(now), sid); err != nil {
		return
	}
	if err := tx.Commit(); err != nil {
		return
	}
	s.Outbox.Wake()
}
