// Package relay is the AgentNet relay server: it authenticates daemons by a
// signed challenge, keeps an in-memory registry of connections keyed by public
// key, and forwards envelopes by their "to" field. The protocol is specified in
// Docs/protocol/envelope.md.
//
// The relay never decodes, inspects or logs envelope payloads, and forwards
// each frame byte for byte.
package relay

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
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

	// Accounts (Docs/protocol/accounts.md, ticket 4.2a). Accounts is the
	// sign-in mode: "" or AccountsOff (the default) serve every key as
	// before; AccountsGitHub, AccountsEmail or AccountsBoth require a bound
	// account, advertise the "accounts" feature and carry the key's account
	// state in ready. LoginURL is the fixed login page bind_pending names;
	// BindPollInterval the minimum time between bind_poll frames (default 5 s).
	Accounts         string
	LoginURL         string
	BindPollInterval time.Duration

	// Abuse limits (Docs/protocol/relay-hosted.md §2, ticket 4.0b). Zero
	// takes the spec default given for each; a negative value turns the
	// limit off (tests only). A "prefix" is the client's /24 (IPv4) or /48
	// (IPv6).
	//
	// UpgradesPerMinute and UpgradeBurst: new WebSocket upgrades per prefix
	// (30/min, burst 60); past them HTTP 429 before the upgrade.
	UpgradesPerMinute int
	UpgradeBurst      int
	// MaxConnsPerPrefix: concurrent connections per prefix (64), HTTP 429.
	MaxConnsPerPrefix int
	// AuthFailuresPerPrefix per AuthFailWindow (10 per 10 min): past them the
	// prefix gets HTTP 429 for the rest of the window. A valid v1 signature
	// refused by a v2-only relay does not count.
	AuthFailuresPerPrefix int
	AuthFailWindow        time.Duration
	// MaxUnauthConns: connections still authenticating, relay-wide (256), HTTP 503.
	MaxUnauthConns int
	// MaxConns: authenticated connections, relay-wide (5000, --max-conns);
	// past it error relay_full and close 1013.
	MaxConns int
	// MaxKeysPerPrefix: distinct authenticated keys connected from one
	// prefix (64); past it relay_full and close 1013.
	MaxKeysPerPrefix int
	// PrefixEnvelopesPerMinute and PrefixBytesPerMinute: non-ephemeral
	// envelopes sent by all keys of a prefix together (600/min, 64 MiB/min);
	// past them error rate_limited.
	PrefixEnvelopesPerMinute int
	PrefixBytesPerMinute     int64
	// KeyEnvelopesPerMinute, KeyEnvelopeBurst and KeyBytesPerMinute:
	// non-ephemeral envelopes one key sends (120/min burst 240, 32 MiB/min);
	// past them error rate_limited (ref = envelope id), the envelope is
	// dropped and the connection stays open.
	KeyEnvelopesPerMinute int
	KeyEnvelopeBurst      int
	KeyBytesPerMinute     int64
	// ControlPerMinute: control frames other than ack one key sends (60/min);
	// past it rate_limited, and 3 such minutes in a row close the connection (1008).
	ControlPerMinute int
	// ReconnectsPerMinute: authentications of one key (20/min); past it
	// rate_limited right after auth and close 1013.
	ReconnectsPerMinute int
	// ConnBufferBytes: bytes waiting in one connection's outbound buffer
	// (4 MiB, on top of SendQueue frames); past it envelopes take the queue path.
	ConnBufferBytes int64
	// MaxInflight: bytes waiting in every outbound buffer together (256 MiB,
	// --max-inflight), queue batches read for delivery included; past it
	// direct sends take the queue path. Frames being read from peers have a
	// second budget of the same size; past it the reading connection is
	// closed with 1013 (R-4.0 H1). MaxInflightEphemeral: presence and
	// control frames waiting in outbound buffers (--max-inflight-ephemeral,
	// default max(MaxInflight/8, 1 MiB)); past it they are dropped. One
	// prefix holds at most an eighth of each budget, and a charge that does
	// not fit first evicts the heaviest holder (R55-F1,
	// relay-hosted.md "Memory budgets and fairness").
	MaxInflight          int64
	MaxInflightEphemeral int64
	// FrameReadTimeout: a frame must be read completely within this time of
	// its first byte (30 s, --frame-read-timeout); past it the connection is
	// closed 1013 (R55-F1).
	FrameReadTimeout time.Duration
	// Offline queue caps on top of QueueMaxEnvelopes/QueueMaxBytes: per
	// sender -> recipient (300, 8 MiB), per sender over all recipients
	// (2000, 64 MiB) and relay-wide (4 GiB, --queue-max-total); past them
	// queue_full. QueueMinFreeDisk (1 GiB): below it new envelopes get
	// internal ("relay storage low"); acks still work. FreeDisk reports the
	// free bytes of the file system holding a directory (default: the OS).
	QueuePairMaxEnvelopes   int
	QueuePairMaxBytes       int64
	QueueSenderMaxEnvelopes int
	QueueSenderMaxBytes     int64
	QueueMaxTotal           int64
	QueueMinFreeDisk        int64
	FreeDisk                func(dir string) (uint64, error)
	// QueueRedeliverPerKey and QueueRedeliverPerPrefix: bytes of queued rows
	// sent again to a recipient that did not ack them, per recipient key and
	// per prefix of the receiving connection, per hour with a burst of the
	// same size (32 MiB and 128 MiB, --queue-redeliver-per-key and
	// --queue-redeliver-per-prefix). Past them the redelivery is skipped and
	// retried once the budget has refilled; first deliveries are not
	// budgeted (R55-F2, relay-hosted.md "Offline queue delivery and expiry").
	QueueRedeliverPerKey    int64
	QueueRedeliverPerPrefix int64

	// ClientIPHeader names the header a trusted proxy puts the client IP in
	// (e.g. Fly-Client-IP or X-Forwarded-For, whose last entry is used). It
	// is honoured only on a Public relay and only when the TCP peer is in
	// TrustedProxies; from any other peer the TCP address is used.
	ClientIPHeader string
	TrustedProxies []netip.Prefix

	// MinClient is the oldest daemon release ("MAJOR.MINOR.PATCH") this
	// relay supports, sent as ready.min_client (ticket 4.4a). Advisory: it
	// never refuses a connection. Empty sends none; anything else must be a
	// release version or Open fails.
	MinClient string
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

	// Abuse limits and the client address rule (4.0b). Fixed after Open.
	// led holds the memory budgets (R55-F1); frameTimeout is
	// --frame-read-timeout (0: none).
	lim          *limits
	led          *ledger
	frameTimeout time.Duration
	public       bool
	ipHeader     string
	trusted      []netip.Prefix

	// onRoute, if set, runs at the start of every route (tests only).
	onRoute func(*conn)

	mu      sync.Mutex
	conns   map[string]*conn // keyed by wire public key
	closing bool             // Close has begun; no new drain may start (guarded by mu)
	// authed counts admitted authenticated connections (MaxConns), and
	// prefixKeys the connections of each key per prefix (MaxKeysPerPrefix).
	// Both are guarded by mu.
	authed     int
	prefixKeys map[string]map[string]int
	// unbound lists each prefix's unbound connections, oldest first, on a
	// relay with accounts (guarded by mu).
	unbound map[string][]*conn

	// acct is the account state; nil on a relay without accounts.
	acct *accounts

	minClient string // ready.min_client; fixed after Open

	journal   *JournalWriter
	drainWG   sync.WaitGroup // outstanding queue-drain goroutines; Close waits for these
	drainHeld atomic.Int64   // bytes queue drains have read and not yet put in an outbound buffer
	// redeliveredBytes and redeliverySkips count queued bytes sent again and
	// redeliveries skipped for want of budget (R55-F2 metrics).
	redeliveredBytes atomic.Int64
	redeliverySkips  atomic.Int64
	stopSweep        chan struct{}
	sweepBudget      time.Duration // work per sweep tick; sweepTickBudget (tests change it)
	sweepDone        chan struct{}
	closeOnce        sync.Once
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
	// QueueRedeliveredBytes and QueueRedeliveriesSkipped count, since start,
	// queued bytes sent again to a key that had not acked them and
	// redeliveries skipped for want of budget (R55-F2).
	QueueRedeliveredBytes    int64
	QueueRedeliveriesSkipped int64
}

