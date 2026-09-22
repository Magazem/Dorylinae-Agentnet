package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
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

func registerTrust(srv *ipc.Server, ps *peers.Store, log *audit.Log) {
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
			if err := log.Append(ctx, audit.ActorCLI, audit.ActionPeerVerifyFail, d); err != nil {
				return nil, err
			}
			return nil, &ipc.Error{Code: CodeFingerprintMismatch, Message: "the fingerprint does not match this peer's key; nothing was changed"}
		}
		if err := ps.SetTrust(ctx, peer.PublicKey, peers.TrustFingerprint); err != nil {
			return nil, peerError(err)
		}
		peer.Trust = peers.TrustFingerprint
		peer.IntroducedBy = nil
		d.Trust = peer.Trust
		if err := log.Append(ctx, audit.ActorCLI, audit.ActionPeerVerify, d); err != nil {
			return nil, err
		}
		return PeerResult{Peer: peer}, nil
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
		if err := ps.Remove(ctx, peer.PublicKey); err != nil {
			return nil, peerError(err)
		}
		d := peerAuditDetail{Peer: peer.PublicKey, Name: peer.Name, Fingerprint: peer.Fingerprint, Trust: peer.Trust}
		if err := log.Append(ctx, audit.ActorCLI, audit.ActionPeerRemove, d); err != nil {
			return nil, err
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
