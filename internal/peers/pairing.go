package peers

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
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
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// Audit actions written for pairing. Details carry the pairing id, role,
// the peer's public key or a failure code; never a code, lookup, secret, key,
// tag, card or announcement.
const (
	ActionPairStart       = "pair.start"
	ActionPairComplete    = "pair.complete"
	ActionPairFail        = "pair.fail"
	ActionPairAttemptFail = "pair.attempt_fail"
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
	FailBadCard        = "bad_card"
	FailBadMbox        = "bad_mbox"
	FailBadConfirm     = "bad_confirm"
	FailConfirmTimeout = "confirm_timeout"
	FailRelayV1        = "relay_v1"
	FailCodeUsed       = "code_used"
	FailStore          = "store_error"
	FailTimeout        = "timeout"
	FailExpired        = "expired"
	failUnavailable    = "relay_unavailable"
)

// ConfirmType is the envelope type of the pairing confirmation.
const ConfirmType = "pair.confirm"

const (
	defaultWait      = time.Second
	noReplyTimeout   = 30 * time.Second
	defaultCodeTTL   = 10 * time.Minute
	defaultConfirm   = 60 * time.Second
	keepFinished     = time.Hour
	maxPending       = 16
	maxAttempts      = 3
	maxLookupRetries = 3
	maxReasonLen     = 200
	maxCodeLen       = 64
	auditWriteBudget = 5 * time.Second
	sendBudget       = 5 * time.Second
	idPrefix         = "pair-"
)

var (
	// ErrNoRelay means the daemon runs without a relay, so it cannot pair.
	ErrNoRelay = errors.New("peers: daemon has no relay configured")
	// ErrBadCode means the code is not a well-formed pairing code.
	ErrBadCode = errors.New("peers: malformed pairing code")
	// ErrNeedV1 means the code is a 10-character v1 code, which is redeemed only with the v1 flag.
	ErrNeedV1 = errors.New("peers: a 10-character code is a legacy v1 code; redeem it with --v1")
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
	// Code and Expires are set for a pending issuer once the relay acknowledged
	// the code; the code is dropped when the pairing ends.
	Code    string   `json:"code,omitempty"`
	Expires string   `json:"expires,omitempty"`
	Peer    *Peer    `json:"peer,omitempty"`
	Error   *Failure `json:"error,omitempty"`
}

// Sender sends frames to the relay (a *relayclient.Client).
type Sender interface {
	SendControl(ctx context.Context, ctl envelope.Control) error
	Send(ctx context.Context, e envelope.Envelope) error
}

// Config configures a Manager.
type Config struct {
	Store *Store
	Audit *audit.Log
	// Card is this agent's signed Agent Card envelope, sent to the relay.
	Card json.RawMessage
	// Self is this daemon's identity key in wire form (the from of pair.confirm).
	Self string
	// Mailbox returns this daemon's current signed mailbox key announcement,
	// creating the first key if there is none. It is required for v2 pairing.
	Mailbox func() ([]byte, error)
	// Sender is the relay connection; nil means the daemon has no relay.
	Sender Sender
	// Wait bounds how long Start and Redeem wait for the relay before returning
	// a still-pending status to poll. Zero means one second.
	Wait time.Duration
	// CodeTTL is the issuer's local code lifetime; zero means 10 minutes.
	CodeTTL time.Duration
	// ConfirmWait is how long each side waits for the peer's tag; zero means 60 seconds.
	ConfirmWait time.Duration
	// RelayWait is how long a request may go without any relay reply; zero means 30 seconds.
	RelayWait time.Duration
	// Now is the clock; nil means time.Now.
	Now    func() time.Time
	Logger *slog.Logger
}

// Manager runs pairings for one daemon. Relay frames are fed to it through
// HandleControl, HandleEnvelope and HandleError.
type Manager struct {
	cfg Config
	log *slog.Logger

	ownCard    []byte // canonical {"card","signature"} of cfg.Card
	ownCardErr error

	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
}

// kderiv is one Argon2id derivation running in the background.
type kderiv struct {
	ready chan struct{} // closed when the derivation finished or was abandoned
	k     []byte        // guarded by Manager.mu; nil once wiped
}