// Stats reports current connections and offline-queue occupancy.
func (s *Server) Stats() (Stats, error) {
	s.mu.Lock()
	n := len(s.conns)
	s.mu.Unlock()
	rows, bytes, err := s.q.stats()
	return Stats{Connections: n, QueueRows: rows, QueueBytes: bytes,
		QueueRedeliveredBytes: s.redeliveredBytes.Load(), QueueRedeliveriesSkipped: s.redeliverySkips.Load()}, err
}

// Budgets are the memory budgets a Server enforces (relay-hosted.md
// "Memory budgets and fairness"), with the defaults applied, and the
// redelivery budgets per hour (R55-F2). 0 means none.
type Budgets struct {
	Outbound, Read, Ephemeral int64
	FrameReadTimeout          time.Duration
	RedeliverPerKey           int64
	RedeliverPerPrefix        int64
}

// Budgets reports the memory budgets in force, for the start line.
func (s *Server) Budgets() Budgets {
	return Budgets{Outbound: s.led.pools[kindOutbound].max, Read: s.led.pools[kindRead].max,
		Ephemeral: s.led.pools[kindEphemeral].max, FrameReadTimeout: s.frameTimeout,
		RedeliverPerKey: int64(s.lim.redeliverKey.burst), RedeliverPerPrefix: int64(s.lim.redeliverPrefix.burst)}
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
	s := &Server{log: opts.Logger, ttl: opts.ChallengeTTL, queue: opts.SendQueue, now: opts.Now, conns: map[string]*conn{}, journal: opts.Journal,
		prefixKeys: map[string]map[string]int{}, unbound: map[string][]*conn{}, public: opts.Public, ipHeader: opts.ClientIPHeader, trusted: opts.TrustedProxies}
	s.pairs = newPairings(opts.PairTTL, opts.PairMaxCodes, opts.PairFailLimit, opts.PairFailWindow)
	s.pairs.v1 = opts.AllowPairingV1
	if err := s.setAuth(opts); err != nil {
		return nil, err
	}
	if opts.MinClient != "" {
		if _, ok := version.ParseRelease(opts.MinClient); !ok {
			return nil, fmt.Errorf("min client version %q is not MAJOR.MINOR.PATCH", opts.MinClient)
		}
		s.minClient = opts.MinClient
	}
	if opts.ClientIPHeader != "" && len(opts.TrustedProxies) == 0 {
		return nil, errors.New("a client IP header needs at least one trusted proxy")
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
	s.lim = newLimits(opts, s.now, s.log)
	inflight := orDefault(opts.MaxInflight, defaultMaxInflight)
	ephemeral := orDefault(opts.MaxInflightEphemeral, 0)
	if opts.MaxInflightEphemeral == 0 && inflight > 0 {
		ephemeral = max(inflight/8, minEphemeralBudget)
	}
	s.led = newLedger(inflight, inflight, ephemeral, s.now)
	s.led.hit = s.lim.hit
	s.frameTimeout = orDefault(opts.FrameReadTimeout, defaultFrameReadTimeout)
	q, err := openQueue(opts.QueuePath, opts.QueueTTL, opts.QueueMaxEnvelopes, opts.QueueMaxBytes, s.now)
	if err != nil {
		return nil, err
	}
	q.lim = queueLimits{
		pairCount:   orDefault(opts.QueuePairMaxEnvelopes, defaultQueuePairMaxEnvelopes),
		pairBytes:   orDefault(opts.QueuePairMaxBytes, defaultQueuePairMaxBytes),
		senderCount: orDefault(opts.QueueSenderMaxEnvelopes, defaultQueueSenderMaxEnvelopes),
		senderBytes: orDefault(opts.QueueSenderMaxBytes, defaultQueueSenderMaxBytes),
		totalBytes:  orDefault(opts.QueueMaxTotal, defaultQueueMaxTotal),
		minFree:     orDefault(opts.QueueMinFreeDisk, defaultQueueMinFreeDisk),
		freeDisk:    opts.FreeDisk,
	}
	if q.lim.freeDisk == nil {
		q.lim.freeDisk = freeDiskSpace
	}
	s.q = q
	if opts.Accounts != "" && opts.Accounts != AccountsOff {
		if s.acct, err = newAccounts(opts, q, opts.Journal, s.now); err != nil {
			_ = q.close()
			return nil, err
		}
		s.acct.stop, s.acct.done = make(chan struct{}), make(chan struct{})
		if opts.QueuePath != "" { // only a file database can be changed by relay admin
			go s.watchAccounts()
		} else {
			close(s.acct.done)
		}
	}
	s.stopSweep, s.sweepDone = make(chan struct{}), make(chan struct{})
	s.sweepBudget = sweepTickBudget
	go s.sweepLoop(opts.SweepInterval)
	return s, nil
}

// sweepLoop purges expired queue entries, then retries skipped
// redeliveries, until Close.
func (s *Server) sweepLoop(every time.Duration) {
	defer close(s.sweepDone)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.stopSweep:
			return
		case <-t.C:
			tick := s.now() // before the sweep, which may take up to 30 s
			_, _ = s.Sweep()
			s.retrySkippedAt(tick)
		}
	}
}

