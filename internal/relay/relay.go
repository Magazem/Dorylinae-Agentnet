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
	// PairFailWindow is the rate limit window. Default 1m.
	PairFailWindow time.Duration
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
}

// Server is an http.Handler serving the relay protocol.
type Server struct {
	log   *slog.Logger
	ttl   time.Duration
	queue int
	now   func() time.Time
	pairs *pairings
	q     *queue

	mu    sync.Mutex
	conns map[string]*conn // keyed by wire public key

	stopSweep chan struct{}
	sweepDone chan struct{}
	closeOnce sync.Once
}

// New returns a Server. It panics if the offline queue cannot be opened, which
// can only happen when Options.QueuePath is set; use Open to handle that error.
func New(opts Options) *Server {
	s, err := Open(opts)
	if err != nil {
		panic(err)
	}
	return s
}

// Open returns a Server, opening (or creating) the offline queue database.
func Open(opts Options) (*Server, error) {
	s := &Server{log: opts.Logger, ttl: opts.ChallengeTTL, queue: opts.SendQueue, now: opts.Now, conns: map[string]*conn{}}
	s.pairs = newPairings(opts.PairTTL, opts.PairFailLimit, opts.PairFailWindow)
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

// Close disconnects every peer (WebSocket status "going away") and closes the
// offline queue. HTTP servers do not track hijacked connections, so call this
// on shutdown after http.Server.Shutdown.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		close(s.stopSweep)
		<-s.sweepDone
		s.mu.Lock()
		for _, c := range s.conns {
			go func() { _ = c.ws.Close(websocket.StatusGoingAway, "relay shutting down") }()
		}
		s.mu.Unlock()
		_ = s.q.close()
	})
}

// ServeHTTP serves envelope.ConnectPath as a WebSocket endpoint.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	c, err := s.authenticate(r.Context(), ws)
	if err != nil {
		s.log.Warn("auth rejected", "event", "auth_failed")
		_ = writeControl(r.Context(), ws, envelope.Control{Op: envelope.OpError, Code: envelope.CodeAuthFailed, Message: "authentication failed"})
		_ = ws.Close(websocket.StatusPolicyViolation, "authentication failed")
		return
	}
	ws.SetReadLimit(envelope.MaxFrameBytes)
	s.serve(r.Context(), c)
}

// authenticate runs the challenge/response and returns the registered-to-be connection.
func (s *Server) authenticate(ctx context.Context, ws *websocket.Conn) (*conn, error) {
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
	pub, err := envelope.VerifyAuth(*f.Control, nonce)
	if err != nil {
		return nil, err
	}
	return newConn(ws, envelope.KeyString(pub), s.queue), nil
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

	c.send(control(envelope.Control{Op: envelope.OpReady, PublicKey: c.key}))
	go c.writeLoop(ctx, cancel)
	if s.drainStep(c) { // first batch inline so an idle queue is settled before we read
		go s.drain(c)
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
		go s.drain(c)
	}
}

// drain delivers c's queued envelopes, oldest first, until none are left.
func (s *Server) drain(c *conn) {
	for s.drainStep(c) {
	}
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
