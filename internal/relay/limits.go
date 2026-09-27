package relay

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Abuse limit defaults, Docs/protocol/relay-hosted.md §2 (ticket 4.0b). An
// Options field left at zero takes the default here; a negative value turns
// that limit off (tests only; cmd/relay accepts positive values only).
const (
	// Per network source (a /24 or /48 prefix), before authentication.
	defaultUpgradesPerMinute     = 30
	defaultUpgradeBurst          = 60
	defaultMaxConnsPerPrefix     = 64
	defaultAuthFailuresPerPrefix = 10
	defaultAuthFailWindow        = 10 * time.Minute
	defaultMaxUnauthConns        = 256
	defaultMaxConns              = 5000
	defaultMaxKeysPerPrefix      = 64
	defaultPrefixEnvelopesPerMin = 600
	defaultPrefixBytesPerMin     = 64 << 20

	// Per authenticated key.
	defaultKeyEnvelopesPerMin = 120
	defaultKeyEnvelopeBurst   = 240
	defaultKeyBytesPerMin     = 32 << 20
	defaultControlPerMin      = 60
	defaultReconnectsPerMin   = 20

	// Memory bound (review 50 M2).
	defaultConnBufferBytes = 4 << 20
	defaultMaxInflight     = 256 << 20

	// controlStrikeLimit is how many one-minute windows in a row a key may
	// exceed its control-frame rate before the connection is closed (1008).
	controlStrikeLimit = 3
	controlWindow      = time.Minute

	// limitLogWindow: each (limit, subject) is logged at most once per window,
	// so an attacker cannot turn refusals into a log flood.
	limitLogWindow = time.Minute
	// bucketPruneThreshold is the map size past which idle entries are dropped.
	bucketPruneThreshold = 1024
)

// Limit names, as logged in event=limit lines.
const (
	limitUpgrades       = "upgrades_per_prefix"
	limitPrefixConns    = "conns_per_prefix"
	limitAuthFailures   = "auth_failures_per_prefix"
	limitUnauthConns    = "unauth_conns"
	limitMaxConns       = "max_conns"
	limitKeysPerPrefix  = "keys_per_prefix"
	limitPrefixEnvs     = "prefix_envelopes"
	limitPrefixBytes    = "prefix_bytes"
	limitKeyEnvs        = "key_envelopes"
	limitKeyBytes       = "key_bytes"
	limitControl        = "control_frames"
	limitControlClose   = "control_frames_close"
	limitReconnects     = "reconnects"
	limitConnBuffer     = "conn_buffer"
	limitInflight       = "max_inflight"
	limitReading        = "max_inflight_read"
	limitHealth         = "health_per_prefix"
	limitQueuePair      = "queue_pair"
	limitQueueSender    = "queue_sender"
	limitQueueRecipient = "queue_recipient"
	limitQueueTotal     = "queue_total"
	limitQueueDisk      = "queue_disk"
)

// orDefault returns v, def when v is 0, or 0 (off) when v is negative.
func orDefault[T int | int64](v, def T) T {
	switch {
	case v == 0:
		return def
	case v < 0:
		return 0
	}
	return v
}

// limits holds the relay's abuse-limit settings and counters. Everything
// keyed by a network prefix or a key lives here; the connection registry's
// admission counters are in Server under Server.mu.
type limits struct {
	now func() time.Time
	log *slog.Logger

	// Settings (0 = off).
	maxConnsPerPrefix int
	maxUnauth         int
	maxConns          int
	maxKeysPerPrefix  int
	controlPerMin     int
	connBuffer        int64

	mu          sync.Mutex
	upgrades    bucketSet // per prefix
	health      bucketSet // per prefix, GET /healthz
	authFails   limiter   // per prefix, fixed window
	prefixEnvs  bucketSet
	prefixBytes bucketSet
	keyEnvs     bucketSet
	keyBytes    bucketSet
	reconnects  bucketSet // per key
	control     map[string]*controlWindowState
	ctlPrune    time.Time
	prefixConns map[string]int // open WebSocket connections per prefix
	unauth      int            // connections still authenticating, relay-wide

	inflight budget // bytes waiting in every connection's outbound buffer
	reading  budget // bytes of frames being read from every connection (R-4.0 H1)

	logMu   sync.Mutex
	logSeen map[string]*logWindow
}

