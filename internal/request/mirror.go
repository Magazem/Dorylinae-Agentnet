package request

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// Sender mirror (Docs/protocol/request.md §Sender mirror): applying the
// lifecycle kinds request.accept, request.decline, request.defer,
// request.complete and request.cancelled, sent by the recipient's daemon and
// received here, at the original sender.

// mirrorOutcome is stashed between Apply and After, keyed by the *mail.Opened
// pointer (mirroring pendingApply in receive.go).
type mirrorOutcome struct {
	applied       bool // step 4 ran (seq advanced the row)
	orphan        bool // no out row for (peer, request)
	cancelRefused bool // step 5 ran
	kind          string
	requestID     string
	peer          string
	teamID        string
	typ           string
	urgency       string
	title         string
	state         string
	seq           int
	resultBytes   int
	outputBytes   int
	artifacts     int
	hasResult     bool
	resultStatus  string
	until         time.Time
}

var pendingMirror sync.Map // map[*mail.Opened]*mirrorOutcome

// mirrorEvent maps a lifecycle mail kind to its notification event
// (Docs/protocol/notify.md §Triggers). KindCancelled is deliberately absent:
// this mirror confirms the recipient's cancel acknowledgement at the
// sender, not the request.cancelled trigger, which fires at the recipient
// (see afterCancel in cancel.go).
var mirrorEvent = map[string]string{
	KindAccept:   EventAccepted,
	KindDecline:  EventDeclined,
	KindDefer:    EventDeferred,
	KindComplete: EventCompleted,
}

// AcceptKind is the receiver Kind for request.accept, one of the five
// lifecycle kinds sent recipient -> sender (Docs/protocol/request.md
// §Lifecycle, §Sender mirror).
func (s *Store) AcceptKind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.applyAccept, After: s.afterMirror}
}

// DeclineKind is the receiver Kind for request.decline.
func (s *Store) DeclineKind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.applyDecline, After: s.afterMirror}
}

// DeferKind is the receiver Kind for request.defer.
func (s *Store) DeferKind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.applyDefer, After: s.afterMirror}
}

// CompleteKind is the receiver Kind for request.complete.
func (s *Store) CompleteKind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.applyComplete, After: s.afterMirror}
}

// CancelledKind is the receiver Kind for request.cancelled.
func (s *Store) CancelledKind() mail.Kind {
	return mail.Kind{Inbox: true, Apply: s.applyCancelled, After: s.afterMirror}
}

func (s *Store) applyAccept(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
	body := op.Msg.Body
	if err := strictMembers(body, "at", "request", "seq"); err != nil {
		return err
	}
	at, reqID, seq, err := decodeAtRequestSeq(body)
	if err != nil {
		return err
	}
	if err := s.applyMirror(ctx, tx, op, KindAccept, reqID, StateAccepted, seq, at, "", nil, nil, time.Time{}); err != nil {
		return err
	}
	if s.Sessions != nil {
		// A creates its own session row when it applies the accept, or when a
		// ws.result for this request overtook it (Docs/protocol/work-session.md
		// §Session id); OpenSession is idempotent either way. teamID comes
		// from A's own out row; its absence (an orphaned accept) means there
		// is nothing to open a session for.
		var teamID string
		err := tx.QueryRowContext(ctx, `SELECT team_id FROM requests WHERE direction = 'out' AND peer = ? AND id = ?`, op.Msg.From, reqID).Scan(&teamID)
		if err == nil {
			if err := s.Sessions.OpenSession(ctx, tx, "requester", op.Msg.From, reqID, teamID, s.now()); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("request: read out row for session open: %w", err)
		}
	}
	return nil
}

func (s *Store) applyDecline(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
	body := op.Msg.Body
	if err := strictMembers(body, "at", "code", "reason", "request", "seq"); err != nil {
		return err
	}
	at, reqID, seq, err := decodeAtRequestSeq(body)
	if err != nil {
		return err
	}
	code, err := decodeString(body, "code", false)
	if err != nil {
		return badBody("%s", err.Error())
	}
	switch code {
	case "user", "not_team_member", "unknown_team", "unverified_peer":
	default:
		return badBody("code must be user, not_team_member, unknown_team or unverified_peer")
	}
	reason, err := decodeNonEmpty(body, "reason")
	if err != nil {
		return err
	}
	if code == "user" {
		if reason == "" {
			return badBody("reason is required when code is user")
		}
		if err := checkCodePoints("reason", reason, 1, 500, ""); err != nil {
			return badBody("%s", err.Error())
		}
	} else if reason != "" {
		return badBody("reason must be absent unless code is user")
	}
	var reasonArg any
	if reason != "" {
		reasonArg = reason
	}
	return s.applyMirror(ctx, tx, op, KindDecline, reqID, StateDeclined, seq, at,
		`decline_code = ?, reason = ?`, []any{code, reasonArg}, nil, time.Time{})
}

