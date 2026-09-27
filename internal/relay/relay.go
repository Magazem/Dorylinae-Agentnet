// Package relay is the AgentNet relay server: it authenticates daemons by a
// signed challenge, keeps an in-memory registry of connections keyed by public
// key, and forwards envelopes by their "to" field. The protocol is specified in
// Docs/protocol/envelope.md.
//
// The relay never decodes, inspects or logs envelope payloads, and forwards
// each frame byte for byte.
package relay

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

const (
	defaultChallengeTTL = 10 * time.Second
	defaultSendQueue    = 64
	writeTimeout        = 10 * time.Second
	// drainWaitTimeout bounds how long Close waits for in-flight queue
	// drains before disconnecting everyone anyway (Docs/protocol/relay-hosted.md
	// §3: "graceful shutdown on SIGTERM waits up to 10 s for writes").
	drainWaitTimeout = 10 * time.Second
)

// Options tune a Server. The zero value is what cmd/relay uses.
type Options struct {
	// Logger receives connection and routing events. Nil discards them.
	Logger *slog.Logger
	// ChallengeTTL is how long a daemon has to answer its challenge. Default 10s.
	ChallengeTTL time.Duration
	// SendQueue is the per-connection outbound buffer, in frames. Default 64.
	SendQueue int
	// Now is the clock used for pairing code expiry and rate limiting. Default time.Now.
	Now func() time.Time
	// PairTTL is how long a pairing code stays valid. Default 10m.
	PairTTL time.Duration
	// PairFailLimit is the failed redemptions one key may make per PairFailWindow
	// before it is rate limited. Default 5.
	PairFailLimit int
	// PairFailWindow is the rate limit window. Default 1m. It also bounds v2
	// pair_new requests to 10 per key per window.
	PairFailWindow time.Duration
	// PairMaxCodes caps the pairing codes outstanding on the whole relay; past
	// it pair_new gets pair_limit. Default 10000.
	PairMaxCodes int
	// AllowPairingV1 accepts the v1 pairing frames (pair_new without lookup,
	// pair_redeem with code). The zero value keeps v1 OFF (review-08b L1);
	// cmd/relay sets it explicitly from --allow-pairing-v1. Without it, both
	// v1 frames get pair_v1_disabled.
	AllowPairingV1 bool
	// QueuePath is the SQLite file holding envelopes queued for offline peers.
	// Empty keeps the queue in memory: it works, but is lost when the relay stops.
	QueuePath string
	// QueueTTL is how long an envelope waits for its recipient. Default 7 days.
	QueueTTL time.Duration
	// QueueMaxEnvelopes and QueueMaxBytes cap what one recipient may have
	// waiting. Defaults 1000 envelopes and 32 MiB.
	QueueMaxEnvelopes int
	QueueMaxBytes     int64
	// SweepInterval is how often expired envelopes are purged. Default 1m.
	SweepInterval time.Duration
	// EphemeralPerMinute caps ephemeral envelopes (presence) per sending key per
	// minute; the excess is dropped silently. Default 600.
	EphemeralPerMinute int
	// EphemeralMaxBytes drops ephemeral frames larger than this. Default 8 KiB.
	EphemeralMaxBytes int

	// Relay authentication (Docs/protocol/relay-hosted.md §1, ticket 4.0a).
	//
	// Public marks a relay reachable from other machines, directly or through
	// a proxy on the same host. A public relay requires auth v2 unless
	// AllowAuthV1 is set, and needs at least one origin. cmd/relay derives it
	// from the listen address and the TLS/proxy flags.
	Public bool
	// AllowAuthV1 keeps v1 auth on a public relay (a migration window). A
	// relay that is not public always accepts v1.
	AllowAuthV1 bool
	// Origins are the relay's own origins (ws:// or wss:// URLs, any form
	// envelope.Origin accepts). A v2 signature is accepted for any of them.
	// Empty on a non-public relay offers v1 only.
	Origins []string

	// Journal receives content-free security events (unbind, account_delete,
	// suspend/unsuspend, invite_redeem/invite_revoke, team_remove) for
	// --replay-journal to restore after a backup. Nil (the zero value)
	// disables the journal; no event is written by this ticket, which only
	// wires the mechanism for 4.2a/4.3a to use.
	Journal *JournalWriter
}

