package daemon

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/approvaltext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// CodeBadFingerprint and CodeFingerprintMismatch are the IPC error codes of
// "peers_verify", documented in Docs/protocol/ipc.md.
const (
	CodeBadFingerprint      = "bad_fingerprint"
	CodeFingerprintMismatch = "fingerprint_mismatch"
)

// PeerVerifyParams are the params of "peers_verify". Peer is a public key or a
// unique peer name; Fingerprint is as typed by the user.
type PeerVerifyParams struct {
	Peer        string `json:"peer"`
	Fingerprint string `json:"fingerprint"`
}

// PeerVerifyResult is the result of "peers_verify": the pending peer_verify
// approval. The peer is raised to fingerprint only once a human approves it
// (D48, R55-082, Docs/protocol/approval.md).
type PeerVerifyResult struct {
	Approval approval.View `json:"approval"`
}

// PeerRemoveParams are the params of "peers_remove".
type PeerRemoveParams struct {
	Peer string `json:"peer"`
}

// PeerResult is the result of "peers_verify" and "peers_remove": the peer as
// it is (verify) or was (remove) stored.
type PeerResult struct {
	Peer peers.Peer `json:"peer"`
}

type peerAuditDetail struct {
	Peer        string `json:"peer"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	Trust       string `json:"trust,omitempty"`
}

func registerTrust(srv *ipc.Server, ps *peers.Store, log *audit.Log, ts *team.Store, apprStore *approval.Store, db *sql.DB) {
	srv.Handle("peers_verify", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p PeerVerifyParams
		if err := json.Unmarshal(params, &p); err != nil || p.Peer == "" || p.Fingerprint == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "peer and fingerprint are required"}
		}
		given, ok := envelope.NormalizeFingerprint(p.Fingerprint)
		if !ok {
			return nil, &ipc.Error{Code: CodeBadFingerprint, Message: "not a valid fingerprint (20 letters and digits, e.g. 2ED9 TGVE R471 63MC C451)"}
		}
		peer, err := resolvePeer(ctx, ps, p.Peer)
		if err != nil {
			return nil, err
		}
		d := peerAuditDetail{Peer: peer.PublicKey, Name: peer.Name, Fingerprint: peer.Fingerprint}
		if subtle.ConstantTimeCompare([]byte(given), []byte(peer.Fingerprint)) != 1 {
			// peer.verify_fail is N: a failing row is logged centrally and the
			// caller still gets the mismatch, not the audit error.
			_ = log.Append(ctx, audit.ActorCLI, audit.ActionPeerVerifyFail, d)
			return nil, &ipc.Error{Code: CodeFingerprintMismatch, Message: "the fingerprint does not match this peer's key; nothing was changed"}
		}
		// The fingerprint matched, which any local agent can arrange (it is
		// public), so raising the trust needs a human (D48, R55-082): a
		// peer_verify approval whose window shows the peer's name and grouped
		// fingerprint. Perform raises the trust and audits in the approval's
		// transaction.
		pf, err := peerFacts(ctx, db, peer.PublicKey)
		if err != nil {
			return nil, err
		}
		facts := approvaltext.PeerVerify{Peer: pf}
		summary, err := approvaltext.BuildPeerVerify(facts)
		if err != nil {
			return nil, summaryField(err, "peer")
		}
		d.Trust = peers.TrustFingerprint
		action := approval.Action{
			Precondition: func(ctx context.Context, tx *sql.Tx) error {
				_, ok, err := peerTrustTx(ctx, tx, peer.PublicKey)
				if err != nil {
					return err
				}
				if !ok {
					return &ipc.Error{Code: CodeUnknownPeer, Message: "no such paired peer (see 'agentnet peers')"}
				}
				return nil
			},
			Rebuild: rebuildWith(facts, func(ctx context.Context, tx *sql.Tx) (approvaltext.PeerVerify, error) {
				pf, err := peerFacts(ctx, tx, peer.PublicKey)
				return approvaltext.PeerVerify{Peer: pf}, err
			}, approvaltext.BuildPeerVerify),
			Perform: func(ctx context.Context, tx *sql.Tx) (any, error) {
				if err := peers.SetTrustTx(ctx, tx, peer.PublicKey, peers.TrustFingerprint); err != nil {
					return nil, peerError(err)
				}
				if err := auditTx(ctx, tx, audit.ActorCLI, audit.ActionPeerVerify, d); err != nil {
					return nil, err
				}
				return nil, nil
			},
		}
		view, aerr := apprStore.Create(ctx, approval.KindPeerVerify, peer.PublicKey, summary, action)
		if aerr != nil {
			return nil, approvalError(aerr)
		}
		return PeerVerifyResult{Approval: view}, nil
	})
	srv.Handle("peers_remove", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p PeerRemoveParams
		if err := json.Unmarshal(params, &p); err != nil || p.Peer == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "peer is required"}
		}
		peer, err := resolvePeer(ctx, ps, p.Peer)
		if err != nil {
			return nil, err
		}
		// Teams first: if this fails, the peer is still there and the user can
		// retry. The other order could leave the owner's teams active, and its
		// introductions trusted, with no peer row left to retry the remove on.
		gcSelf := false
		if ts != nil {
			// Owned-team membership first (review 16 L12): broadcasting the
			// updated roster needs the peer's mailbox key, so this runs
			// before OwnerRemoved and before the peer row is deleted.
			if err := cascadeTeamRemoval(ctx, ts, log, peer.PublicKey, clockNow(ts.Now)); err != nil {
				return nil, err
			}
		}
		if ts != nil {
			_, removed, terr := ts.OwnerRemoved(ctx, peer.PublicKey, clockNow(ts.Now))
			if terr != nil {
				return nil, terr
			}
			for _, r := range removed {
				gcSelf = gcSelf || r.PublicKey == peer.PublicKey
			}
		}
		// peer.remove is an S- row: written in the removal's transaction
		// through a savepoint, so the removal commits even if the row fails.
		// GC may already have deleted the row (a stale introduced peer); then
		// there is no removal transaction and the row is an N row.
		d := peerAuditDetail{Peer: peer.PublicKey, Name: peer.Name, Fingerprint: peer.Fingerprint, Trust: peer.Trust}
		err = ps.RemoveWith(ctx, peer.PublicKey, func(ctx context.Context, tx *sql.Tx) error {
			return audit.AppendTxSoft(ctx, tx, audit.ActorCLI, audit.ActionPeerRemove, d)
		})
		if errors.Is(err, peers.ErrNoPeer) && gcSelf {
			_ = log.Append(ctx, audit.ActorCLI, audit.ActionPeerRemove, d)
		} else if err != nil {
			return nil, peerError(err)
		}
		return PeerResult{Peer: peer}, nil
	})
}

func peerError(err error) error {
	if errors.Is(err, peers.ErrNoPeer) {
		return &ipc.Error{Code: CodeUnknownPeer, Message: "no such paired peer (see 'agentnet peers')"}
	}
	return err
}