type logWindow struct {
	start      time.Time
	suppressed int
}

func newLimits(opts Options, now func() time.Time, log *slog.Logger) *limits {
	perMin := func(n int) float64 { return float64(n) / 60 }
	upgrades := orDefault(opts.UpgradesPerMinute, defaultUpgradesPerMinute)
	burst := orDefault(opts.UpgradeBurst, defaultUpgradeBurst)
	if burst < upgrades {
		burst = upgrades
	}
	keyEnvs := orDefault(opts.KeyEnvelopesPerMinute, defaultKeyEnvelopesPerMin)
	keyBurst := orDefault(opts.KeyEnvelopeBurst, defaultKeyEnvelopeBurst)
	if keyBurst < keyEnvs {
		keyBurst = keyEnvs
	}
	prefixEnvs := orDefault(opts.PrefixEnvelopesPerMinute, defaultPrefixEnvelopesPerMin)
	prefixBytes := orDefault(opts.PrefixBytesPerMinute, defaultPrefixBytesPerMin)
	keyBytes := orDefault(opts.KeyBytesPerMinute, defaultKeyBytesPerMin)
	reconnects := orDefault(opts.ReconnectsPerMinute, defaultReconnectsPerMin)
	authWindow := opts.AuthFailWindow
	if authWindow <= 0 {
		authWindow = defaultAuthFailWindow
	}
	l := &limits{
		now:               now,
		log:               log,
		maxConnsPerPrefix: orDefault(opts.MaxConnsPerPrefix, defaultMaxConnsPerPrefix),
		maxUnauth:         orDefault(opts.MaxUnauthConns, defaultMaxUnauthConns),
		maxConns:          orDefault(opts.MaxConns, defaultMaxConns),
		maxKeysPerPrefix:  orDefault(opts.MaxKeysPerPrefix, defaultMaxKeysPerPrefix),
		controlPerMin:     orDefault(opts.ControlPerMinute, defaultControlPerMin),
		connBuffer:        orDefault(opts.ConnBufferBytes, defaultConnBufferBytes),
		upgrades:          newBucketSet(perMin(upgrades), float64(burst)),
		health:            newBucketSet(perMin(upgrades), float64(burst)),
		authFails:         limiter{limit: orDefault(opts.AuthFailuresPerPrefix, defaultAuthFailuresPerPrefix), window: authWindow, buckets: map[string]*bucket{}},
		prefixEnvs:        newBucketSet(perMin(prefixEnvs), float64(prefixEnvs)),
		prefixBytes:       newBucketSet(float64(prefixBytes)/60, float64(prefixBytes)),
		keyEnvs:           newBucketSet(perMin(keyEnvs), float64(keyBurst)),
		keyBytes:          newBucketSet(float64(keyBytes)/60, float64(keyBytes)),
		reconnects:        newBucketSet(perMin(reconnects), float64(reconnects)),
		control:           map[string]*controlWindowState{},
		prefixConns:       map[string]int{},
		inflight:          budget{max: orDefault(opts.MaxInflight, defaultMaxInflight)},
		reading:           budget{max: orDefault(opts.MaxInflight, defaultMaxInflight)},
		logSeen:           map[string]*logWindow{},
	}
	return l
}

// hit logs that limit refused subject (a /24 or /48 prefix, or a key
// truncated with short). Never a payload, id or frame. Repeats of the same
// (limit, subject) within limitLogWindow are counted, not logged.
func (l *limits) hit(limit, subjectKind, subject string) {
	now := l.now()
	k := limit + "\x00" + subject
	l.logMu.Lock()
	w := l.logSeen[k]
	if w != nil && now.Sub(w.start) < limitLogWindow {
		w.suppressed++
		l.logMu.Unlock()
		return
	}
	suppressed := 0
	if w != nil {
		suppressed = w.suppressed
	}
	if len(l.logSeen) >= bucketPruneThreshold {
		for key, lw := range l.logSeen {
			if now.Sub(lw.start) >= limitLogWindow {
				delete(l.logSeen, key)
			}
		}
	}
	l.logSeen[k] = &logWindow{start: now}
	l.logMu.Unlock()
	l.log.Warn("limit reached", "event", "limit", "limit", limit, subjectKind, subject, "suppressed_before", suppressed)
}

