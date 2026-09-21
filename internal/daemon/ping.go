package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/noise"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/session"
)

// IPC error codes for ping, documented in Docs/protocol/ipc.md.
const (
	CodeUnknownPeer   = "unknown_peer"
	CodeAmbiguousPeer = "ambiguous_peer"
	CodeUnknownPing   = "unknown_ping"
	CodeTooManyPings  = "too_many_pings"
)

// PingStatus is the result of the ping and ping_status IPC methods.
type PingStatus = session.PingStatus

// PingParams are the params of "ping". Peer is a paired peer's name or public
// key, with or without a leading "@".
type PingParams struct {
	Peer string `json:"peer"`
}

// PingStatusParams are the params of "ping_status".
type PingStatusParams struct {
	PingID string `json:"ping_id"`
}

// newSessions binds a fresh Noise static key to the identity and returns the
// session manager (Docs/protocol/session.md). Only paired peers may talk to it.
func newSessions(id *identity.Identity, ks *keystore.Store, log *audit.Log, ps *peers.Store, opts Options) (*session.Manager, error) {
	pub, err := envelope.ParseKey(id.Card().Card.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("agent card public key: %w", err)
	}
	static, err := noise.NewStatic(pub, relayclient.NewKeystoreSigner(ks, pub).Sign)
	if err != nil {
		return nil, err
	}
	return session.NewManager(session.Config{
		Static: static,
		Audit:  log,
		IsPaired: func(ctx context.Context, key string) (bool, error) {
			list, err := ps.List(ctx)
			if err != nil {
				return false, err
			}
			for _, p := range list {
				if p.PublicKey == key {
					return true, nil
				}
			}
			return false, nil
		},
		Logger: opts.Logger,
	}), nil
}

func registerPing(srv *ipc.Server, m *session.Manager, ps *peers.Store) {
	srv.Handle("ping", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p PingParams
		if err := json.Unmarshal(params, &p); err != nil || strings.TrimPrefix(p.Peer, "@") == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "peer is required"}
		}
		peer, err := resolvePeer(ctx, ps, p.Peer)
		if err != nil {
			return nil, err
		}
		st, err := m.Ping(ctx, session.PeerRef{PublicKey: peer.PublicKey, Name: peer.Name})
		if err != nil {
			return nil, pingError(err)
		}
		return st, nil
	})
	srv.Handle("ping_status", func(_ context.Context, params json.RawMessage) (any, error) {
		var p PingStatusParams
		if err := json.Unmarshal(params, &p); err != nil || p.PingID == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "ping_id is required"}
		}
		st, ok := m.Get(p.PingID)
		if !ok {
			return nil, pingError(session.ErrNotFound)
		}
		return st, nil
	})
}

// resolvePeer finds a paired peer by public key or, case-insensitively, by name.
func resolvePeer(ctx context.Context, ps *peers.Store, ref string) (peers.Peer, error) {
	ref = strings.TrimPrefix(ref, "@")
	list, err := ps.List(ctx)
	if err != nil {
		return peers.Peer{}, err
	}
	var byName []peers.Peer
	for _, p := range list {
		if p.PublicKey == ref {
			return p, nil
		}
		if strings.EqualFold(p.Name, ref) {
			byName = append(byName, p)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return peers.Peer{}, &ipc.Error{Code: CodeUnknownPeer, Message: fmt.Sprintf("no paired peer named %q (see 'agentnet peers')", ref)}
	default:
		keys := make([]string, len(byName))
		for i, p := range byName {
			keys[i] = p.PublicKey
		}
		return peers.Peer{}, &ipc.Error{Code: CodeAmbiguousPeer, Message: fmt.Sprintf("several peers are named %q; use a public key: %s", ref, strings.Join(keys, ", "))}
	}
}

func pingError(err error) error {
	switch {
	case errors.Is(err, session.ErrNoRelay):
		return &ipc.Error{Code: CodeNoRelay, Message: "the daemon has no relay configured (start agentnetd with --relay)"}
	case errors.Is(err, relayclient.ErrNotConnected):
		return &ipc.Error{Code: CodeRelayUnavailable, Message: "the daemon is not connected to the relay"}
	case errors.Is(err, session.ErrNotFound):
		return &ipc.Error{Code: CodeUnknownPing, Message: "no ping with that id"}
	case errors.Is(err, session.ErrTooMany):
		return &ipc.Error{Code: CodeTooManyPings, Message: "too many pings in progress"}
	default:
		return err
	}
}
