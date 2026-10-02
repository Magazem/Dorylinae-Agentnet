// Package session runs end-to-end encrypted sessions between paired daemons
// over relay envelopes (Docs/protocol/session.md): Noise XX handshakes,
// session.data framing with replay rejection, and the ping round trip.
package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/lograte"
	"github.com/Magazem/Dorylinae-Agentnet/internal/noise"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// Envelope types used by sessions.
const (
	TypeInit = "session.init"
	TypeResp = "session.resp"
	TypeFin  = "session.fin"
	TypeData = "session.data"
)

// ActionOpen is the audit action for an opened session. Rejects are logged
// only, never audited (Docs/protocol/session.md §Rejection, R55-F14).
const ActionOpen = "session.open"

// Reject reasons, see Docs/protocol/session.md.
const (
	ReasonUnpaired       = "unpaired"
	ReasonMalformed      = "malformed"
	ReasonUnknownSession = "unknown_session"
	ReasonBadHandshake   = "bad_handshake"
	ReasonBadBinding     = "bad_binding"
	ReasonDecrypt        = "decrypt"
	ReasonReplay         = "replay"
)

// Ping states.
const (
	StatePending  = "pending"
	StateComplete = "complete"
	StateFailed   = "failed"
)

// Ping failure codes not supplied by the relay.
const (
	FailTimeout   = "timeout"
	FailHandshake = "handshake_failed"
	FailSend      = "send_failed"
)

// SIDSize is the length of a session ID.
const SIDSize = 16

// MaxPayload is the largest decoded session.* payload queued: the sid, the
// 8-byte counter and one maximal 65535-byte Noise message. A longer one is
// dropped before the queue (R55-F13, review 55 R55-052).
const MaxPayload = SIDSize + 8 + 65535

// Drop reasons of the session_drop log line.
const (
	DropOversize  = "oversize"
	DropQueueFull = "queue_full"
)

const (
	defaultWait        = time.Second
	defaultPingTimeout = 10 * time.Second
	handshakeTTL       = 10 * time.Second
	keepFinished       = time.Hour
	refTTL             = 30 * time.Second
	maxQueuePerPeer    = 16
	maxOpenPerPeer     = 4
	maxPendingPerPeer  = 4
	maxPendingPings    = 64
	maxPings           = 1024
	maxRefs            = 4096
	inboxSize          = 256
	inboxBytes         = 16 << 20 // decoded payload bytes in the inbox (R55-F13)
	auditBudget        = 5 * time.Second
	sendBudget         = 5 * time.Second

	dataDomain = "dorylinae-session-data-v1\n"
)

var (
	// ErrNoRelay means the daemon runs without a relay.
	ErrNoRelay = errors.New("session: daemon has no relay configured")
	// ErrNotFound means no ping has that id.
	ErrNotFound = errors.New("session: unknown ping id")
	// ErrTooMany means too many pings are in flight.
	ErrTooMany = errors.New("session: too many pings in progress")
)

// Sender sends envelopes to the relay (a *relayclient.Client).
type Sender interface {
	Send(ctx context.Context, e envelope.Envelope) error
	Connected() bool
}

// Config configures a Manager.
type Config struct {
	// Static is this daemon's bound Noise static key; its identity is our key.
	Static *noise.Static
	Audit  *audit.Log
	// IsPaired reports whether key is a paired peer.
	IsPaired func(ctx context.Context, key string) (bool, error)
	// Sender is the relay connection; nil means no relay (see SetSender).
	Sender Sender
	// Wait bounds how long Ping waits before returning a pending status. Default 1s.
	Wait time.Duration
	// PingTimeout fails a ping with no answer. Default 10s.
	PingTimeout time.Duration
	Logger      *slog.Logger
	// CountReject, if set, is called with the reason of every reject except
	// unpaired, for the daily relay.reject_summary audit row (OD-F14-7).
	CountReject func(reason string)
}

// Failure is why a ping failed.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// PeerRef names the pinged peer.
type PeerRef struct {
	PublicKey string `json:"public_key"`
	Name      string `json:"name"`
}