// Refusals before the WebSocket upgrade.

// upgradeRefusal is an HTTP status and the limit that caused it.
type upgradeRefusal struct {
	status int
	limit  string
}

// admitUpgrade checks the per-prefix and relay-wide limits for a new
// WebSocket upgrade from prefix. On success the connection is counted as
// open and unauthenticated; the caller must call release when it ends and
// authDone once authentication has finished (either way).
func (l *limits) admitUpgrade(prefix string) *upgradeRefusal {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.authFails.limit > 0 && !l.authFails.allow(prefix, now) {
		return &upgradeRefusal{http.StatusTooManyRequests, limitAuthFailures}
	}
	if !l.upgrades.take(prefix, now, 1) {
		return &upgradeRefusal{http.StatusTooManyRequests, limitUpgrades}
	}
	if l.maxConnsPerPrefix > 0 && l.prefixConns[prefix] >= l.maxConnsPerPrefix {
		return &upgradeRefusal{http.StatusTooManyRequests, limitPrefixConns}
	}
	if l.maxUnauth > 0 && l.unauth >= l.maxUnauth {
		return &upgradeRefusal{http.StatusServiceUnavailable, limitUnauthConns}
	}
	l.prefixConns[prefix]++
	l.unauth++
	return nil
}

// admitHealth charges one GET /healthz to prefix at the rate of new upgrades
// ("rate limited per IP like connections", relay-hosted.md §1), in a bucket
// of its own so that health checks never spend a prefix's upgrades.
func (l *limits) admitHealth(prefix string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.health.take(prefix, now, 1)
}

// authDone ends the unauthenticated phase of a connection admitted by
// admitUpgrade. failed counts a failed authentication against prefix.
func (l *limits) authDone(prefix string, failed bool) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.unauth--
	if failed && l.authFails.limit > 0 {
		l.authFails.fail(prefix, now)
	}
}

// release forgets a connection admitted by admitUpgrade.
func (l *limits) release(prefix string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.prefixConns[prefix]--; l.prefixConns[prefix] <= 0 {
		delete(l.prefixConns, prefix)
	}
}

// reconnect counts one successful authentication of key and reports whether
// it is within the key's reconnect rate.
func (l *limits) reconnect(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reconnects.take(key, now, 1)
}

// sendAllowed charges one non-ephemeral envelope of n bytes to key and to
// prefix. Either all four buckets are charged or none; the name of the first
// limit that refused is returned.
func (l *limits) sendAllowed(key, prefix string, n int) string {
	now := l.now()
	size := float64(n)
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case !l.keyEnvs.has(key, now, 1):
		return limitKeyEnvs
	case !l.keyBytes.has(key, now, size):
		return limitKeyBytes
	case !l.prefixEnvs.has(prefix, now, 1):
		return limitPrefixEnvs
	case !l.prefixBytes.has(prefix, now, size):
		return limitPrefixBytes
	}
	l.keyEnvs.take(key, now, 1)
	l.keyBytes.take(key, now, size)
	l.prefixEnvs.take(prefix, now, 1)
	l.prefixBytes.take(prefix, now, size)
	return ""
}

// controlAllowed counts one control frame (not ack) from key. ok is false when
// the key is over its rate this minute; closeConn is true when that has now
// happened in controlStrikeLimit one-minute windows in a row.
func (l *limits) controlAllowed(key string) (ok, closeConn bool) {
	if l.controlPerMin <= 0 {
		return true, false
	}
	now := l.now()
	idx := now.UnixNano() / int64(controlWindow)
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.control) >= bucketPruneThreshold && now.Sub(l.ctlPrune) >= controlWindow {
		l.ctlPrune = now
		for k, w := range l.control {
			if w.idx < idx-1 {
				delete(l.control, k)
			}
		}
	}
	w := l.control[key]
	if w == nil {
		w = &controlWindowState{idx: idx}
		l.control[key] = w
	}
	if w.idx != idx {
		if w.overIdx != idx-1 {
			w.strikes = 0 // the previous window was not over: the run is broken
		}
		w.idx, w.n = idx, 0
	}
	w.n++
	if w.n <= l.controlPerMin {
		return true, false
	}
	if w.overIdx != idx {
		w.overIdx = idx
		w.strikes++
	}
	return false, w.strikes >= controlStrikeLimit
}

