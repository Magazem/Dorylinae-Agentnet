package presence

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"math"
	mrand "math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// DefaultAgentWindow is how recently an IPC call must have landed for the
// sender to report agent: 1, Docs/protocol/presence.md §Levels.
const DefaultAgentWindow = 5 * time.Minute

// resyncCooldown is how often the same team/peer pair may be resent a roster
// on a stale epoch report, Docs/protocol/presence.md §Receiving step 7.
const resyncCooldown = 10 * time.Minute

// EnvelopeSender is the relay connection the sender hands sealed heartbeats
// to (satisfied by *relayclient.Client).
type EnvelopeSender interface {
	Send(ctx context.Context, e envelope.Envelope) error
}

// MailboxLookup answers a peer's newest mailbox public key, like
// internal/daemon's peerDirectory.
type MailboxLookup interface {
	MailboxPub(peer string) ([]byte, bool)
}

// Sender runs the presence sender loop of Docs/protocol/presence.md
// §Sending: ticks, the visible set, agent/human edges, goodbye, and the
// owner-side roster resync of §Receiving step 7.
type Sender struct {
	// Priv loads the own identity key. The sender clears it after each use.
	Priv func() (ed25519.PrivateKey, error)
	// Self is this daemon's own identity key, wire form.
	Self  string
	Peers MailboxLookup
	Team  *team.Store
	Store *Store
	// Settings persists the visibility mode and human-share flag (1.3),
	// Docs/protocol/presence.md §Visibility and §Human sharing. Nil skips
	// persistence (tests): the mode still applies in memory for the process
	// lifetime.
	Settings *Settings
	// Audit records presence.mode and presence.human (1.3),
	// Docs/protocol/presence.md §Audit. Nil skips auditing.
	Audit *audit.Log
	// Idle reports OS input idle time; nil always reports human: 2 (unknown).
	Idle func(context.Context) (time.Duration, bool)
	Now  func() time.Time
	Log  *slog.Logger

	// PresenceInterval overrides the visible-set interval formula when set,
	// the test option of Docs/protocol/presence.md §Body.
	PresenceInterval time.Duration
	// AgentWindow overrides DefaultAgentWindow for tests.
	AgentWindow time.Duration

	mu           sync.Mutex
	boot         string
	seqs         map[string]int64
	lastActivity time.Time
	visible      map[string]bool
	lastResync   map[string]time.Time
	// mode is the visibility mode ("" behaves like ModeVisible, the 1.2c
	// default); onlyTeam is the team id for ModeOnlyTeam. Loaded from
	// Settings by LoadSettings and updated by SetMode.
	mode       string
	onlyTeam   string
	humanSet   bool // true once humanShare has been loaded or set
	humanShare bool
	// sendMu serializes every visibleSet-compute-then-send operation
	// (sendAll, SyncVisibility, Goodbye), so a concurrent tick and a mode
	// change can never race: whichever acquires the lock second always
	// computes the visible set under the other's effect. Without this, a
	// tick already past its visibleSet() call under the old mode could still
	// reach a peer, with a fresher seq, after that peer's goodbye from a
	// mode change that logically happened later, undoing it.
	sendMu sync.Mutex
	// sender is the relay connection; nil counts as not connected, and a
	// heartbeat is silently dropped (Docs/protocol/presence.md §Sending). Set
	// through SetSender, guarded by mu since Run reads it concurrently with
	// the relay's ready/reconnect callbacks.
	sender EnvelopeSender
}

// SetSender sets the relay connection heartbeats are sent over. Pass nil to
// turn presence off (no relay, or a relay without the ephemeral feature):
// Docs/protocol/presence.md §Sending, "The daemon sends presence only when
// the relay advertised the ephemeral feature."
func (s *Sender) SetSender(es EnvelopeSender) {
	s.mu.Lock()
	s.sender = es
	s.mu.Unlock()
}

func (s *Sender) getSender() EnvelopeSender {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sender
}

// AgentActive reports this daemon's own agent-active value: an IPC call
// landed within the agent window (Docs/protocol/presence.md §Levels).
func (s *Sender) AgentActive() bool { return s.agentFlag(s.now()) == 1 }

// HumanPresent reports this daemon's own human-present value, detected
// locally; nil when unknown or not shared (1.2c always shares).
func (s *Sender) HumanPresent(ctx context.Context) *bool {
	switch s.humanFlag(ctx) {
	case 1:
		v := true
		return &v
	case 0:
		v := false
		return &v
	default:
		return nil
	}
}

func (s *Sender) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sender) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Sender) agentWindow() time.Duration {
	if s.AgentWindow > 0 {
		return s.AgentWindow
	}
	return DefaultAgentWindow
}

