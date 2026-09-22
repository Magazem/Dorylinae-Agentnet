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
	"github.com/Magazem/Dorylinae-Agentnet/internal/presence"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
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
	// Presence is this daemon's own presence, Docs/protocol/ipc.md §status (1.2c).
	Presence PresenceStatus `json:"presence"`
	// Team is the requested team's members and their presence, present only
	// when the "team" param was given (Docs/protocol/ipc.md §status, 1.2c).
	Team *StatusTeamResult `json:"team,omitempty"`
}

// PresenceStatus is the "presence" object of "status", own values only.
type PresenceStatus struct {
	// Mode is the visibility mode (Docs/protocol/presence.md §Visibility);
	// 1.2c only ever reports "visible" (1.3 implements the others).
	Mode string `json:"mode"`
	// Team names the only_team target, omitted otherwise. Unused in 1.2c.
	Team *TeamRef `json:"team,omitempty"`
	// Relay is "connected", "disconnected", "unsupported" (relay has no
	// ephemeral feature) or "none" (no relay configured).
	Relay        string `json:"relay"`
	AgentActive  bool   `json:"agent_active"`
	HumanPresent *bool  `json:"human_present"`
}

// TeamRef names a team without its full summary.
type TeamRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// StatusParams are the params of "status" (1.2c adds "team").
type StatusParams struct {
	Team string `json:"team,omitempty"`
}

// StatusTeamMember is one member of StatusTeamResult, Docs/protocol/ipc.md §status.
type StatusTeamMember struct {
	Name             string  `json:"name"`
	PublicKey        string  `json:"public_key"`
	Fingerprint      string  `json:"fingerprint"`
	Self             bool    `json:"self"`
	Owner            bool    `json:"owner"`
	Trust            *string `json:"trust"`
	DaemonOnline     bool    `json:"daemon_online"`
	AgentActive      bool    `json:"agent_active"`
	HumanPresent     *bool   `json:"human_present"`
	LastSeen         *string `json:"last_seen"`
	AgentLastActive  *string `json:"agent_last_active"`
	HumanLastPresent *string `json:"human_last_present"`
}

