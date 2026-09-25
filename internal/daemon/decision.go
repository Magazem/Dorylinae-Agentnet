package daemon

// Ticket 3.3b: decision_list, decision_show (Docs/protocol/decision.md
// §IPC): reading the Decisions internal/debate stores at close. The
// derivation, signing exchange and storage are 3.3a
// (internal/debate/decision.go); this file only builds the read views. The
// Markdown renderer and offline `decision verify` live in internal/decision
// and cmd/agentnet/decision.go.

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// CodeUnknownDecision is decision_show's error when the id names no stored
// Decision (Docs/protocol/decision.md §IPC).
const CodeUnknownDecision = "unknown_decision"

// DecisionListItem is one row of decision_list.
type DecisionListItem struct {
	ID      string `json:"id"`
	Session string `json:"session"`
	Peer    string `json:"peer"`
	Outcome string `json:"outcome,omitempty"`
	State   string `json:"state"`
	Created string `json:"created"`
	Title   string `json:"title,omitempty"`
}

// DecisionListResult is the result of decision_list.
type DecisionListResult struct {
	Decisions []DecisionListItem `json:"decisions"`
}

// DecisionSignatures is the signed file's "signatures" member
// (Docs/protocol/decision.md §Signed file).
type DecisionSignatures struct {
	Initiator  string `json:"initiator,omitempty"`
	Respondent string `json:"respondent,omitempty"`
}

// DecisionShowResult is decision_show's result: the signed file plus the
// local state and petnames (Docs/protocol/decision.md §IPC).
type DecisionShowResult struct {
	Decision   json.RawMessage    `json:"decision"`
	Hash       string             `json:"hash"`
	Signatures DecisionSignatures `json:"signatures"`
	State      string             `json:"state"`
	PeerNames  map[string]string  `json:"peer_names"`
	PeerFPs    map[string]string  `json:"peer_fingerprints,omitempty"`
}

// decisionError maps an internal/debate/decision error to its IPC error.
func decisionError(err error) error {
	if errors.Is(err, debate.ErrUnknownDecision) {
		return &ipc.Error{Code: CodeUnknownDecision, Message: "no such Decision"}
	}
	return err
}

// decisionRequestDirection mirrors debateRequestDirection: the request's
// direction from this side's role in the debate.
func decisionRequestDirection(role string) string {
	if role == debate.RoleInitiator {
		return "out"
	}
	return "in"
}

// decisionTitle looks up the debate's request title for a Decision list row
// (best effort: an empty string if the debate or request cannot be
// resolved, which never blocks the list).
func decisionTitle(ctx context.Context, ds *debate.Store, rs *request.Store, role, peer, session string) string {
	if rs == nil || ds == nil {
		return ""
	}
	v, err := ds.Get(ctx, session)
	if err != nil {
		return ""
	}
	req, err := rs.ShowKey(ctx, request.Key{Direction: decisionRequestDirection(role), Peer: peer, ID: v.RequestID})
	if err != nil {
		return ""
	}
	return req.Title
}

// decisionNamesAndFingerprints builds the "initiator"/"respondent" petnames
// and fingerprints for a stored Decision: this side's own name for its own
// role, the resolved peer name for the other (Docs/protocol/decision.md
// §Markdown "Participants").
func decisionNamesAndFingerprints(ctx context.Context, ps *peers.Store, selfKey, selfName string, d debate.DecisionRecord) (map[string]string, map[string]string) {
	other := debate.RoleRespondent
	if d.Role == debate.RoleRespondent {
		other = debate.RoleInitiator
	}
	peerName := d.Peer
	if p, err := resolvePeer(ctx, ps, d.Peer); err == nil {
		peerName = p.Name
	}
	names := map[string]string{d.Role: selfName, other: peerName}
	fps := map[string]string{}
	if fp, err := envelope.KeyFingerprint(selfKey); err == nil {
		fps[d.Role] = fp
	}
	if fp, err := envelope.KeyFingerprint(d.Peer); err == nil {
		fps[other] = fp
	}
	return names, fps
}

// registerDecision wires decision_list and decision_show
// (Docs/protocol/decision.md §IPC).
func registerDecision(srv *ipc.Server, ds *debate.Store, ps *peers.Store, rs *request.Store, selfKey, selfName string) {
	srv.Handle("decision_list", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ State, Peer string }
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
		recs, err := ds.DecisionList(ctx, p.State, peer)
		if err != nil {
			return nil, decisionError(err)
		}
		out := make([]DecisionListItem, 0, len(recs))
		for _, d := range recs {
			outcome, _ := debate.DecisionOutcome(d.Decision)
			peerName := d.Peer
			if pr, err := resolvePeer(ctx, ps, d.Peer); err == nil {
				peerName = pr.Name
			}
			out = append(out, DecisionListItem{
				ID: d.ID, Session: d.Session, Peer: peerName, Outcome: outcome, State: d.State,
				Created: d.Created, Title: decisionTitle(ctx, ds, rs, d.Role, d.Peer, d.Session),
			})
		}
		return DecisionListResult{Decisions: out}, nil
	})

	srv.Handle("decision_show", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ ID string }
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id is required"}
		}
		d, err := ds.Decision(ctx, p.ID)
		if err != nil {
			return nil, decisionError(err)
		}
		names, fps := decisionNamesAndFingerprints(ctx, ps, selfKey, selfName, d)
		return DecisionShowResult{
			Decision:   json.RawMessage(d.Decision),
			Hash:       d.Hash,
			Signatures: DecisionSignatures{Initiator: d.SigInitiator, Respondent: d.SigRespondent},
			State:      d.State,
			PeerNames:  names,
			PeerFPs:    fps,
		}, nil
	})
}
