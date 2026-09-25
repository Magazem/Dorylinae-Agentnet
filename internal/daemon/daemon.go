// Package daemon runs the agentnetd lifecycle: open the store, serve the local
// IPC socket, and record start/stop in the audit log.
package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mailbox"
	"github.com/Magazem/Dorylinae-Agentnet/internal/notify"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/presence"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/session"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
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
	// Approval is the approval channel: "desktop", "terminal" or
	// "terminal-debug" (Docs/protocol/approval.md §Headless machines).
	Approval string `json:"approval"`
	// Git is "ok" or "unsupported: <reason>" (D23, Docs/protocol/grant.md
	// §Serving git): below Git 2.32, git.read grants are refused; fs serving
	// is unaffected.
	Git string `json:"git"`
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
	// NotifyShow overrides the desktop notification channel (a test option).
	// Nil uses notify.Desktop{}.Show.
	NotifyShow notify.ShowFunc
	// ApprovalNotify overrides the approval notification channel (a test
	// option). Nil selects notify.Approval{} or, under
	// DORYLINAE_APPROVAL=terminal, a notifier that writes to Stderr.
	ApprovalNotify approval.Notifier
	// ApprovalNow overrides the approval store's clock (a test option). Nil
	// uses time.Now.
	ApprovalNow func() time.Time
	// ApprovalWindow overrides the approval window runner (a test option: a
	// fake that records id/tag/kind/summary and replies without ever
	// spawning a real dialog process, Docs/protocol/approval.md §The
	// approval window). Nil selects notify.ApprovalWindow{} in desktop mode;
	// terminal mode never has a window regardless of this option.
	ApprovalWindow approval.WindowRunner
	// Stderr is where terminal-mode approval codes are written and where the
	// terminal-required-for-DORYLINAE_APPROVAL=terminal check is made. Nil
	// uses os.Stderr.
	Stderr io.Writer
	// Stdin is where terminal-mode approval answers (`<tag> <code>`,
	// `reject <tag>`) are read from, and where the same terminal-required
	// check is made (Docs/protocol/approval.md §Headless machines). Nil uses
	// os.Stdin.
	Stdin io.Reader
	// OnApprovalReady, if set, is called once with the daemon's approval.Store
	// right after it is built (a test option: 2.2a has no IPC method yet that
	// creates an approval, so tests seed one directly through the store).
	OnApprovalReady func(*approval.Store)
	// Quarantine overrides the work session store's quarantine rule (a test
	// option: 2.4 wires the real rule from grants; until then this lets a
	// test drive a session into quarantined to exercise discard,
	// request-changes-without-release and the mail_inbox blanking they do,
	// Docs/protocol/work-session.md §Quarantine (2.4), D18). Nil never
	// quarantines, the Phase 2.1b default.
	Quarantine func(ctx context.Context, tx *sql.Tx, sid, peer string, round int) (bool, error)
	// OnServerReady, if set, is called once with the daemon's *ipc.Server
	// after every method is registered, just before it starts serving (a
	// test option: lets a test enumerate every registered method,
	// Docs/review/23-phase2-tickets.md 2.2d acceptance).
	OnServerReady func(*ipc.Server)
	// OnStoresReady, if set, is called once with the daemon's capability.Store
	// and worksession.Store right after they are built (a test option: 2.2c
	// lets a test seed a work session directly, without a relay peer to run
	// the accept flow).
	OnStoresReady func(*capability.Store, *worksession.Store)
	// OnDebateReady, if set, is called once with the daemon's debate.Store
	// right after it is built, before any mail is handled (a test option:
	// until 3.1b adds debate_submit, tests drive entries through the store,
	// and may set its Now for the timeout rule).
	OnDebateReady func(*debate.Store)
	// DeviceNow overrides the own-device link's clock (a test option: intents
	// and offers expire after 10 minutes, Docs/protocol/device.md §Link flow).
	// Nil uses time.Now.
	DeviceNow func() time.Time
	// DeviceRunWatch bounds how long a running helper command goes between
	// two re-checks of its scope (a test option: with DeviceNow, a scope
	// that expires on the fake clock stops the run within this time). Zero
	// uses one minute, besides the check at the scope's expiry.
	DeviceRunWatch time.Duration
}

// Run starts the daemon with default options; see RunWithOptions.
func Run(ctx context.Context, p paths.Paths, ready chan<- struct{}) error {
	return RunWithOptions(ctx, p, ready, Options{})
}