// attempt is one pair_peer an issuer accepted for processing.
type attempt struct {
	peer     string
	lookup   string
	sc       *agentcard.Signed
	rawCard  []byte
	mbox     []byte // canonical verified announcement of the peer
	transcr  [sha256.Size]byte
	waiting  bool // waiting for the peer's tag
	accepted bool // a pair.confirm was accepted (its tag is being checked)
	over     bool // failed or abandoned
	timer    *time.Timer
}

type session struct {
	st        Status
	v1        bool
	tag       Tag           // caller-supplied, delivered to a Completer once the session ends
	codeReady chan struct{} // closed once the issuer has its code, or the pairing ended
	done      chan struct{} // closed when the pairing ends
	settled   chan struct{} // closed after done, once the end is audited
	timer     *time.Timer
	finished  time.Time

	// v2 state.
	lookup   string
	secret   []byte // wiped when the pairing ends
	usedHash []byte // redeemer: hash recorded before the tag is sent
	kd       *kderiv
	ownMbox  []byte
	issuedAt time.Time
	sends    int        // issuer: pair_new frames sent
	attempts []*attempt // issuer: at most maxAttempts; redeemer: at most one
	failures int
	checkMu  sync.Mutex // serialises tag checks of one pairing
	// completing is set once a tag has verified and the peer is being stored.
	// From then on only checkConfirm may end the pairing, so a timer or relay
	// error cannot report a failure for a pairing whose peer gets stored.
	completing bool
	// notified is set once a Completer tag's Completed has run.
	notified bool
}

