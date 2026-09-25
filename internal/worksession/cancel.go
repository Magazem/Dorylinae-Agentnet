package worksession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// SubmitCancel runs ws_cancel for B (Docs/protocol/work-session.md §Cancel,
// "B"): sends ws.cancel to A and marks the row cancel = requested. B's row
// does not change state until A's ws.state arrives (applyState clears the
// column on any new state, review 27 L6); this call never changes state
// itself. duplicate is true, and no new mail is sent, when the row already
// had cancel = requested.
func (s *Store) SubmitCancel(ctx context.Context, id, reason string) (view View, mailID string, duplicate bool, err error) {
	if reason != "" {
		if err := checkCodePoints("reason", reason, 1, 500, ""); err != nil {
			return View{}, "", false, err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return View{}, "", false, fmt.Errorf("worksession: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	r, err := scanByID(ctx, tx, id)
	if err != nil {
		return View{}, "", false, err
	}
	if r.role != RoleWorker {
		return View{}, "", false, ErrNotWorker
	}
	if r.state == StateClosed {
		return View{}, "", false, &BadStateError{State: r.state, Msg: fmt.Sprintf("%s is %s", id, r.state)}
	}
	now := s.now()
	duplicate = r.cancel.Valid && r.cancel.String == "requested"
	if !duplicate {
		body := map[string]any{"at": wireTime(now), "request": r.requestID, "session": id}
		if reason != "" {
			body["reason"] = reason
		}
		sub, err := s.Outbox.SubmitTx(ctx, tx, r.peer, KindCancel, body)
		if err != nil {
			return View{}, "", false, err
		}
		mailID = sub.ID
		if _, err := tx.ExecContext(ctx, `UPDATE work_sessions SET cancel = 'requested', updated = ? WHERE id = ?`, storeTime(now), id); err != nil {
			return View{}, "", false, fmt.Errorf("worksession: mark cancel requested: %w", err)
		}
	}
	var afterDebate func(context.Context)
	if r.kind == SessionKindDebate {
		// §Cancel and abandon, "B abandon": B has no ws.state to learn A's
		// answer from, so its own --cancel also closes its mirror at once.
		if s.Debate == nil {
			return View{}, "", false, fmt.Errorf("worksession: debate sessions are not wired")
		}
		fn, err := s.Debate.AbandonTx(ctx, tx, id, now)
		if err != nil {
			return View{}, "", false, err
		}
		afterDebate = fn
	}
	if err := tx.Commit(); err != nil {
		return View{}, "", false, fmt.Errorf("worksession: commit: %w", err)
	}
	s.Outbox.Wake()
	if s.Audit != nil && !duplicate {
		_ = s.Audit.Append(ctx, "cli", "ws.cancel", map[string]any{"session": id, "peer": r.peer, "role": RoleWorker})
	}
	if afterDebate != nil {
		afterDebate(ctx)
	}
	newRow, err := findByID(ctx, s.DB, id)
	if err != nil {
		return View{}, "", false, err
	}
	v, err := toView(newRow)
	if err != nil {
		return View{}, "", false, err
	}
	return v, mailID, duplicate, nil
}

// ws.cancel: B -> A (Docs/protocol/work-session.md §Cancel, "B"). Applied
// automatically when the session is open; otherwise refused and A's
// last_state is re-sent so B learns the real state.

type cancelOutcome struct {
	orphan    bool
	result    string // "cancelled" or "refused"
	sessionID string
	peer      string
	requestID string
	closed    *storedRow // the row before a close, for ws.close
	// debate is set for a debate session: its close (and ws.close) is
	// audited by debateAfter, and a refusal is not echoed with ws.state.
	debate       bool
	debateAfter  func(context.Context)
	expBytes     int
	expTruncated bool
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
	if err != nil && !errors.Is(err, ErrUnknownSession) {
		return err
	}
	// Docs/protocol/work-session.md §Quarantine (2.4): "the reason of a
	// ws.cancel from B is not stored or shown on A" while the quarantine
	// rule holds for the session; #inbox-copy-d18 (4) extends that to the
	// mail_inbox copy, which otherwise still carries the reason in plaintext.
	// Evaluated before the orphan and refused branches too (review 35 H2): a
	// cancel refused in quarantined or awaiting_result, or one for a session
	// A has no row for (the rule's peer-wide clause), is applied nowhere, and
	// its reason must not survive in the inbox copy either.
	if s.Quarantine != nil {
		round := row.round
		if errors.Is(err, ErrUnknownSession) {
			round = 0
		}
		q, qerr := s.Quarantine(ctx, tx, sid, op.Msg.From, round)
		if qerr != nil {
			return fmt.Errorf("worksession: quarantine check: %w", qerr)
		}
		if q {
			op.Withhold = true
		}
	}
	if errors.Is(err, ErrUnknownSession) {
		pendingCancel.Store(op, &cancelOutcome{orphan: true, requestID: reqID, peer: op.Msg.From})
		return nil
	}
	if row.state != StateOpen {
		pendingCancel.Store(op, &cancelOutcome{result: "refused", sessionID: row.id, requestID: reqID, peer: op.Msg.From, debate: row.kind == SessionKindDebate})
		return nil
	}
	if row.kind == SessionKindDebate {
		if s.Debate == nil {
			return fmt.Errorf("worksession: debate sessions are not wired")
		}
		applied, after, err := s.Debate.PeerCancelTx(ctx, tx, row.id, s.now())
		if err != nil {
			return err
		}
		result := "refused"
		if applied {
			result = "cancelled"
		}
		pendingCancel.Store(op, &cancelOutcome{result: result, sessionID: row.id, requestID: reqID, peer: op.Msg.From, debate: true, debateAfter: after})
		return nil
	}
	expBytes, expTruncated, err := s.closeSessionTx(ctx, tx, row, OutcomeCancelled, "", "", RoleWorker, s.now())
	if err != nil {
		return err
	}
	pendingCancel.Store(op, &cancelOutcome{result: "cancelled", sessionID: row.id, requestID: reqID, peer: op.Msg.From, closed: &row, expBytes: expBytes, expTruncated: expTruncated})
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
	if out.result == "refused" && !out.debate {
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
	if out.closed != nil {
		s.auditClose(ctx, *out.closed, OutcomeCancelled, s.now())
		s.auditExperience(ctx, out.closed.id, out.closed.role, out.expBytes, out.expTruncated)
	}
	if out.debateAfter != nil {
		out.debateAfter(ctx)
	}
}
