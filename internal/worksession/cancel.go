package worksession

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// ws.cancel: B -> A (Docs/protocol/work-session.md §Cancel, "B"). Applied
// automatically when the session is open; otherwise refused and A's
// last_state is re-sent so B learns the real state.

type cancelOutcome struct {
	orphan    bool
	result    string // "cancelled" or "refused"
	sessionID string
	peer      string
	requestID string
}

var pendingCancel sync.Map // map[*mail.Opened]*cancelOutcome

// CancelKind is the receiver Kind for "ws.cancel", applied on A.
func (s *Store) CancelKind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.applyCancel, After: s.afterCancel}
}

func (s *Store) applyCancel(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
	body := op.Msg.Body
	if err := strictMembers(body, "at", "reason", "request", "session"); err != nil {
		return err
	}
	if _, err := decodeTime("at", body["at"]); err != nil {
		return err
	}
	reqID, err := decodeString(body, "request", false)
	if err != nil {
		return err
	}
	sid, err := decodeString(body, "session", false)
	if err != nil {
		return err
	}
	if sid != DeriveID(op.Msg.To, op.Msg.From, reqID) {
		return badBody("session is not the derived id for (to, from, request)")
	}
	reason, err := decodeOptionalNonEmpty(body, "reason")
	if err != nil {
		return err
	}
	if reason != "" {
		if err := checkCodePoints("reason", reason, 1, 500, ""); err != nil {
			return badBody("%s", err.Error())
		}
	}

	row, err := findRowTx(ctx, tx, RoleRequester, op.Msg.From, reqID)
	if errors.Is(err, ErrUnknownSession) {
		pendingCancel.Store(op, &cancelOutcome{orphan: true, requestID: reqID, peer: op.Msg.From})
		return nil
	}
	if err != nil {
		return err
	}
	if row.state != StateOpen {
		pendingCancel.Store(op, &cancelOutcome{result: "refused", sessionID: row.id, requestID: reqID, peer: op.Msg.From})
		return nil
	}
	if err := s.closeSessionTx(ctx, tx, row, OutcomeCancelled, "", s.now()); err != nil {
		return err
	}
	pendingCancel.Store(op, &cancelOutcome{result: "cancelled", sessionID: row.id, requestID: reqID, peer: op.Msg.From})
	return nil
}

// afterCancel audits the outcome, and re-echoes last_state for a refused
// cancel (10-minute rule), after commit.
func (s *Store) afterCancel(ctx context.Context, op *mail.Opened) {
	v, ok := pendingCancel.LoadAndDelete(op)
	if !ok {
		return
	}
	out := v.(*cancelOutcome)
	if s.Outbox != nil {
		s.Outbox.Wake()
	}
	if out.result == "refused" {
		s.resendLastState(ctx, out.sessionID, s.now())
	}
	if s.Audit == nil {
		return
	}
	if out.orphan {
		_ = s.Audit.Append(ctx, "daemon", "ws.orphan", map[string]any{"peer": out.peer, "kind": KindCancel})
		return
	}
	_ = s.Audit.Append(ctx, "daemon", "ws.cancel_in", map[string]any{"session": out.sessionID, "peer": out.peer, "result": out.result})
}