// NewManager returns a Manager. cfg.Store, cfg.Audit and cfg.Card are required.
func NewManager(cfg Config) *Manager {
	if cfg.Wait <= 0 {
		cfg.Wait = defaultWait
	}
	if cfg.CodeTTL <= 0 {
		cfg.CodeTTL = defaultCodeTTL
	}
	if cfg.ConfirmWait <= 0 {
		cfg.ConfirmWait = defaultConfirm
	}
	if cfg.RelayWait <= 0 {
		cfg.RelayWait = noReplyTimeout
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	m := &Manager{cfg: cfg, log: cfg.Logger, sessions: map[string]*session{}}
	if m.log == nil {
		m.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	m.ownCard, m.ownCardErr = canonicalPart(cfg.Card, "card")
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
		for _, a := range s.attempts {
			if a.timer != nil {
				a.timer.Stop()
			}
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

// Start issues a v2 pairing code. It returns within the configured wait: with
// the code if the relay accepted it, otherwise with a pending status without a
// code that Get will fill in.
func (m *Manager) Start(ctx context.Context) (Status, error) { return m.StartTagged(ctx, nil) }

// StartTagged is Start with a Tag attached to the session (see Tag and
// Completer): a Completer tag's Completed method runs once, when the pairing
// ends, complete or failed.
func (m *Manager) StartTagged(ctx context.Context, tag Tag) (Status, error) {
	// One deadline covers both the send and the wait, so callers return within Wait.
	wctx, cancel := context.WithTimeout(ctx, m.cfg.Wait)
	defer cancel()
	s, err := m.beginIssuer(wctx, tag)
	if err != nil {
		return Status{}, err
	}
	select {
	case <-s.codeReady:
	case <-wctx.Done():
	}
	return m.settledSnapshot(wctx, s), nil
}

// Redeem redeems a code typed by the user. A 15-character code is v2. A
// 10-character code is v1 and is accepted only if allowV1 is set. It returns
// within the configured wait: with the final status if the exchange finished,
// otherwise pending.
func (m *Manager) Redeem(ctx context.Context, rawCode string, allowV1 bool) (Status, error) {
	return m.RedeemTagged(ctx, rawCode, allowV1, nil)
}

// RedeemTagged is Redeem with a Tag attached to the session (see Tag and
// Completer).
func (m *Manager) RedeemTagged(ctx context.Context, rawCode string, allowV1 bool, tag Tag) (Status, error) {
	code, v2, ok := NormalizeCode(rawCode)
	if !ok {
		return Status{}, ErrBadCode
	}
	if !v2 && !allowV1 {
		return Status{}, ErrNeedV1
	}
	wctx, cancel := context.WithTimeout(ctx, m.cfg.Wait)
	defer cancel()
	var s *session
	var err error
	if v2 {
		s, err = m.beginRedeemer(wctx, code, tag)
	} else {
		s, err = m.beginRedeemerV1(wctx, code, tag)
	}
	if err != nil {
		return Status{}, err
	}
	select {
	case <-s.done:
	case <-wctx.Done():
	}
	return m.settledSnapshot(wctx, s), nil
}

// settledSnapshot is snapshot, except that a pairing that has ended is
// returned only once its pair.complete or pair.fail audit row is written, so
// whatever the caller does next is audited after it. It stops waiting when
// ctx ends.
func (m *Manager) settledSnapshot(ctx context.Context, s *session) Status {
	select {
	case <-s.done:
		select {
		case <-s.settled:
		case <-ctx.Done():
		}
	default:
	}
	return m.snapshot(s)
}

func (m *Manager) snapshot(s *session) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return s.st
}

func (m *Manager) precheck() error {
	if m.cfg.Sender == nil {
		return ErrNoRelay
	}
	return nil
}

// ownMaterial returns the canonical announcement to send, or an error.
func (m *Manager) ownMaterial() ([]byte, error) {
	if m.ownCardErr != nil {
		return nil, fmt.Errorf("peers: own agent card: %w", m.ownCardErr)
	}
	if m.cfg.Mailbox == nil {
		return nil, errors.New("peers: no mailbox key source configured")
	}
	raw, err := m.cfg.Mailbox()
	if err != nil {
		return nil, fmt.Errorf("peers: mailbox key: %w", err)
	}
	canon, err := canonicalPart(raw, "announcement")
	if err != nil {
		return nil, fmt.Errorf("peers: own mailbox announcement: %w", err)
	}
	return canon, nil
}

// startKDF derives K in the background for the session's current lookup and
// secret. The secret bytes are copied and the copy wiped once Argon2id is done.
func (m *Manager) startKDFLocked(s *session) *kderiv {
	kd := &kderiv{ready: make(chan struct{})}
	s.kd = kd
	lookup, secret := s.lookup, append([]byte(nil), s.secret...)
	go func() {
		k := deriveK(lookup, secret)
		clear(secret)
		m.mu.Lock()
		if s.st.State == StatePending && s.kd == kd {
			kd.k = k
		} else {
			clear(k)
		}
		m.mu.Unlock()
		close(kd.ready)
	}()
	return kd
}

// newSession registers a pending session. Caller holds no lock.
func (m *Manager) newSession(role string, tag Tag, mutate func(*session)) (*session, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	s := &session{
		st:        Status{ID: id, Role: role, State: StatePending},
		tag:       tag,
		codeReady: make(chan struct{}),
		done:      make(chan struct{}),
		settled:   make(chan struct{}),
	}
	if mutate != nil {
		mutate(s)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.cfg.Now()
	pending := 0
	for sid, o := range m.sessions {
		switch {
		case o.st.State == StatePending:
			pending++
		case now.Sub(o.finished) > keepFinished:
			delete(m.sessions, sid)
		}
	}
	if pending >= maxPending {
		return nil, ErrTooMany
	}
	m.sessions[id] = s
	m.armLocked(s, m.cfg.RelayWait, FailTimeout, "no reply from the relay")
	return s, nil
}

func (m *Manager) beginIssuer(ctx context.Context, tag Tag) (*session, error) {
	if err := m.precheck(); err != nil {
		return nil, err
	}
	mbox, err := m.ownMaterial()
	if err != nil {
		return nil, err
	}
	code, err := NewCode()
	if err != nil {
		return nil, err
	}
	s, err := m.newSession(RoleIssuer, tag, func(s *session) {
		s.lookup, s.secret = code[:lookupLen], []byte(code[lookupLen:])
		s.ownMbox = mbox
		s.issuedAt = m.cfg.Now()
		s.sends = 1
	})
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.startKDFLocked(s)
	lookup := s.lookup
	m.mu.Unlock()
	m.audit(ctx, audit.ActorCLI, ActionPairStart, map[string]any{"id": s.st.ID, "role": RoleIssuer, "version": 2})
	req := envelope.Control{Op: envelope.OpPairNew, Lookup: lookup, Card: m.cfg.Card, Mbox: mbox, Ref: s.st.ID}
	if err := m.sendControl(ctx, req); err != nil {
		return nil, m.failSend(s, err)
	}
	return s, nil
}

func (m *Manager) beginRedeemer(ctx context.Context, code string, tag Tag) (*session, error) {
	if err := m.precheck(); err != nil {
		return nil, err
	}
	used, err := m.cfg.Store.CodeUsed(ctx, usedCodeHash(code), m.cfg.Now())
	if err != nil {
		return nil, err
	}
	mbox, err := m.ownMaterial()
	if err != nil {
		return nil, err
	}
	s, err := m.newSession(RoleRedeemer, tag, func(s *session) {
		s.lookup, s.secret = code[:lookupLen], []byte(code[lookupLen:])
		s.usedHash = usedCodeHash(code)
		s.ownMbox = mbox
	})
	if err != nil {
		return nil, err
	}
	m.audit(ctx, audit.ActorCLI, ActionPairStart, map[string]any{"id": s.st.ID, "role": RoleRedeemer, "version": 2})
	if used {
		m.finish(s.st.ID, StateFailed, nil, &Failure{Code: FailCodeUsed, Message: "this code was already used from this daemon; ask for a new one"})
		return s, nil
	}
	m.mu.Lock()
	m.startKDFLocked(s)
	lookup := s.lookup
	m.mu.Unlock()
	req := envelope.Control{Op: envelope.OpPairRedeem, Lookup: lookup, Card: m.cfg.Card, Mbox: mbox, Ref: s.st.ID}
	if err := m.sendControl(ctx, req); err != nil {
		return nil, m.failSend(s, err)
	}
	return s, nil
}

func (m *Manager) beginRedeemerV1(ctx context.Context, code string, tag Tag) (*session, error) {
	if err := m.precheck(); err != nil {
		return nil, err
	}
	s, err := m.newSession(RoleRedeemer, tag, func(s *session) { s.v1 = true })
	if err != nil {
		return nil, err
	}
	m.audit(ctx, audit.ActorCLI, ActionPairStart, map[string]any{"id": s.st.ID, "role": RoleRedeemer, "version": 1})
	req := envelope.Control{Op: envelope.OpPairRedeem, Code: code, Card: m.cfg.Card, Ref: s.st.ID}
	if err := m.sendControl(ctx, req); err != nil {
		return nil, m.failSend(s, err)
	}
	return s, nil
}

func (m *Manager) sendControl(ctx context.Context, ctl envelope.Control) error {
	return m.cfg.Sender.SendControl(ctx, ctl)
}

func (m *Manager) failSend(s *session, err error) error {
	m.finish(s.st.ID, StateFailed, nil, &Failure{Code: failUnavailable, Message: "could not reach the relay"})
	if errors.Is(err, relayclient.ErrNotConnected) {
		return relayclient.ErrNotConnected
	}
	return fmt.Errorf("send to relay: %w", err)
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
	m.mu.Lock()
	s, ok := m.sessions[e.Ref]
	// sends counts the first pair_new too: up to maxLookupRetries new codes after it.
	retry := ok && s.st.State == StatePending && s.st.Role == RoleIssuer && s.st.Code == "" &&
		e.Code == envelope.CodeLookupTaken && s.sends <= maxLookupRetries
	m.mu.Unlock()
	if retry {
		m.reissue(s)
		return
	}
	// The relay chooses both strings; bound them before they reach the audit log.
	code := e.Code
	if len(code) > maxCodeLen {
		code = strings.ToValidUTF8(code[:maxCodeLen], "")
	}
	m.finish(e.Ref, StateFailed, nil, &Failure{Code: code, Message: truncate(e.Message)})
}

// reissue answers pair_lookup_taken with a completely new code.
func (m *Manager) reissue(s *session) {
	code, err := NewCode()
	if err != nil {
		m.finish(s.st.ID, StateFailed, nil, &Failure{Code: FailStore, Message: "could not generate a code"})
		return
	}
	m.mu.Lock()
	if s.st.State != StatePending {
		m.mu.Unlock()
		return
	}
	old := s.kd
	clear(s.secret)
	s.lookup, s.secret = code[:lookupLen], []byte(code[lookupLen:])
	s.sends++
	if old != nil {
		clear(old.k)
		old.k = nil
	}
	m.startKDFLocked(s)
	lookup, mbox := s.lookup, s.ownMbox
	m.armLocked(s, m.cfg.RelayWait, FailTimeout, "no reply from the relay")
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), sendBudget)
	defer cancel()
	req := envelope.Control{Op: envelope.OpPairNew, Lookup: lookup, Card: m.cfg.Card, Mbox: mbox, Ref: s.st.ID}
	if err := m.sendControl(ctx, req); err != nil {
		m.finish(s.st.ID, StateFailed, nil, &Failure{Code: failUnavailable, Message: "could not reach the relay"})
	}
}

func (m *Manager) onCode(ctl envelope.Control) {
	m.mu.Lock()
	s, ok := m.sessions[ctl.Ref]
	if !ok || s.st.State != StatePending || s.st.Role != RoleIssuer || s.st.Code != "" {
		m.mu.Unlock()
		return
	}
	if ctl.Code != "" {
		// A v1-only relay ignored the lookup and made its own code. Never show it.
		m.mu.Unlock()
		m.finish(ctl.Ref, StateFailed, nil, &Failure{Code: FailRelayV1, Message: "the relay only supports legacy (v1) pairing; upgrade the relay"})
		return
	}
	s.st.Code = FormatCode(s.lookup + string(s.secret))
	exp := s.issuedAt.Add(m.cfg.CodeTTL)
	s.st.Expires = exp.UTC().Format(time.RFC3339)
	m.armLocked(s, max(exp.Sub(m.cfg.Now()), 0), FailExpired, "the pairing code expired before it was used")
	close(s.codeReady)
	m.mu.Unlock()
}

// peerMaterial is a verified pair_peer.
type peerMaterial struct {
	key     string
	sc      *agentcard.Signed
	rawCard []byte
	card    []byte // canonical
	mbox    []byte // canonical
}

// verifyPeer checks the card and announcement of a pair_peer frame.
func (m *Manager) verifyPeer(ctl envelope.Control) (*peerMaterial, *Failure) {
	sc, err := agentcard.Verify(ctl.Card)
	if err == nil {
		if _, kerr := envelope.ParseKey(ctl.PublicKey); kerr != nil {
			err = kerr
		} else if sc.Card.PublicKey != ctl.PublicKey {
			err = errors.New("agent card key does not match the key the relay authenticated")
		} else if ctl.PublicKey == m.cfg.Self {
			err = errors.New("the peer has our own key")
		}
	}
	if err != nil {
		return nil, &Failure{Code: FailBadCard, Message: truncate("rejected the peer's Agent Card: " + err.Error())}
	}
	card, err := canonicalPart(ctl.Card, "card")
	if err != nil {
		return nil, &Failure{Code: FailBadCard, Message: truncate("rejected the peer's Agent Card: " + err.Error())}
	}
	_, mbox, err := mail.ParseAnnouncement(ctl.Mbox, ctl.PublicKey, m.cfg.Now())
	if err != nil {
		return nil, &Failure{Code: FailBadMbox, Message: truncate("rejected the peer's mailbox key announcement: " + err.Error())}
	}
	// Store the canonical card, which the tags cover, not the relay's bytes:
	// those may carry extra top-level members that no tag covers.
	return &peerMaterial{key: ctl.PublicKey, sc: sc, rawCard: card, card: card, mbox: mbox}, nil
}

func (m *Manager) onPeer(ctl envelope.Control) {
	m.mu.Lock()
	s, ok := m.sessions[ctl.Ref]
	if !ok || s.st.State != StatePending {
		m.mu.Unlock()
		return
	}
	v1, role := s.v1, s.st.Role
	m.mu.Unlock()
	switch {
	case v1:
		m.onPeerV1(s, ctl)
	case role == RoleIssuer:
		m.onPeerIssuer(s, ctl)
	default:
		m.onPeerRedeemer(s, ctl)
	}
}

// onPeerV1 completes a legacy pairing: the card is verified and the peer is
// stored with trust=relay and no mailbox key.
func (m *Manager) onPeerV1(s *session, ctl envelope.Control) {
	sc, err := agentcard.Verify(ctl.Card)
	if err == nil && sc.Card.PublicKey != ctl.PublicKey {
		err = errors.New("agent card key does not match the key the relay authenticated")
	}
	if err != nil {
		m.finish(s.st.ID, StateFailed, nil, &Failure{Code: FailBadCard, Message: truncate("rejected the peer's Agent Card: " + err.Error())})
		return
	}
	peer, fail := m.store(sc, ctl.Card, TrustRelay, nil)
	m.finish(s.st.ID, choose(fail == nil, StateComplete, StateFailed), peer, fail)
}

// store saves a verified peer. It returns the Peer for the status, or a failure.
func (m *Manager) store(sc *agentcard.Signed, rawCard []byte, trust string, mbox []byte) (*Peer, *Failure) {
	at := m.cfg.Now().UTC().Truncate(time.Second)
	sctx, cancel := context.WithTimeout(context.Background(), auditWriteBudget)
	defer cancel()
	if err := m.cfg.Store.AddTrusted(sctx, sc, rawCard, at, trust, mbox); err != nil {
		m.log.Error("could not store peer", "event", "pair_store_error", "error", err)
		return nil, &Failure{Code: FailStore, Message: "could not store the peer"}
	}
	fp, _ := envelope.KeyFingerprint(sc.Card.PublicKey)
	return &Peer{
		PublicKey: sc.Card.PublicKey, Name: sc.Card.Name, Harness: sc.Card.Harness,
		Skills: sc.Card.Skills, PairedAt: at.Format(time.RFC3339), Trust: trust, Fingerprint: fp,
	}, nil
}

func choose[T any](c bool, a, b T) T {
	if c {
		return a
	}
	return b
}

// onPeerIssuer starts an attempt. The issuer sends nothing yet: it waits for tag_R.
func (m *Manager) onPeerIssuer(s *session, ctl envelope.Control) {
	m.mu.Lock()
	if s.st.State != StatePending || s.st.Code == "" {
		m.mu.Unlock()
		return
	}
	if len(s.attempts) >= maxAttempts {
		m.mu.Unlock()
		m.log.Debug("ignoring pair_peer beyond the attempt limit", "event", "pair_peer_ignored")
		return
	}
	att := &attempt{peer: ctl.PublicKey, lookup: s.lookup}
	s.attempts = append(s.attempts, att)
	m.mu.Unlock()

	pm, fail := m.verifyPeer(ctl)
	m.mu.Lock()
	if s.st.State != StatePending {
		att.over = true
		m.mu.Unlock()
		return
	}
	if fail != nil {
		m.mu.Unlock()
		m.attemptFailed(s, att, fail.Code)
		return
	}
	att.sc, att.rawCard, att.mbox = pm.sc, pm.rawCard, pm.mbox
	att.transcr = transcript(s.lookup, m.ownCard, pm.card, s.ownMbox, pm.mbox)
	att.waiting = true
	if !m.closed {
		att.timer = time.AfterFunc(m.cfg.ConfirmWait, func() {
			m.mu.Lock()
			accepted := att.accepted
			m.mu.Unlock()
			if !accepted { // an accepted tag decides the attempt itself
				m.attemptFailed(s, att, FailConfirmTimeout)
			}
		})
	}
	m.mu.Unlock()
}

// attemptFailed ends one issuer attempt. The third failed attempt fails the pairing.
func (m *Manager) attemptFailed(s *session, att *attempt, code string) {
	m.mu.Lock()
	if s.st.State != StatePending || att.over || s.completing {
		m.mu.Unlock()
		return
	}
	att.over, att.waiting = true, false
	if att.timer != nil {
		att.timer.Stop()
	}
	s.failures++
	tooMany := s.failures >= maxAttempts
	id := s.st.ID
	m.mu.Unlock()
	peer := att.peer // relay-chosen; audit it only if it is a well-formed key
	if _, err := envelope.ParseKey(peer); err != nil {
		peer = ""
	}
	m.audit(context.Background(), audit.ActorDaemon, ActionPairAttemptFail, map[string]string{"id": id, "peer": peer, "code": code})
	if tooMany {
		m.finish(id, StateFailed, nil, &Failure{Code: FailBadConfirm, Message: "too many failed attempts"})
	}
}

// onPeerRedeemer handles the redeemer's only pair_peer: verify, compute T, then
// send tag_R once K is ready.
func (m *Manager) onPeerRedeemer(s *session, ctl envelope.Control) {
	m.mu.Lock()
	if s.st.State != StatePending || len(s.attempts) > 0 {
		m.mu.Unlock()
		return
	}
	att := &attempt{peer: ctl.PublicKey, lookup: s.lookup}
	s.attempts = append(s.attempts, att)
	m.mu.Unlock()

	pm, fail := m.verifyPeer(ctl)
	if fail != nil {
		m.finish(s.st.ID, StateFailed, nil, fail)
		return
	}
	m.mu.Lock()
	if s.st.State != StatePending {
		m.mu.Unlock()
		return
	}
	att.sc, att.rawCard, att.mbox = pm.sc, pm.rawCard, pm.mbox
	att.transcr = transcript(s.lookup, pm.card, m.ownCard, pm.mbox, s.ownMbox) // issuer's first
	kd := s.kd
	// Computing K may take a while; give it the relay-wait budget.
	m.armLocked(s, m.cfg.RelayWait, FailTimeout, "could not finish the key derivation in time")
	m.mu.Unlock()
	go m.sendRedeemerTag(s, att, kd)
}

func (m *Manager) sendRedeemerTag(s *session, att *attempt, kd *kderiv) {
	select {
	case <-kd.ready:
	case <-s.done:
		return
	}
	m.mu.Lock()
	if s.st.State != StatePending || kd.k == nil {
		m.mu.Unlock()
		return
	}
	tag := redeemerTag(kd.k, att.transcr)
	hash, lookup := s.usedHash, s.lookup
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), sendBudget)
	defer cancel()
	fresh, err := m.cfg.Store.MarkCodeUsed(ctx, hash, m.cfg.Now())
	switch {
	case err != nil:
		m.finish(s.st.ID, StateFailed, nil, &Failure{Code: FailStore, Message: "could not record the code as used"})
		return
	case !fresh:
		m.finish(s.st.ID, StateFailed, nil, &Failure{Code: FailCodeUsed, Message: "this code was already used from this daemon; ask for a new one"})
		return
	}
	m.mu.Lock()
	if s.st.State != StatePending {
		m.mu.Unlock()
		return
	}
	att.waiting = true
	m.armLocked(s, m.cfg.ConfirmWait, FailConfirmTimeout, "the peer did not confirm in time")
	m.mu.Unlock()
	if err := m.sendConfirm(ctx, att.peer, lookup, tag); err != nil {
		m.finish(s.st.ID, StateFailed, nil, &Failure{Code: failUnavailable, Message: "could not reach the relay"})
	}
}

