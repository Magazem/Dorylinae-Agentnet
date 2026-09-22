package request

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// Cancel (sender IPC request_cancel, Docs/protocol/request.md §Cancel
// (OD-P1-11)), its recipient-side Apply (request.cancel, including the
// tombstone case) and request_resend.

// maxTombstones is the per-sender cap on request_cancels rows: beyond it a
// cancel is acked and ignored (Docs/protocol/request.md §Cancel (OD-P1-11)).
const maxTombstones = 1000

// tombstoneMaxAge is how long a request_cancels row is kept.
const tombstoneMaxAge = 31 * 24 * time.Hour

// resendMaxAge is how old (by request.created) an out row may be and still
// be resendable (Docs/protocol/request.md §Idempotency).
const resendMaxAge = 21 * 24 * time.Hour

// resendThrottle is the minimum gap between echoes of the same last_reply
// (Docs/protocol/request.md §Receiving step 2, §Cancel (OD-P1-11)).
const resendThrottle = 10 * time.Minute

// CancelOutcome is the result of Cancel, enough to build request_cancel's
// IPC result (Docs/protocol/ipc.md §Requests).
type CancelOutcome struct {
	View      View
	MailID    string // "" when nothing was sent
	Duplicate bool
}

// Cancel runs request_cancel (Docs/protocol/request.md §Cancel (OD-P1-11)
// "Sender").
func (s *Store) Cancel(ctx context.Context, id, reason string) (CancelOutcome, error) {
	if reason != "" {
		if err := checkCodePoints("reason", reason, 1, 500, "\n"); err != nil {
			return CancelOutcome{}, err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return CancelOutcome{}, fmt.Errorf("request: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row, err := s.findOutRow(ctx, tx, id)
	if err != nil {
		return CancelOutcome{}, err
	}
	if row.state == StateCancelled {
		v, err := toView(row)
		if err != nil {
			return CancelOutcome{}, err
		}
		return CancelOutcome{View: v, Duplicate: true}, nil
	}
	if row.state == StateAccepted || row.state == StateDeclined || row.state == StateCompleted {
		return CancelOutcome{}, &BadStateError{State: row.state, Msg: fmt.Sprintf("%s is %s", id, row.state)}
	}
	if row.cancel.Valid && row.cancel.String == "requested" && row.cancelMailID.Valid {
		var st string
		err := tx.QueryRowContext(ctx, `SELECT state FROM outbox WHERE id = ?`, row.cancelMailID.String).Scan(&st)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return CancelOutcome{}, fmt.Errorf("request: read cancel mail state: %w", err)
		}
		if st == "queued" || st == "relayed" || st == "delivered" {
			v, err := toView(row)
			if err != nil {
				return CancelOutcome{}, err
			}
			return CancelOutcome{View: v, Duplicate: true}, nil
		}
	}

	now := s.now()
	body := map[string]any{"at": wireTime(now), "request": id}
	if reason != "" {
		body["reason"] = reason
	}
	sub, err := s.Outbox.SubmitTx(ctx, tx, row.peer, KindCancel, body)
	if err != nil {
		return CancelOutcome{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE requests SET cancel = 'requested', cancel_at = ?, cancel_mail_id = ?, updated = ? WHERE direction = 'out' AND peer = ? AND id = ?`,
		storeTime(now), sub.ID, storeTime(now), row.peer, row.id); err != nil {
		return CancelOutcome{}, fmt.Errorf("request: set cancel requested: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CancelOutcome{}, fmt.Errorf("request: commit: %w", err)
	}
	s.Outbox.Wake()
	if s.Audit != nil {
		_ = s.Audit.Append(ctx, "cli", "request.cancel", map[string]any{"request": id, "peer": row.peer, "mail": sub.ID})
	}
	newRow, err := getRow(ctx, s.DB, "out", row.peer, row.id)
	if err != nil {
		return CancelOutcome{}, err
	}
	v, err := toView(newRow)
	if err != nil {
		return CancelOutcome{}, err
	}
	return CancelOutcome{View: v, MailID: sub.ID}, nil
}

// ResendOutcome is the result of Resend.
type ResendOutcome struct {
	MailID string
}

// Resend runs request_resend (Docs/protocol/request.md §Idempotency).
func (s *Store) Resend(ctx context.Context, id string) (ResendOutcome, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ResendOutcome{}, fmt.Errorf("request: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row, err := s.findOutRow(ctx, tx, id)
	if err != nil {
		return ResendOutcome{}, err
	}
	now := s.now()
	bad := func() error { return &BadStateError{State: row.state, Msg: fmt.Sprintf("%s cannot be resent", id)} }
	if row.state != StatePending {
		return ResendOutcome{}, bad()
	}
	if row.cancel.Valid && row.cancel.String != "" {
		return ResendOutcome{}, bad()
	}
	if now.Sub(parseWireTime(row.created)) >= resendMaxAge {
		return ResendOutcome{}, bad()
	}
	var mailState string
	err = tx.QueryRowContext(ctx, `SELECT state FROM outbox WHERE id = ?`, row.mailID).Scan(&mailState)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ResendOutcome{}, fmt.Errorf("request: read mail state: %w", err)
	}
	if mailState != "expired" && mailState != "failed" {
		return ResendOutcome{}, bad()
	}

	obj, err := parseStoredBodyObj(row.body)
	if err != nil {
		return ResendOutcome{}, err
	}
	sub, err := s.Outbox.SubmitTx(ctx, tx, row.peer, "request", map[string]any{"request": obj})
	if err != nil {
		return ResendOutcome{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE requests SET mail_id = ?, updated = ? WHERE direction = 'out' AND peer = ? AND id = ?`,
		sub.ID, storeTime(now), row.peer, row.id); err != nil {
		return ResendOutcome{}, fmt.Errorf("request: update mail_id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ResendOutcome{}, fmt.Errorf("request: commit: %w", err)
	}
	s.Outbox.Wake()
	if s.Audit != nil {
		_ = s.Audit.Append(ctx, "cli", "request.resend", map[string]any{"request": id, "peer": row.peer, "mail": sub.ID})
	}
	return ResendOutcome{MailID: sub.ID}, nil
}

// parseStoredBodyObj parses the stored canonical(request) text back into its
// generic form (json.Number leaves), for request_resend: it must resubmit
// the stored body unchanged (Docs/protocol/request.md §Idempotency).
func parseStoredBodyObj(body string) (map[string]any, error) {
	v, err := agentcard.ParseStrict([]byte(body))
	if err != nil {
		return nil, fmt.Errorf("request: stored body: %w", err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("request: stored body is not an object")
	}
	return obj, nil
}

// cancelOutcome is stashed between applyCancel and afterCancel.
type cancelOutcome struct {
	result               string // cancelled, refused, duplicate, early, tombstone_limit
	requestID, peer      string
	hadRow               bool
	state                string
	teamID, typ, urgency string
	seq                  int
}

var pendingCancel sync.Map // map[*mail.Opened]*cancelOutcome

// CancelKind is the receiver Kind for "request.cancel", applied on the
// original recipient of the request (Docs/protocol/request.md §Cancel
// (OD-P1-11) "Recipient").
func (s *Store) CancelKind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.applyCancel, After: s.afterCancel}
}

func (s *Store) applyCancel(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
	body := op.Msg.Body
	if err := strictMembers(body, "at", "reason", "request"); err != nil {
		return err
	}
	at, err := decodeTime("at", body["at"])
	if err != nil {
		return badBody("%s", err.Error())
	}
	if at.After(op.Msg.Created) {
		return badBody("at is after the mail's created time")
	}
	reqID, err := decodeString(body, "request", false)
	if err != nil {
		return badBody("%s", err.Error())
	}
	if !ValidID(reqID) {
		return badBody("request must be a valid request id")
	}
	reason, err := decodeNonEmpty(body, "reason")
	if err != nil {
		return err
	}
	if reason != "" {
		if err := checkCodePoints("reason", reason, 1, 500, "\n"); err != nil {
			return badBody("%s", err.Error())
		}
	}

	now := s.now()
	row, err := getRow(ctx, tx, "in", op.Msg.From, reqID)
	if errors.Is(err, ErrUnknownRequest) {
		return s.applyEarlyCancel(ctx, tx, op, reqID, reason, now)
	}
	if err != nil {
		return fmt.Errorf("request: read in row: %w", err)
	}

	out := &cancelOutcome{requestID: reqID, peer: op.Msg.From, hadRow: true, state: row.state, teamID: row.teamID, typ: row.typ, urgency: row.urgency}
	if row.state != StatePending && row.state != StateDeferred {
		out.result = "refused"
		if row.state == StateCancelled {
			out.result = "duplicate"
		}
		pendingCancel.Store(op, out)
		return nil
	}
	seq := row.stateSeq + 1
	var reasonArg any
	if reason != "" {
		reasonArg = reason
	}
	replyBody := map[string]any{"at": wireTime(now), "request": reqID, "seq": seq}
	lastReply, err := jsonObject(map[string]any{"kind": KindCancelled, "body": replyBody})
	if err != nil {
		return fmt.Errorf("request: encode last_reply: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE requests SET state = ?, state_seq = ?, state_at = ?, reason = ?, last_reply = ?, last_reply_sent = ?, updated = ?
WHERE direction = 'in' AND peer = ? AND id = ?`,
		StateCancelled, seq, wireTime(now), reasonArg, lastReply, storeTime(now), storeTime(now), row.peer, row.id); err != nil {
		return fmt.Errorf("request: cancel in row: %w", err)
	}
	if _, err := s.Outbox.SubmitTx(ctx, tx, row.peer, KindCancelled, replyBody); err != nil {
		return err
	}
	out.result = "cancelled"
	out.seq = seq
	pendingCancel.Store(op, out)
	return nil
}

// applyEarlyCancel handles a cancel that arrived before its request
// (Docs/protocol/request.md §Cancel (OD-P1-11), the "no row" bullets).
func (s *Store) applyEarlyCancel(ctx context.Context, tx *sql.Tx, op *mail.Opened, reqID, reason string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM request_cancels WHERE received_at < ?`, storeTime(now.Add(-tombstoneMaxAge))); err != nil {
		return fmt.Errorf("request: prune tombstones: %w", err)
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_cancels WHERE peer = ?`, op.Msg.From).Scan(&count); err != nil {
		return fmt.Errorf("request: count tombstones: %w", err)
	}
	out := &cancelOutcome{requestID: reqID, peer: op.Msg.From}
	if count >= maxTombstones {
		out.result = "tombstone_limit"
		pendingCancel.Store(op, out)
		return nil
	}
	var reasonArg any
	if reason != "" {
		reasonArg = reason
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO request_cancels (peer, id, reason, received_at) VALUES (?, ?, ?, ?)`,
		op.Msg.From, reqID, reasonArg, storeTime(now)); err != nil {
		return fmt.Errorf("request: insert tombstone: %w", err)
	}
	replyBody := map[string]any{"at": wireTime(now), "request": reqID, "seq": 1}
	if _, err := s.Outbox.SubmitTx(ctx, tx, op.Msg.From, KindCancelled, replyBody); err != nil {
		return err
	}
	out.result = "early"
	out.seq = 1
	pendingCancel.Store(op, out)
	return nil
}

// afterCancel audits the outcome, and re-echoes the last reply for a refused
// or duplicate cancel (Docs/protocol/request.md §Cancel (OD-P1-11), final
// paragraph "Ordering, resend and inbox").
func (s *Store) afterCancel(ctx context.Context, op *mail.Opened) {
	v, ok := pendingCancel.LoadAndDelete(op)
	if !ok {
		return
	}
	out := v.(*cancelOutcome)
	if s.Outbox != nil {
		s.Outbox.Wake()
	}
	if out.result == "refused" || out.result == "duplicate" {
		s.resubmitStale(ctx, "in", out.peer, out.requestID, s.now())
	}
	if s.Audit == nil {
		return
	}
	detail := map[string]any{"request": out.requestID, "peer": out.peer, "result": out.result}
	if out.hadRow {
		detail["state"] = out.state
	}
	if out.result == "cancelled" {
		detail["team"] = out.teamID
		detail["type"] = out.typ
		detail["urgency"] = out.urgency
		detail["seq"] = out.seq
	}
	_ = s.Audit.Append(ctx, "daemon", "request.cancel_in", detail)
}

// resubmitStale re-sends direction/peer/id's stored last_reply as a new
// mail, if its state is not pending and it was last echoed over
// resendThrottle ago (Docs/protocol/request.md §Receiving step 2, §Cancel
// (OD-P1-11)). Errors are swallowed: this is a best-effort echo, run after
// the mail dedupe transaction has already committed.
func (s *Store) resubmitStale(ctx context.Context, direction, peer, id string, now time.Time) {
	row, err := getRow(ctx, s.DB, direction, peer, id)
	if err != nil {
		return
	}
	if row.state == StatePending {
		return
	}
	if !row.lastReply.Valid || row.lastReply.String == "" {
		return
	}
	if row.lastReplySent.Valid {
		if now.Sub(parseStoreTime(row.lastReplySent.String)) < resendThrottle {
			return
		}
	}
	var payload struct {
		Kind string          `json:"kind"`
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal([]byte(row.lastReply.String), &payload); err != nil {
		return
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := s.Outbox.SubmitTx(ctx, tx, peer, payload.Kind, payload.Body); err != nil {
		return
	}
	if _, err := tx.ExecContext(ctx, `UPDATE requests SET last_reply_sent = ? WHERE direction = ? AND peer = ? AND id = ?`,
		storeTime(now), direction, peer, id); err != nil {
		return
	}
	if err := tx.Commit(); err != nil {
		return
	}
	s.Outbox.Wake()
}