// Sweep purges envelopes older than the queue TTL now and reports how many.
// The relay also does this periodically. It works in batches for at most
// sweepTickBudget and stops early on Close; what is left expires on the next
// tick (R55-011). A sweep that deleted more than the WAL limit ends with a
// non-waiting TRUNCATE checkpoint, and the delivered marks of keys with no
// row left are pruned.
func (s *Server) Sweep() (int64, error) {
	n, bytes, err := s.q.sweepExpired(s.sweepBudget, s.stopSweep)
	if bytes > walSizeLimit {
		if _, cerr := s.q.truncateWAL(); cerr != nil {
			s.log.Warn("queue checkpoint failed", "event", "queue_error", "op", "checkpoint", "error", cerr)
		}
	}
	if _, perr := s.q.pruneDelivered(s.sweepBudget, s.stopSweep); perr != nil {
		s.log.Warn("queue prune failed", "event", "queue_error", "op", "prune_delivered", "error", perr)
	}
	if s.acct != nil {
		if perr := s.acct.store.prune(); perr != nil {
			s.log.Warn("accounts failed", "event", "accounts_error", "op", "prune", "error", perr)
		}
	}
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
		if s.acct != nil {
			close(s.acct.stop)
			<-s.acct.done
		}
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
	if r.URL.Path != HealthPath && r.URL.Path != envelope.ConnectPath {
		http.NotFound(w, r)
		return
	}
	prefix := clientPrefix(s.clientAddr(r))
	if r.URL.Path == HealthPath {
		if !s.lim.admitHealth(prefix) {
			s.lim.hit(limitHealth, "prefix", prefix)
			w.Header().Set("Retry-After", "60")
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}
		s.serveHealth(w, r)
		return
	}
	if refused := s.lim.admitUpgrade(prefix); refused != nil {
		s.lim.hit(refused.limit, "prefix", prefix)
		w.Header().Set("Retry-After", "60")
		http.Error(w, http.StatusText(refused.status), refused.status)
		return
	}
	defer s.lim.release(prefix)
	hw := &hijackWriter{ResponseWriter: w}
	ws, err := websocket.Accept(hw, r, nil)
	if err != nil {
		s.lim.authDone(prefix, false)
		return // Accept has already replied
	}
	defer func() { _ = ws.CloseNow() }()
	ws.SetReadLimit(envelope.MaxAuthFrameBytes)

	c, err := s.authenticate(r.Context(), ws, prefix)
	s.lim.authDone(prefix, err != nil && !errors.Is(err, envelope.ErrAuthV1Refused) && !errors.Is(err, errNoAnswer))
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
	if !s.lim.reconnect(c.key) {
		s.lim.hit(limitReconnects, "peer", short(c.key))
		s.refuseAfterAuth(r.Context(), ws, envelope.CodeRateLimited, "too many reconnects; retry later")
		return
	}
	if limit := s.admit(c); limit != "" {
		if limit == limitKeysPerPrefix {
			s.lim.hit(limit, "prefix", prefix)
		} else {
			s.lim.hit(limit, "relay", "all")
		}
		s.refuseAfterAuth(r.Context(), ws, envelope.CodeRelayFull, "relay is full; retry later")
		return
	}
	defer s.dismiss(c)
	c.maxBytes, c.led, c.raw = s.lim.connBuffer, s.led, hw.raw
	// Deferred so that a panic while routing cannot leak budget (R55-144).
	defer c.release()
	ws.SetReadLimit(envelope.MaxFrameBytes)
	s.serve(r.Context(), c)
}

