// Package daemon runs the agentnetd lifecycle: open the store, serve the local
// IPC socket, and record start/stop in the audit log.
package daemon

import (
	"context"
	"database/sql"
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
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mailbox"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/session"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
)

// StatusResult is the result of the "status" IPC method.
type StatusResult struct {
	PID           int     `json:"pid"`
	StartedAt     string  `json:"started_at"`
	UptimeSeconds float64 `json:"uptime_seconds"`
	Version       string  `json:"version"`
	// Outbox counts the sender outbox rows by state (Docs/protocol/mail.md §Outbox).
	Outbox mail.OutboxCounts `json:"outbox"`
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
	// Fingerprint is fp(card.public_key) without spaces.
	Fingerprint string `json:"fingerprint"`
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
	// MailboxKeys gives the own mailbox private keys (0.8c, 1.0b). Nil disables
	// the mail receiver: mail envelopes are then ignored.
	MailboxKeys mail.Keys
	// MailKinds are extra mail kinds the receiver understands, beyond keys
	// (Phase 1 registers request, result and so on here). Mail of any other kind
	// is acked as unsupported.
	MailKinds map[string]mail.Kind
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
	peerStore.SetAudit(log)
	idPub, err := envelope.ParseKey(id.Card().Card.PublicKey)
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("agent card public key: %w", err)
	}
	// An injected identity keystore means tests: keep the mailbox key out of the real keychain too.
	mailboxMode := os.Getenv(identity.KeystoreEnv)
	if opts.Keystore != nil {
		mailboxMode = "file"
	}
	mailboxKeys := mailbox.New(p.Dir, mailboxMode, idPub, relayclient.NewKeystoreSigner(ks, idPub).Sign, nil)
	if err := mailboxKeys.Attach(ctx, st.DB(), log, opts.Logger); err != nil {
		_ = ln.Close()
		return err
	}
	if opts.MailboxKeys == nil {
		opts.MailboxKeys = mailboxKeys
	}
	pairs := peers.NewManager(peers.Config{
		Store:   peerStore,
		Audit:   log,
		Card:    cardJSON,
		Self:    id.Card().Card.PublicKey,
		Mailbox: mailboxKeys.Announcement,
		Logger:  opts.Logger,
	})
	defer pairs.Close()
	sessions, err := newSessions(id, ks, log, peerStore, opts)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer sessions.Close()
	outbox := newOutbox(st.DB(), log, ks, opts.Logger)
	teamStore := team.NewStore(st.DB(), peerStore, id.Card().Card.PublicKey)
	teamStore.SetAudit(log)
	teamStore.Outbox = outbox
	teamStore.Announcement = mailboxKeys.Announcement
	teamStore.OwnCard = func() ([]byte, error) { return cardJSON, nil }
	teamStore.Log = opts.Logger
	stopRelay, err := startRelay(ctx, st.DB(), log, id, ks, pairs, sessions, outbox, opts, teamStore)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer stopRelay()
	octx, stopOutbox := context.WithCancel(ctx)
	obDone := make(chan struct{})
	go func() { defer close(obDone); outbox.Run(octx) }()
	defer func() { stopOutbox(); <-obDone }()
	if opts.RelayURL == "" {
		// Without a relay nothing can be pushed, but old keys must still be deleted.
		if rot, ok := opts.MailboxKeys.(ownKeys); ok {
			rctx, stopRotate := context.WithCancel(ctx)
			defer stopRotate()
			go rot.Run(rctx)
		}
	}

	started := time.Now()
	srv := ipc.NewServer()
	registerPairing(srv, pairs)
	registerPing(srv, sessions, peerStore)
	registerTrust(srv, peerStore, log, teamStore)
	registerTeam(srv, teamStore, peerStore, log, id.Card().Card.Name)
	registerMail(srv, outbox, peerStore)
	srv.Handle("identity", func(context.Context, json.RawMessage) (any, error) {
		sc := id.Card()
		fp, err := envelope.KeyFingerprint(sc.Card.PublicKey)
		if err != nil {
			return nil, err
		}
		return IdentityResult{Card: sc.Card, Signature: sc.Signature, KeyBackend: id.KeyBackend(), Fingerprint: fp}, nil
	})
	srv.Handle("status", func(ctx context.Context, _ json.RawMessage) (any, error) {
		counts, err := outbox.Counts(ctx)
		if err != nil {
			return nil, err
		}
		return StatusResult{
			Outbox:        counts,
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
func startRelay(ctx context.Context, db *sql.DB, alog *audit.Log, id *identity.Identity, ks *keystore.Store, pairs *peers.Manager, sessions *session.Manager, outbox *mail.Outbox, opts Options, ts *team.Store) (stop func(), err error) {
	if opts.RelayURL == "" {
		return func() {}, nil
	}
	pub, err := envelope.ParseKey(id.Card().Card.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("agent card public key: %w", err)
	}
	// handleMail is set before Run starts, so the read loop never races it.
	var handleMail func(envelope.Envelope)
	client, err := relayclient.New(relayclient.Config{
		URL:    opts.RelayURL,
		Signer: relayclient.NewKeystoreSigner(ks, pub),
		Logger: opts.Logger,

		OnControl: pairs.HandleControl,
		OnEnvelope: func(e envelope.Envelope) {
			if e.Type == relayclient.MailType {
				if handleMail != nil {
					handleMail(e)
				}
				return
			}
			pairs.HandleEnvelope(e) // pair.confirm comes from a peer that is not paired yet
			sessions.HandleEnvelope(e)
		},
		OnReady: func() { outbox.OnReady(context.Background()) },
		OnError: func(ef envelope.ErrorFrame) {
			outbox.HandleError(ef)
			pairs.HandleError(ef)
			sessions.HandleError(ef)
		},
	})
	if err != nil {
		return nil, err
	}
	pairs.SetSender(client)
	sessions.SetSender(client)
	stopMail := func() {}
	if opts.MailboxKeys != nil {
		rcv, pusher := newMailReceiver(db, alog, ks, pub, opts.MailboxKeys, opts.Logger, ts)
		for k, v := range opts.MailKinds {
			if k != "keys" && k != "ack" {
				rcv.Kinds[k] = v
			}
		}
		handleMail, stopMail = startMail(ctx, rcv, pusher, client, opts.MailboxKeys, outbox)
	}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = client.Run(rctx) // returns only when rctx is cancelled
	}()
	return func() {
		cancel()
		<-done
		stopMail()
	}, nil
}