// PingStatus is a snapshot of one ping.
type PingStatus struct {
	ID    string  `json:"ping_id"`
	Peer  PeerRef `json:"peer"`
	State string  `json:"state"`
	// RTTMillis is the encrypted round trip, from sending the ping to
	// receiving the pong; set when complete.
	RTTMillis *float64 `json:"rtt_ms,omitempty"`
	// Handshake is true when a new session was set up for this ping.
	Handshake bool     `json:"handshake"`
	Error     *Failure `json:"error,omitempty"`
}

// message is the plaintext of a session.data envelope.
type message struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

type sess struct {
	sid       []byte
	peer      string
	initiator bool
	hs        *noise.Handshake // non-nil while the handshake runs
	tr        *noise.Transport // non-nil once open
	created   time.Time
	opened    time.Time
}

type queued struct {
	pt     []byte
	pingID string
}

type ping struct {
	st       PingStatus
	sid      string // session the ping was sent (or queued) on
	sentAt   time.Time
	done     chan struct{}
	timer    *time.Timer
	finished time.Time
}

type ref struct {
	peer string
	at   time.Time
}

// Manager runs all sessions of one daemon. Relay envelopes and errors are fed
// to it with HandleEnvelope and HandleError.
type Manager struct {
	cfg  Config
	self string
	log  *slog.Logger

	pingGate func(ctx context.Context, peer string) bool // guarded by mu
	initGate func(ctx context.Context, peer string) bool // guarded by mu

	inbox chan envelope.Envelope
	// queued is the decoded payload bytes in inbox: added before an envelope
	// is queued, taken off when the worker receives it.
	queued atomic.Int64
	stop   chan struct{}
	wg     sync.WaitGroup

	// sendMu serialises everything that sends, so counters leave in order.
	// Lock order: sendMu, then mu.
	sendMu sync.Mutex

	mu       sync.Mutex
	sender   Sender
	sessions map[string]*sess  // by string(sid)
	current  map[string]string // peer -> sid of the session to send on
	dialing  map[string]string // peer -> sid of our pending initiator handshake
	queue    map[string][]queued
	pings    map[string]*ping
	refs     map[string]ref // envelope id -> peer, to match relay errors
	handlers map[string]DataHandler
	closed   bool

	// lines limits the relay-driven log lines session_reject, session_drop
	// and session_send_failed (Docs/protocol/envelope.md §Relay-driven log
	// lines (daemon)).
	lines *lograte.Limiter
}

// SetPingGate sets a check that decides whether a ping from a paired peer is
// answered; an unanswered ping simply times out on the peer's side. Nil (the
// default) answers every ping. The daemon uses it so an invisible daemon is
// not a liveness oracle (R55-077, Docs/protocol/presence.md §Visibility).
func (m *Manager) SetPingGate(gate func(ctx context.Context, peer string) bool) {
	m.mu.Lock()
	m.pingGate = gate
	m.mu.Unlock()
}

// SetInitGate sets a check that decides whether a handshake Init from a paired
// peer is answered. A refused Init is dropped without a Resp, an error or an
// audit row, so the peer sees what it sees for an offline daemon. Nil (the
// default) answers every Init. Together with SetPingGate it keeps an invisible
// daemon from being probed by a ping (R55-077, review 79 M1).
func (m *Manager) SetInitGate(gate func(ctx context.Context, peer string) bool) {
	m.mu.Lock()
	m.initGate = gate
	m.mu.Unlock()
}

