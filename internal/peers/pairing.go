package peers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// Audit actions written for pairing. Details carry the pairing id, role,
// the peer's public key or a failure code; never a code or card contents.
const (
	ActionPairStart    = "pair.start"
	ActionPairComplete = "pair.complete"
	ActionPairFail     = "pair.fail"
)

// Pairing states.
const (
	StatePending  = "pending"
	StateComplete = "complete"
	StateFailed   = "failed"
)

// Pairing roles.
const (
	RoleIssuer   = "issuer"   // ran `pair --new`
	RoleRedeemer = "redeemer" // ran `pair <code>`
)

// Failure codes not supplied by the relay.
const (
	FailBadCard = "bad_card"
	FailStore   = "store_error"
	FailTimeout = "timeout"
	FailExpired = "expired"
)

const (
	defaultWait      = time.Second
	noReplyTimeout   = 30 * time.Second
	expiryGrace      = 2 * time.Second
	keepFinished     = time.Hour
	maxPending       = 16
	maxReasonLen     = 200
	auditWriteBudget = 5 * time.Second
	idPrefix         = "pair-"
)

var (
	// ErrNoRelay means the daemon runs without a relay, so it cannot pair.
	ErrNoRelay = errors.New("peers: daemon has no relay configured")
	// ErrBadCode means the code is not a well-formed pairing code.
	ErrBadCode = errors.New("peers: malformed pairing code")
	// ErrNotFound means no pairing has that id.
	ErrNotFound = errors.New("peers: unknown pairing id")
	// ErrTooMany means too many pairings are already in progress.
	ErrTooMany = errors.New("peers: too many pairings in progress")
)

// Failure is why a pairing failed. Code is the relay's error code when the
// relay refused, or one of the Fail* codes.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Status is a snapshot of one pairing.
type Status struct {
	ID    string `json:"pairing_id"`
	Role  string `json:"role"`
	State string `json:"state"`
	// Code and Expires are set for a pending issuer once the relay issued the
	// code; the code is dropped when the pairing ends.
	Code    string   `json:"code,omitempty"`
	Expires string   `json:"expires,omitempty"`
	Peer    *Peer    `json:"peer,omitempty"`
	Error   *Failure `json:"error,omitempty"`
}

// Sender sends control frames to the relay.
type Sender interface {
	SendControl(ctx context.Context, ctl envelope.Control) error
}

// Config configures a Manager.
type Config struct {
	Store *Store
	Audit *audit.Log
	// Card is this agent's signed Agent Card envelope, sent to the relay.
	Card json.RawMessage
	// Sender is the relay connection; nil means the daemon has no relay.
	Sender Sender
	// Wait bounds how long Start and Redeem wait for the relay before returning
	// a still-pending status to poll. Zero means one second.
	Wait time.Duration
	// Now is the clock; nil means time.Now.
	Now    func() time.Time
	Logger *slog.Logger
}

// Manager runs pairings for one daemon. Relay frames are fed to it through
// HandleControl and HandleError.
type Manager struct {
	cfg Config
	log *slog.Logger

	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
}

type session struct {
	st        Status
	codeReady chan struct{} // closed once the issuer has its code, or the pairing ended
	done      chan struct{} // closed when the pairing ends
	timer     *time.Timer
	finished  time.Time
}

// NewManager returns a Manager. cfg.Store, cfg.Audit and cfg.Card are required.
func NewManager(cfg Config) *Manager {
	if cfg.Wait <= 0 {
		cfg.Wait = defaultWait
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	m := &Manager{cfg: cfg, log: cfg.Logger, sessions: map[string]*session{}}
	if m.log == nil {
		m.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return m
}

// SetSender sets the relay connection. Call it before the first pairing.
func (m *Manager) SetSender(s Sender) { m.cfg.Sender = s }

// Close stops pairing timers. Pending pairings stay pending.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for _, s := range m.sessions {
		if s.timer != nil {
			s.timer.Stop()
		}
	}
}

