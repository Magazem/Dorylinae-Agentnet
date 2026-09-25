package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// IPC error codes for work sessions (Docs/protocol/work-session.md §IPC).
const (
	CodeUnknownSession   = "unknown_session"
	CodeNotRequester     = "not_requester"
	CodeNotWorker        = "not_worker"
	CodeResultTooLargeWS = "result_too_large"
	CodeNotAvailable     = "not_available"
)

// SessionRequestRef is the session view's "request" member.
type SessionRequestRef struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

// SessionResultView is the session view's "result" member
// (Docs/protocol/work-session.md §IPC, "Session view"). List views omit
// Output and Notes.
type SessionResultView struct {
	Status      string            `json:"status"`
	Summary     string            `json:"summary,omitempty"`
	ExitCode    *int64            `json:"exit_code,omitempty"`
	Output      string            `json:"output,omitempty"`
	Artifacts   []RequestArtifact `json:"artifacts,omitempty"`
	Notes       string            `json:"notes,omitempty"`
	OutputBytes int               `json:"output_bytes"`
	ResultBytes int               `json:"result_bytes"`
}

// SessionQuarantineView is the session view's "quarantine" member, present
// instead of "result" while quarantined on the requester
// (Docs/protocol/work-session.md §Quarantine (2.4)).
type SessionQuarantineView struct {
	Status      string `json:"status"`
	ResultBytes int    `json:"result_bytes"`
	OutputBytes int    `json:"output_bytes"`
	Artifacts   int    `json:"artifacts"`
}

// SessionView is the "session view" of Docs/protocol/work-session.md §IPC.
type SessionView struct {
	ID             string                 `json:"id"`
	Role           string                 `json:"role"`
	Peer           RequestPeerRef         `json:"peer"`
	Team           teamRefResult          `json:"team"`
	Request        SessionRequestRef      `json:"request"`
	State          string                 `json:"state"`
	Outcome        string                 `json:"outcome,omitempty"`
	Round          int                    `json:"round"`
	Seq            int                    `json:"seq"`
	Opened         string                 `json:"opened"`
	StateAt        string                 `json:"state_at"`
	Closed         string                 `json:"closed,omitempty"`
	Result         *SessionResultView     `json:"result,omitempty"`
	Quarantine     *SessionQuarantineView `json:"quarantine,omitempty"`
	Verification   string                 `json:"verification,omitempty"`
	VerificationBy string                 `json:"verification_by,omitempty"`
	Changes        string                 `json:"changes,omitempty"`
	Cancel         string                 `json:"cancel,omitempty"`
	Grants         []any                  `json:"grants"`
}

// SessionResult is the result of ws_result, ws_accept_result (without
// --human), ws_request_changes, ws_discard and A's ws_cancel.
type SessionResult struct {
	Session SessionView `json:"session"`
	MailID  string      `json:"mail_id"`
}

// SessionCancelResult is the result of B's ws_cancel.
type SessionCancelResult struct {
	Session   SessionView `json:"session"`
	MailID    string      `json:"mail_id"`
	Duplicate bool        `json:"duplicate"`
}

// SessionListResult is the result of ws_list.
type SessionListResult struct {
	Sessions []SessionView `json:"sessions"`
}

// SessionShowResult is the result of ws_show.
type SessionShowResult struct {
	Session SessionView `json:"session"`
}

// sessionError maps a worksession package error to an *ipc.Error
// (Docs/protocol/work-session.md §Error codes (summary)).
func sessionError(err error) error {
	var fe *worksession.FieldError
	var bse *worksession.BadStateError
	var tl *worksession.TooLargeResultError
	switch {
	case errors.Is(err, worksession.ErrUnknownSession):
		return &ipc.Error{Code: CodeUnknownSession, Message: "no such session"}
	case errors.Is(err, worksession.ErrNotRequester):
		return &ipc.Error{Code: CodeNotRequester, Message: "not the requester"}
	case errors.Is(err, worksession.ErrNotWorker):
		return &ipc.Error{Code: CodeNotWorker, Message: "not the worker"}
	case errors.As(err, &bse):
		return &ipc.Error{Code: CodeBadState, Message: bse.Msg}
	case errors.As(err, &fe):
		return &ipc.Error{Code: ipc.CodeBadRequest, Message: fe.Error()}
	case errors.As(err, &tl):
		return &ipc.Error{Code: CodeResultTooLargeWS, Message: tl.Error()}
	default:
		return err
	}
}