// hijackWriter keeps the socket websocket.Accept hijacks, so an evicted
// connection can be closed without waiting for its close handshake.
type hijackWriter struct {
	http.ResponseWriter
	raw net.Conn
}

func (h *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := h.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("http.ResponseWriter does not implement http.Hijacker")
	}
	c, brw, err := hj.Hijack()
	h.raw = c
	return c, brw, err
}

// refuseAfterAuth answers an authenticated connection the relay will not
// serve now with an error frame and close 1013 (try again later).
func (s *Server) refuseAfterAuth(ctx context.Context, ws *websocket.Conn, code, msg string) {
	_ = writeControl(ctx, ws, envelope.Control{Op: envelope.OpError, Code: code, Message: msg})
	_ = ws.Close(websocket.StatusTryAgainLater, msg)
}

// admit counts c against MaxConns and MaxKeysPerPrefix, or returns the
// name of the limit that refuses it. A key that is already connected
// replaces its old connection, so it never counts as a new key or
// connection against the caps. Every admitted c must be dismissed.
func (s *Server) admit(c *conn) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, replacing := s.conns[c.key]
	if !replacing && s.lim.maxConns > 0 && s.authed >= s.lim.maxConns {
		return limitMaxConns
	}
	keys := s.prefixKeys[c.prefix]
	if keys[c.key] == 0 && s.lim.maxKeysPerPrefix > 0 && len(keys) >= s.lim.maxKeysPerPrefix {
		return limitKeysPerPrefix
	}
	if keys == nil {
		keys = map[string]int{}
		s.prefixKeys[c.prefix] = keys
	}
	keys[c.key]++
	s.authed++
	return ""
}

// dismiss undoes admit.
func (s *Server) dismiss(c *conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authed--
	keys := s.prefixKeys[c.prefix]
	if keys[c.key]--; keys[c.key] <= 0 {
		delete(keys, c.key)
	}
	if len(keys) == 0 {
		delete(s.prefixKeys, c.prefix)
	}
}

// clientPrefix groups a connecting client's address (host:port or a bare
// IP) into a network prefix for the per-prefix limits: /24 for IPv4, /48 for
// IPv6 (Docs/protocol/relay-hosted.md). ServeHTTP passes it clientAddr: the
// TCP peer, or the client IP header of a trusted proxy (4.0b).
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