// RunWithOptions starts the daemon and blocks until ctx is cancelled or serving fails.
// ready, if non-nil, is closed once the IPC endpoint accepts connections.
func RunWithOptions(ctx context.Context, p paths.Paths, ready chan<- struct{}, opts Options) (err error) {
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	approvalMode, err := resolveApprovalMode(os.Getenv(ApprovalEnv), os.Getenv(DebugEnv) == "1", isTerminal(stderr), isTerminal(stdin))
	if err != nil {
		return err
	}
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

	notifySettings := notify.NewSettings(st.DB())
	webhookKS := webhookKeystore(p.Dir, mailboxMode)
	notifyWebhook := &notify.Webhook{
		Settings: notifySettings,
		Queue:    notify.NewQueue(st.DB()),
		Secret:   webhookKS,
		Audit:    log,
		Log:      opts.Logger,
	}
	notifyTrigger := &notify.Trigger{Settings: notifySettings, Show: opts.NotifyShow, Webhook: notifyWebhook, Audit: log, Log: opts.Logger}
	wctx, stopWebhook := context.WithCancel(ctx)
	whDone := make(chan struct{})
	go func() { defer close(whDone); notifyWebhook.Run(wctx) }()
	defer func() { stopWebhook(); <-whDone }()

	apprNotifier := opts.ApprovalNotify
	var apprWindow approval.WindowRunner
	if approvalMode == ApprovalModeDesktop {
		if apprNotifier == nil {
			apprNotifier = notify.Approval{}
		}
		apprWindow = opts.ApprovalWindow
		if apprWindow == nil {
			apprWindow = notify.ApprovalWindow{}
		}
	} else {
		// Terminal mode has no window at all (Docs/protocol/approval.md
		// §Headless machines): apprWindow stays nil.
		if apprNotifier == nil {
			apprNotifier = terminalNotifier{w: stderr}
		}
	}
	approvalNow := opts.ApprovalNow
	if approvalNow == nil {
		approvalNow = time.Now
	}
	apprStore, err := approval.NewStore(st.DB(), log, apprNotifier, apprWindow, approvalNow)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer apprStore.Close()
	if err := apprStore.ExpireStale(ctx); err != nil {
		_ = ln.Close()
		return err
	}
	if opts.OnApprovalReady != nil {
		opts.OnApprovalReady(apprStore)
	}
	if approvalMode != ApprovalModeDesktop {
		if err := log.Append(ctx, audit.ActorDaemon, "approval.mode", map[string]string{"mode": approvalMode}); err != nil {
			_ = ln.Close()
			return err
		}
		// Best effort: visible on the desktop if there is one, so a restart
		// into this mode by someone else is noticed (Docs/protocol/approval.md
		// §Headless machines, "Visible switch").
		go func() {
			_ = notify.Desktop{}.Show(context.WithoutCancel(ctx), "AgentNet", "AgentNet daemon started with terminal approvals")
		}()
		// Terminal mode reads `<tag> <code>` / `reject <tag>` from the
		// daemon's own stdin (Docs/protocol/approval.md §Headless machines).
		// A desktop-mode daemon never reads its stdin (review 29, M6). The
		// reader is not waited on at shutdown: a blocking Read on a real
		// console's stdin cannot be interrupted by ctx cancellation, so this
		// only stops promptly between lines (tests that want a clean stop
		// close their own Stdin/pipe).
		tctx, stopTerminal := context.WithCancel(ctx)
		go runTerminalApprovalReader(tctx, stdin, stderr, apprStore)
		defer stopTerminal()
	}

	nonLoopbackRelay := relayIsNonLoopback(opts.RelayURL)
	reqStore := newRequestStore(st.DB(), id.Card().Card.PublicKey, outbox, log, teamStore, nonLoopbackRelay, notifyTrigger, peerStore)
	wsStore := &worksession.Store{DB: st.DB(), Self: id.Card().Card.PublicKey, Outbox: outbox, Audit: log, Requests: reqStore, Quarantine: opts.Quarantine}
	// Wiring Sessions makes every request_complete on an accepted request
	// redirect into the session shorthand (Docs/protocol/work-session.md,
	// "request_complete while a session exists"), and newMailReceiver
	// registers ws.* (internal/daemon/mail.go). 2.1b's ws_accept_result /
	// ws_request_changes / ws_discard / ws_cancel IPC (registered below) is
	// what lets a session opened this way ever close again.
	reqStore.Sessions = wsStore
	// Phase 1 fallback trigger (Docs/protocol/work-session.md §Kinds,
	// "Phase 1 requester"; 2.1a review, "For 2.1b" item 2): once an outbox
	// row addressed to a session's peer, carrying ws.result or ws.cancel,
	// ends failed/unsupported_kind, check every open worker-role session with
	// that peer. finish() (internal/mail/outbox.go) calls this outside any
	// transaction, off the outbox's own goroutine.
	outbox.OnFinal = func(_, peer, kind, state, errText string) {
		if state != mail.StateFailed || errText != "unsupported_kind" {
			return
		}
		if kind != worksession.KindResult && kind != worksession.KindCancel {
			return
		}
		go wsStore.CheckPhase1FallbackForPeer(context.WithoutCancel(ctx), peer)
	}
	capStore := &capability.Store{DB: st.DB()}
	// Every grant of a session ends in the same transaction as the session's
	// close (Docs/protocol/grant.md §Session end), on both the grantor (A,
	// closeSessionTx) and the holder (B, the ws.state closed mirror step).
	wsStore.RevokeGrants = func(ctx context.Context, tx *sql.Tx, sid string, now time.Time) error {
		_, err := capStore.RevokeForSessionTx(ctx, tx, sid, capability.ReasonSessionClosed, now)
		return err
	}
	// peers remove revokes all of that peer's grants and policies
	// (Docs/protocol/grant.md §Session end, §Policies).
	// It runs inside the transaction that deletes the peer, on every removal
	// path (peers remove and team GC), review 28 M4.
	// The own-device link (2.D1) ends the same way (Docs/protocol/device.md
	// §Unlink and expiry), and its two mail kinds join the receiver's.
	devStore := &device.Store{DB: st.DB(), Self: id.Card().Card.PublicKey, Now: opts.DeviceNow}
	// The own-device helper (2.D2, Docs/protocol/device.md §Running): every
	// new pending request passes through the router, and in-scope ones run
	// one at a time on the runner's goroutine.
	helper := newHelperRunner(st.DB(), devStore, wsStore, log, id.Card().Card.PublicKey, opts.Logger)
	helper.watchEvery = opts.DeviceRunWatch
	reqStore.Helper = helper
	scopeApprovalsPending := newScopeApprovals()
	onUnlinked := func(peer string) {
		if old := scopeApprovalsPending.take(peer); old != "" {
			_, _ = apprStore.Reject(context.WithoutCancel(ctx), old, "unlinked")
		}
		helper.kick()
	}
	peerStore.OnRemovedTx = chainRemovedTx(revokeForRemovedPeer(capStore), revokeDeviceForRemovedPeer(devStore, helper))
	// A link that became active is announced on the desktop, content-free
	// (D22, review 36 L5): the peer's name and its role.
	onLinked := func(ctx context.Context, peer, role string) {
		if reqStore.Notify != nil {
			reqStore.Notify(ctx, notify.EventDeviceLinked, request.NotifyInfo{Peer: peer, Type: device.ComplementaryRole(role)})
		}
	}
	devHooks := deviceHooks{onUnlinked: onUnlinked, onLinked: onLinked}
	opts.MailKinds = withDeviceKinds(opts.MailKinds, devStore, id.Card().Card.PublicKey, devHooks)
	if err := helper.recoverAfterRestart(ctx); err != nil && opts.Logger != nil {
		// Not fatal: the daemon must still start; a bad queue row only
		// means some controller sessions stay open until cancelled there.
		opts.Logger.Warn("device: recover run queue", "error", err)
	}
	hctx, stopHelper := context.WithCancel(ctx)
	hDone := make(chan struct{})
	go func() { defer close(hDone); helper.loop(hctx) }()
	defer func() { stopHelper(); <-hDone }()
	wireQuarantine(wsStore, capStore, apprStore, reqStore, opts.Quarantine)
	// Debates (Docs/protocol/debate.md, 3.1a): the request type debate, its
	// session kind and the debate.* kinds. The peer-wide quarantine clause
	// is checked at a debate's edges (§Quarantine interplay).
	debates := &debate.Store{
		DB: st.DB(), Self: id.Card().Card.PublicKey, Outbox: outbox, Audit: log,
		Requests: reqStore, Sessions: wsStore, PeerQuarantine: capStore.PeerQuarantineHoldsTx,
	}
	reqStore.Debates = debates
	wsStore.Debate = debates
	if opts.OnDebateReady != nil {
		opts.OnDebateReady(debates)
	}
	if opts.OnStoresReady != nil {
		opts.OnStoresReady(capStore, wsStore)
	}
	stopFetch, gitReason := startFetchServer(sessions, capStore, wsStore, log, id.Card().Card.PublicKey, opts.Logger)
	defer stopFetch()
	fetchClient := startFetchClient(sessions, capStore, wsStore, id.Card().Card.PublicKey)
	defer fetchClient.Close()
	relayClient, stopRelay, err := startRelay(ctx, st.DB(), log, id, ks, pairs, sessions, outbox, opts, teamStore, presenceSender, presenceReceiver, reqStore, wsStore, capStore)
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
	registerRequest(srv, presenceStore, reqStore, peerStore, teamStore, log, nonLoopbackRelay)
	registerLifecycle(srv, reqStore, peerStore, teamStore, wsStore)
	registerSession(srv, wsStore, reqStore, peerStore, teamStore, apprStore, log)
	registerNotify(srv, notifySettings, notify.Desktop{}, notifyWebhook, log)
	registerApproval(srv, apprStore)
	registerGrant(srv, capStore, wsStore, apprStore, peerStore, outbox, log,
		grantIdentity{Self: id.Card().Card.PublicKey, Priv: identityPriv(ks)}, p.Dir, nonLoopbackRelay)
	registerAudit(srv, log)
	registerFetch(srv, fetchClient)
	registerDevice(srv, devStore, apprStore, peerStore, outbox, log, nonLoopbackRelay, helper, scopeApprovalsPending, devHooks)
	registerDeviceScope(srv, devStore, apprStore, peerStore, helper, p.Dir, scopeApprovalsPending)
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
		gitStatus := "ok"
		if gitReason != "" {
			gitStatus = "unsupported: " + gitReason
		}
		res := StatusResult{
			Outbox:        counts,
			PID:           os.Getpid(),
			StartedAt:     started.UTC().Format(time.RFC3339),
			UptimeSeconds: time.Since(started).Seconds(),
			Approval:      approvalMode,
			Version:       version.Version,
			Git:           gitStatus,
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

	if opts.OnServerReady != nil {
		opts.OnServerReady(srv)
	}
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

// webhookKeystore builds the key storage for the webhook secret
// (Docs/protocol/notify.md §Secret): keychain service "dorylinae", account
// scoped per config dir like the identity key (so two daemon homes on one
// machine don't overwrite each other's secret), or file "webhook.key" in the
// config dir, owner-only. mode is the mailbox key's: $DORYLINAE_KEYSTORE, or
// "file" when an injected identity keystore means tests, which must not touch
// the real keychain.
func webhookKeystore(dir, mode string) *keystore.Store {
	file := keystore.NewFile(filepath.Join(dir, "webhook.key"))
	if mode == "file" {
		return keystore.New(file)
	}
	account := "webhook-" + strings.TrimPrefix(keystore.AccountFor(dir), "identity-")
	return keystore.New(keystore.NewKeychain(account), file)
}

// startRelay connects to opts.RelayURL in the background, if set. The returned
// function stops the client and waits for it to exit. The returned *Client is
// nil when there is no relay (RelayURL empty).
func startRelay(ctx context.Context, db *sql.DB, alog *audit.Log, id *identity.Identity, ks *keystore.Store, pairs *peers.Manager, sessions *session.Manager, outbox *mail.Outbox, opts Options, ts *team.Store, psender *presence.Sender, precv *presence.Receiver, rs *request.Store, ws *worksession.Store, caps *capability.Store) (client *relayclient.Client, stop func(), err error) {
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
		rcv, pusher := newMailReceiver(db, alog, ks, pub, opts.MailboxKeys, opts.Logger, ts, rs, ws, caps)
		for k, v := range opts.MailKinds {
			if k != "keys" && k != "ack" {
				rcv.Kinds[k] = v
			}
		}
		handleMail, stopMail = startMail(ctx, rcv, pusher, client, opts.MailboxKeys, outbox)
	}
	// The client outlives ctx until stop: on graceful shutdown the deferred
	// presence goodbye (Docs/protocol/presence.md §Sending) runs after ctx is
	// cancelled and must still find the relay connected.
	rctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
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