func (s *Sender) bootID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.boot == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b) // crypto/rand.Read never fails
		s.boot = hex.EncodeToString(b)
	}
	return s.boot
}

func (s *Sender) nextSeq(peer string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seqs == nil {
		s.seqs = map[string]int64{}
	}
	s.seqs[peer]++
	return s.seqs[peer]
}

// NoteActivity records that an IPC call just reached the daemon. If the
// previous call was 5 min or more ago (or there was none), this is the agent
// edge of Docs/protocol/presence.md §Sending, and an immediate heartbeat goes
// out to the whole visible set, asynchronously.
func (s *Sender) NoteActivity() {
	now := s.now()
	s.mu.Lock()
	prev := s.lastActivity
	edge := prev.IsZero() || now.Sub(prev) >= s.agentWindow()
	s.lastActivity = now
	s.mu.Unlock()
	if edge {
		go s.sendAll(context.Background(), "online")
	}
}

func (s *Sender) agentFlag(now time.Time) int {
	s.mu.Lock()
	last := s.lastActivity
	s.mu.Unlock()
	if !last.IsZero() && now.Sub(last) < s.agentWindow() {
		return 1
	}
	return 0
}

func (s *Sender) humanFlag(ctx context.Context) int {
	if !s.humanShareEnabled() {
		return 2
	}
	if s.Idle == nil {
		return 2
	}
	d, ok := s.Idle(ctx)
	if !ok {
		return 2
	}
	if d < 10*time.Minute {
		return 1
	}
	return 0
}

// humanShareEnabled reports the presence.human share flag
// (Docs/protocol/presence.md §Human sharing), default true until
// LoadSettings or SetHumanShare has run.
func (s *Sender) humanShareEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.humanSet {
		return true
	}
	return s.humanShare
}

