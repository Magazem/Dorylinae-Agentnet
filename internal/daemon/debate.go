package daemon

// Ticket 3.1b: debate IPC (Docs/protocol/debate.md §IPC): debate_list,
// debate_show, debate_submit (including the one-step accept + position), the
// timeout sweep wiring (the rule itself is internal/debate) and cancel
// (ws_cancel, already wired to internal/worksession for a debate session).
// debate_constrain (3.4) is a deliberate seam: it is not registered here.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// CodeNotYourTurn is debate_submit's refusal when the given kind or the
// caller is not the next slot (Docs/protocol/debate.md §IPC).
const CodeNotYourTurn = "not_your_turn"

// DebateRequestRef is the debate view's "request" member.
type DebateRequestRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// DebateContextRef is one entry of the debate view's "context" member
// (Docs/protocol/debate.md §IPC): the name and size only, never the text.
type DebateContextRef struct {
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
}

// DebateRoundsView is the debate view's "rounds" member.
type DebateRoundsView struct {
	Max     int `json:"max"`
	Current int `json:"current"`
}

// DebateEntryView is one transcript entry.
type DebateEntryView struct {
	Slot   int             `json:"slot"`
	Author string          `json:"author"`
	Kind   string          `json:"kind"`
	At     string          `json:"at"`
	Entry  json.RawMessage `json:"entry"`
}

// DebateConstraintView is one constraint (3.4 fills debate_constraints; this
// is always empty until then).
type DebateConstraintView struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	At     string `json:"at"`
	Text   string `json:"text"`
	State  string `json:"state"`
}

// DebateView is the "debate" view of Docs/protocol/debate.md §IPC. The list
// view (debate_list) omits Topic, Context, Transcript and Constraints,
// keeping Entries and ConstraintCount instead.
type DebateView struct {
	Session         string                 `json:"session"`
	Request         DebateRequestRef       `json:"request"`
	Role            string                 `json:"role"`
	Peer            RequestPeerRef         `json:"peer"`
	Team            teamRefResult          `json:"team"`
	Phase           string                 `json:"phase"`
	Outcome         string                 `json:"outcome,omitempty"`
	Reason          string                 `json:"reason,omitempty"`
	Rounds          DebateRoundsView       `json:"rounds"`
	Turn            string                 `json:"turn"`
	Expect          string                 `json:"expect,omitempty"`
	Deadline        string                 `json:"deadline,omitempty"`
	Waiting         string                 `json:"waiting,omitempty"`
	Topic           string                 `json:"topic,omitempty"`
	Context         []DebateContextRef     `json:"context,omitempty"`
	Transcript      []DebateEntryView      `json:"transcript,omitempty"`
	Entries         int                    `json:"entries,omitempty"`
	Constraints     []DebateConstraintView `json:"constraints,omitempty"`
	ConstraintCount int                    `json:"constraint_count,omitempty"`
}

// DebateListResult is the result of debate_list.
type DebateListResult struct {
	Debates []DebateView `json:"debates"`
}

// DebateShowResult is the result of debate_show.
type DebateShowResult struct {
	Debate DebateView `json:"debate"`
}

// DebateSubmitResult is the result of debate_submit.
type DebateSubmitResult struct {
	Debate DebateView `json:"debate"`
	MailID string     `json:"mail_id"`
}

// debateRequestDirection is "out" for the initiator's own request row, "in"
// for the respondent's received one: the role already says which, so there
// is no ambiguity to resolve (unlike the generic notify adapter, which does
// not know the role).
func debateRequestDirection(role string) string {
	if role == debate.RoleInitiator {
		return "out"
	}
	return "in"
}

// buildDebateView renders v (Docs/protocol/debate.md §IPC). listMode omits
// topic, context, the transcript and constraint texts, keeping their counts.
func buildDebateView(ctx context.Context, v debate.View, ps *peers.Store, ts *team.Store, rs *request.Store, listMode bool) DebateView {
	fp, _ := envelope.KeyFingerprint(v.Peer)
	name := v.Peer
	if p, err := resolvePeer(ctx, ps, v.Peer); err == nil {
		name = p.Name
	}
	var title, topic string
	var teamID string
	var contextFiles []request.ContextFile
	if rs != nil {
		if req, err := rs.ShowKey(ctx, request.Key{Direction: debateRequestDirection(v.Role), Peer: v.Peer, ID: v.RequestID}); err == nil {
			title, topic, teamID, contextFiles = req.Title, req.Brief, req.TeamID, req.Context
		}
	}
	t, _ := ts.Get(ctx, teamID)

	dv := DebateView{
		Session: v.Session, Request: DebateRequestRef{ID: v.RequestID, Title: title}, Role: v.Role,
		Peer:  RequestPeerRef{Name: name, PublicKey: v.Peer, Fingerprint: fp},
		Team:  teamRefResult{ID: t.ID, Name: t.Name},
		Phase: v.Phase, Outcome: v.Outcome, Reason: v.Reason,
		Rounds: DebateRoundsView{Max: v.RoundsMax, Current: v.RoundsUsed},
		Turn:   v.Turn, Expect: v.Expect, Waiting: v.Waiting,
	}
	if !v.Deadline.IsZero() {
		dv.Deadline = wireTimeString(v.Deadline)
	}
	if listMode {
		dv.Entries = len(v.Transcript)
		return dv
	}
	dv.Topic = topic
	for _, c := range contextFiles {
		dv.Context = append(dv.Context, DebateContextRef{Name: c.Name, Bytes: len(c.Text)})
	}
	for _, e := range v.Transcript {
		dv.Transcript = append(dv.Transcript, DebateEntryView{Slot: e.Slot, Author: e.Author, Kind: e.Kind, At: e.At, Entry: e.Entry})
	}
	dv.Constraints = []DebateConstraintView{} // 3.4 fills this
	return dv
}

