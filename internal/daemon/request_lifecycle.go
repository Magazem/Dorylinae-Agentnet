package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// IPC error codes for the request lifecycle (Docs/protocol/ipc.md §Requests).
const (
	CodeUnknownRequest   = "unknown_request"
	CodeAmbiguousRequest = "ambiguous_request"
	CodeBadState         = "bad_state"
	CodeResultTooLarge   = "result_too_large"
)

// decodeResultParam strictly decodes request_complete's "result" param with
// the same rules as the wire (request.DecodeResult): unknown members, null or
// empty optional members, a fractional exit_code and an empty artifacts
// array are rejected rather than silently dropped
// (Docs/protocol/request.md §Result payload (D14)). raw is nil when absent.
func decodeResultParam(raw json.RawMessage) (*request.Result, error) {
	if raw == nil {
		return nil, nil
	}
	v, err := agentcard.ParseStrict(raw)
	if err != nil {
		return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "result: " + err.Error()}
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "result must be an object"}
	}
	r, err := request.DecodeResult(obj)
	if err != nil {
		return nil, lifecycleError(err)
	}
	return r, nil
}

// RequestPeerRef is the "peer ref" common object of Docs/protocol/ipc.md
// §Methods ({"name", "public_key", "fingerprint"}).
type RequestPeerRef struct {
	Name        string `json:"name"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
}

// RequestArtifact is one entry of a request or result view's "artifacts".
type RequestArtifact struct {
	URL    string `json:"url,omitempty"`
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
	Path   string `json:"path,omitempty"`
}

// RequestResult is the "result" member of a request view
// (Docs/protocol/request.md §Result payload (D14)).
type RequestResult struct {
	Status      string            `json:"status"`
	Summary     string            `json:"summary,omitempty"`
	ExitCode    *int64            `json:"exit_code,omitempty"`
	Output      string            `json:"output,omitempty"`
	Artifacts   []RequestArtifact `json:"artifacts,omitempty"`
	OutputBytes int               `json:"output_bytes"`
}

// RequestView is the "request view" of Docs/protocol/ipc.md §Requests.
type RequestView struct {
	ID              string            `json:"id"`
	Direction       string            `json:"direction"`
	Peer            RequestPeerRef    `json:"peer"`
	Team            teamRefResult     `json:"team"`
	Type            string            `json:"type"`
	Title           string            `json:"title"`
	Brief           string            `json:"brief"`
	Urgency         string            `json:"urgency"`
	UrgencyDeclared string            `json:"urgency_declared"`
	DowngradedBy    *string           `json:"downgraded_by"`
	UrgencyReason   string            `json:"urgency_reason,omitempty"`
	Artifacts       []RequestArtifact `json:"artifacts"`
	RequestedGrant  *GrantParam       `json:"requested_grant,omitempty"`
	Deadline        string            `json:"deadline,omitempty"`
	Created         string            `json:"created"`
	ReceivedAt      string            `json:"received_at,omitempty"`
	State           string            `json:"state"`
	StateAt         *string           `json:"state_at"`
	DeferredUntil   string            `json:"deferred_until,omitempty"`
	DeclineCode     string            `json:"decline_code,omitempty"`
	Reason          string            `json:"reason,omitempty"`
	Note            string            `json:"note,omitempty"`
	Cancel          string            `json:"cancel,omitempty"`
	Result          *RequestResult    `json:"result,omitempty"`
	Priority        *int              `json:"priority,omitempty"`
	Due             *bool             `json:"due,omitempty"`
	UrgencyNote     string            `json:"urgency_note,omitempty"`
	Delivery        string            `json:"delivery,omitempty"`
	MailID          string            `json:"mail_id"`
}

const wireTimeFmt = "2006-01-02T15:04:05Z"

func timeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(wireTimeFmt)
}

func timePtr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(wireTimeFmt)
	return &s
}

func requestArtifacts(a []request.Artifact) []RequestArtifact {
	out := make([]RequestArtifact, len(a))
	for i, x := range a {
		out[i] = RequestArtifact{URL: x.URL, Branch: x.Branch, Commit: x.Commit, Path: x.Path}
	}
	return out
}

// ViewResult builds the request view for id v, with includeOutput controlling
// whether result.output is included (Docs/protocol/ipc.md §Requests).
func ViewResult(ctx context.Context, ps *peers.Store, ts *team.Store, v request.View, includeOutput bool) RequestView {
	fp, _ := envelope.KeyFingerprint(v.Peer)
	name := v.Peer
	if p, err := resolvePeer(ctx, ps, v.Peer); err == nil {
		name = p.Name
	}
	t, _ := ts.Get(ctx, v.TeamID)

	r := RequestView{
		ID: v.ID, Direction: v.Direction,
		Peer: RequestPeerRef{Name: name, PublicKey: v.Peer, Fingerprint: fp},
		Team: teamRefResult{ID: t.ID, Name: t.Name},
		Type: v.Type, Title: v.Title, Brief: v.Brief,
		Urgency: v.Urgency, UrgencyDeclared: v.UrgencyDeclared, UrgencyReason: v.UrgencyReason,
		Artifacts: requestArtifacts(v.Artifacts), Deadline: timeOrEmpty(v.Deadline),
		Created: timeOrEmpty(v.Created), ReceivedAt: timeOrEmpty(v.ReceivedAt),
		State: v.State, StateAt: timePtr(v.StateAt), DeferredUntil: timeOrEmpty(v.DeferredUntil),
		DeclineCode: v.DeclineCode, Reason: v.Reason, Note: v.Note, Cancel: v.Cancel,
		Delivery: v.Delivery, MailID: v.MailID,
	}
	if v.DowngradedBy != "" {
		db := v.DowngradedBy
		r.DowngradedBy = &db
	}
	if v.RequestedGrant != nil {
		r.RequestedGrant = &GrantParam{Action: v.RequestedGrant.Action, Resource: v.RequestedGrant.Resource, Note: v.RequestedGrant.Note}
	}
	if v.Result != nil {
		res := &RequestResult{Status: v.Result.Status, Summary: v.Result.Summary, ExitCode: v.Result.ExitCode, OutputBytes: v.OutputBytes}
		if includeOutput {
			res.Output = v.Result.Output
		}
		if len(v.Result.Artifacts) > 0 {
			res.Artifacts = requestArtifacts(v.Result.Artifacts)
		}
		r.Result = res
	}
	if v.Priority != 0 {
		p := v.Priority
		r.Priority = &p
	}
	if v.Due {
		due := true
		r.Due = &due
	}
	r.UrgencyNote = v.UrgencyNote
	return r
}

// lifecycleError maps a request package error to an *ipc.Error
// (Docs/protocol/ipc.md §Requests "Lifecycle errors").
func lifecycleError(err error) error {
	var fe *request.FieldError
	var bse *request.BadStateError
	var tl *request.TooLargeCompleteError
	switch {
	case errors.Is(err, request.ErrUnknownRequest):
		return &ipc.Error{Code: CodeUnknownRequest, Message: "no such request"}
	case errors.Is(err, request.ErrAmbiguousRequest):
		return &ipc.Error{Code: CodeAmbiguousRequest, Message: "the id matches requests from several peers; pass from"}
	case errors.As(err, &bse):
		return &ipc.Error{Code: CodeBadState, Message: bse.Msg}
	case errors.As(err, &fe):
		return &ipc.Error{Code: ipc.CodeBadRequest, Message: fe.Error()}
	case errors.As(err, &tl):
		return &ipc.Error{Code: CodeResultTooLarge, Message: tl.Error()}
	default:
		return err
	}
}

type idFromParams struct {
	ID   string `json:"id"`
	From string `json:"from,omitempty"`
}

// RequestLifecycleResult is the result of request_accept, request_decline,
// request_defer and request_complete.
type RequestLifecycleResult struct {
	Request RequestView `json:"request"`
	MailID  string      `json:"mail_id"`
}

// RequestShowResult is the result of request_show.
type RequestShowResult struct {
	Request RequestView `json:"request"`
}

// RequestListResult is the result of request_list.
type RequestListResult struct {
	Requests []RequestView `json:"requests"`
}

// RequestResendResult is the result of request_resend.
type RequestResendResult struct {
	ID     string `json:"id"`
	MailID string `json:"mail_id"`
	Status string `json:"status"`
}

// RequestCancelResult is the result of request_cancel.
type RequestCancelResult struct {
	Request   RequestView `json:"request"`
	MailID    *string     `json:"mail_id"`
	Duplicate bool        `json:"duplicate"`
}

// registerLifecycle wires the recipient-side lifecycle IPC (request_accept,
// request_decline, request_defer, request_complete), request_cancel,
// request_resend, request_show, request_list and inbox_list
// (Docs/protocol/ipc.md §Requests). The CLI for
// inbox/accept/decline/defer/complete is 1.6b.
func registerLifecycle(srv *ipc.Server, rs *request.Store, ps *peers.Store, ts *team.Store) {
	srv.Handle("request_accept", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p idFromParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		v, err := rs.Accept(ctx, p.ID, p.From)
		if err != nil {
			return nil, lifecycleError(err)
		}
		return RequestLifecycleResult{Request: ViewResult(ctx, ps, ts, v, true), MailID: v.ReplyMailID}, nil
	})

	srv.Handle("request_decline", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			ID, From, Reason string
		}
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" || p.Reason == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id and reason are required"}
		}
		v, err := rs.Decline(ctx, p.ID, p.From, p.Reason)
		if err != nil {
			return nil, lifecycleError(err)
		}
		return RequestLifecycleResult{Request: ViewResult(ctx, ps, ts, v, true), MailID: v.ReplyMailID}, nil
	})

	srv.Handle("request_defer", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			ID, From, Until string
		}
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" || p.Until == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id and until are required"}
		}
		until, err := parseDeadline(p.Until, time.Now())
		if err != nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "until: " + err.Error()}
		}
		v, err := rs.Defer(ctx, p.ID, p.From, until)
		if err != nil {
			return nil, lifecycleError(err)
		}
		return RequestLifecycleResult{Request: ViewResult(ctx, ps, ts, v, true), MailID: v.ReplyMailID}, nil
	})

	srv.Handle("request_complete", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			ID, From, Note string
			Result         json.RawMessage
		}
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		result, err := decodeResultParam(p.Result)
		if err != nil {
			return nil, err
		}
		v, err := rs.Complete(ctx, p.ID, p.From, p.Note, result)
		if err != nil {
			return nil, lifecycleError(err)
		}
		return RequestLifecycleResult{Request: ViewResult(ctx, ps, ts, v, true), MailID: v.ReplyMailID}, nil
	})

	srv.Handle("request_cancel", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			ID, Reason string
		}
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		out, err := rs.Cancel(ctx, p.ID, p.Reason)
		if err != nil {
			return nil, lifecycleError(err)
		}
		var mailID *string
		if out.MailID != "" {
			mailID = &out.MailID
		}
		return RequestCancelResult{Request: ViewResult(ctx, ps, ts, out.View, true), MailID: mailID, Duplicate: out.Duplicate}, nil
	})

	srv.Handle("request_resend", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ ID string }
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		out, err := rs.Resend(ctx, p.ID)
		if err != nil {
			return nil, lifecycleError(err)
		}
		return RequestResendResult{ID: p.ID, MailID: out.MailID, Status: "queued"}, nil
	})

	srv.Handle("request_show", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p idFromParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		v, err := rs.Show(ctx, p.ID, p.From)
		if err != nil {
			return nil, lifecycleError(err)
		}
		return RequestShowResult{Request: ViewResult(ctx, ps, ts, v, true)}, nil
	})

	srv.Handle("request_list", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			State, Team, Peer string
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
		}
		f := request.ListFilter{State: p.State}
		if p.Team != "" {
			t, err := resolveTeam(ctx, ts, p.Team)
			if err != nil {
				return nil, err
			}
			f.Team = t.ID
		}
		if p.Peer != "" {
			peer, err := resolvePeer(ctx, ps, p.Peer)
			if err != nil {
				return nil, err
			}
			f.Peer = peer.PublicKey
		}
		views, err := rs.List(ctx, f)
		if err != nil {
			return nil, err
		}
		results := make([]RequestView, len(views))
		for i, v := range views {
			results[i] = ViewResult(ctx, ps, ts, v, false)
		}
		return RequestListResult{Requests: results}, nil
	})

	srv.Handle("inbox_list", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Team string
			All  bool
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
		}
		f := request.InboxFilter{All: p.All}
		if p.Team != "" {
			t, err := resolveTeam(ctx, ts, p.Team)
			if err != nil {
				return nil, err
			}
			f.Team = t.ID
		}
		views, err := rs.InboxList(ctx, f)
		if err != nil {
			return nil, err
		}
		results := make([]RequestView, len(views))
		for i, v := range views {
			results[i] = ViewResult(ctx, ps, ts, v, false)
		}
		return RequestListResult{Requests: results}, nil
	})
}
