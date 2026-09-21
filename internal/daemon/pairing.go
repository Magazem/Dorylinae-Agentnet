package daemon

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// IPC error codes for pairing, documented in Docs/protocol/ipc.md.
const (
	CodeNoRelay          = "no_relay"
	CodeRelayUnavailable = "relay_unavailable"
	CodeBadCode          = "bad_code"
	CodeUnknownPairing   = "unknown_pairing"
	CodeTooManyPairings  = "too_many_pairings"
)

// PairStatus is the result of the pair_new, pair_redeem and pair_status IPC methods.
type PairStatus = peers.Status

// PeersResult is the result of the "peers" IPC method.
type PeersResult struct {
	Peers []peers.Peer `json:"peers"`
}

// PairRedeemParams are the params of "pair_redeem".
type PairRedeemParams struct {
	Code string `json:"code"`
}

// PairStatusParams are the params of "pair_status".
type PairStatusParams struct {
	PairingID string `json:"pairing_id"`
}

func registerPairing(srv *ipc.Server, m *peers.Manager) {
	srv.Handle("pair_new", func(ctx context.Context, _ json.RawMessage) (any, error) {
		st, err := m.Start(ctx)
		if err != nil {
			return nil, pairError(err)
		}
		return st, nil
	})
	srv.Handle("pair_redeem", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p PairRedeemParams
		if err := json.Unmarshal(params, &p); err != nil || p.Code == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "code is required"}
		}
		st, err := m.Redeem(ctx, p.Code)
		if err != nil {
			return nil, pairError(err)
		}
		return st, nil
	})
	srv.Handle("pair_status", func(_ context.Context, params json.RawMessage) (any, error) {
		var p PairStatusParams
		if err := json.Unmarshal(params, &p); err != nil || p.PairingID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "pairing_id is required"}
		}
		st, ok := m.Get(p.PairingID)
		if !ok {
			return nil, pairError(peers.ErrNotFound)
		}
		return st, nil
	})
	srv.Handle("peers", func(ctx context.Context, _ json.RawMessage) (any, error) {
		list, err := m.List(ctx)
		if err != nil {
			return nil, err
		}
		return PeersResult{Peers: list}, nil
	})
}

// pairError maps a pairing setup failure to an IPC error. Failures after the
// request was sent are reported in the PairStatus instead.
func pairError(err error) error {
	switch {
	case errors.Is(err, peers.ErrNoRelay):
		return &ipc.Error{Code: CodeNoRelay, Message: "the daemon has no relay configured (start agentnetd with --relay)"}
	case errors.Is(err, relayclient.ErrNotConnected):
		return &ipc.Error{Code: CodeRelayUnavailable, Message: "the daemon is not connected to the relay"}
	case errors.Is(err, peers.ErrBadCode):
		return &ipc.Error{Code: CodeBadCode, Message: "not a valid pairing code (10 letters and digits, e.g. 7KQ2M-9XHF4)"}
	case errors.Is(err, peers.ErrNotFound):
		return &ipc.Error{Code: CodeUnknownPairing, Message: "no pairing with that id"}
	case errors.Is(err, peers.ErrTooMany):
		return &ipc.Error{Code: CodeTooManyPairings, Message: "too many pairings in progress"}
	default:
		return err
	}
}