// Server is an http.Handler serving the relay protocol.
type Server struct {
	log    *slog.Logger
	ttl    time.Duration
	queue  int
	now    func() time.Time
	pairs  *pairings
	q      *queue
	eph    *ephemeralLimiter
	ephMax int

	// Relay authentication: which versions the challenge offers, and the
	// canonical origins a v2 signature may name. Fixed after Open.
	authV1, authV2 bool
	origins        []string

	mu      sync.Mutex
	conns   map[string]*conn // keyed by wire public key
	closing bool             // Close has begun; no new drain may start (guarded by mu)

	journal   *JournalWriter
	drainWG   sync.WaitGroup // outstanding queue-drain goroutines; Close waits for these
	stopSweep chan struct{}
	sweepDone chan struct{}
	closeOnce sync.Once
}

// Journal returns the security journal writer configured by Options.Journal,
// or nil if none was configured. Extension point for 4.2a/4.3a.
func (s *Server) Journal() *JournalWriter { return s.journal }

// Stats is a snapshot of relay state for the operator metrics endpoint
// (Docs/protocol/relay-hosted.md §5); it carries no per-key or per-account data.
type Stats struct {
	Connections int
	QueueRows   int64
	QueueBytes  int64
}

// Stats reports current connections and offline-queue occupancy.
func (s *Server) Stats() (Stats, error) {
	s.mu.Lock()
	n := len(s.conns)
	s.mu.Unlock()
	rows, bytes, err := s.q.stats()
	return Stats{Connections: n, QueueRows: rows, QueueBytes: bytes}, err
}

// New returns a Server with an in-memory offline queue, ignoring
// Options.QueuePath. It is for tests and cannot fail; anything that wants a
// persistent queue must use Open and handle its error.
func New(opts Options) *Server {
	opts.QueuePath = ""
	s, err := Open(opts)
	if err != nil {
		panic(err)
	}
	return s
}

// Open returns a Server, opening (or creating) the offline queue database.
func Open(opts Options) (*Server, error) {
	s := &Server{log: opts.Logger, ttl: opts.ChallengeTTL, queue: opts.SendQueue, now: opts.Now, conns: map[string]*conn{}, journal: opts.Journal}
	s.pairs = newPairings(opts.PairTTL, opts.PairMaxCodes, opts.PairFailLimit, opts.PairFailWindow)
	s.pairs.v1 = opts.AllowPairingV1
	if err := s.setAuth(opts); err != nil {
		return nil, err
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if s.ttl <= 0 {
		s.ttl = defaultChallengeTTL
	}
	if s.queue <= 0 {
		s.queue = defaultSendQueue
	}
	if opts.QueueTTL <= 0 {
		opts.QueueTTL = defaultQueueTTL
	}
	if opts.QueueMaxEnvelopes <= 0 {
		opts.QueueMaxEnvelopes = defaultQueueMaxEnvelope
	}
	if opts.QueueMaxBytes <= 0 {
		opts.QueueMaxBytes = defaultQueueMaxBytes
	}
	if opts.SweepInterval <= 0 {
		opts.SweepInterval = defaultSweepInterval
	}
	if opts.EphemeralPerMinute <= 0 {
		opts.EphemeralPerMinute = defaultEphemeralPerMinute
	}
	if opts.EphemeralMaxBytes <= 0 {
		opts.EphemeralMaxBytes = defaultEphemeralMaxBytes
	}
	s.eph = newEphemeralLimiter(opts.EphemeralPerMinute, s.now)
	s.ephMax = opts.EphemeralMaxBytes
	q, err := openQueue(opts.QueuePath, opts.QueueTTL, opts.QueueMaxEnvelopes, opts.QueueMaxBytes, s.now)
	if err != nil {
		return nil, err
	}
	s.q = q
	s.stopSweep, s.sweepDone = make(chan struct{}), make(chan struct{})
	go s.sweepLoop(opts.SweepInterval)
	return s, nil
}

// sweepLoop purges expired queue entries until Close.
func (s *Server) sweepLoop(every time.Duration) {
	defer close(s.sweepDone)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.stopSweep:
			return
		case <-t.C:
			_, _ = s.Sweep()
		}
	}
}