func (m *Manager) sendConfirm(ctx context.Context, to, lookup string, tag []byte) error {
	var idb [8]byte
	if _, err := rand.Read(idb[:]); err != nil {
		return err
	}
	return m.cfg.Sender.Send(ctx, envelope.Envelope{
		From: m.cfg.Self, To: to, Type: ConfirmType,
		ID:      "pc-" + hex.EncodeToString(idb[:]),
		TS:      m.cfg.Now().UTC().Format(time.RFC3339),
		Payload: confirmPayload(lookup, tag),
	})
}

// HandleEnvelope consumes pair.confirm envelopes. Everything else is ignored.
// It runs before the session layer's unpaired check: the sender is not paired
// yet. It never blocks.
func (m *Manager) HandleEnvelope(e envelope.Envelope) {
	if e.Type != ConfirmType {
		return
	}
	drop := func(why string) {
		m.log.Debug("pair.confirm dropped", "event", "pair_confirm_dropped", "reason", why)
	}
	lookup, tag, err := parseConfirm(e.Payload)
	if err != nil {
		drop("malformed")
		return
	}
	m.mu.Lock()
	var s *session
	var att *attempt
	for _, o := range m.sessions {
		if o.st.State != StatePending || o.v1 || o.lookup != lookup {
			continue
		}
		for _, a := range o.attempts {
			if a.peer == e.From && a.waiting && !a.accepted && !a.over {
				s, att = o, a
			}
		}
	}
	if att == nil {
		m.mu.Unlock()
		drop("no waiting attempt")
		return
	}
	att.accepted = true // each attempt takes exactly one confirm
	if att.timer != nil {
		// The tag arrived in time; a slow K must not turn it into confirm_timeout.
		att.timer.Stop()
	}
	kd := s.kd
	m.mu.Unlock()
	go m.checkConfirm(s, att, kd, tag)
}