type controlWindowState struct {
	idx     int64 // current one-minute window
	n       int   // control frames in it
	overIdx int64 // last window in which the rate was exceeded
	strikes int   // consecutive windows over the rate, ending at overIdx
}

// bucketSet is a token bucket per key: rate tokens a second, at most burst.
// Callers hold limits.mu. A rate of 0 means no limit.
type bucketSet struct {
	rate, burst float64
	m           map[string]*tokenBucket
	lastPrune   time.Time
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

func newBucketSet(rate, burst float64) bucketSet {
	return bucketSet{rate: rate, burst: burst, m: map[string]*tokenBucket{}}
}

// refill returns key's bucket brought up to now, creating it full.
func (b *bucketSet) refill(key string, now time.Time) *tokenBucket {
	if len(b.m) >= bucketPruneThreshold && now.Sub(b.lastPrune) >= time.Minute {
		b.lastPrune = now
		for k, tb := range b.m {
			if tb.tokens+now.Sub(tb.last).Seconds()*b.rate >= b.burst {
				delete(b.m, k) // full again: indistinguishable from a new bucket
			}
		}
	}
	tb := b.m[key]
	if tb == nil {
		tb = &tokenBucket{tokens: b.burst, last: now}
		b.m[key] = tb
		return tb
	}
	if now.After(tb.last) {
		tb.tokens = min(b.burst, tb.tokens+now.Sub(tb.last).Seconds()*b.rate)
		tb.last = now
	}
	return tb
}

// has reports whether key's bucket holds cost tokens, without taking them.
func (b *bucketSet) has(key string, now time.Time, cost float64) bool {
	if b.rate <= 0 {
		return true
	}
	return b.refill(key, now).tokens >= cost
}

// take removes cost tokens from key's bucket if it holds them.
func (b *bucketSet) take(key string, now time.Time, cost float64) bool {
	if b.rate <= 0 {
		return true
	}
	tb := b.refill(key, now)
	if tb.tokens < cost {
		return false
	}
	tb.tokens -= cost
	return true
}

// budget is a relay-wide byte budget (--max-inflight). max 0 means no limit.
type budget struct {
	max  int64
	used atomic.Int64
}

// tryAdd reserves n bytes if they fit.
func (b *budget) tryAdd(n int64) bool {
	for {
		cur := b.used.Load()
		if b.max > 0 && cur+n > b.max && cur > 0 {
			return false
		}
		if b.used.CompareAndSwap(cur, cur+n) {
			return true
		}
	}
}

func (b *budget) add(n int64) { b.used.Add(n) }

// Client address behind a proxy (Docs/protocol/relay-hosted.md §2 "Client IP
// behind a proxy").

// clientAddr is the address whose prefix the per-source limits use: the TCP
// peer, unless this relay is public, has a client IP header configured, and
// the TCP peer is one of the trusted proxies. Then it is the header's last
// entry (the hop the trusted proxy added). A missing or malformed header from
// a trusted proxy falls back to the TCP peer.
func (s *Server) clientAddr(r *http.Request) string {
	if !s.public || s.ipHeader == "" || len(s.trusted) == 0 {
		return r.RemoteAddr
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !s.isTrustedProxy(peer.Unmap()) {
		return r.RemoteAddr
	}
	vals := r.Header.Values(s.ipHeader)
	if len(vals) == 0 {
		return r.RemoteAddr
	}
	last := vals[len(vals)-1]
	if i := strings.LastIndexByte(last, ','); i >= 0 {
		last = last[i+1:]
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(last))
	if err != nil {
		return r.RemoteAddr
	}
	return ip.Unmap().WithZone("").String() // a zone would make each value a prefix of its own
}

func (s *Server) isTrustedProxy(ip netip.Addr) bool {
	for _, p := range s.trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
