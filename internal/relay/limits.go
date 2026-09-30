package relay

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
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
	// Accounts (4.2a): an unbound connection closed because its prefix
	// opened more than maxUnboundPerPrefix.
	limitUnboundPerPrefix = "unbound_per_prefix"
)

// Limit names of R55-F1: the ephemeral budget refused a presence or control
// frame (dropped); a prefix's share of the outbound budget sent mail down the
// queue path; a frame was not read within --frame-read-timeout. The eviction
// names are evictLimits (budget.go).
const (
	limitEphemeral      = "max_inflight_ephemeral"
	limitInflightPrefix = "max_inflight_prefix"
	limitFrameTimeout   = "frame_read_timeout"
)

// Redelivery budgets of R55-F2 (relay-hosted.md §2 "Offline queue delivery
// and expiry"): bytes of queued rows sent again to a key that did not ack
// them, per recipient key and per prefix of the receiving connection, per
// hour with a burst of the same size; and their limit names.
const (
	defaultQueueRedeliverPerKey    = 32 << 20
	defaultQueueRedeliverPerPrefix = 128 << 20

	limitRedeliverKey    = "queue_redeliver_key"
	limitRedeliverPrefix = "queue_redeliver_prefix"
)

// orDefault returns v, def when v is 0, or 0 (off) when v is negative.
func orDefault[T ~int | ~int64](v, def T) T {
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

	// Redelivery (R55-F2): the key and prefix buckets, each prefix's wait
	// list of connections skipped for want of prefix budget, oldest first,
	// and the connections the retry is serving in each prefix, with the time
	// their turn began (rule 6, review 66b H2, review 74 M-1 and L-3).
	// conn.waiting and conn.waitClosed are guarded by mu too.
	redeliverKey    bucketSet
	redeliverPrefix bucketSet
	redeliverWait   map[string][]*conn
	redeliverServed map[string]map[*conn]time.Time

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
		logSeen:           map[string]*logWindow{},
		redeliverKey:      perHour(orDefault(opts.QueueRedeliverPerKey, defaultQueueRedeliverPerKey)),
		redeliverPrefix:   perHour(orDefault(opts.QueueRedeliverPerPrefix, defaultQueueRedeliverPerPrefix)),
		redeliverWait:     map[string][]*conn{},
		redeliverServed:   map[string]map[*conn]time.Time{},
	}
	return l
}

// perHour is a byte bucket refilling n bytes an hour with a burst of n (0: off).
func perHour(n int64) bucketSet { return newBucketSet(float64(n)/3600, float64(n)) }

// redeliverTurn is the time slice of a served connection (review 74 M-1):
// one sweep tick. A connection still served after it loses its turn, so a
// recipient that reads slowly cannot hold its prefix's turn.
const redeliverTurn = time.Minute

// redeliverAllowed charges a redelivery of n bytes to c's key and prefix, both
// or neither, or returns the limit that refused it. While c's prefix has a
// connection waiting or being served, only the served ones may redeliver; any
// other is refused and joins the wait list's tail. A served connection that
// its prefix bucket cannot pay ends its turn and goes back to the head of the
// list: the prefix ran short, not it (review 74 M-1). One the key bucket
// cannot pay keeps its place.
func (l *limits) redeliverAllowed(c *conn, n int64) string {
	now := l.now()
	size := float64(n)
	l.mu.Lock()
	defer l.mu.Unlock()
	served := l.redeliverServed[c.prefix]
	_, isServed := served[c]
	if !isServed && (len(served) > 0 || len(l.redeliverWait[c.prefix]) > 0) {
		l.joinWaitLocked(c, false)
		return limitRedeliverPrefix
	}
	if !l.redeliverKey.has(c.key, now, size) {
		return limitRedeliverKey
	}
	if !l.redeliverPrefix.has(c.prefix, now, size) {
		if isServed {
			l.endTurnLocked(c)
		}
		l.joinWaitLocked(c, isServed)
		return limitRedeliverPrefix
	}
	l.redeliverKey.take(c.key, now, size)
	l.redeliverPrefix.take(c.prefix, now, size)
	return ""
}

// redeliverAvail reports the bytes c's key and prefix buckets both hold now
// (limit when neither is on), so a redelivery reads no more frames than it can
// pay for (review 74 M-2).
func (l *limits) redeliverAvail(c *conn, limit int64) int64 {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	avail := float64(limit)
	if l.redeliverKey.rate > 0 {
		avail = min(avail, l.redeliverKey.refill(c.key, now).tokens)
	}
	if l.redeliverPrefix.rate > 0 {
		avail = min(avail, l.redeliverPrefix.refill(c.prefix, now).tokens)
	}
	return int64(avail)
}

// joinWaitLocked puts c on its prefix's wait list, at the head or the tail,
// unless it is on it or closed.
func (l *limits) joinWaitLocked(c *conn, head bool) {
	if c.waiting || c.waitClosed {
		return
	}
	c.waiting = true
	if head {
		l.redeliverWait[c.prefix] = slices.Insert(l.redeliverWait[c.prefix], 0, c)
		return
	}
	l.redeliverWait[c.prefix] = append(l.redeliverWait[c.prefix], c)
}