// NewManager returns a running Manager; call Close to stop it.
func NewManager(cfg Config) *Manager {
	if cfg.Wait <= 0 {
		cfg.Wait = defaultWait
	}
	if cfg.PingTimeout <= 0 {
		cfg.PingTimeout = defaultPingTimeout
	}
	m := &Manager{
		cfg: cfg, self: cfg.Static.Identity(), log: cfg.Logger, sender: cfg.Sender,
		inbox: make(chan envelope.Envelope, inboxSize), stop: make(chan struct{}),
		sessions: map[string]*sess{}, current: map[string]string{}, dialing: map[string]string{},
		queue: map[string][]queued{}, pings: map[string]*ping{}, refs: map[string]ref{},
		handlers: map[string]DataHandler{},
	}
	if m.log == nil {
		m.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	m.lines = lograte.New(m.log, 0)
	m.wg.Add(2)
	go m.worker()
	go m.sweeper()
	return m
}

// DataHandler receives the decrypted plaintext of a session.data message of a
// registered type, with the authenticated identity of the sending peer. It runs
// on the manager's receive goroutine while the send lock is held, so it must
// return quickly and must not call SendData itself: it hands the work to its
// own workers (Docs/protocol/grant.md §Transport).
type DataHandler func(peer string, plaintext []byte)

// ErrNoSession means there is no open session to the peer to send on.
var ErrNoSession = errors.New("session: no open session with the peer")

// Handle registers h for session.data plaintexts whose "type" is typ (for
// example "fetch.req"). Call it before envelopes flow. Built-in ping and pong
// cannot be replaced.
func (m *Manager) Handle(typ string, h DataHandler) {
	if typ == "ping" || typ == "pong" || h == nil {
		return
	}
	m.mu.Lock()
	m.handlers[typ] = h
	m.mu.Unlock()
}

// SendData encrypts pt on the peer's current open session and sends it. It
// never starts a handshake: a reply needs the session the request arrived on.
func (m *Manager) SendData(ctx context.Context, peer string, pt []byte) error {
	m.sendMu.Lock()
	defer m.sendMu.Unlock()
	env, err := m.sealCurrent(peer, pt)
	if err != nil {
		return err
	}
	return m.send(ctx, env)
}

// sealCurrent seals pt on peer's current open session.
func (m *Manager) sealCurrent(peer string, pt []byte) (envelope.Envelope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sid, ok := m.current[peer]
	if !ok {
		return envelope.Envelope{}, ErrNoSession
	}
	return m.sealLocked(m.sessions[sid], pt, "")
}

// withLock runs f with m.mu held. The send paths that IPC handlers reach
// (ping, fetch, device, debate) take m.mu only through it or a deferred
// unlock, so a panic the IPC server recovers never leaves m.mu held, which
// would stop the worker and with it every inbound envelope (review 77b).
func (m *Manager) withLock(f func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f()
}

// Send encrypts pt to peer on its current session, or queues it and starts a
// handshake (unlike SendData, which only replies on an open session). The
// fetch client uses it to reach a grantor (Docs/protocol/grant.md §Transport).
func (m *Manager) Send(ctx context.Context, peer string, pt []byte) error {
	return m.sendApp(ctx, peer, pt, "")
}

// SetSender sets the relay connection once it exists.
func (m *Manager) SetSender(s Sender) {
	m.mu.Lock()
	m.sender = s
	m.mu.Unlock()
}

// Close stops the manager and writes its pending log lines. Pending pings
// stay pending.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	for _, p := range m.pings {
		if p.timer != nil {
			p.timer.Stop()
		}
	}
	m.mu.Unlock()
	close(m.stop)
	m.wg.Wait()
	m.lines.Flush()
}

// HandleEnvelope queues an envelope from the relay. It never blocks. An
// envelope whose payload is longer than MaxPayload, or that would take the
// inbox past 256 envelopes or 16 MiB, is dropped: it is counted into the
// limited session_drop line, never audited (the sender is not known yet).
func (m *Manager) HandleEnvelope(e envelope.Envelope) {
	if !strings.HasPrefix(e.Type, "session.") {
		return
	}
	n := int64(len(e.Payload))
	if n > MaxPayload {
		m.drop(e, DropOversize)
		return
	}
	if m.queued.Add(n) > inboxBytes {
		m.queued.Add(-n)
		m.drop(e, DropQueueFull)
		return
	}
	select {
	case m.inbox <- e:
	default:
		m.queued.Add(-n)
		m.drop(e, DropQueueFull)
	}
}

// drop counts a dropped envelope into the limited session_drop line, by
// reason (Docs/protocol/session.md §Rejection). Never the envelope id.
func (m *Manager) drop(e envelope.Envelope, reason string) {
	m.lines.Note(slog.LevelWarn, "session_drop", "session envelopes dropped before the queue", reason, "type", e.Type)
}

