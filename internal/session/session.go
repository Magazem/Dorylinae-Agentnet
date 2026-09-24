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
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
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

// Audit actions.
const (
	ActionOpen   = "session.open"
	ActionReject = "session.reject"
)

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
	rejectsPerMinute   = 30
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

	inbox chan envelope.Envelope
	stop  chan struct{}
	wg    sync.WaitGroup

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

	rejMu      sync.Mutex
	rejWindow  time.Time
	rejCount   int
	suppressed int
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
	m.mu.Lock()
	sid, ok := m.current[peer]
	if !ok {
		m.mu.Unlock()
		return ErrNoSession
	}
	env, err := m.sealLocked(m.sessions[sid], pt, "")
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return m.send(ctx, env)
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

// Close stops the manager. Pending pings stay pending.
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
}

// HandleEnvelope queues an envelope from the relay. It never blocks.
func (m *Manager) HandleEnvelope(e envelope.Envelope) {
	if !strings.HasPrefix(e.Type, "session.") {
		return
	}
	select {
	case m.inbox <- e:
	default:
		m.log.Warn("session inbox full, dropping envelope", "event", "session_drop", "type", e.Type, "id", e.ID)
	}
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
	m.failPeerLocked(r.peer, ef.Code, ef.Message)
}

// Ping sends an encrypted ping to peer and waits up to Config.Wait for the pong.
func (m *Manager) Ping(ctx context.Context, peer PeerRef) (PingStatus, error) {
	m.mu.Lock()
	snd := m.sender
	pending := 0
	for _, p := range m.pings {
		if p.st.State == StatePending {
			pending++
		}
	}
	m.mu.Unlock()
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
	m.mu.Lock()
	m.pings[id] = p
	p.timer = time.AfterFunc(m.cfg.PingTimeout, func() { m.pingTimeout(id) })
	m.mu.Unlock()

	pt, _ := json.Marshal(message{Type: "ping", ID: id})
	if err := m.sendApp(ctx, peer.PublicKey, pt, id); err != nil {
		m.mu.Lock()
		m.finishLocked(p, &Failure{Code: FailSend, Message: "could not send to the relay"})
		m.mu.Unlock()
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
	m.mu.Lock()
	if sid, ok := m.current[peer]; ok {
		s := m.sessions[sid]
		env, err := m.sealLocked(s, pt, pingID)
		m.mu.Unlock()
		if err != nil {
			return err
		}
		return m.send(ctx, env)
	}
	q := m.queue[peer]
	if len(q) >= maxQueuePerPeer {
		m.mu.Unlock()
		return errors.New("session: too many messages waiting for a handshake")
	}
	m.queue[peer] = append(q, queued{pt: pt, pingID: pingID})
	if p := m.pings[pingID]; p != nil {
		p.st.Handshake = true
	}
	if sid, ok := m.dialing[peer]; ok {
		if p := m.pings[pingID]; p != nil {
			p.sid = sid
		}
		m.mu.Unlock()
		return nil
	}
	hs, err := noise.NewHandshake(m.cfg.Static, peer, true)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	msg, _, err := hs.Write()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	sid := randBytes(SIDSize)
	m.sessions[string(sid)] = &sess{sid: sid, peer: peer, initiator: true, hs: hs, created: time.Now()}
	m.dialing[peer] = string(sid)
	if p := m.pings[pingID]; p != nil {
		p.sid = string(sid)
	}
	env := m.envelopeLocked(peer, TypeInit, append(sid, msg...))
	m.mu.Unlock()
	return m.send(ctx, env)
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
	m.mu.Lock()
	snd := m.sender
	m.mu.Unlock()
	if snd == nil {
		return ErrNoRelay
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendBudget)
	defer cancel()
	err := snd.Send(sctx, env)
	if err != nil {
		m.log.Warn("session send failed", "event", "session_send_failed", "type", env.Type, "error", err)
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

func (m *Manager) onData(ctx context.Context, from string, sid, body []byte) string {
	if len(body) < noise.CounterSize {
		return ReasonMalformed
	}
	n := noise.Counter(body[:noise.CounterSize])
	m.mu.Lock()
	s, ok := m.sessions[string(sid)]
	if !ok || s.peer != from || s.tr == nil {
		m.mu.Unlock()
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

// reject audits a dropped envelope, rate limited. Never logs payload bytes.
func (m *Manager) reject(e envelope.Envelope, sidTag, reason string) {
	m.log.Info("session envelope rejected", "event", "session_reject", "reason", reason, "type", e.Type, "id", e.ID)
	now := time.Now()
	m.rejMu.Lock()
	if now.Sub(m.rejWindow) >= time.Minute {
		if m.suppressed > 0 {
			m.log.Warn("session rejects not audited", "event", "session_reject_suppressed", "count", m.suppressed)
		}
		m.rejWindow, m.rejCount, m.suppressed = now, 0, 0
	}
	allowed := m.rejCount < rejectsPerMinute
	if allowed {
		m.rejCount++
	} else {
		m.suppressed++
	}
	m.rejMu.Unlock()
	if !allowed {
		return
	}
	detail := map[string]string{"peer": e.From, "type": e.Type, "reason": reason}
	if sidTag != "" {
		detail["session"] = sidTag
	}
	actx, cancel := context.WithTimeout(context.Background(), auditBudget)
	defer cancel()
	if err := m.cfg.Audit.Append(actx, audit.ActorDaemon, ActionReject, detail); err != nil {
		m.log.Warn("session: audit failed", "event", "session_error", "error", err)
	}
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return b
}

func randHex(n int) string { return hex.EncodeToString(randBytes(n)) }