// checkConfirm compares a received tag with the expected one, once K is ready.
func (m *Manager) checkConfirm(s *session, att *attempt, kd *kderiv, tag []byte) {
	select {
	case <-kd.ready:
	case <-s.done:
		return
	}
	s.checkMu.Lock() // tags of one pairing are checked one at a time
	defer s.checkMu.Unlock()

	m.mu.Lock()
	if s.st.State != StatePending || att.over || kd.k == nil {
		m.mu.Unlock()
		return
	}
	issuer := s.st.Role == RoleIssuer
	var want, reply []byte
	if issuer {
		want, reply = redeemerTag(kd.k, att.transcr), issuerTag(kd.k, att.transcr)
	} else {
		want = issuerTag(kd.k, att.transcr)
	}
	lookup, id := s.lookup, s.st.ID
	m.mu.Unlock()

	if !hmac.Equal(tag, want) {
		if issuer {
			m.attemptFailed(s, att, FailBadConfirm)
		} else {
			m.finish(id, StateFailed, nil, &Failure{Code: FailBadConfirm, Message: "the peer's confirmation did not verify; nothing was stored"})
		}
		return
	}
	// Claim the pairing: a timer or error frame that fired meanwhile has ended
	// it (then nothing is stored), otherwise none can end it from now on.
	m.mu.Lock()
	if s.st.State != StatePending || att.over {
		m.mu.Unlock()
		return
	}
	s.completing = true
	m.mu.Unlock()
	peer, fail := m.store(att.sc, att.rawCard, TrustCode, att.mbox)
	if fail != nil {
		m.end(id, StateFailed, nil, fail, true)
		return
	}
	if issuer {
		// A tag's side effects (team_invites for team_invite) must be in place
		// before tag_I lets the redeemer complete and act on the pairing
		// (submit team.join), so Completed runs before tag_I is sent.
		m.notify(s, CompletionInfo{PairingID: id, Role: RoleIssuer, Lookup: lookup, State: StateComplete, Peer: copyPeer(peer)})
		// Store first, then confirm, then cancel (the cancel happens in finish).
		ctx, cancel := context.WithTimeout(context.Background(), sendBudget)
		if err := m.sendConfirm(ctx, att.peer, lookup, reply); err != nil {
			m.log.Warn("could not send the issuer confirmation", "event", "pair_confirm_send_failed", "error", err)
		}
		cancel()
	}
	m.end(id, StateComplete, peer, nil, true)
}