// StatusTeamResult is the "team" object of "status" with a "team" param.
type StatusTeamResult struct {
	ID      string             `json:"id"`
	Name    string             `json:"name"`
	Owner   string             `json:"owner"`
	Epoch   int64              `json:"epoch"`
	State   string             `json:"state"`
	Members []StatusTeamMember `json:"members"`
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
	// PresenceInterval overrides the visible-set heartbeat interval formula
	// (Docs/protocol/presence.md §Body, a test option); zero uses
	// max(30, ceil(|visible|/3)) seconds.
	PresenceInterval time.Duration
	// AgentWindow overrides how recently an IPC call must have landed to
	// count as agent-active (Docs/protocol/presence.md §Levels); zero uses
	// presence.DefaultAgentWindow (5 min).
	AgentWindow time.Duration
	// Idle reports OS input idle time for the human-present level
	// (Docs/protocol/presence.md §Idle detection). Nil always reports it
	// unknown (human: 2).
	Idle func(context.Context) (time.Duration, bool)
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

	pdir := peerDirectory{st.DB()}
	presenceStore := presence.NewStore(st.DB())
	presenceSettings := presence.NewSettings(st.DB())
	presenceSender := &presence.Sender{
		Priv:             identityPriv(ks),
		Self:             id.Card().Card.PublicKey,
		Peers:            pdir,
		Team:             teamStore,
		Store:            presenceStore,
		Settings:         presenceSettings,
		Audit:            log,
		Idle:             opts.Idle,
		Log:              opts.Logger,
		PresenceInterval: opts.PresenceInterval,
		AgentWindow:      opts.AgentWindow,
	}
	if err := presenceSender.LoadSettings(ctx); err != nil {
		_ = ln.Close()
		return err
	}
	teamStore.OnMembersChanged = func() { go presenceSender.SyncVisibility(context.Background()) }
	presenceReceiver := &presence.Receiver{
		Opener: &mail.Opener{Self: id.Card().Card.PublicKey, Peers: pdir, Keys: opts.MailboxKeys},
		Store:  presenceStore,
		Log:    opts.Logger,
	}
	presenceReceiver.OnResync = presenceSender.MaybeResync

	nonLoopbackRelay := relayIsNonLoopback(opts.RelayURL)
	reqStore := newRequestStore(st.DB(), id.Card().Card.PublicKey, outbox, log, teamStore, nonLoopbackRelay)
	relayClient, stopRelay, err := startRelay(ctx, st.DB(), log, id, ks, pairs, sessions, outbox, opts, teamStore, presenceSender, presenceReceiver, reqStore)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer stopRelay()
	octx, stopOutbox := context.WithCancel(ctx)
	obDone := make(chan struct{})
	go func() { defer close(obDone); outbox.Run(octx) }()
	defer func() { stopOutbox(); <-obDone }()
	pctx, stopPresence := context.WithCancel(ctx)
	pDone := make(chan struct{})
	go func() { defer close(pDone); presenceSender.Run(pctx) }()
	defer func() { stopPresence(); <-pDone }()
	defer func() {
		gctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		presenceSender.Goodbye(gctx)
	}()
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
	srv.Activity = presenceSender.NoteActivity
	registerPairing(srv, pairs)
	registerPing(srv, sessions, peerStore)
	registerTrust(srv, peerStore, log, teamStore)
	registerTeam(srv, teamStore, peerStore, pairs, log, id.Card().Card.Name)
	registerPresence(srv, presenceSender, teamStore)
	registerMail(srv, outbox, peerStore)
	registerRequest(srv, st.DB(), reqStore, peerStore, teamStore, log, nonLoopbackRelay)
	srv.Handle("identity", func(context.Context, json.RawMessage) (any, error) {
		sc := id.Card()
		fp, err := envelope.KeyFingerprint(sc.Card.PublicKey)
		if err != nil {
			return nil, err
		}
		return IdentityResult{Card: sc.Card, Signature: sc.Signature, KeyBackend: id.KeyBackend(), Fingerprint: fp}, nil
	})
	srv.Handle("status", func(ctx context.Context, params json.RawMessage) (any, error) {
		counts, err := outbox.Counts(ctx)
		if err != nil {
			return nil, err
		}
		var p StatusParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
			}
		}
		res := StatusResult{
			Outbox:        counts,
			PID:           os.Getpid(),
			StartedAt:     started.UTC().Format(time.RFC3339),
			UptimeSeconds: time.Since(started).Seconds(),
			Version:       version.Version,
			Presence:      presenceStatus(ctx, relayClient, opts.RelayURL, presenceSender, teamStore),
		}
		if p.Team != "" {
			tr, err := statusTeam(ctx, teamStore, peerStore, presenceStore, presenceSender, id.Card().Card.Name, relayClient, opts.RelayURL, p.Team)
			if err != nil {
				return nil, err
			}
			res.Team = tr
		}
		return res, nil
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
// function stops the client and waits for it to exit. The returned *Client is
// nil when there is no relay (RelayURL empty).
func startRelay(ctx context.Context, db *sql.DB, alog *audit.Log, id *identity.Identity, ks *keystore.Store, pairs *peers.Manager, sessions *session.Manager, outbox *mail.Outbox, opts Options, ts *team.Store, psender *presence.Sender, precv *presence.Receiver, rs *request.Store) (client *relayclient.Client, stop func(), err error) {
	if opts.RelayURL == "" {
		return nil, func() {}, nil
	}
	pub, err := envelope.ParseKey(id.Card().Card.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("agent card public key: %w", err)
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	// handleMail and client are set before Run starts, so the read loop and
	// the ready callback never race their assignment.
	var handleMail func(envelope.Envelope)
	client, err = relayclient.New(relayclient.Config{
		URL:    opts.RelayURL,
		Signer: relayclient.NewKeystoreSigner(ks, pub),
		Logger: opts.Logger,

		OnControl: pairs.HandleControl,
		OnEnvelope: func(e envelope.Envelope) {
			switch e.Type {
			case relayclient.MailType:
				if handleMail != nil {
					handleMail(e)
				}
			case envelope.TypePresence:
				accepted, edge, _, herr := precv.Handle(context.Background(), e)
				if herr != nil {
					logger.Warn("presence: handle failed", "event", "presence_error", "error", herr)
					return
				}
				if accepted && edge {
					outbox.OnPeerOnline(e.From)
				}
			default:
				pairs.HandleEnvelope(e) // pair.confirm comes from a peer that is not paired yet
				sessions.HandleEnvelope(e)
			}
		},
		OnReady: func() {
			outbox.OnReady(context.Background())
			if client.HasFeature(envelope.FeatureEphemeral) {
				psender.SetSender(client)
			} else {
				psender.SetSender(nil)
			}
			go psender.SendNow(context.Background())
		},
		OnError: func(ef envelope.ErrorFrame) {
			outbox.HandleError(ef)
			pairs.HandleError(ef)
			sessions.HandleError(ef)
		},
	})
	if err != nil {
		return nil, nil, err
	}
	pairs.SetSender(client)
	sessions.SetSender(client)
	stopMail := func() {}
	if opts.MailboxKeys != nil {
		rcv, pusher := newMailReceiver(db, alog, ks, pub, opts.MailboxKeys, opts.Logger, ts, rs)
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
	return client, func() {
		cancel()
		<-done
		stopMail()
	}, nil
}