// resolveSessionID accepts an s-... session id directly, or an r-... request
// id resolved through its session (Docs/protocol/work-session.md §IPC,
// "ws_show").
func resolveSessionID(ctx context.Context, ws *worksession.Store, id string) (string, error) {
	switch {
	case worksession.ValidID(id):
		return id, nil
	case request.ValidID(id):
		v, err := ws.GetByRequestID(ctx, id)
		if err != nil {
			return "", err
		}
		return v.ID, nil
	default:
		return "", &ipc.Error{Code: ipc.CodeBadRequest, Message: "id must be a session (s-...) or request (r-...) id"}
	}
}

// sessionView builds the session view for v. listMode omits result.output,
// result.notes and changes, keeping their sizes
// (Docs/protocol/work-session.md §IPC, "Session view").
func sessionView(ctx context.Context, ps *peers.Store, ts *team.Store, rs *request.Store, v worksession.View, listMode bool) SessionView {
	fp, _ := envelope.KeyFingerprint(v.Peer)
	name := v.Peer
	if p, err := resolvePeer(ctx, ps, v.Peer); err == nil {
		name = p.Name
	}
	t, _ := ts.Get(ctx, v.TeamID)
	direction := "in"
	if v.Role == worksession.RoleRequester {
		direction = "out"
	}
	var typ, title string
	if rs != nil {
		typ, title, _ = rs.PeekTypeTitle(ctx, direction, v.Peer, v.RequestID)
	}

	sv := SessionView{
		ID: v.ID, Role: v.Role,
		Peer:    RequestPeerRef{Name: name, PublicKey: v.Peer, Fingerprint: fp},
		Team:    teamRefResult{ID: t.ID, Name: t.Name},
		Request: SessionRequestRef{ID: v.RequestID, Type: typ, Title: title},
		State:   v.State, Outcome: v.Outcome, Round: v.Round, Seq: v.Seq,
		Opened: timeOrEmpty(v.Opened), StateAt: timeOrEmpty(v.StateAt),
		Cancel: v.Cancel,
		Grants: []any{},
	}
	if !v.Closed.IsZero() {
		sv.Closed = timeOrEmpty(v.Closed)
	}
	if !listMode {
		sv.Changes = v.Changes
	}
	if v.Result != nil {
		rv := &SessionResultView{
			Status: v.Result.Status, Summary: v.Result.Summary, ExitCode: v.Result.ExitCode,
			OutputBytes: v.OutputBytes, ResultBytes: v.ResultBytes,
		}
		if len(v.Result.Artifacts) > 0 {
			rv.Artifacts = requestArtifacts(v.Result.Artifacts)
		}
		if !listMode {
			rv.Output = v.Result.Output
			rv.Notes = v.Result.Notes
		}
		sv.Result = rv
	} else if v.State == worksession.StateQuarantined && v.Role == worksession.RoleRequester {
		sv.Quarantine = &SessionQuarantineView{
			Status: v.ResultStatus, ResultBytes: v.ResultBytes, OutputBytes: v.OutputBytes, Artifacts: v.Artifacts,
		}
	}
	if v.Verification != "" {
		sv.Verification = v.Verification
		sv.VerificationBy = "worker"
		if v.Verification == worksession.VerificationHumanAccepted {
			sv.VerificationBy = "requester"
		}
	}
	return sv
}