// HandleError fails the pings affected by a relay error frame (peer_offline, ...).
func (m *Manager) HandleError(ef envelope.ErrorFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.refs[ef.Ref]
	if !ok {
		return
	}
	delete(m.refs, ef.Ref)
	if sid, ok := m.dialing[r.peer]; ok {
		m.dropLocked(sid)
	}
	// ef is relayclient's converted frame. Its message is relay text and is
	// never shown: the ping carries daemon text for the code (OD-R55F9-10).
	m.failPeerLocked(r.peer, ef.Code, envelope.ErrorText(ef.Code))
}

// Ping sends an encrypted ping to peer and waits up to Config.Wait for the pong.
func (m *Manager) Ping(ctx context.Context, peer PeerRef) (PingStatus, error) {
	var snd Sender
	pending := 0
	m.withLock(func() {
		snd = m.sender
		for _, p := range m.pings {
			if p.st.State == StatePending {
				pending++
			}
		}
	})
	switch {
	case snd == nil:
		return PingStatus{}, ErrNoRelay
	case !snd.Connected():
		return PingStatus{}, relayclient.ErrNotConnected
	case pending >= maxPendingPings:
		return PingStatus{}, ErrTooMany
	}

	id := "ping-" + randHex(8)
	p := &ping{st: PingStatus{ID: id, Peer: peer, State: StatePending}, done: make(chan struct{})}
	m.withLock(func() {
		m.pings[id] = p
		p.timer = time.AfterFunc(m.cfg.PingTimeout, func() { m.pingTimeout(id) })
	})

	pt, _ := json.Marshal(message{Type: "ping", ID: id})
	if err := m.sendApp(ctx, peer.PublicKey, pt, id); err != nil {
		m.withLock(func() {
			m.finishLocked(p, &Failure{Code: FailSend, Message: "could not send to the relay"})
		})
	}

	wctx, cancel := context.WithTimeout(ctx, m.cfg.Wait)
	defer cancel()
	select {
	case <-p.done:
	case <-wctx.Done():
	}
	return m.snapshot(id)
}

// Get returns the status of a ping.
func (m *Manager) Get(id string) (PingStatus, bool) {
	st, err := m.snapshot(id)
	return st, err == nil
}

func (m *Manager) snapshot(id string) (PingStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pings[id]
	if !ok {
		return PingStatus{}, ErrNotFound
	}
	st := p.st
	if st.RTTMillis != nil {
		v := *st.RTTMillis
		st.RTTMillis = &v
	}
	if st.Error != nil {
		e := *st.Error
		st.Error = &e
	}
	return st, nil
}

// sendApp encrypts pt to peer on its current session, or queues it and starts
// a handshake. pingID, if set, names the ping pt carries.
func (m *Manager) sendApp(ctx context.Context, peer string, pt []byte, pingID string) error {
	m.sendMu.Lock()
	defer m.sendMu.Unlock()
	env, ok, err := m.prepareApp(peer, pt, pingID)
	if err != nil || !ok {
		return err
	}
	return m.send(ctx, env)
}

// prepareApp is sendApp's work under m.mu: it seals pt on peer's current
// session, or queues it and, unless a handshake is already under way,
// builds the Init. ok reports whether env is to be sent.
func (m *Manager) prepareApp(peer string, pt []byte, pingID string) (env envelope.Envelope, ok bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sid, cur := m.current[peer]; cur {
		env, err = m.sealLocked(m.sessions[sid], pt, pingID)
		return env, err == nil, err
	}
	q := m.queue[peer]
	if len(q) >= maxQueuePerPeer {
		return envelope.Envelope{}, false, errors.New("session: too many messages waiting for a handshake")
	}
	m.queue[peer] = append(q, queued{pt: pt, pingID: pingID})
	if p := m.pings[pingID]; p != nil {
		p.st.Handshake = true
	}
	if sid, dialing := m.dialing[peer]; dialing {
		if p := m.pings[pingID]; p != nil {
			p.sid = sid
		}
		return envelope.Envelope{}, false, nil
	}
	hs, err := noise.NewHandshake(m.cfg.Static, peer, true)
	if err != nil {
		return envelope.Envelope{}, false, err
	}
	msg, _, err := hs.Write()
	if err != nil {
		return envelope.Envelope{}, false, err
	}
	sid := randBytes(SIDSize)
	m.sessions[string(sid)] = &sess{sid: sid, peer: peer, initiator: true, hs: hs, created: time.Now()}
	m.dialing[peer] = string(sid)
	if p := m.pings[pingID]; p != nil {
		p.sid = string(sid)
	}
	return m.envelopeLocked(peer, TypeInit, append(sid, msg...)), true, nil
}