// visibleSet is the recipients of the next heartbeat, Docs/protocol/
// presence.md §Sending "Recipients": every peer sharing an active team with
// self in mode `visible` (the default), members of the target team in mode
// `only_team`, or nobody in mode `invisible`. Peers without a mailbox key
// are skipped.
func (s *Sender) visibleSet(ctx context.Context) ([]string, error) {
	s.checkTeamGone(ctx)
	vm := s.Mode()

	switch vm.Mode {
	case ModeInvisible:
		return nil, nil
	case ModeOnlyTeam:
		members, err := s.Team.Members(ctx, vm.Team)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(members))
		for _, m := range members {
			if m.Key == s.Self {
				continue
			}
			if _, ok := s.Peers.MailboxPub(m.Key); ok {
				out = append(out, m.Key)
			}
		}
		sort.Strings(out)
		return out, nil
	default: // ModeVisible, and "" (the 1.2c default before LoadSettings runs)
		teams, err := s.Team.List(ctx)
		if err != nil {
			return nil, err
		}
		set := map[string]bool{}
		for _, t := range teams {
			if t.State != team.StateActive {
				continue
			}
			members, err := s.Team.Members(ctx, t.ID)
			if err != nil {
				return nil, err
			}
			for _, m := range members {
				if m.Key == s.Self {
					continue
				}
				set[m.Key] = true
			}
		}
		out := make([]string, 0, len(set))
		for k := range set {
			if _, ok := s.Peers.MailboxPub(k); ok {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		return out, nil
	}
}

// checkTeamGone auto-degrades an only_team mode to invisible when the target
// team is no longer active on this daemon (left, removed or dissolved),
// Docs/protocol/presence.md §Sending "only_team": persists the change,
// audits {mode: invisible, reason: team_gone} as actor daemon, and updates
// the cache so visibleSet sees it immediately. Called before every
// visible-set computation.
func (s *Sender) checkTeamGone(ctx context.Context) {
	mode := s.Mode()
	if mode.Mode != ModeOnlyTeam {
		return
	}
	gone := true
	if s.Team != nil {
		if t, err := s.Team.Get(ctx, mode.Team); err == nil && t.State == team.StateActive {
			gone = false
		}
	}
	if !gone {
		return
	}
	now := s.now()
	if s.Settings != nil {
		if err := s.Settings.SetMode(ctx, VisibilityMode{Mode: ModeInvisible}, now); err != nil {
			s.log().Warn("presence: persist auto-invisible", "event", "presence_error", "error", err)
		}
	}
	s.mu.Lock()
	s.mode, s.onlyTeam = ModeInvisible, ""
	s.mu.Unlock()
	if s.Audit != nil {
		if err := s.Audit.Append(ctx, audit.ActorDaemon, ActionMode, map[string]any{"mode": ModeInvisible, "reason": "team_gone"}); err != nil {
			s.log().Warn("presence: audit auto-invisible", "event", "presence_error", "error", err)
		}
	}
}

// LoadSettings loads the persisted visibility mode and human-share flag into
// the sender's cache. Call it once before Run, after Settings is set.
func (s *Sender) LoadSettings(ctx context.Context) error {
	if s.Settings == nil {
		return nil
	}
	m, err := s.Settings.GetMode(ctx)
	if err != nil {
		return err
	}
	share, err := s.Settings.GetHumanShare(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.mode, s.onlyTeam = m.Mode, m.Team
	s.humanShare, s.humanSet = share, true
	s.mu.Unlock()
	return nil
}

// Mode returns the current visibility mode.
func (s *Sender) Mode() VisibilityMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	mode := s.mode
	if mode == "" {
		mode = ModeVisible
	}
	return VisibilityMode{Mode: mode, Team: s.onlyTeam}
}

// SetMode applies a new visibility mode: persists it, updates the cache,
// audits presence.mode as actor cli, and sends the goodbye/online diff via
// SyncVisibility (Docs/protocol/presence.md §Visibility). The caller
// resolves and validates the team id (ModeOnlyTeam) before calling.
func (s *Sender) SetMode(ctx context.Context, mode VisibilityMode) error {
	now := s.now()
	if s.Settings != nil {
		if err := s.Settings.SetMode(ctx, mode, now); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.mode, s.onlyTeam = mode.Mode, mode.Team
	s.mu.Unlock()
	if s.Audit != nil {
		detail := map[string]any{"mode": mode.Mode}
		if mode.Mode == ModeOnlyTeam {
			detail["team"] = mode.Team
		}
		if err := s.Audit.Append(ctx, audit.ActorCLI, ActionMode, detail); err != nil {
			return err
		}
	}
	s.SyncVisibility(ctx)
	return nil
}

// HumanShare reports the current presence.human share flag.
func (s *Sender) HumanShare() bool { return s.humanShareEnabled() }

// SetHumanShare applies a new presence.human share flag: persists it,
// updates the cache, and audits presence.human as actor cli
// (Docs/protocol/presence.md §Human sharing). It does not itself trigger a
// heartbeat; the change takes effect on the next one.
func (s *Sender) SetHumanShare(ctx context.Context, share bool) error {
	now := s.now()
	if s.Settings != nil {
		if err := s.Settings.SetHumanShare(ctx, share, now); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.humanShare, s.humanSet = share, true
	s.mu.Unlock()
	if s.Audit != nil {
		if err := s.Audit.Append(ctx, audit.ActorCLI, ActionHuman, map[string]any{"share": share}); err != nil {
			return err
		}
	}
	return nil
}

// epochsFor is the epochs member of a heartbeat to recipient: this daemon's
// locally held epoch of every active team it holds that recipient owns
// (Docs/protocol/presence.md §Body).
func (s *Sender) epochsFor(ctx context.Context, recipient string) map[string]int64 {
	teams, err := s.Team.List(ctx)
	if err != nil {
		return nil
	}
	out := map[string]int64{}
	for _, t := range teams {
		if t.State == team.StateActive && t.Owner == recipient {
			out[t.ID] = t.Epoch
			if len(out) >= maxEpochs {
				break
			}
		}
	}
	return out
}

// intervalSeconds is the heartbeat interval, Docs/protocol/presence.md
// §Sending: max(30, ceil(|visible set| / 3)), overridden by PresenceInterval.
func (s *Sender) intervalSeconds(visibleCount int) int {
	if s.PresenceInterval > 0 {
		sec := int(s.PresenceInterval / time.Second)
		if sec < minInterval {
			sec = minInterval
		}
		return sec
	}
	n := int(math.Ceil(float64(visibleCount) / 3))
	if n < 30 {
		n = 30
	}
	if n > maxInterval {
		n = maxInterval
	}
	return n
}

// nextTick is the jittered tick delay, interval x U(0.9, 1.1).
func (s *Sender) nextTick(visibleCount int) time.Duration {
	base := s.intervalSeconds(visibleCount)
	jitter := 0.9 + 0.2*mrand.Float64() //nolint:gosec // scheduling jitter needs no crypto randomness
	return time.Duration(float64(base) * float64(time.Second) * jitter)
}

// sendOne seals and sends one heartbeat to peer. A failed Send (not
// connected, or no relay yet) is dropped without retry.
func (s *Sender) sendOne(ctx context.Context, peer, state string) {
	es := s.getSender()
	if es == nil {
		return
	}
	pub, ok := s.Peers.MailboxPub(peer)
	if !ok {
		return
	}
	priv, err := s.Priv()
	if err != nil {
		s.log().Warn("presence: load identity key", "event", "presence_error", "error", err)
		return
	}
	defer clear(priv)
	now := s.now()
	body := Body{State: state, Boot: s.bootID(), Seq: s.nextSeq(peer), Interval: s.intervalSeconds(len(s.currentVisible()))}
	if state == "online" {
		body.Agent = s.agentFlag(now)
		body.Human = s.humanFlag(ctx)
	} else {
		body.Agent = 0
		body.Human = 2
	}
	body.Epochs = s.epochsFor(ctx, peer)
	sl, err := Seal(SealInput{Priv: priv, To: peer, MailboxPub: pub, Created: now, Body: body})
	if err != nil {
		s.log().Warn("presence: seal", "event", "presence_error", "error", err)
		return
	}
	from := envelope.KeyString(priv.Public().(ed25519.PublicKey))
	e := envelope.Envelope{
		From: from, To: peer, Type: envelope.TypePresence, ID: sl.ID,
		TS: now.UTC().Format(time.RFC3339), Payload: sl.Payload,
	}
	if err := es.Send(ctx, e); err != nil {
		s.log().Debug("presence: send failed", "event", "presence_send_failed", "error", err)
	}
}

func (s *Sender) currentVisible() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.visible
}

// sendAll sends state to the whole current visible set.
func (s *Sender) sendAll(ctx context.Context, state string) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	peers, err := s.visibleSet(ctx)
	if err != nil {
		s.log().Warn("presence: visible set", "event", "presence_error", "error", err)
		return
	}
	for _, p := range peers {
		s.sendOne(ctx, p, state)
	}
}

// SendNow sends an immediate online heartbeat to the whole visible set, for
// the relay-ready hook of Docs/protocol/presence.md §Sending.
func (s *Sender) SendNow(ctx context.Context) { s.sendAll(ctx, "online") }

// SyncVisibility recomputes the visible set and sends an immediate online
// heartbeat to newly visible peers and a goodbye to peers that left it
// (Docs/protocol/presence.md §Sending "Immediately to one peer" and
// "Goodbye"). Call it after any team membership change.
func (s *Sender) SyncVisibility(ctx context.Context) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	list, err := s.visibleSet(ctx)
	if err != nil {
		s.log().Warn("presence: visible set", "event", "presence_error", "error", err)
		return
	}
	next := make(map[string]bool, len(list))
	for _, p := range list {
		next[p] = true
	}
	s.mu.Lock()
	old := s.visible
	s.visible = next
	s.mu.Unlock()
	for p := range next {
		if !old[p] {
			s.sendOne(ctx, p, "online")
		}
	}
	for p := range old {
		if !next[p] {
			s.sendOne(ctx, p, "offline")
		}
	}
}