// List returns the paired peers.
func (m *Manager) List(ctx context.Context) ([]Peer, error) { return m.cfg.Store.List(ctx) }

// Get returns the current status of pairing id.
func (m *Manager) Get(id string) (Status, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return Status{}, false
	}
	return s.st, true
}

// Start asks the relay for a pairing code. It returns within the configured
// wait: with the code if the relay answered, otherwise with a pending status
// without a code that Get will fill in.
func (m *Manager) Start(ctx context.Context) (Status, error) {
	// One deadline covers both the send and the wait, so callers return within Wait.
	wctx, cancel := context.WithTimeout(ctx, m.cfg.Wait)
	defer cancel()
	s, err := m.begin(wctx, RoleIssuer, envelope.Control{Op: envelope.OpPairNew})
	if err != nil {
		return Status{}, err
	}
	select {
	case <-s.codeReady:
	case <-wctx.Done():
	}
	return m.snapshot(s), nil
}

// Redeem redeems a code typed by the user. It returns within the configured
// wait: with the final status if the exchange finished, otherwise pending.
func (m *Manager) Redeem(ctx context.Context, rawCode string) (Status, error) {
	code, ok := envelope.NormalizePairCode(rawCode)
	if !ok {
		return Status{}, ErrBadCode
	}
	wctx, cancel := context.WithTimeout(ctx, m.cfg.Wait)
	defer cancel()
	s, err := m.begin(wctx, RoleRedeemer, envelope.Control{Op: envelope.OpPairRedeem, Code: code})
	if err != nil {
		return Status{}, err
	}
	select {
	case <-s.done:
	case <-wctx.Done():
	}
	return m.snapshot(s), nil
}

func (m *Manager) snapshot(s *session) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return s.st
}

// begin registers a session, audits pair.start and sends the request frame.
func (m *Manager) begin(ctx context.Context, role string, req envelope.Control) (*session, error) {
	if m.cfg.Sender == nil {
		return nil, ErrNoRelay
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	req.Ref = id
	req.Card = m.cfg.Card

	s := &session{
		st:        Status{ID: id, Role: role, State: StatePending},
		codeReady: make(chan struct{}),
		done:      make(chan struct{}),
	}
	if err := m.register(s); err != nil {
		return nil, err
	}
	m.audit(ctx, audit.ActorCLI, ActionPairStart, map[string]string{"id": id, "role": role})

	if err := m.cfg.Sender.SendControl(ctx, req); err != nil {
		m.finish(id, StateFailed, nil, &Failure{Code: "relay_unavailable", Message: "could not reach the relay"})
		if errors.Is(err, relayclient.ErrNotConnected) {
			return nil, relayclient.ErrNotConnected
		}
		return nil, fmt.Errorf("send to relay: %w", err)
	}
	return s, nil
}

func (m *Manager) register(s *session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.cfg.Now()
	pending := 0
	for id, o := range m.sessions {
		switch {
		case o.st.State == StatePending:
			pending++
		case now.Sub(o.finished) > keepFinished:
			delete(m.sessions, id)
		}
	}
	if pending >= maxPending {
		return ErrTooMany
	}
	m.sessions[s.st.ID] = s
	m.armLocked(s, noReplyTimeout, FailTimeout, "no reply from the relay")
	return nil
}

// armLocked (re)starts the session's deadline. Caller holds m.mu.
func (m *Manager) armLocked(s *session, d time.Duration, code, msg string) {
	if s.timer != nil {
		s.timer.Stop()
	}
	if m.closed {
		return
	}
	id := s.st.ID
	s.timer = time.AfterFunc(d, func() {
		m.finish(id, StateFailed, nil, &Failure{Code: code, Message: msg})
	})
}

// HandleControl consumes pair_code and pair_peer frames from the relay.
// Other frames are ignored. It is meant for relayclient.Config.OnControl.
func (m *Manager) HandleControl(ctl envelope.Control) {
	switch ctl.Op {
	case envelope.OpPairCode:
		m.onCode(ctl)
	case envelope.OpPairPeer:
		m.onPeer(ctl)
	}
}

// HandleError fails the pairing an error frame refers to. Errors for anything
// else (the ref is not a pairing id) are ignored. It is meant for
// relayclient.Config.OnError.
func (m *Manager) HandleError(e envelope.ErrorFrame) {
	m.finish(e.Ref, StateFailed, nil, &Failure{Code: e.Code, Message: e.Message})
}

func (m *Manager) onCode(ctl envelope.Control) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[ctl.Ref]
	if !ok || s.st.State != StatePending || s.st.Role != RoleIssuer || s.st.Code != "" {
		return
	}
	s.st.Code = ctl.Code
	s.st.Expires = ctl.Expires
	if exp, err := time.Parse(time.RFC3339, ctl.Expires); err == nil {
		m.armLocked(s, max(time.Until(exp), 0)+expiryGrace, FailExpired, "the pairing code expired before it was used")
	}
	close(s.codeReady)
}