// errNoAnswer is a client that went away before answering its challenge.
// It is not a failed authentication for the per-prefix lockout (4.0b): only
// a wrong or late answer is.
var errNoAnswer = errors.New("connection closed before auth")

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
		return nil, errNoAnswer
	}
	typ, frame, err := ws.Read(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err // no answer within the challenge TTL: a failed authentication
		}
		return nil, errNoAnswer
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
	old, evict, bound, suspended := s.register(c)
	if suspended {
		// Docs/protocol/accounts.md: ready, then account_suspended and close 1008.
		s.log.Info("account closed connection", "event", "account_close", "reason", envelope.CodeAccountSuspended, "peer", short(c.key))
		_ = writeControl(ctx, c.ws, s.readyFor(c))
		_ = writeControl(ctx, c.ws, envelope.Control{Op: envelope.OpError, Code: envelope.CodeAccountSuspended, Message: "the account is suspended"})
		_ = c.ws.Close(websocket.StatusPolicyViolation, envelope.CodeAccountSuspended)
		return
	}
	if old != nil {
		go old.kick("replaced") // Close waits for the peer's reply; don't stall the new connection
	}
	if evict != nil {
		s.lim.hit(limitUnboundPerPrefix, "prefix", evict.prefix)
		go func() {
			_ = evict.ws.Close(websocket.StatusTryAgainLater, "too many unbound connections from this network")
		}()
	}
	defer s.unregister(c)
	s.log.Info("peer connected", "event", "connect", "peer", short(c.key))

	c.sendReady(control(s.readyFor(c)))
	go c.writeLoop(ctx, cancel)
	// On a relay with accounts nothing is delivered to an unbound key: its
	// queue waits (for a re-bind) until accountsChanged sees it bound.
	if bound {
		if s.acct != nil {
			s.touchAccountKey(c.key)
		}
		if s.drainStep(c, false) { // first batch inline so an idle queue is settled before we read
			s.startDrain(c)
		}
	}

	for {
		typ, frame, err := s.readFrame(ctx, c)
		switch {
		case errors.Is(err, errReadBudget):
			s.lim.hit(limitReading, "relay", "all")
			c.closeBounded(websocket.StatusTryAgainLater, "relay busy; retry later")
			return
		case err != nil:
			s.log.Info("peer disconnected", "event", "disconnect", "peer", short(c.key))
			return
		}
		ok := s.handleFrame(c, typ, frame)
		c.doneRead()
		if !ok {
			return
		}
	}
}

// handleFrame routes one frame read from c and reports whether to read the
// next one. A frame c finished after it was evicted is discarded (R55-F1),
// and every frame is charged to the byte buckets before it is parsed
// (R55-035).
func (s *Server) handleFrame(c *conn, typ websocket.MessageType, frame []byte) bool {
	if c.gone() {
		return false
	}
	if limit := s.lim.frameAllowed(c.key, c.prefix, len(frame)); limit != "" {
		if limit == limitPrefixBytes {
			s.lim.hit(limit, "prefix", c.prefix)
		} else {
			s.lim.hit(limit, "peer", short(c.key))
		}
		s.reject(c, envelope.CodeRateLimited, "sending too fast; retry later", "")
		return true
	}
	if typ != websocket.MessageText {
		c.kick("binary frames are not allowed")
		return false
	}
	if !s.route(c, frame) {
		c.kick("unexpected control frame")
		return false
	}
	return true
}

// readChunk is the initial buffer of a frame being read, and its growth step.
const readChunk = 4 << 10

// errReadBudget is a frame that could not be read because the relay-wide
// budget for frames being read is spent, even after one eviction.
var errReadBudget = errors.New("relay-wide read budget spent")

// errEvicted is a frame whose connection was evicted, or timed out, while
// it was being read.
var errEvicted = errors.New("connection evicted")

// defaultFrameReadTimeout is --frame-read-timeout (R55-F1).
const defaultFrameReadTimeout = 30 * time.Second