// Run is the tick loop. It returns when ctx is cancelled.
func (s *Sender) Run(ctx context.Context) {
	s.SyncVisibility(ctx)
	peers, _ := s.visibleSet(ctx)
	t := time.NewTimer(s.nextTick(len(peers)))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sendAll(ctx, "online")
			peers, _ = s.visibleSet(ctx)
			t.Reset(s.nextTick(len(peers)))
		}
	}
}

// Goodbye sends state: offline to the whole current visible set, best
// effort, bounded to about 1 s in total (Docs/protocol/presence.md §Sending
// "Goodbye", graceful shutdown).
func (s *Sender) Goodbye(ctx context.Context) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	gctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	s.mu.Lock()
	peers := make([]string, 0, len(s.visible))
	for p := range s.visible {
		peers = append(peers, p)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range peers {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			s.sendOne(gctx, p, "offline")
		}(p)
	}
	wg.Wait()
}

// MaybeResync applies Docs/protocol/presence.md §Receiving step 7: for each
// team this daemon owns named in epochs, if the reported epoch is behind and
// no resync was sent to peer for that team in the last 10 minutes, resend the
// roster (the owner-only roster if peer is no longer a member).
func (s *Sender) MaybeResync(peer string, epochs map[string]int64) {
	if s.Team == nil || len(epochs) == 0 {
		return
	}
	ctx := context.Background()
	now := s.now()
	for teamID, epoch := range epochs {
		t, err := s.Team.Get(ctx, teamID)
		if err != nil || t.Owner != s.Self || epoch >= t.Epoch {
			continue
		}
		key := teamID + "|" + peer
		s.mu.Lock()
		last, seen := s.lastResync[key]
		stale := !seen || now.Sub(last) >= resyncCooldown
		if stale {
			if s.lastResync == nil {
				s.lastResync = map[string]time.Time{}
			}
			s.lastResync[key] = now
		}
		s.mu.Unlock()
		if !stale {
			continue
		}
		if err := s.Team.ResyncMember(ctx, teamID, peer); err != nil {
			s.log().Warn("presence: roster resync failed", "event", "presence_error", "error", err)
		}
	}
}