// sealLocked builds a session.data envelope for pt on s.
func (m *Manager) sealLocked(s *sess, pt []byte, pingID string) (envelope.Envelope, error) {
	n, ct, err := s.tr.Seal(func(n uint64) []byte { return m.dataAD(m.self, s.peer, s.sid, n) }, pt)
	if err != nil {
		return envelope.Envelope{}, err
	}
	payload := make([]byte, 0, SIDSize+noise.CounterSize+len(ct))
	payload = append(payload, s.sid...)
	var cb [noise.CounterSize]byte
	noise.PutCounter(cb[:], n)
	payload = append(payload, cb[:]...)
	payload = append(payload, ct...)
	if p := m.pings[pingID]; p != nil {
		p.sentAt = time.Now()
		p.sid = string(s.sid)
	}
	return m.envelopeLocked(s.peer, TypeData, payload), nil
}

func (m *Manager) dataAD(from, to string, sid []byte, n uint64) []byte {
	var cb [noise.CounterSize]byte
	noise.PutCounter(cb[:], n)
	ad := make([]byte, 0, len(dataDomain)+len(from)+len(to)+2+SIDSize+noise.CounterSize)
	ad = append(ad, dataDomain...)
	ad = append(ad, from...)
	ad = append(ad, '\n')
	ad = append(ad, to...)
	ad = append(ad, '\n')
	ad = append(ad, sid...)
	return append(ad, cb[:]...)
}

func (m *Manager) envelopeLocked(to, typ string, payload []byte) envelope.Envelope {
	now := time.Now()
	id := "s" + randHex(12)
	if len(m.refs) < maxRefs {
		m.refs[id] = ref{peer: to, at: now}
	}
	return envelope.Envelope{
		From: m.self, To: to, Type: typ, ID: id,
		TS: now.UTC().Format(time.RFC3339Nano), Payload: payload,
	}
}

// send writes env to the relay. Caller holds sendMu, not mu.
func (m *Manager) send(ctx context.Context, env envelope.Envelope) error {
	var snd Sender
	m.withLock(func() { snd = m.sender })
	if snd == nil {
		return ErrNoRelay
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendBudget)
	defer cancel()
	err := snd.Send(sctx, env)
	if err != nil {
		m.lines.Note(slog.LevelWarn, "session_send_failed", "session sends failed", "", "type", env.Type, "error", err)
	}
	return err
}

func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.stop:
			return
		case e := <-m.inbox:
			m.queued.Add(-int64(len(e.Payload)))
			m.handle(e)
		}
	}
}

// handle processes one inbound session envelope.
func (m *Manager) handle(e envelope.Envelope) {
	ctx := context.Background()
	sidTag := ""
	if len(e.Payload) >= SIDSize {
		sidTag = hex.EncodeToString(e.Payload[:4])
	}
	ok, err := m.cfg.IsPaired(ctx, e.From)
	if err != nil {
		m.log.Warn("session: peer lookup failed", "event", "session_error", "error", err)
		return
	}
	if !ok {
		m.reject(e, sidTag, ReasonUnpaired)
		return
	}
	if len(e.Payload) < SIDSize {
		m.reject(e, "", ReasonMalformed)
		return
	}
	sid, body := e.Payload[:SIDSize], e.Payload[SIDSize:]

	m.sendMu.Lock()
	defer m.sendMu.Unlock()
	var reason string
	switch e.Type {
	case TypeInit:
		reason = m.onInit(ctx, e.From, sid, body)
	case TypeResp:
		reason = m.onResp(ctx, e.From, sid, body)
	case TypeFin:
		reason = m.onFin(ctx, e.From, sid, body)
	case TypeData:
		reason = m.onData(ctx, e.From, sid, body)
	default:
		reason = ReasonMalformed
	}
	if reason != "" {
		m.reject(e, sidTag, reason)
	}
}