// readFrame reads one message from c. Its buffer is charged to the read
// budget as it grows (R-4.0 H1); a charge that does not fit evicts the
// holder that pays once (R55-F1). The frame must be read completely within
// frameTimeout of its first byte, or c is handled as evicted. c.doneRead
// uncharges the frame once it has been routed.
func (s *Server) readFrame(ctx context.Context, c *conn) (websocket.MessageType, []byte, error) {
	typ, r, err := c.ws.Reader(ctx)
	if err != nil {
		return 0, nil, err
	}
	c.startRead(time.Now()) // real time: frames are ordered by when they really started
	if s.frameTimeout > 0 {
		t := time.AfterFunc(s.frameTimeout, func() { s.frameTimedOut(c) })
		defer t.Stop()
	}
	var buf []byte
	for {
		if len(buf) == cap(buf) {
			// Double, but never past what the read limit lets a frame hold.
			grown := slices.Grow(buf, max(1, min(max(cap(buf), readChunk), envelope.MaxFrameBytes+1-len(buf))))
			if err := s.growRead(c, int64(cap(grown)-cap(buf))); err != nil {
				c.doneRead()
				return 0, nil, err
			}
			buf = grown
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if errors.Is(err, io.EOF) {
			return typ, buf, nil
		}
		if err != nil {
			c.doneRead()
			return 0, nil, err
		}
	}
}

// growRead charges n more bytes of c's frame to the read budget, evicting
// once if they do not fit (relay-hosted.md "When a charge does not fit").
func (s *Server) growRead(c *conn, n int64) error {
	ok, refused, over, dead := c.chargeRead(n)
	if refused && s.led.evictFor(c, kindRead, n, over) {
		ok, _, _, dead = c.chargeRead(n)
	}
	switch {
	case ok:
		return nil
	case dead:
		return errEvicted
	}
	return errReadBudget
}

// frameTimedOut ends c, whose frame was not read within frameTimeout, as an
// eviction: every charge is released at once and the close is bounded.
func (s *Server) frameTimedOut(c *conn) {
	if c.evict("frame too slow; retry later") {
		s.lim.hit(limitFrameTimeout, "peer", short(c.key))
	}
}

// route forwards one frame from sender. It returns false if the frame is a
// protocol violation that must close the connection.
func (s *Server) route(sender *conn, frame []byte) bool {
	if s.onRoute != nil {
		s.onRoute(sender)
	}
	f, err := envelope.Classify(frame)
	if err != nil {
		s.reject(sender, envelope.CodeBadEnvelope, "frame is not a valid envelope", "")
		return true
	}
	if f.Control != nil {
		if f.Control.Op != envelope.OpAck {
			ok, closeConn := s.lim.controlAllowed(sender.key)
			if !ok {
				s.lim.hit(limitControl, "peer", short(sender.key))
				s.reject(sender, envelope.CodeRateLimited, "too many control frames; slow down", f.Control.Ref)
				if closeConn {
					s.lim.hit(limitControlClose, "peer", short(sender.key))
					go sender.flushThenKick("control frame rate exceeded")
				}
				return true
			}
		}
		return s.handleControl(sender, f.Control)
	}
	// Every envelope, presence included: a payload the recipient cannot
	// parse is refused here and never queued or forwarded (R55-010).
	h, err := envelope.ParseHeader(frame)
	if err != nil {
		ref := ""
		if errors.Is(err, envelope.ErrBadPayload) {
			ref = h.ID // the routing fields are valid
		}
		s.reject(sender, envelope.CodeBadEnvelope, "invalid envelope: "+err.Error(), ref)
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
	if limit := s.lim.sendAllowed(sender.key, sender.prefix); limit != "" {
		if limit == limitPrefixEnvs {
			s.lim.hit(limit, "prefix", sender.prefix)
		} else {
			s.lim.hit(limit, "peer", short(sender.key))
		}
		s.reject(sender, envelope.CodeRateLimited, "sending too fast; retry later", h.ID)
		return true
	}
	if !s.accountRoutes(sender, h) {
		return true
	}
	s.mu.Lock()
	dst := s.conns[h.To]
	s.mu.Unlock()
	res := directDraining // no connection: queue it
	if dst != nil {
		res = dst.direct(exact(frame))
	}
	switch res {
	case directSent:
		s.log.Info("routed", "event", "route", "from", short(h.From), "to", short(h.To), "type", h.Type, "id", h.ID, "bytes", len(frame))
	case directBusy, directDraining:
		if res == directBusy {
			s.busyLimit(dst, len(frame))
		}
		// A recipient that is slow, offline or still receiving its backlog gets
		// the envelope through the queue, behind everything queued before it.
		s.enqueue(sender, h, frame)
	}
	return true
}

// enqueue stores an envelope for a peer that is offline, slow or still
// receiving its backlog, and tells the sender it was queued.
func (s *Server) enqueue(sender *conn, h envelope.Header, frame []byte) {
	err := s.q.add(h, frame)
	var full *queueFullError
	switch {
	case errors.As(err, &full):
		s.lim.hit(full.limit, "peer", short(h.From))
		s.log.Info("dropped", "event", "drop", "reason", envelope.CodeQueueFull, "from", short(h.From), "to", short(h.To), "type", h.Type, "id", h.ID, "bytes", len(frame))
		s.reject(sender, envelope.CodeQueueFull, "recipient's offline queue is full", h.ID)
		return
	case errors.Is(err, errStorageLow):
		s.lim.hit(limitQueueDisk, "relay", "all")
		s.reject(sender, envelope.CodeInternal, "relay storage low", h.ID)
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

// busyLimit logs which byte cap sent an envelope for dst down the queue
// path, if one did (a buffer full by frame count is not a 4.0b limit).
func (s *Server) busyLimit(dst *conn, n int) {
	if b := dst.buffered(); dst.maxBytes > 0 && b > 0 && b+int64(n) > dst.maxBytes {
		s.lim.hit(limitConnBuffer, "peer", short(dst.key))
		return
	}
	switch s.led.over(dst, kindOutbound, int64(n)) {
	case "relay":
		s.lim.hit(limitInflight, "relay", "all")
	case "prefix":
		s.lim.hit(limitInflightPrefix, "prefix", dst.prefix)
	}
}

// exact returns frame in a slice whose capacity is its length, copying it
// if readFrame left spare capacity, so a frame waiting in an outbound
// buffer pins no more than the bytes it is charged for (R55-034).
func exact(frame []byte) []byte {
	if cap(frame) == len(frame) {
		return frame
	}
	b := make([]byte, len(frame))
	copy(b, frame)
	return b
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
		for s.drainStep(c, true) {
		}
	}()
	return true
}

// drainStep sends one batch of c's queue and reports whether more may remain.
// When the queue is empty it switches c back to direct forwarding; that check
// and the switch happen under c.mu, so an envelope is either seen here or
// forwarded directly after everything queued ahead of it.
//
// The batch is charged to the relay-wide MaxInflight budget before it is read
// from the database, and waits for room in c's buffer (ConnBufferBytes) and
// in that budget first (review 50 M2, R-4.0 H1): a drain to a peer that
// stops reading never holds frames the budget does not count. With wait
// false (the inline first batch) it does not wait for room; it then reports
// true so that the drain goroutine waits instead of the read loop.
//
// Rows above the key's delivered mark H are first deliveries, claimed before
// they are sent; rows at or below it, from c's cursor up to H, are
// redeliveries under the budget (redeliverStep, R55-F2).
func (s *Server) drainStep(c *conn, wait bool) bool {
	if !c.reserve(c.ctx, drainReserve, wait) {
		return !wait
	}
	reserved := int64(drainReserve)
	defer func() { c.unreserve(reserved) }() // what the batch did not use
	c.mu.Lock()
	rows, high, err := s.q.claim(c.key, c.cursor, drainBatch, drainBatchBytes)
	for err == nil && rows == nil && c.cursor < high { // rows delivered before: a redelivery, H unchanged
		after := c.cursor
		c.mu.Unlock()
		next, sent, ok := s.redeliverStep(c, after, high, &reserved)
		if !ok {
			return false
		}
		c.mu.Lock()
		c.cursor = max(c.cursor, next)
		if sent > 0 {
			c.mu.Unlock()
			return true
		}
		// Nothing left to redeliver, or skipped: go on with first
		// deliveries in this step, so an idle queue is still settled here.
		rows, high, err = s.q.claim(c.key, c.cursor, drainBatch, drainBatchBytes)
	}
	if err != nil {
		c.mu.Unlock()
		s.queueFailed(c, "claim", err)
		return false
	}
	if len(rows) == 0 {
		c.draining = false
		c.mu.Unlock()
		return false
	}
	c.cursor = rows[len(rows)-1].seq
	c.mu.Unlock()
	if !s.sendBatch(c, rows, &reserved) {
		return false
	}
	s.log.Info("delivered from queue", "event", "queue_flush", "peer", short(c.key), "count", len(rows))
	return true
}

// sendBatch hands rows read from the queue to c's buffer, counting them as
// held by drains until they are in it. False means c is gone.
func (s *Server) sendBatch(c *conn, rows []queued, reserved *int64) bool {
	var held int64
	for _, r := range rows {
		held += int64(len(r.frame))
	}
	s.drainHeld.Add(held)
	defer func() { s.drainHeld.Add(-held) }()
	for _, r := range rows {
		if !c.sendReserved(c.ctx, r.frame, reserved) {
			return false
		}
		held -= int64(len(r.frame))
		s.drainHeld.Add(-int64(len(r.frame)))
	}
	return true
}

// queueFailed logs a queue error during a drain and closes c, as a failed
// read always has.
func (s *Server) queueFailed(c *conn, op string, err error) {
	s.log.Warn("queue failed", "event", "queue_error", "op", op, "peer", short(c.key), "error", err)
	go c.kick("offline queue unavailable")
}

// redeliverStep sends one batch of c's rows in (after, upto], all delivered
// before, under the redelivery budget (relay-hosted.md §2 "Redelivery policy
// for unacked rows"). The decision for the first row reads only its seq and
// length (review 66b M1); every row is paid before it is handed over, the
// rest of the batch from bytes reserved before the read, whose unused part
// is given back. At the first row the budget refuses, the rest of the range
// is recorded as skipped on c and the step ends there. It returns the position reached (upto after a
// skip or when the range is done), how many rows it sent, and false in ok
// when c is gone or the queue failed.
func (s *Server) redeliverStep(c *conn, after, upto int64, reserved *int64) (next int64, sent int, ok bool) {
	seq, size, found, err := s.q.probe(c.key, after, upto)
	if err != nil {
		s.queueFailed(c, "probe", err)
		return after, 0, false
	}
	if !found {
		return upto, 0, true
	}
	if limit := s.lim.redeliverAllowed(c, size); limit != "" {
		s.skipRedelivery(c, seq, upto, size, limit)
		return upto, 0, true
	}
	// Read only what the budget pays: the probed row (paid) and what the
	// buckets hold after it, taken now and given back below if not sent
	// (review 74 M-2, 74b L-a).
	paid := size + s.lim.redeliverReserve(c, drainBatchBytes-size)
	rows, err := s.q.nextRange(c.key, after, upto, drainBatch, paid)
	if err != nil {
		s.lim.redeliverRefund(c, paid)
		s.queueFailed(c, "next", err)
		return after, 0, false
	}
	next = upto // acked since the probe if there are no rows
	for i, r := range rows {
		n := int64(len(r.frame))
		if n <= paid {
			paid -= n
			next = r.seq
			continue
		}
		// The rows changed since the probe (an ack): charge this one alone.
		if limit := s.lim.redeliverAllowed(c, n); limit != "" {
			s.skipRedelivery(c, r.seq, upto, n, limit)
			rows, next = rows[:i], upto
			break
		}
		next = r.seq
	}
	s.lim.redeliverRefund(c, paid)
	if !s.sendBatch(c, rows, reserved) {
		return after, 0, false
	}
	s.redelivered(rows)
	if len(rows) > 0 {
		s.log.Info("redelivered from queue", "event", "queue_redeliver", "peer", short(c.key), "count", len(rows))
	}
	return next, len(rows), true
}

// redelivered counts rows sent again, for the metrics.
func (s *Server) redelivered(rows []queued) {
	for _, r := range rows {
		s.redeliveredBytes.Add(int64(len(r.frame)))
	}
}

// skipRedelivery records on c that the rows [from, to] were not redelivered
// for want of budget; size is the first one's length. A range already
// recorded is widened, so the retry covers both.
func (s *Server) skipRedelivery(c *conn, from, to, size int64, limit string) {
	c.mu.Lock()
	if c.skipSize == 0 || from < c.skipFrom {
		c.skipFrom, c.skipSize = from, size
	}
	c.skipTo = max(c.skipTo, to)
	c.mu.Unlock()
	s.redeliverySkips.Add(1)
	if limit == limitRedeliverPrefix {
		s.lim.hit(limit, "prefix", c.prefix)
	} else {
		s.lim.hit(limit, "peer", short(c.key))
	}
}

// retrySkipped serves the redeliveries skipped for want of budget, once per
// sweep tick (rule 5): in each prefix the waiting connections, in order,
// whose first skipped rows the buckets now pay (nextServed), and every
// connection skipped by
// its key bucket alone whose buckets now hold it. Each drains its skipped
// range again under the same rules, then goes on with its normal drain. A
// connection that is draining keeps its place until a later tick.
func (s *Server) retrySkipped() { s.retrySkippedAt(s.now()) }

// retrySkippedAt is retrySkipped for the sweep tick that began at tick.
func (s *Server) retrySkippedAt(tick time.Time) {
	skipped := func(c *conn) int64 {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.skipSize
	}
	start := func(c *conn) bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.draining || c.skipSize == 0 {
			return false
		}
		c.draining = true
		return true
	}
	for _, c := range s.lim.nextServedAt(tick, skipped, start) {
		s.startRetry(c)
	}
	s.mu.Lock()
	conns := make([]*conn, 0, len(s.conns))
	for _, c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		size := skipped(c)
		if size == 0 || s.lim.waitingOrServed(c) || !s.lim.redeliverHas(c, size) || !start(c) {
			continue
		}
		s.startRetry(c)
	}
}

// startRetry starts the drain goroutine of a retry for c, which start has
// marked draining: it redelivers c's skipped range, then resumes c's normal
// drain at the cursor it had (direct forwarding was off meanwhile).
func (s *Server) startRetry(c *conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		s.lim.redeliverDone(c)
		return
	}
	s.drainWG.Add(1)
	go func() {
		defer s.drainWG.Done()
		s.retryRange(c)
		for s.drainStep(c, true) {
		}
	}()
}