// Sweep purges envelopes older than the queue TTL now and reports how many.
// The relay also does this periodically.
func (s *Server) Sweep() (int64, error) {
	n, err := s.q.sweep()
	switch {
	case err != nil:
		s.log.Warn("queue sweep failed", "event", "queue_error", "op", "sweep", "error", err)
	case n > 0:
		s.log.Info("queue expired", "event", "queue_expire", "count", n)
	}
	return n, err
}

// Queued reports how many unexpired envelopes are waiting for the peer with the given key.
func (s *Server) Queued(key string) (int, error) { return s.q.count(key) }

// Connected reports whether the peer with the given wire public key is online.
func (s *Server) Connected(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.conns[key]
	return ok
}

// Close waits for every in-flight queue drain to finish sending, then
// disconnects each peer (WebSocket status "going away", itself waiting for
// that peer's own outbound buffer to flush) before closing the offline
// queue. HTTP servers do not track hijacked connections, so call this on
// shutdown after http.Server.Shutdown; together they are the SIGTERM drain
// (Docs/protocol/relay-hosted.md §3 "Restart without loss").
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		close(s.stopSweep)
		<-s.sweepDone
		// Under mu, so every drainWG.Add in startDrain happens before this
		// Wait or not at all (sync.WaitGroup forbids Add racing with Wait).
		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()
		drained := make(chan struct{})
		go func() { s.drainWG.Wait(); close(drained) }()
		select {
		case <-drained:
		case <-time.After(drainWaitTimeout):
		}
		s.mu.Lock()
		for _, c := range s.conns {
			// The close handshake itself (coder/websocket waits up to 5s to
			// write it and 5s for the peer's reply) runs in the background,
			// as before: Close must not block on a peer that stopped
			// reading. drainClose only waits for this connection's own
			// outbound buffer to flush first.
			go func(c *conn) { c.drainClose("relay shutting down") }(c)
		}
		s.mu.Unlock()
		_ = s.q.close()
	})
}

// ServeHTTP serves envelope.ConnectPath as a WebSocket endpoint and
// HealthPath as the unauthenticated health check.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == HealthPath {
		s.serveHealth(w, r)
		return
	}
	if r.URL.Path != envelope.ConnectPath {
		http.NotFound(w, r)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept has already replied
	}
	defer func() { _ = ws.CloseNow() }()
	ws.SetReadLimit(envelope.MaxAuthFrameBytes)

	c, err := s.authenticate(r.Context(), ws, clientPrefix(r.RemoteAddr))
	if err != nil {
		msg := "authentication failed"
		if errors.Is(err, envelope.ErrAuthV1Refused) {
			// A valid v1 signature from an old daemon: not a failed
			// authentication for any lockout, but still refused.
			msg = err.Error()
			s.log.Warn("auth rejected", "event", "auth_failed", "reason", "auth_v1_refused")
		} else {
			s.log.Warn("auth rejected", "event", "auth_failed")
		}
		_ = writeControl(r.Context(), ws, envelope.Control{Op: envelope.OpError, Code: envelope.CodeAuthFailed, Message: msg})
		_ = ws.Close(websocket.StatusPolicyViolation, "authentication failed")
		return
	}
	ws.SetReadLimit(envelope.MaxFrameBytes)
	s.serve(r.Context(), c)
}