func (m *Manager) onInit(ctx context.Context, from string, sid, body []byte) string {
	m.mu.Lock()
	gate := m.initGate
	m.mu.Unlock()
	if gate != nil && !gate(ctx, from) {
		return "" // look offline: no Resp, no reject, no audit
	}
	m.mu.Lock()
	if _, dup := m.sessions[string(sid)]; dup {
		m.mu.Unlock()
		return ReasonBadHandshake
	}
	m.limitPendingLocked(from)
	hs, err := noise.NewHandshake(m.cfg.Static, from, false)
	if err != nil {
		m.mu.Unlock()
		return ReasonBadHandshake
	}
	if _, err := hs.Read(body); err != nil {
		m.mu.Unlock()
		return ReasonBadHandshake
	}
	msg, _, err := hs.Write()
	if err != nil {
		m.mu.Unlock()
		return ReasonBadHandshake
	}
	s := &sess{sid: bytes.Clone(sid), peer: from, hs: hs, created: time.Now()}
	m.sessions[string(sid)] = s
	env := m.envelopeLocked(from, TypeResp, append(bytes.Clone(sid), msg...))
	m.mu.Unlock()
	_ = m.send(ctx, env)
	return ""
}

func (m *Manager) onResp(ctx context.Context, from string, sid, body []byte) string {
	m.mu.Lock()
	s, ok := m.sessions[string(sid)]
	if !ok || s.peer != from {
		m.mu.Unlock()
		return ReasonUnknownSession
	}
	if s.hs == nil || !s.initiator {
		m.mu.Unlock()
		return ReasonBadHandshake
	}
	if _, err := s.hs.Read(body); err != nil {
		m.dropLocked(string(sid))
		m.failPeerLocked(from, FailHandshake, "the peer's handshake did not verify")
		m.mu.Unlock()
		return handshakeReason(err)
	}
	msg, tr, err := s.hs.Write()
	if err != nil || tr == nil {
		m.dropLocked(string(sid))
		m.mu.Unlock()
		return ReasonBadHandshake
	}
	fin := m.envelopeLocked(from, TypeFin, append(bytes.Clone(sid), msg...))
	m.openLocked(s, tr)
	flush := m.flushLocked(s)
	m.mu.Unlock()

	m.auditOpen(ctx, s)
	if m.send(ctx, fin) != nil {
		return ""
	}
	for _, env := range flush {
		_ = m.send(ctx, env)
	}
	return ""
}

func (m *Manager) onFin(ctx context.Context, from string, sid, body []byte) string {
	m.mu.Lock()
	s, ok := m.sessions[string(sid)]
	if !ok || s.peer != from {
		m.mu.Unlock()
		return ReasonUnknownSession
	}
	if s.hs == nil || s.initiator {
		m.mu.Unlock()
		return ReasonBadHandshake
	}
	tr, err := s.hs.Read(body)
	if err != nil || tr == nil {
		m.dropLocked(string(sid))
		m.mu.Unlock()
		if err == nil {
			return ReasonBadHandshake
		}
		return handshakeReason(err)
	}
	m.openLocked(s, tr)
	flush := m.flushLocked(s)
	m.mu.Unlock()

	m.auditOpen(ctx, s)
	for _, env := range flush {
		_ = m.send(ctx, env)
	}
	return ""
}

// refuses reports whether the init gate turns peer away now (review 79b M1b).
// The gate reads the database, so it runs without the lock.
func (m *Manager) refuses(ctx context.Context, peer string) bool {
	m.mu.Lock()
	gate := m.initGate
	m.mu.Unlock()
	return gate != nil && !gate(ctx, peer)
}

// DropGatedSessions closes every open session whose peer the init gate now
// refuses. The daemon calls it when the presence mode changes, so a session
// opened while it was visible does not outlive that (review 79b M1b). The
// peer is not told: its next message is dropped without an answer.
func (m *Manager) DropGatedSessions(ctx context.Context) {
	m.mu.Lock()
	peers := map[string]bool{}
	for _, s := range m.sessions {
		peers[s.peer] = true
	}
	m.mu.Unlock()
	refused := map[string]bool{}
	for p := range peers {
		if m.refuses(ctx, p) {
			refused[p] = true
		}
	}
	if len(refused) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for sid, s := range m.sessions {
		if refused[s.peer] {
			m.dropLocked(sid)
		}
	}
}