func (s *Store) applyDefer(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
	body := op.Msg.Body
	if err := strictMembers(body, "at", "request", "seq", "until"); err != nil {
		return err
	}
	at, reqID, seq, err := decodeAtRequestSeq(body)
	if err != nil {
		return err
	}
	until, err := decodeTime("until", body["until"])
	if err != nil {
		return badBody("%s", err.Error())
	}
	if !until.After(at) {
		return badBody("until must be after at")
	}
	if until.After(at.Add(maxDeferAhead)) {
		return badBody("until must be at most 90 days after at")
	}
	return s.applyMirror(ctx, tx, op, KindDefer, reqID, StateDeferred, seq, at,
		`deferred_until = ?`, []any{wireTime(until)}, nil, until)
}

func (s *Store) applyComplete(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
	body := op.Msg.Body
	if err := strictMembers(body, "at", "note", "request", "result", "seq"); err != nil {
		return err
	}
	at, reqID, seq, err := decodeAtRequestSeq(body)
	if err != nil {
		return err
	}
	note, err := decodeNonEmpty(body, "note")
	if err != nil {
		return err
	}
	var result *Result
	if raw, ok := body["result"]; ok {
		obj, ok := raw.(map[string]any)
		if !ok {
			return badBody("result must be an object")
		}
		result, err = DecodeResult(obj)
		if err != nil {
			return badBody("%s", err.Error())
		}
	}
	if err := ValidateComplete(note, result); err != nil {
		return badBody("%s", err.Error())
	}
	canon, err := agentcard.CanonicalValue(body)
	if err != nil {
		return badBody("%s", err.Error())
	}
	if err := CheckCompleteSize(canon); err != nil {
		return badBody("%s", err.Error())
	}
	if s.Sessions != nil {
		// Docs/protocol/work-session.md §Early complete and Phase 1 workers:
		// this request.complete may be arriving while A's session for it is
		// still open (or, rarely, mid-round). keepContent is false only when
		// the quarantine rule holds; dropping note/result here (before they
		// are stored) means they are never stored or visible.
		keep, err := s.Sessions.EarlyComplete(ctx, tx, op.Msg.From, reqID, result != nil || note != "")
		if err != nil {
			return fmt.Errorf("request: early complete: %w", err)
		}
		if !keep {
			note, result = "", nil
		}
	}
	var noteArg, resultArg any
	var resultBytes, outputBytes, artifacts int
	hasResult := result != nil
	if note != "" {
		noteArg = note
	}
	if result != nil {
		resultCanon, err := CanonicalResult(result)
		if err != nil {
			return badBody("%s", err.Error())
		}
		resultArg = string(resultCanon)
		resultBytes = ResultBytes(resultCanon)
		outputBytes = OutputBytes(result.Output)
		artifacts = len(result.Artifacts)
	}
	ra := &resultAudit{hasResult: hasResult, resultBytes: resultBytes, outputBytes: outputBytes, artifacts: artifacts}
	if result != nil {
		ra.status = result.Status
	}
	return s.applyMirror(ctx, tx, op, KindComplete, reqID, StateCompleted, seq, at,
		`note = ?, result = ?`, []any{noteArg, resultArg}, ra, time.Time{})
}

func (s *Store) applyCancelled(ctx context.Context, tx *sql.Tx, op *mail.Opened) error {
	body := op.Msg.Body
	if err := strictMembers(body, "at", "request", "seq"); err != nil {
		return err
	}
	at, reqID, seq, err := decodeAtRequestSeq(body)
	if err != nil {
		return err
	}
	return s.applyMirror(ctx, tx, op, KindCancelled, reqID, StateCancelled, seq, at, "", nil, nil, time.Time{})
}

type resultAudit struct {
	hasResult                           bool
	resultBytes, outputBytes, artifacts int
	status                              string
}