// afterCommitResult wraps an approval.Action's Perform result with a
// callback to run once, after Confirm's transaction has committed: the audit
// log shares the daemon's single SQLite connection (SetMaxOpenConns(1)), so
// an Append while a transaction is still open would block forever (review
// 27, C1). It implements approval.AfterCommitter, so approval.Store.Confirm
// runs it directly (2.2d: confirmation happens from the window's answer or
// the terminal's stdin, not from a synchronous IPC caller that could unwrap
// it itself).
type afterCommitResult struct {
	after func(context.Context)
}

// AfterCommit implements approval.AfterCommitter.
func (r afterCommitResult) AfterCommit(ctx context.Context) {
	if r.after != nil {
		r.after(ctx)
	}
}

// registerSession wires the work session IPC (Docs/protocol/work-session.md
// §IPC): ws_list, ws_show, ws_result, ws_accept_result, ws_request_changes,
// ws_discard, ws_cancel and ws_release.
func registerSession(srv *ipc.Server, ws *worksession.Store, rs *request.Store, ps *peers.Store, ts *team.Store, as *approval.Store, log *audit.Log) {
	srv.Handle("ws_list", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ State, Role, Peer, Team string }
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
			}
		}
		f := worksession.ListFilter{State: p.State, Role: p.Role}
		if p.Team != "" {
			t, err := resolveTeam(ctx, ts, p.Team)
			if err != nil {
				return nil, err
			}
			f.TeamID = t.ID
		}
		if p.Peer != "" {
			peer, err := resolvePeer(ctx, ps, p.Peer)
			if err != nil {
				return nil, err
			}
			f.Peer = peer.PublicKey
		}
		views, err := ws.List(ctx, f)
		if err != nil {
			return nil, err
		}
		out := make([]SessionView, len(views))
		for i, v := range views {
			out[i] = sessionView(ctx, ps, ts, rs, v, true)
		}
		return SessionListResult{Sessions: out}, nil
	})

	srv.Handle("ws_show", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ ID string }
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		sid, err := resolveSessionID(ctx, ws, p.ID)
		if err != nil {
			return nil, sessionError(err)
		}
		v, err := ws.Get(ctx, sid)
		if err != nil {
			return nil, sessionError(err)
		}
		return SessionShowResult{Session: sessionView(ctx, ps, ts, rs, v, false)}, nil
	})

	srv.Handle("ws_result", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			ID     string
			Result json.RawMessage
			Notes  string
		}
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" || len(p.Result) == 0 {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id and result are required"}
		}
		rv, err := agentcard.ParseStrict(p.Result)
		if err != nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "result: " + err.Error()}
		}
		obj, ok := rv.(map[string]any)
		if !ok {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "result must be an object"}
		}
		// A result on a pending or deferred question is the one-step answer
		// of a consult (Docs/protocol/consult.md §Answering): its status
		// defaults to n/a and its verification to none.
		target, err := findAnswerable(ctx, ws, rs, ts.Self, p.ID)
		if err != nil {
			return nil, lifecycleError(err)
		}
		if target != nil {
			if _, has := obj["status"]; !has {
				obj["status"] = "n/a"
			}
			if _, has := obj["verification"]; !has {
				obj["verification"] = worksession.VerificationNone
			}
		}
		if p.Notes != "" {
			if _, exists := obj["notes"]; exists {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "notes must be given once, not in both result and notes"}
			}
			obj["notes"] = p.Notes
		}
		result, err := worksession.DecodeResult(obj)
		if err != nil {
			return nil, sessionError(err)
		}
		if err := worksession.ValidateResult(result); err != nil {
			return nil, sessionError(err)
		}
		if target != nil {
			sid, mailID, err := ws.AnswerQuestion(ctx, target.requestID, target.from, result)
			if err != nil {
				return nil, answerError(err)
			}
			newV, err := ws.Get(ctx, sid)
			if err != nil {
				return nil, sessionError(err)
			}
			return SessionResult{Session: sessionView(ctx, ps, ts, rs, newV, false), MailID: mailID}, nil
		}
		sid, err := resolveSessionID(ctx, ws, p.ID)
		if err != nil {
			return nil, sessionError(err)
		}
		v, err := ws.Get(ctx, sid)
		if err != nil {
			return nil, sessionError(err)
		}
		if v.Role != worksession.RoleWorker {
			return nil, sessionError(worksession.ErrNotWorker)
		}
		_, mailID, err := ws.SubmitResult(ctx, v.Peer, v.RequestID, result)
		if err != nil {
			return nil, sessionError(err)
		}
		newV, err := ws.Get(ctx, sid)
		if err != nil {
			return nil, sessionError(err)
		}
		return SessionResult{Session: sessionView(ctx, ps, ts, rs, newV, false), MailID: mailID}, nil
	})

	srv.Handle("ws_accept_result", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			ID    string
			Human bool
		}
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		sid, err := resolveSessionID(ctx, ws, p.ID)
		if err != nil {
			return nil, sessionError(err)
		}
		v, err := ws.Get(ctx, sid)
		if err != nil {
			return nil, sessionError(err)
		}
		if v.Role != worksession.RoleRequester {
			return nil, sessionError(worksession.ErrNotRequester)
		}
		if !p.Human {
			nv, err := ws.AcceptResult(ctx, sid)
			if err != nil {
				return nil, sessionError(err)
			}
			return SessionResult{Session: sessionView(ctx, ps, ts, rs, nv, false)}, nil
		}
		if as == nil {
			return nil, &ipc.Error{Code: CodeNotAvailable, Message: "human approval is not available"}
		}
		if v.State != worksession.StateAwaitingResult {
			return nil, sessionError(&worksession.BadStateError{State: v.State, Msg: fmt.Sprintf("%s is %s", sid, v.State)})
		}
		summary := fmt.Sprintf("Accept the result for session %s?", sid)
		createdSeq := v.Seq
		view, err := as.Create(ctx, "accept_result", sid, summary, approval.Action{
			Precondition: func(ctx context.Context, tx *sql.Tx) error {
				role, state, seq, err := ws.PeekTx(ctx, tx, sid)
				if err != nil {
					return err
				}
				if role != worksession.RoleRequester {
					return worksession.ErrNotRequester
				}
				if state != worksession.StateAwaitingResult {
					return &worksession.BadStateError{State: state, Msg: fmt.Sprintf("%s is %s", sid, state)}
				}
				// Bound to the result the human was asked about (review 35
				// H1): a new round since Create moves seq.
				if seq != createdSeq {
					return &worksession.BadStateError{State: state, Msg: fmt.Sprintf("%s changed since this approval was requested", sid)}
				}
				return nil
			},
			Perform: func(ctx context.Context, tx *sql.Tx) (any, error) {
				peer, round, opened, expBytes, expTruncated, err := ws.AcceptResultInTx(ctx, tx, sid, time.Now())
				if err != nil {
					return nil, err
				}
				now := time.Now()
				return afterCommitResult{after: func(ctx context.Context) {
					ws.AuditAfterHumanAccept(ctx, sid, peer, round, opened, now, expBytes, expTruncated)
				}}, nil
			},
		})
		if err != nil {
			return nil, approvalError(err)
		}
		return map[string]approval.View{"approval": view}, nil
	})

	srv.Handle("ws_request_changes", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ ID, Changes string }
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" || p.Changes == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id and changes are required"}
		}
		sid, err := resolveSessionID(ctx, ws, p.ID)
		if err != nil {
			return nil, sessionError(err)
		}
		v, err := ws.Get(ctx, sid)
		if err != nil {
			return nil, sessionError(err)
		}
		if v.Role != worksession.RoleRequester {
			return nil, sessionError(worksession.ErrNotRequester)
		}
		nv, err := ws.RequestChanges(ctx, sid, p.Changes)
		if err != nil {
			return nil, sessionError(err)
		}
		return SessionResult{Session: sessionView(ctx, ps, ts, rs, nv, false)}, nil
	})

	srv.Handle("ws_discard", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ ID string }
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		sid, err := resolveSessionID(ctx, ws, p.ID)
		if err != nil {
			return nil, sessionError(err)
		}
		v, err := ws.Get(ctx, sid)
		if err != nil {
			return nil, sessionError(err)
		}
		if v.Role != worksession.RoleRequester {
			return nil, sessionError(worksession.ErrNotRequester)
		}
		nv, err := ws.Discard(ctx, sid)
		if err != nil {
			return nil, sessionError(err)
		}
		return SessionResult{Session: sessionView(ctx, ps, ts, rs, nv, false)}, nil
	})

	srv.Handle("ws_cancel", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ ID, Reason string }
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		sid, err := resolveSessionID(ctx, ws, p.ID)
		if err != nil {
			return nil, sessionError(err)
		}
		v, err := ws.Get(ctx, sid)
		if err != nil {
			return nil, sessionError(err)
		}
		if v.Role == worksession.RoleRequester {
			nv, err := ws.Cancel(ctx, sid, p.Reason)
			if err != nil {
				return nil, sessionError(err)
			}
			return SessionResult{Session: sessionView(ctx, ps, ts, rs, nv, false)}, nil
		}
		nv, mailID, dup, err := ws.SubmitCancel(ctx, sid, p.Reason)
		if err != nil {
			return nil, sessionError(err)
		}
		return SessionCancelResult{Session: sessionView(ctx, ps, ts, rs, nv, false), MailID: mailID, Duplicate: dup}, nil
	})

	srv.Handle("ws_release", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ ID string }
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		sid, err := resolveSessionID(ctx, ws, p.ID)
		if err != nil {
			return nil, sessionError(err)
		}
		v, err := ws.Get(ctx, sid)
		if err != nil {
			return nil, sessionError(err)
		}
		if v.Role != worksession.RoleRequester {
			return nil, sessionError(worksession.ErrNotRequester)
		}
		if as == nil {
			return nil, &ipc.Error{Code: CodeNotAvailable, Message: "human approval is not available"}
		}
		if v.State != worksession.StateQuarantined {
			return nil, sessionError(&worksession.BadStateError{State: v.State, Msg: fmt.Sprintf("%s is %s", sid, v.State)})
		}
		summary := fmt.Sprintf("Release the quarantined result of session %s?", sid)
		// approvalID is set once Create returns below, before Perform can ever
		// run (Perform only runs later, once a human confirms through the
		// approval window or the daemon's terminal stdin, 2.2d): the closure
		// captures the variable, not its zero value.
		var approvalID string
		createdSeq := v.Seq
		view, err := as.Create(ctx, "release", sid, summary, approval.Action{
			Precondition: func(ctx context.Context, tx *sql.Tx) error {
				role, state, seq, err := ws.PeekTx(ctx, tx, sid)
				if err != nil {
					return err
				}
				if role != worksession.RoleRequester {
					return worksession.ErrNotRequester
				}
				if state != worksession.StateQuarantined {
					return &worksession.BadStateError{State: state, Msg: fmt.Sprintf("%s is %s", sid, state)}
				}
				// Bound to the quarantined result the human was asked about
				// (review 35 H1): request-changes from quarantined and a new
				// quarantined result bring the state back to quarantined, but
				// seq has moved, so a round-1 approval cannot release round 2.
				if seq != createdSeq {
					return &worksession.BadStateError{State: state, Msg: fmt.Sprintf("%s changed since this approval was requested", sid)}
				}
				return nil
			},
			Perform: func(ctx context.Context, tx *sql.Tx) (any, error) {
				peer, round, err := ws.ReleaseInTx(ctx, tx, sid, time.Now())
				if err != nil {
					return nil, err
				}
				return afterCommitResult{after: func(ctx context.Context) {
					if ws.Outbox != nil {
						ws.Outbox.Wake()
					}
					if log != nil {
						_ = log.Append(ctx, "cli", "ws.release", map[string]any{
							"session": sid, "peer": peer, "round": round, "approval": approvalID,
						})
					}
				}}, nil
			},
		})
		if err != nil {
			return nil, approvalError(err)
		}
		approvalID = view.ID
		return map[string]approval.View{"approval": view}, nil
	})
}
