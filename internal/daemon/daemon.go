// Package daemon runs the agentnetd lifecycle: open the store, serve the local
// IPC socket, and record start/stop in the audit log.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/session"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
)

// StatusResult is the result of the "status" IPC method.
type StatusResult struct {
	PID           int     `json:"pid"`
	StartedAt     string  `json:"started_at"`
	UptimeSeconds float64 `json:"uptime_seconds"`
	Version       string  `json:"version"`
}

type auditDetail struct {
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

// IdentityResult is the result of the "identity" IPC method: the signed Agent
// Card and where the private key is kept. The key itself is never included.
type IdentityResult struct {
	Card       agentcard.Card `json:"card"`
	Signature  string         `json:"signature"`
	KeyBackend string         `json:"key_backend"`
}

// Options tune the daemon; the zero value is what agentnetd uses.
type Options struct {
	// Keystore holds the identity key. Nil selects it from $DORYLINAE_KEYSTORE.
	Keystore *keystore.Store
	// Identity gives the card fields for a new identity. Nil reads the environment.
	Identity *identity.Options
	// RelayURL is the relay to keep a persistent connection to, e.g.
	// ws://127.0.0.1:8787. Empty runs the daemon without a relay.
	RelayURL string
	// Logger receives relay connection events. Nil discards them.
	Logger *slog.Logger
}

// Run starts the daemon with default options; see RunWithOptions.
func Run(ctx context.Context, p paths.Paths, ready chan<- struct{}) error {
	return RunWithOptions(ctx, p, ready, Options{})
}

// RunWithOptions starts the daemon and blocks until ctx is cancelled or serving fails.
// ready, if non-nil, is closed once the IPC endpoint accepts connections.
func RunWithOptions(ctx context.Context, p paths.Paths, ready chan<- struct{}, opts Options) (err error) {
	if err := p.Ensure(); err != nil {
		return err
	}
	st, err := store.Open(ctx, p.DB)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := st.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("close store: %w", cerr)
		}
	}()

	// Listen before auditing so a losing second instance leaves no daemon.start row.
	ln, err := ipc.Listen(p.Endpoint)
	if err != nil {
		return err
	}

	log := audit.New(st.DB())
	detail := auditDetail{PID: os.Getpid(), Version: version.Version}
	if err := log.Append(ctx, audit.ActorDaemon, audit.ActionDaemonStart, detail); err != nil {
		_ = ln.Close()
		return err
	}
	defer func() {
		// ctx is already cancelled at shutdown, so use a fresh bounded one.
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if aerr := log.Append(sctx, audit.ActorDaemon, audit.ActionDaemonStop, detail); err == nil && aerr != nil {
			err = aerr
		}
	}()

	// Only the daemon that won the endpoint creates the identity.
	id, ks, err := loadIdentity(ctx, p, log, opts)
	if err != nil {
		_ = ln.Close()
		return err
	}
	cardJSON, err := json.Marshal(id.Card())
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("encode agent card: %w", err)
	}
	peerStore := peers.NewStore(st.DB())
	pairs := peers.NewManager(peers.Config{
		Store:  peerStore,
		Audit:  log,
		Card:   cardJSON,
		Logger: opts.Logger,
	})
	defer pairs.Close()
	sessions, err := newSessions(id, ks, log, peerStore, opts)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer sessions.Close()
	stopRelay, err := startRelay(ctx, id, ks, pairs, sessions, opts)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer stopRelay()

	started := time.Now()
	srv := ipc.NewServer()
	registerPairing(srv, pairs)
	registerPing(srv, sessions, peerStore)
	srv.Handle("identity", func(context.Context, json.RawMessage) (any, error) {
		sc := id.Card()
		return IdentityResult{Card: sc.Card, Signature: sc.Signature, KeyBackend: id.KeyBackend()}, nil
	})
	srv.Handle("status", func(context.Context, json.RawMessage) (any, error) {
		return StatusResult{
			PID:           os.Getpid(),
			StartedAt:     started.UTC().Format(time.RFC3339),
			UptimeSeconds: time.Since(started).Seconds(),
			Version:       version.Version,
		}, nil
	})

	if ready != nil {
		close(ready)
	}
	return srv.Serve(ctx, ln)
}

// loadIdentity loads or creates the agent identity and audits a creation.
func loadIdentity(ctx context.Context, p paths.Paths, log *audit.Log, opts Options) (*identity.Identity, *keystore.Store, error) {
	ks := opts.Keystore
	if ks == nil {
		var err error
		if ks, err = identity.NewKeystoreFromEnv(p.Dir); err != nil {
			return nil, nil, err
		}
	}
	iopts := identity.OptionsFromEnv()
	if opts.Identity != nil {
		iopts = *opts.Identity
	}
	id, rep, err := identity.LoadOrCreate(p.Dir, ks, iopts, time.Now())
	if err != nil {
		return nil, nil, err
	}
	if rep.Created {
		if err := log.Append(ctx, audit.ActorDaemon, identity.ActionCreate, rep.Detail); err != nil {
			return nil, nil, err
		}
	}
	return id, ks, nil
}

// startRelay connects to opts.RelayURL in the background, if set. The returned
// function stops the client and waits for it to exit.
func startRelay(ctx context.Context, id *identity.Identity, ks *keystore.Store, pairs *peers.Manager, sessions *session.Manager, opts Options) (stop func(), err error) {
	if opts.RelayURL == "" {
		return func() {}, nil
	}
	pub, err := envelope.ParseKey(id.Card().Card.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("agent card public key: %w", err)
	}
	client, err := relayclient.New(relayclient.Config{
		URL:    opts.RelayURL,
		Signer: relayclient.NewKeystoreSigner(ks, pub),
		Logger: opts.Logger,

		OnControl:  pairs.HandleControl,
		OnEnvelope: sessions.HandleEnvelope,
		OnError: func(ef envelope.ErrorFrame) {
			pairs.HandleError(ef)
			sessions.HandleError(ef)
		},
	})
	if err != nil {
		return nil, err
	}
	pairs.SetSender(client)
	sessions.SetSender(client)
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = client.Run(rctx) // returns only when rctx is cancelled
	}()
	return func() {
		cancel()
		<-done
	}, nil
}