// clientPrefix groups a connecting client's remote address into a network
// prefix for the per-prefix pairing limits: /24 for IPv4, /48 for IPv6
// (Docs/protocol/relay-hosted.md). This reads the raw TCP peer address; a
// relay behind a trusted proxy takes the real client IP instead (4.0b).
func clientPrefix(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(24, 32)).String()
	}
	return ip.Mask(net.CIDRMask(48, 128)).String()
}

// authenticate runs the challenge/response and returns the registered-to-be connection.
func (s *Server) authenticate(ctx context.Context, ws *websocket.Conn, prefix string) (*conn, error) {
	nonce := make([]byte, envelope.NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	issued := time.Now()
	ctx, cancel := context.WithTimeout(ctx, s.ttl)
	defer cancel()

	err := writeControl(ctx, ws, envelope.Control{
		Op:      envelope.OpChallenge,
		Version: envelope.ProtocolVersion,
		Nonce:   envelope.EncodeNonce(nonce),
		Expires: issued.Add(s.ttl).UTC().Format(time.RFC3339),
		Auth:    s.authOffer(),
	})
	if err != nil {
		return nil, err
	}
	typ, frame, err := ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText || time.Since(issued) > s.ttl {
		return nil, errors.New("expired or wrong frame type")
	}
	f, err := envelope.Classify(frame)
	if err != nil || f.Control == nil {
		return nil, errors.New("first frame is not auth")
	}
	pub, err := s.verifyAuth(*f.Control, nonce)
	if err != nil {
		return nil, err
	}
	return newConn(ws, envelope.KeyString(pub), s.queue, prefix), nil
}

// serve registers c, tells it it is ready, and forwards its envelopes until it disconnects.
func (s *Server) serve(ctx context.Context, c *conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	c.ctx = ctx
	if old := s.register(c); old != nil {
		go old.kick("replaced") // Close waits for the peer's reply; don't stall the new connection
	}
	defer s.unregister(c)
	s.log.Info("peer connected", "event", "connect", "peer", short(c.key))

	c.send(control(envelope.Control{Op: envelope.OpReady, PublicKey: c.key, Features: []string{envelope.FeatureEphemeral}}))
	go c.writeLoop(ctx, cancel)
	if s.drainStep(c) { // first batch inline so an idle queue is settled before we read
		s.startDrain(c)
	}

	for {
		typ, frame, err := c.ws.Read(ctx)
		if err != nil {
			s.log.Info("peer disconnected", "event", "disconnect", "peer", short(c.key))
			return
		}
		if typ != websocket.MessageText {
			c.kick("binary frames are not allowed")
			return
		}
		if !s.route(c, frame) {
			c.kick("unexpected control frame")
			return
		}
	}
}

// route forwards one frame from sender. It returns false if the frame is a
// protocol violation that must close the connection.
func (s *Server) route(sender *conn, frame []byte) bool {
	f, err := envelope.Classify(frame)
	if err != nil {
		s.reject(sender, envelope.CodeBadEnvelope, "frame is not a valid envelope", "")
		return true
	}
	if f.Control != nil {
		return s.handleControl(sender, f.Control)
	}
	h, err := envelope.ParseHeader(frame)
	if err != nil {
		s.reject(sender, envelope.CodeBadEnvelope, "invalid envelope: "+err.Error(), "")
		return true
	}
	if h.From != sender.key {
		s.reject(sender, envelope.CodeBadSender, "from does not match the authenticated key", h.ID)
		return true
	}
	if envelope.IsEphemeral(h.Type) {
		s.routeEphemeral(sender, h, frame)
		return true
	}
	s.mu.Lock()
	dst := s.conns[h.To]
	s.mu.Unlock()
	res := directDraining // no connection: queue it
	if dst != nil {
		res = dst.direct(frame)
	}
	switch res {
	case directSent:
		s.log.Info("routed", "event", "route", "from", short(h.From), "to", short(h.To), "type", h.Type, "id", h.ID, "bytes", len(frame))
	case directBusy, directDraining:
		// A recipient that is slow, offline or still receiving its backlog gets
		// the envelope through the queue, behind everything queued before it.
		s.enqueue(sender, h, frame)
	}
	return true
}

// enqueue stores an envelope for a peer that is offline, slow or still
// receiving its backlog, and tells the sender it was queued.
func (s *Server) enqueue(sender *conn, h envelope.Header, frame []byte) {
	switch err := s.q.add(h, frame); {
	case errors.Is(err, errQueueFull):
		s.log.Info("dropped", "event", "drop", "reason", envelope.CodeQueueFull, "from", short(h.From), "to", short(h.To), "type", h.Type, "id", h.ID, "bytes", len(frame))
		s.reject(sender, envelope.CodeQueueFull, "recipient's offline queue is full", h.ID)
		return
	case err != nil:
		s.log.Warn("queue failed", "event", "queue_error", "op", "add", "error", err)
		s.reject(sender, envelope.CodeInternal, "could not queue the envelope", h.ID)
		return
	}
	s.log.Info("queued", "event", "queue", "from", short(h.From), "to", short(h.To), "type", h.Type, "id", h.ID, "bytes", len(frame))
	sender.send(control(envelope.Control{Op: envelope.OpQueued, Ref: h.ID}))
	// The recipient may have connected while we were storing it.
	s.mu.Lock()
	dst := s.conns[h.To]
	s.mu.Unlock()
	if dst != nil {
		s.arm(dst)
	}
}

// arm makes sure c's backlog is being delivered.
func (s *Server) arm(c *conn) {
	c.mu.Lock()
	start := !c.draining
	c.draining = true
	c.mu.Unlock()
	if start {
		s.startDrain(c)
	}
}

// startDrain starts a goroutine delivering c's queued envelopes, oldest
// first, until none are left, and reports whether it did. Close waits for
// these before disconnecting anyone, so the drain is counted in drainWG
// before the goroutine exists; once Close has begun none is started (the
// relay is going away and c with it).
func (s *Server) startDrain(c *conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.drainWG.Add(1)
	go func() {
		defer s.drainWG.Done()
		for s.drainStep(c) {
		}
	}()
	return true
}

// drainStep sends one batch of c's queue and reports whether more may remain.
// When the queue is empty it switches c back to direct forwarding; that check
// and the switch happen under c.mu, so an envelope is either seen here or
// forwarded directly after everything queued ahead of it.
func (s *Server) drainStep(c *conn) bool {
	c.mu.Lock()
	rows, err := s.q.next(c.key, c.cursor, drainBatch)
	if err != nil {
		c.mu.Unlock()
		s.log.Warn("queue failed", "event", "queue_error", "op", "next", "peer", short(c.key), "error", err)
		go c.kick("offline queue unavailable")
		return false
	}
	if len(rows) == 0 {
		c.draining = false
		c.mu.Unlock()
		return false
	}
	c.cursor = rows[len(rows)-1].seq
	c.mu.Unlock()
	for _, r := range rows {
		select {
		case c.out <- r.frame:
		case <-c.ctx.Done():
			return false
		}
	}
	s.log.Info("delivered from queue", "event", "queue_flush", "peer", short(c.key), "count", len(rows))
	return true
}

func (s *Server) reject(to *conn, code, msg, ref string) {
	to.send(control(envelope.Control{Op: envelope.OpError, Code: code, Message: msg, Ref: ref}))
}

func (s *Server) register(c *conn) (old *conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old = s.conns[c.key]
	s.conns[c.key] = c
	return old
}

func (s *Server) unregister(c *conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns[c.key] == c {
		delete(s.conns, c.key)
	}
}

func short(key string) string {
	if len(key) > 8 {
		return key[:8]
	}
	return key
}