func wireTimeString(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
}

// debateError maps an internal/debate error to its IPC error
// (Docs/protocol/debate.md §IPC): FieldError -> bad_request, TooLargeError ->
// entry_too_large, NotYourTurnError -> not_your_turn, BadStateError ->
// bad_state, ErrUnknownDebate -> unknown_session (also ambiguous_request,
// review 45 L1), ErrQuarantineActive -> quarantine_active.
func debateError(err error) error {
	var nyt *debate.NotYourTurnError
	var bse *debate.BadStateError
	var etl *debate.TooLargeError
	var fe *debate.FieldError
	switch {
	case errors.Is(err, debate.ErrUnknownDebate):
		return &ipc.Error{Code: CodeUnknownSession, Message: "no such debate"}
	case errors.Is(err, request.ErrAmbiguousRequest):
		return &ipc.Error{Code: CodeAmbiguousRequest, Message: "the id matches requests from several peers; use the session id (s-...)"}
	case errors.Is(err, debate.ErrQuarantineActive):
		return &ipc.Error{Code: CodeQuarantineActive, Message: err.Error()}
	case errors.As(err, &nyt):
		return &ipc.Error{Code: CodeNotYourTurn, Message: nyt.Error()}
	case errors.As(err, &etl):
		return &ipc.Error{Code: CodeEntryTooLarge, Message: "entry: " + etl.Error()}
	case errors.As(err, &fe):
		return &ipc.Error{Code: ipc.CodeBadRequest, Message: fe.Error()}
	case errors.As(err, &bse):
		return &ipc.Error{Code: CodeBadState, Message: bse.Msg}
	default:
		return err
	}
}

// resolveDebateID accepts an s-... session id directly, or an r-... request
// id (Docs/protocol/debate.md §IPC): both debate_show and debate_submit
// resolve through the same internal/debate lookup, so the id is passed
// through as given and errors are mapped by debateError.

// registerDebate wires debate_list, debate_show and debate_submit
// (Docs/protocol/debate.md §IPC). Cancel is ws_cancel (internal/daemon/session.go,
// already dispatching to internal/worksession's debate hooks).
func registerDebate(srv *ipc.Server, ds *debate.Store, ps *peers.Store, ts *team.Store, rs *request.Store) {
	srv.Handle("debate_list", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ Phase, Peer string }
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
			}
		}
		peer := ""
		if p.Peer != "" {
			pr, err := resolvePeer(ctx, ps, p.Peer)
			if err != nil {
				return nil, err
			}
			peer = pr.PublicKey
		}
		views, err := ds.List(ctx, p.Phase, peer)
		if err != nil {
			return nil, debateError(err)
		}
		out := make([]DebateView, len(views))
		for i, v := range views {
			out[i] = buildDebateView(ctx, v, ps, ts, rs, true)
		}
		return DebateListResult{Debates: out}, nil
	})

	srv.Handle("debate_show", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ ID string }
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		v, err := ds.Get(ctx, p.ID)
		if err != nil {
			return nil, debateError(err)
		}
		return DebateShowResult{Debate: buildDebateView(ctx, v, ps, ts, rs, false)}, nil
	})

	srv.Handle("debate_submit", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			ID    string
			Kind  string
			Entry json.RawMessage
		}
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" || p.Kind == "" || len(p.Entry) == 0 {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id, kind and entry are required"}
		}
		entry, perr := agentcard.ParseStrict(p.Entry)
		if perr != nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "entry: " + perr.Error()}
		}
		out, err := ds.Submit(ctx, p.ID, "", p.Kind, entry)
		if err != nil {
			return nil, debateError(err)
		}
		v, err := ds.Get(ctx, out.Session)
		if err != nil {
			return nil, debateError(err)
		}
		return DebateSubmitResult{Debate: buildDebateView(ctx, v, ps, ts, rs, false), MailID: out.MailID}, nil
	})
}

// debateSweepInterval is how often the daemon checks debate turn deadlines
// (Docs/protocol/debate.md §Timeouts: "at least once a minute").
const debateSweepInterval = 20 * time.Second

// startDebateSweep runs debate.Store.Sweep on a ticker until ctx is
// cancelled. Sweep itself logs and continues past one bad debate (review 45
// L2); this loop only logs a failure to even list debates, which would be a
// database problem, not a single bad row.
func startDebateSweep(ctx context.Context, ds *debate.Store, log *slog.Logger) {
	t := time.NewTicker(debateSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := ds.Sweep(ctx); err != nil && log != nil {
				log.Error("debate: sweep", "error", err)
			}
		}
	}
}