func (m *Manager) onData(ctx context.Context, from string, sid, body []byte) string {
	if len(body) < noise.CounterSize {
		return ReasonMalformed
	}
	// From a peer the init gate refuses, data gets no answer and no error,
	// whether or not the session is known (review 79b M1b); the exemption for
	// responses is below, after decryption.
	gated := m.refuses(ctx, from)
	n := noise.Counter(body[:noise.CounterSize])
	m.mu.Lock()
	s, ok := m.sessions[string(sid)]
	if !ok || s.peer != from || s.tr == nil {
		m.mu.Unlock()
		if gated {
			return ""
		}
		return ReasonUnknownSession
	}
	pt, err := s.tr.Open(n, m.dataAD(from, m.self, s.sid, n), body[noise.CounterSize:])
	if err != nil {
		m.mu.Unlock()
		if errors.Is(err, noise.ErrReplay) {
			return ReasonReplay
		}
		return ReasonDecrypt
	}
	var msg message
	if json.Unmarshal(pt, &msg) != nil {
		m.mu.Unlock()
		return ""
	}
	switch msg.Type {
	case "ping":
		gate := m.pingGate
		if gate != nil {
			// The gate reads the database: ask it without the lock (R55-077).
			m.mu.Unlock()
			if !gate(ctx, from) {
				return ""
			}
			m.mu.Lock()
			if cur, ok := m.sessions[string(sid)]; !ok || cur != s || s.tr == nil {
				m.mu.Unlock()
				return ""
			}
		}
		reply, _ := json.Marshal(message{Type: "pong", ID: msg.ID})
		env, err := m.sealLocked(s, reply, "")
		m.mu.Unlock()
		if err == nil {
			_ = m.send(ctx, env)
		}
		return ""
	case "pong":
		if p, ok := m.pings[msg.ID]; ok && p.st.Peer.PublicKey == from && p.st.State == StatePending && !p.sentAt.IsZero() {
			rtt := float64(time.Since(p.sentAt).Microseconds()) / 1000
			p.st.RTTMillis = &rtt
			m.finishLocked(p, nil)
		}
	default:
		// Requests from a gated peer are dropped; a ".resp" answers something
		// we sent, so it is let through.
		if gated && !strings.HasSuffix(msg.Type, ".resp") {
			m.mu.Unlock()
			return ""
		}
		if h := m.handlers[msg.Type]; h != nil {
			m.mu.Unlock()
			h(from, bytes.Clone(pt))
			return ""
		}
	}
	m.mu.Unlock()
	return ""
}

func handshakeReason(err error) string {
	if errors.Is(err, noise.ErrBinding) {
		return ReasonBadBinding
	}
	return ReasonBadHandshake
}

// openLocked moves s from handshake to open and makes it the peer's current session.
func (m *Manager) openLocked(s *sess, tr *noise.Transport) {
	s.hs, s.tr, s.opened = nil, tr, time.Now()
	sid := string(s.sid)
	m.current[s.peer] = sid
	if m.dialing[s.peer] == sid {
		delete(m.dialing, s.peer)
	}
	// Keep at most maxOpenPerPeer open sessions; drop the oldest.
	for {
		var oldest *sess
		count := 0
		for _, o := range m.sessions {
			if o.peer == s.peer && o.tr != nil {
				count++
				if oldest == nil || o.opened.Before(oldest.opened) {
					oldest = o
				}
			}
		}
		if count <= maxOpenPerPeer {
			return
		}
		delete(m.sessions, string(oldest.sid))
	}
}

// flushLocked seals the messages queued for s's peer.
func (m *Manager) flushLocked(s *sess) []envelope.Envelope {
	q := m.queue[s.peer]
	delete(m.queue, s.peer)
	var out []envelope.Envelope
	for _, item := range q {
		env, err := m.sealLocked(s, item.pt, item.pingID)
		if err != nil {
			break
		}
		out = append(out, env)
	}
	return out
}