func (m *Manager) onPeer(ctl envelope.Control) {
	if st, ok := m.Get(ctl.Ref); !ok || st.State != StatePending {
		return
	}
	sc, err := agentcard.Verify(ctl.Card)
	if err == nil && sc.Card.PublicKey != ctl.PublicKey {
		err = errors.New("agent card key does not match the key the relay authenticated")
	}
	if err != nil {
		m.finish(ctl.Ref, StateFailed, nil, &Failure{Code: FailBadCard, Message: truncate("rejected the peer's Agent Card: " + err.Error())})
		return
	}
	at := m.cfg.Now().UTC().Truncate(time.Second)
	sctx, cancel := context.WithTimeout(context.Background(), auditWriteBudget)
	defer cancel()
	if err := m.cfg.Store.Add(sctx, sc, ctl.Card, at); err != nil {
		m.log.Error("could not store peer", "event", "pair_store_error", "error", err)
		m.finish(ctl.Ref, StateFailed, nil, &Failure{Code: FailStore, Message: "could not store the peer"})
		return
	}
	peer := &Peer{
		PublicKey: sc.Card.PublicKey, Name: sc.Card.Name, Harness: sc.Card.Harness,
		Skills: sc.Card.Skills, PairedAt: at.Format(time.RFC3339),
	}
	m.finish(ctl.Ref, StateComplete, peer, nil)
}

// finish ends a pending pairing exactly once and audits the outcome.
func (m *Manager) finish(id, state string, peer *Peer, fail *Failure) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok || s.st.State != StatePending {
		m.mu.Unlock()
		return
	}
	s.st.State = state
	s.st.Peer = peer
	s.st.Error = fail
	s.st.Code, s.st.Expires = "", ""
	s.finished = m.cfg.Now()
	if s.timer != nil {
		s.timer.Stop()
	}
	select {
	case <-s.codeReady:
	default:
		close(s.codeReady)
	}
	close(s.done)
	role := s.st.Role
	m.mu.Unlock()

	detail := map[string]string{"id": id, "role": role}
	if peer != nil {
		detail["peer"] = peer.PublicKey
		m.audit(context.Background(), audit.ActorDaemon, ActionPairComplete, detail)
		return
	}
	detail["code"] = fail.Code
	detail["reason"] = truncate(fail.Message)
	m.audit(context.Background(), audit.ActorDaemon, ActionPairFail, detail)
}

func (m *Manager) audit(ctx context.Context, actor, action string, detail any) {
	// Audit even if the caller went away, but never block for long.
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditWriteBudget)
	defer cancel()
	if err := m.cfg.Audit.Append(actx, actor, action, detail); err != nil {
		m.log.Error("audit write failed", "event", "audit_error", "action", action, "error", err)
	}
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("peers: generate pairing id: %w", err)
	}
	return idPrefix + hex.EncodeToString(b[:]), nil
}

func truncate(s string) string {
	if len(s) > maxReasonLen {
		return strings.ToValidUTF8(s[:maxReasonLen], "")
	}
	return s
}