// applyMirror is Docs/protocol/request.md §Sender mirror steps 2-5, shared by
// the five lifecycle kinds. until is the new deferred_until for KindDefer,
// zero otherwise (Docs/protocol/notify.md §Text and sanitising).
func (s *Store) applyMirror(ctx context.Context, tx *sql.Tx, op *mail.Opened, kind, reqID, newState string, seq int, at time.Time, extraSet string, extraArgs []any, ra *resultAudit, until time.Time) error {
	row, err := getRow(ctx, tx, "out", op.Msg.From, reqID)
	if errors.Is(err, ErrUnknownRequest) {
		pendingMirror.Store(op, &mirrorOutcome{orphan: true, kind: kind, requestID: reqID, peer: op.Msg.From})
		return nil
	}
	if err != nil {
		return fmt.Errorf("request: read out row: %w", err)
	}

	title := ""
	if req, terr := decodeStoredBody(row.body); terr == nil {
		title = req.Title
	}
	out := &mirrorOutcome{kind: kind, requestID: reqID, peer: row.peer, teamID: row.teamID, typ: row.typ, urgency: row.urgency, title: title, until: until}
	finalState := row.state
	if seq > row.stateSeq {
		setSQL := `state = ?, state_seq = ?, state_at = ?`
		args := []any{newState, seq, wireTime(at)}
		if extraSet != "" {
			setSQL += ", " + extraSet
			args = append(args, extraArgs...)
		}
		args = append(args, row.peer, row.id)
		if _, err := tx.ExecContext(ctx, `UPDATE requests SET `+setSQL+` WHERE direction = 'out' AND peer = ? AND id = ?`, args...); err != nil {
			return fmt.Errorf("request: update out row: %w", err)
		}
		out.applied = true
		out.state = newState
		out.seq = seq
		finalState = newState
		if ra != nil {
			out.hasResult, out.resultBytes, out.outputBytes, out.artifacts = ra.hasResult, ra.resultBytes, ra.outputBytes, ra.artifacts
			out.resultStatus = ra.status
		}
	}

	if row.cancel.Valid && row.cancel.String == "requested" &&
		(finalState == StateAccepted || finalState == StateDeclined || finalState == StateCompleted) {
		if _, err := tx.ExecContext(ctx, `UPDATE requests SET cancel = 'refused' WHERE direction = 'out' AND peer = ? AND id = ?`, row.peer, row.id); err != nil {
			return fmt.Errorf("request: refuse cancel: %w", err)
		}
		out.cancelRefused = true
		out.state = finalState
	}

	pendingMirror.Store(op, out)
	return nil
}

// afterMirror audits the outcome once, after a successful commit
// (Docs/protocol/request.md §Sender mirror, §Audit and metrics).
func (s *Store) afterMirror(ctx context.Context, op *mail.Opened) {
	v, ok := pendingMirror.LoadAndDelete(op)
	if !ok {
		return
	}
	out := v.(*mirrorOutcome)
	if s.Outbox != nil {
		s.Outbox.Wake()
	}
	if out.applied && s.Notify != nil {
		if event, ok := mirrorEvent[out.kind]; ok {
			s.Notify(ctx, event, NotifyInfo{
				Peer: out.peer, Type: out.typ, Urgency: out.urgency, Title: out.title,
				Until: out.until, ResultStatus: out.resultStatus, HasResult: out.hasResult,
				RequestID: out.requestID, State: out.state, TeamID: out.teamID,
			})
		}
	}
	if s.Audit == nil {
		return
	}
	if out.orphan {
		_ = s.Audit.Append(ctx, "daemon", "request.orphan", map[string]any{"request": out.requestID, "peer": out.peer, "kind": out.kind})
		return
	}
	if out.applied {
		detail := map[string]any{"request": out.requestID, "peer": out.peer, "state": out.state, "seq": out.seq}
		if out.hasResult {
			detail["result_bytes"] = out.resultBytes
			detail["output_bytes"] = out.outputBytes
			detail["artifacts"] = out.artifacts
		}
		_ = s.Audit.Append(ctx, "daemon", "request.state", detail)
	}
	if out.cancelRefused {
		_ = s.Audit.Append(ctx, "daemon", "request.cancel_refused", map[string]any{"request": out.requestID, "peer": out.peer, "state": out.state})
	}
}

// strictMembers rejects any key of body not in allowed.
func strictMembers(body map[string]any, allowed ...string) error {
	set := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		set[a] = true
	}
	for k := range body {
		if !set[k] {
			return badBody("unknown member %q", k)
		}
	}
	return nil
}

// decodeNonEmpty decodes an optional string member that, when present, must
// not be empty (Docs/protocol/request.md: optional members are absent, never
// empty). A failure is bad_body.
func decodeNonEmpty(body map[string]any, field string) (string, error) {
	s, err := decodeString(body, field, true)
	if err != nil {
		return "", badBody("%s", err.Error())
	}
	if _, ok := body[field]; ok && s == "" {
		return "", badBody("%s must be absent, not empty", field)
	}
	return s, nil
}

// decodeAtRequestSeq decodes the three members common to every lifecycle
// kind. seq must be >= 1.
func decodeAtRequestSeq(body map[string]any) (at time.Time, reqID string, seq int, err error) {
	at, err = decodeTime("at", body["at"])
	if err != nil {
		return time.Time{}, "", 0, badBody("%s", err.Error())
	}
	reqID, err = decodeString(body, "request", false)
	if err != nil {
		return time.Time{}, "", 0, badBody("%s", err.Error())
	}
	if !ValidID(reqID) {
		return time.Time{}, "", 0, badBody("request must be a valid request id")
	}
	seq, err = decodeInt(body, "seq")
	if err != nil {
		return time.Time{}, "", 0, badBody("%s", err.Error())
	}
	if seq < 1 {
		return time.Time{}, "", 0, badBody("seq must be >= 1")
	}
	return at, reqID, seq, nil
}