// limitPendingLocked drops the oldest responder handshakes from peer beyond the cap.
func (m *Manager) limitPendingLocked(peer string) {
	for {
		var oldest *sess
		count := 0
		for _, s := range m.sessions {
			if s.peer == peer && s.hs != nil && !s.initiator {
				count++
				if oldest == nil || s.created.Before(oldest.created) {
					oldest = s
				}
			}
		}
		if count < maxPendingPerPeer {
			return
		}
		delete(m.sessions, string(oldest.sid))
	}
}

// dropLocked forgets a session or handshake.
func (m *Manager) dropLocked(sid string) {
	s, ok := m.sessions[sid]
	if !ok {
		return
	}
	delete(m.sessions, sid)
	if m.current[s.peer] == sid {
		delete(m.current, s.peer)
	}
	if m.dialing[s.peer] == sid {
		delete(m.dialing, s.peer)
		delete(m.queue, s.peer)
	}
}

func (m *Manager) failPeerLocked(peer, code, msg string) {
	for _, p := range m.pings {
		if p.st.Peer.PublicKey == peer && p.st.State == StatePending {
			m.finishLocked(p, &Failure{Code: code, Message: msg})
		}
	}
}

// finishLocked ends a pending ping: complete when f is nil, failed otherwise.
func (m *Manager) finishLocked(p *ping, f *Failure) {
	if p.st.State != StatePending {
		return
	}
	if f == nil {
		p.st.State = StateComplete
	} else {
		p.st.State, p.st.Error = StateFailed, f
	}
	p.finished = time.Now()
	if p.timer != nil {
		p.timer.Stop()
	}
	close(p.done)
}

func (m *Manager) pingTimeout(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pings[id]
	if !ok || p.st.State != StatePending {
		return
	}
	m.finishLocked(p, &Failure{Code: FailTimeout, Message: "no answer from the peer"})
	// The session may be dead (for example the peer restarted): start over next time.
	if p.sid != "" {
		m.dropLocked(p.sid)
	}
}

func (m *Manager) sweeper() {
	defer m.wg.Done()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case now := <-t.C:
			m.sweep(now)
		}
	}
}

func (m *Manager) sweep(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for sid, s := range m.sessions {
		if s.hs != nil && now.Sub(s.created) > handshakeTTL {
			if s.initiator {
				m.failPeerLocked(s.peer, FailHandshake, "the peer did not complete the handshake")
			}
			m.dropLocked(sid)
		}
	}
	for id, r := range m.refs {
		if now.Sub(r.at) > refTTL {
			delete(m.refs, id)
		}
	}
	for id, p := range m.pings {
		if p.st.State != StatePending && (now.Sub(p.finished) > keepFinished || len(m.pings) > maxPings) {
			delete(m.pings, id)
		}
	}
}

func (m *Manager) auditOpen(ctx context.Context, s *sess) {
	role := "responder"
	if s.initiator {
		role = "initiator"
	}
	actx, cancel := context.WithTimeout(ctx, auditBudget)
	defer cancel()
	detail := map[string]string{"peer": s.peer, "role": role, "session": hex.EncodeToString(s.sid[:4])}
	if err := m.cfg.Audit.Append(actx, audit.ActorDaemon, ActionOpen, detail); err != nil {
		m.log.Warn("session: audit failed", "event", "session_error", "error", err)
	}
}

// reject counts a dropped envelope into the limited session_reject log line.
// Every reason can be caused by the relay alone, so none is audited
// (Docs/protocol/session.md §Rejection, R55-F14). Never logs the envelope id
// or payload bytes.
func (m *Manager) reject(e envelope.Envelope, sidTag, reason string) {
	args := []any{"reason", reason, "type", e.Type, "peer", e.From}
	if sidTag != "" {
		args = append(args, "session", sidTag)
	}
	m.lines.Note(slog.LevelInfo, "session_reject", "session envelopes rejected", reason, args...)
	if reason != ReasonUnpaired && m.cfg.CountReject != nil {
		m.cfg.CountReject(reason)
	}
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return b
}

func randHex(n int) string { return hex.EncodeToString(randBytes(n)) }