// removeWaitLocked takes c off its prefix's wait list.
func (l *limits) removeWaitLocked(c *conn) {
	if !c.waiting {
		return
	}
	c.waiting = false
	list := l.redeliverWait[c.prefix]
	if i := slices.Index(list, c); i >= 0 {
		list = slices.Delete(list, i, i+1)
	}
	if len(list) == 0 {
		delete(l.redeliverWait, c.prefix)
	} else {
		l.redeliverWait[c.prefix] = list
	}
}

// endTurnLocked removes c from its prefix's served connections.
func (l *limits) endTurnLocked(c *conn) {
	served := l.redeliverServed[c.prefix]
	if _, ok := served[c]; !ok {
		return
	}
	delete(served, c)
	if len(served) == 0 {
		delete(l.redeliverServed, c.prefix)
	}
}

// redeliverForget removes a closed connection from the wait lists (rule 6: a
// place is not kept across connections).
func (l *limits) redeliverForget(c *conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c.waitClosed = true
	l.removeWaitLocked(c)
	l.endTurnLocked(c)
}

// redeliverDone ends c's turn as a served connection of its prefix.
func (l *limits) redeliverDone(c *conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.endTurnLocked(c)
}

// redeliverHas reports whether c's key and prefix buckets both hold n bytes,
// without taking them.
func (l *limits) redeliverHas(c *conn, n int64) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.redeliverKey.has(c.key, now, float64(n)) && l.redeliverPrefix.has(c.prefix, now, float64(n))
}

// nextServed first ends every turn older than redeliverTurn (review 74 M-1;
// such a connection redelivers again only after the others waiting, since
// its next refusal puts it at the tail). Then, in each prefix, it walks the
// wait list in order and picks every connection whose key bucket holds its
// first skipped row and whose row the prefix bucket can still pay after the
// rows of the connections picked before it in this tick (review 74 L-3).
// skipped reports that row's size (0 while none is recorded); start accepts
// a connection that is not draining and marks it draining. A connection whose
// key bucket is short, or that is draining, keeps its place and the next is
// tried; one the prefix bucket cannot pay ends the walk in that prefix, since
// nobody behind it may go first. Picked connections leave the list and are
// served from now. skipped and start run with mu held and may only take
// conn.mu (lock order: limits.mu, then conn.mu).
func (l *limits) nextServed(skipped func(c *conn) int64, start func(c *conn) bool) []*conn {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, served := range l.redeliverServed {
		for c, since := range served {
			if now.Sub(since) >= redeliverTurn {
				l.endTurnLocked(c)
			}
		}
	}
	var picked []*conn
	for prefix, list := range l.redeliverWait {
		var pending float64
		for _, c := range slices.Clone(list) {
			size := float64(skipped(c))
			if size == 0 || !l.redeliverKey.has(c.key, now, size) {
				continue
			}
			if !l.redeliverPrefix.has(prefix, now, pending+size) {
				break
			}
			if !start(c) {
				continue
			}
			pending += size
			l.removeWaitLocked(c)
			if l.redeliverServed[prefix] == nil {
				l.redeliverServed[prefix] = map[*conn]time.Time{}
			}
			l.redeliverServed[prefix][c] = now
			picked = append(picked, c)
		}
	}
	return picked
}

// waitingOrServed reports whether c is on a wait list or being served.
func (l *limits) waitingOrServed(c *conn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, served := l.redeliverServed[c.prefix][c]
	return c.waiting || served
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

// smallFrame is the size up to which a frame is charged to the byte buckets
// but never refused before it is parsed: acks (review 56a A7).
const smallFrame = 1 << 10

// frameAllowed charges one frame of n bytes, read after auth and not yet
// parsed, to the byte buckets of key and prefix (R55-035). Both are charged
// or neither; the name of the first limit that refused is returned. A frame
// of at most smallFrame bytes is always charged and never refused.
func (l *limits) frameAllowed(key, prefix string, n int) string {
	now := l.now()
	size := float64(n)
	l.mu.Lock()
	defer l.mu.Unlock()
	if n > smallFrame {
		switch {
		case !l.keyBytes.has(key, now, size):
			return limitKeyBytes
		case !l.prefixBytes.has(prefix, now, size):
			return limitPrefixBytes
		}
	}
	l.keyBytes.spend(key, now, size)
	l.prefixBytes.spend(prefix, now, size)
	return ""
}

// sendAllowed charges one non-ephemeral envelope to key and to prefix. Both
// are charged or neither; the name of the first limit that refused is
// returned. Its bytes were charged when it was read (frameAllowed).
func (l *limits) sendAllowed(key, prefix string) string {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case !l.keyEnvs.has(key, now, 1):
		return limitKeyEnvs
	case !l.prefixEnvs.has(prefix, now, 1):
		return limitPrefixEnvs
	}
	l.keyEnvs.take(key, now, 1)
	l.prefixEnvs.take(prefix, now, 1)
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

// spend removes cost tokens from key's bucket, or empties it if it holds
// fewer. There is no debt: frames that are charged but never refused (acks)
// would otherwise lock a whole prefix out for as long as they flooded it
// (review 63 S-5).
func (b *bucketSet) spend(key string, now time.Time, cost float64) {
	if b.rate <= 0 {
		return
	}
	tb := b.refill(key, now)
	tb.tokens = max(0, tb.tokens-cost)
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