// retryRange redelivers c's skipped range (rule 5) with a cursor of its own.
// Rows acked meanwhile are gone; rows skipped again are recorded anew.
func (s *Server) retryRange(c *conn) {
	defer s.lim.redeliverDone(c)
	c.mu.Lock()
	after, upto := c.skipFrom-1, c.skipTo
	c.skipFrom, c.skipTo, c.skipSize = 0, 0, 0
	c.mu.Unlock()
	for {
		if !c.reserve(c.ctx, drainReserve, true) {
			return
		}
		reserved := int64(drainReserve)
		next, sent, ok := s.redeliverStep(c, after, upto, &reserved)
		c.unreserve(reserved)
		if !ok || sent == 0 || next >= upto {
			return
		}
		after = next
	}
}

// drainReserve is what one queue batch may hold in memory: next stops once a
// batch reaches drainBatchBytes, so at most one frame more.
const drainReserve = drainBatchBytes + envelope.MaxFrameBytes

func (s *Server) reject(to *conn, code, msg, ref string) {
	to.send(control(envelope.Control{Op: envelope.OpError, Code: code, Message: msg, Ref: ref}))
}

// register makes c the connection of its key and returns the one it
// replaces. On a relay with accounts it also records c's account state in
// the same step (so accountsChanged sees each connection either before or
// after, never half set up): bound reports whether c may receive now, and
// evict is the oldest unbound connection of c's prefix if c is unbound and
// the prefix is over its cap. Without accounts bound is always true. A key
// of a suspended account is not registered at all (suspended true): the
// check and the registration are one step, so a suspension committed at the
// same time is either seen here or by accountsChanged afterwards.
func (s *Server) register(c *conn) (old, evict *conn, bound, suspended bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b binding
	var ok bool
	if s.acct != nil {
		if b, ok = s.acct.state(c.key); ok && b.suspended {
			return nil, nil, false, true
		}
	}
	old = s.conns[c.key]
	s.conns[c.key] = c
	if s.acct == nil {
		return old, nil, true, false
	}
	c.acctInit = true
	if ok {
		c.acctSeen = b.account
		return old, nil, true, false
	}
	return old, s.listUnbound(c), false, false
}

func (s *Server) unregister(c *conn) {
	s.lim.redeliverForget(c)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unlistUnbound(c)
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
