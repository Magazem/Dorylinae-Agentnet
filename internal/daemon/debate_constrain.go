package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/approvaltext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
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

// constraintFacts is what the human approves: the debate, the peer and the
// constraint text in full (Docs/protocol/approval.md §Contents per kind).
func constraintFacts(pc debate.PendingConstraint, peer approvaltext.Peer) approvaltext.Constraint {
	return approvaltext.Constraint{Session: pc.Session, Peer: peer, ID: pc.ID, Text: pc.Text}
}

// registerDebateConstrain wires "debate_constrain" (Docs/protocol/debate.md
// §Human constraints, §IPC): the text and the debate are checked now, and a
// debate_constraint approval holds the constraint until a human confirms it.
// Only then, in the approval's transaction, is it stored, audited and sent.
func registerDebateConstrain(srv *ipc.Server, ds *debate.Store, apprStore *approval.Store) {
	srv.Handle("debate_constrain", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p DebateConstrainParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "id and text are required"}
		}
		pc, err := ds.PrepareConstraint(ctx, p.ID, p.Text)
		if err != nil {
			return nil, constrainError(err)
		}
		// The human approves what the window shows, so the text is never cut
		// there (review 46 H2): the builder refuses a summary that does not
		// fit. A 500-code-point text always fits (R55-F5).
		pf, err := peerFacts(ctx, ds.DB, pc.Peer)
		if err != nil {
			return nil, err
		}
		facts := constraintFacts(pc, pf)
		summary, err := approvaltext.BuildConstraint(facts)
		if err != nil {
			return nil, summaryField(err, "text")
		}
		var approvalID string
		var idMu sync.Mutex
		action := approval.Action{
			// The debate is still in positions, rounds or converge and has
			// room (review 26 N1): else the approval is rejected,
			// precondition.
			Precondition: func(ctx context.Context, tx *sql.Tx) error {
				return constrainError(ds.ConstraintPreconditionTx(ctx, tx, pc))
			},
			// Precondition compares (R55-F5): the captured constraint and the
			// peer's current name.
			Rebuild: rebuildWith(facts, func(ctx context.Context, tx *sql.Tx) (approvaltext.Constraint, error) {
				pf, err := peerFacts(ctx, tx, pc.Peer)
				return constraintFacts(pc, pf), err
			}, approvaltext.BuildConstraint),
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
