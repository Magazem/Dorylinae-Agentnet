package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/notify"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
)

// CodeConstraintLimit refuses an 11th active constraint on a debate
// (Docs/protocol/debate.md §Human constraints, "Limits").
const CodeConstraintLimit = "constraint_limit"

// DebateConstrainParams are the params of "debate_constrain".
type DebateConstrainParams struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// DebateConstrainResult is the result of "debate_constrain": the pending
// approval. The constraint exists only once a human approves it (OD-P3-3).
type DebateConstrainResult struct {
	Approval approval.View `json:"approval"`
}

// constrainError maps the debate package's errors to the codes of
// Docs/protocol/debate.md §IPC.
func constrainError(err error) error {
	var fe *debate.FieldError
	var bse *debate.BadStateError
	switch {
	case errors.As(err, &fe):
		return &ipc.Error{Code: ipc.CodeBadRequest, Message: fe.Error()}
	case errors.Is(err, debate.ErrUnknownDebate):
		return &ipc.Error{Code: CodeUnknownSession, Message: "no such debate"}
	case errors.As(err, &bse):
		return &ipc.Error{Code: CodeBadState, Message: bse.Error()}
	case errors.Is(err, debate.ErrConstraintLimit):
		return &ipc.Error{Code: CodeConstraintLimit, Message: "the debate already has 10 constraints"}
	}
	return err
}

// constraintSummary is what the human approves: the peer, the session and
// the constraint text in full, rendered with the DisplayQuote rule so no
// invisible character can hide in it (review 43 H3).
func constraintSummary(who, sid, text string) string {
	return fmt.Sprintf("add a human constraint to the debate %s with %s: %s. It is signed into the Decision as a human decision. Confirm only if you wrote this constraint yourself.",
		sid, who, device.DisplayQuote(text))
}

// peerDisplayName is a paired peer's name, cleaned for an approval summary
// (no decoy codes, review 26 N4), or the key when it is no longer paired.
func peerDisplayName(ctx context.Context, ps *peers.Store, key string) string {
	list, err := ps.List(ctx)
	if err == nil {
		for _, p := range list {
			if p.PublicKey == key {
				return stripLongDigits(notify.Clean(p.Name, 40))
			}
		}
	}
	return key
}

// registerDebateConstrain wires "debate_constrain" (Docs/protocol/debate.md
// §Human constraints, §IPC): the text and the debate are checked now, and a
// debate_constraint approval holds the constraint until a human confirms it.
// Only then, in the approval's transaction, is it stored, audited and sent.
func registerDebateConstrain(srv *ipc.Server, ds *debate.Store, apprStore *approval.Store, ps *peers.Store) {
	srv.Handle("debate_constrain", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p DebateConstrainParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id and text are required"}
		}
		pc, err := ds.PrepareConstraint(ctx, p.ID, p.Text)
		if err != nil {
			return nil, constrainError(err)
		}
		summary := constraintSummary(peerDisplayName(ctx, ps, pc.Peer), pc.Session, pc.Text)
		var approvalID string
		var idMu sync.Mutex
		action := approval.Action{
			// The debate is still in positions, rounds or converge and has
			// room (review 26 N1): else the approval is rejected,
			// precondition.
			Precondition: func(ctx context.Context, tx *sql.Tx) error {
				return constrainError(ds.ConstraintPreconditionTx(ctx, tx, pc))
			},
			// Perform touches only tx: the row, its audit (ids only, never
			// the text) and the mail commit together.
			Perform: func(ctx context.Context, tx *sql.Tx) (any, error) {
				idMu.Lock()
				aid := approvalID
				idMu.Unlock()
				if err := ds.AddConstraintTx(ctx, tx, pc, aid); err != nil {
					return nil, err
				}
				if err := auditTx(ctx, tx, audit.ActorCLI, "debate.constraint", map[string]any{
					"session": pc.Session, "peer": pc.Peer, "id": pc.ID, "approval": aid,
				}); err != nil {
					return nil, err
				}
				return afterCommitResult{after: func(context.Context) {
					if ds.Outbox != nil {
						ds.Outbox.Wake()
					}
				}}, nil
			},
		}
		view, aerr := apprStore.Create(ctx, approval.KindDebateConstraint, pc.Session, summary, action)
		if aerr != nil {
			return nil, approvalError(aerr)
		}
		idMu.Lock()
		approvalID = view.ID
		idMu.Unlock()
		return DebateConstrainResult{Approval: view}, nil
	})
}