// finish ends a pending pairing exactly once and audits the outcome. It does
// nothing while a verified tag is being completed.
func (m *Manager) finish(id, state string, peer *Peer, fail *Failure) {
	m.end(id, state, peer, fail, false)
}

// end is finish; completer is true only for checkConfirm, which may end a
// pairing it has claimed.
func (m *Manager) end(id, state string, peer *Peer, fail *Failure, completer bool) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok || s.st.State != StatePending || (s.completing && !completer) {
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
	for _, a := range s.attempts {
		a.over, a.waiting = true, false
		if a.timer != nil {
			a.timer.Stop()
		}
	}
	// Wipe the secret and K.
	clear(s.secret)
	s.secret = nil
	if s.kd != nil {
		clear(s.kd.k)
		s.kd.k = nil
	}
	select {
	case <-s.codeReady:
	default:
		close(s.codeReady)
	}
	close(s.done)
	role, lookup := s.st.Role, s.lookup
	cancelEntry := role == RoleIssuer && !s.v1 && lookup != ""
	m.mu.Unlock()
	defer close(s.settled)

	if cancelEntry {
		ctx, cancel := context.WithTimeout(context.Background(), sendBudget)
		_ = m.cfg.Sender.SendControl(ctx, envelope.Control{Op: envelope.OpPairCancel, Lookup: lookup})
		cancel()
	}
	m.notify(s, CompletionInfo{PairingID: id, Role: role, Lookup: lookup, State: state, Peer: copyPeer(peer)})
	detail := map[string]string{"id": id, "role": role}
	if peer != nil {
		detail["peer"] = peer.PublicKey
		detail["trust"] = peer.Trust
		m.audit(context.Background(), audit.ActorDaemon, ActionPairComplete, detail)
		return
	}
	detail["code"] = fail.Code
	detail["reason"] = truncate(fail.Message)
	m.audit(context.Background(), audit.ActorDaemon, ActionPairFail, detail)
}

// notify runs the session's Completer tag, if any, at most once.
func (m *Manager) notify(s *session, info CompletionInfo) {
	m.mu.Lock()
	c, ok := s.tag.(Completer)
	if !ok || s.notified {
		m.mu.Unlock()
		return
	}
	s.notified = true
	m.mu.Unlock()
	c.Completed(info)
}

func copyPeer(p *Peer) *Peer {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
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
