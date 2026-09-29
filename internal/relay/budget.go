package relay

import (
	"sync"
	"time"
)

// Memory budgets and fairness, Docs/protocol/relay-hosted.md §2 "Memory
// budgets and fairness (R55-F1)". Every byte the relay holds for a connection
// is charged to one of three relay-wide budgets, and to the connection and
// its prefix inside that budget. When a charge does not fit, the heaviest
// holder is evicted once instead of refusing the newcomer.

// budgetKind names one of the three budgets.
type budgetKind int

const (
	// kindOutbound: envelopes waiting in outbound buffers, and queue-drain
	// reservations (--max-inflight).
	kindOutbound budgetKind = iota
	// kindRead: the buffer of the frame being read from each connection.
	kindRead
	// kindEphemeral: presence and relay-generated control frames waiting in
	// outbound buffers (--max-inflight-ephemeral).
	kindEphemeral
	numKinds
)

const (
	// Share floors: a prefix may hold share(B, floor) of budget B.
	outboundShareFloor  = 6 << 20
	readShareFloor      = 2 << 20
	ephemeralShareFloor = 512 << 10
	// minEphemeralBudget is the smallest default ephemeral budget.
	minEphemeralBudget = 1 << 20

	// evictStaleBase and evictMinRate: an outbound or ephemeral holder is
	// eligible for eviction once the oldest frame in its buffer has waited
	// evictStaleBase + held ÷ evictMinRate, held being all it holds of both
	// (OD-R55F1-9, review 63 S-1).
	evictStaleBase = 2 * time.Second
	evictMinRate   = 512 << 10 // bytes a second

	// evictCloseWait bounds the close handshake of an evicted connection;
	// then its socket is closed without waiting for the peer.
	evictCloseWait = time.Second
)

// Limit names of R55-F1, as logged in event=limit lines.
var evictLimits = [numKinds]string{kindOutbound: "evict_outbound", kindRead: "evict_read", kindEphemeral: "evict_ephemeral"}

// share is a prefix's share of a budget of size b: an eighth, never less
// than floor, never more than b. 0 (no budget) means no share.
func share(b, floor int64) int64 {
	if b <= 0 {
		return 0
	}
	return min(b, max(b/8, floor))
}

// evictStale is how long the oldest frame of a holder of held bytes must
// have waited for the holder to count as not keeping up.
func evictStale(held int64) time.Duration {
	return evictStaleBase + time.Duration(held*int64(time.Second)/evictMinRate)
}

// prefixUse is what one prefix holds of one budget, and who holds it.
type prefixUse struct {
	used    int64
	holders map[*conn]struct{}
}

// pool is one relay-wide budget. max 0 means no limit (and no share).
type pool struct {
	max, share int64
	used       int64
	prefixes   map[string]*prefixUse
}

// fits reports whether n more bytes for prefix p fit; over is true when the
// refusal is p's share. A prefix holding nothing may always take one
// charge, as an empty budget may (relay-hosted.md "Shares").
func (b *pool) fits(p *prefixUse, n int64) (ok, over bool) {
	if b.share > 0 && p != nil && p.used > 0 && p.used+n > b.share {
		return false, true
	}
	if b.max > 0 && b.used > 0 && b.used+n > b.max {
		return false, false
	}
	return true, false
}

// pending is one frame waiting in an outbound buffer: its budget, size and
// the time it was pushed.
type pending struct {
	kind budgetKind
	n    int64
	at   time.Time
}

// ledger holds the three budgets. Everything a connection holds in them is
// recorded on the connection (hold, readStart, queue) and changed only under
// mu, which is taken inside the connection's bmu: lock order conn.mu, then
// conn.bmu, then ledger.mu. No path takes a second connection's bmu while
// holding one, so eviction runs with no bmu held.
type ledger struct {
	now func() time.Time
	// hit logs an event=limit line (limits.hit); nil logs nothing.
	hit func(limit, subjectKind, subject string)

	mu    sync.Mutex
	pools [numKinds]pool
}

// newLedger returns budgets of the given sizes (0: no limit).
func newLedger(outbound, read, ephemeral int64, now func() time.Time) *ledger {
	l := &ledger{now: now}
	for k, sz := range [numKinds]struct{ max, floor int64 }{
		kindOutbound:  {outbound, outboundShareFloor},
		kindRead:      {read, readShareFloor},
		kindEphemeral: {ephemeral, ephemeralShareFloor},
	} {
		l.pools[k] = pool{max: sz.max, share: share(sz.max, sz.floor), prefixes: map[string]*prefixUse{}}
	}
	return l
}

// used reports the bytes charged to budget k, relay-wide.
func (l *ledger) used(k budgetKind) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pools[k].used
}

// usedBy reports the bytes prefix holds of budget k.
func (l *ledger) usedBy(k budgetKind, prefix string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if p := l.pools[k].prefixes[prefix]; p != nil {
		return p.used
	}
	return 0
}

// addUnowned charges n bytes (n < 0 returns them) to budget k without a
// connection or prefix (tests: a budget spent by nobody who can be evicted).
func (l *ledger) addUnowned(k budgetKind, n int64) {
	l.mu.Lock()
	l.pools[k].used += n
	l.mu.Unlock()
}

// over reports why n more bytes for c in budget k do not fit: "relay" (the
// budget), "prefix" (c's prefix share) or "" (they fit).
func (l *ledger) over(c *conn, k budgetKind, n int64) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := &l.pools[k]
	switch ok, over := b.fits(b.prefixes[c.prefix], n); {
	case ok:
		return ""
	case over:
		return "prefix"
	}
	return "relay"
}

// charge adds n bytes of budget k to c if they fit, or unconditionally with
// force; with frame it also records a frame of n bytes pushed into c's
// outbound buffer. The caller holds c.bmu and has checked c.dead.
func (l *ledger) charge(c *conn, k budgetKind, n int64, force, frame bool) (ok, over bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 {
		return true, false
	}
	b := &l.pools[k]
	p := b.prefixes[c.prefix]
	if !force {
		if ok, over = b.fits(p, n); !ok {
			return false, over
		}
	}
	if p == nil {
		p = &prefixUse{holders: map[*conn]struct{}{}}
		b.prefixes[c.prefix] = p
	}
	b.used += n
	p.used += n
	c.hold[k] += n
	p.holders[c] = struct{}{}
	if frame {
		c.queue = append(c.queue, pending{k, n, l.now()})
	}
	return true, false
}

// pushed records a frame of n bytes of budget k pushed into c's outbound
// buffer whose bytes are already charged (a drain reservation). The caller
// holds c.bmu.
func (l *ledger) pushed(c *conn, k budgetKind, n int64) {
	l.mu.Lock()
	c.queue = append(c.queue, pending{k, n, l.now()})
	l.mu.Unlock()
}

// popWritten forgets the oldest frame of c's outbound buffer, which the
// write loop has finished with, and uncharges it. The caller holds c.bmu and
// has checked c.dead.
func (l *ledger) popWritten(c *conn) (pending, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(c.queue) == 0 {
		return pending{}, false
	}
	f := c.queue[0]
	c.queue[0] = pending{}
	c.queue = c.queue[1:]
	if len(c.queue) == 0 {
		c.queue = nil // let the backing array go
	}
	l.unchargeLocked(c, f.kind, f.n)
	return f, true
}

// uncharge returns n bytes of budget k held by c. The caller holds c.bmu
// and has checked c.dead.
func (l *ledger) uncharge(c *conn, k budgetKind, n int64) {
	l.mu.Lock()
	l.unchargeLocked(c, k, n)
	l.mu.Unlock()
}

func (l *ledger) unchargeLocked(c *conn, k budgetKind, n int64) {
	n = min(n, c.hold[k]) // never below what c holds: a budget must not go negative
	if n <= 0 {
		return
	}
	b := &l.pools[k]
	b.used -= n
	c.hold[k] -= n
	p := b.prefixes[c.prefix]
	if p == nil {
		return
	}
	p.used -= n
	if c.hold[k] == 0 {
		delete(p.holders, c)
	}
	if len(p.holders) == 0 {
		delete(b.prefixes, c.prefix)
	}
}

// releaseAll uncharges everything c holds in every budget at once (release
// and eviction). The caller holds c.bmu and has just set c.dead.
func (l *ledger) releaseAll(c *conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range numKinds {
		l.unchargeLocked(c, k, c.hold[k])
	}
	c.queue = nil
}

// startRead records when c's current frame started (the read eviction age
// rule). The caller holds c.bmu.
func (l *ledger) startRead(c *conn, at time.Time) {
	l.mu.Lock()
	c.readStart = at
	l.mu.Unlock()
}

// age is the time of c's oldest charge in budget k: its frame's start for
// the read budget, else the oldest frame in its outbound buffer (zero if
// none, as for a drain reservation alone). Callers hold mu.
func (c *conn) age(k budgetKind) time.Time {
	if k == kindRead {
		return c.readStart
	}
	if len(c.queue) > 0 {
		return c.queue[0].at
	}
	return time.Time{}
}

// evictFor evicts, once, the holder that pays for a charge of n bytes of
// budget k for r that did not fit (over: it passed r's prefix share),
// following relay-hosted.md "When a charge does not fit". It reports
// whether it evicted anyone; the caller then retries its charge once. The
// caller holds no bmu.
func (l *ledger) evictFor(r *conn, k budgetKind, n int64, over bool) bool {
	h, prefix := l.pick(r, k, n, over)
	if h == nil || !h.evict("relay busy; retry later") {
		return false
	}
	if l.hit != nil {
		l.hit(evictLimits[k], "prefix", prefix)
	}
	return true
}

// pick chooses the connection that pays for r's charge, or nil.
func (l *ledger) pick(r *conn, k budgetKind, n int64, over bool) (*conn, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := &l.pools[k]
	rp := b.prefixes[r.prefix]
	var rUsed int64
	if rp != nil {
		rUsed = rp.used
	}
	rAge := r.age(k)
	if k != kindRead && rAge.IsZero() {
		rAge = l.now()
	}
	pay, payName := rp, r.prefix
	if !over {
		// The prefix holding the most of k, ties to the oldest charge. The
		// age of a prefix is a walk of its holders, so it is taken only on
		// a tie (review 63 S-4: pick runs under mu on every refused charge).
		var best *prefixUse
		var bestAge time.Time
		aged := false
		for name, p := range b.prefixes {
			switch {
			case best == nil || p.used > best.used:
				best, payName, aged = p, name, false
			case p.used == best.used:
				if !aged {
					bestAge, aged = best.oldest(k), true
				}
				if a := p.oldest(k); older(a, bestAge) {
					best, bestAge, payName = p, a, name
				}
			}
		}
		if best == nil || best == rp || best.used < rUsed+n {
			return nil, "" // r's own prefix is the heaviest: it pays by the fallback
		}
		if best.used == rUsed+n {
			if !aged {
				bestAge = best.oldest(k)
			}
			if !older(bestAge, rAge) {
				return nil, ""
			}
		}
		pay = best
	}
	if pay == nil {
		return nil, ""
	}
	now := l.now()
	var h *conn
	for c := range pay.holders {
		if c == r || c.hold[k] <= 0 || !l.eligible(c, r, k, pay == rp, now) {
			continue
		}
		if h == nil || c.hold[k] > h.hold[k] || (c.hold[k] == h.hold[k] && older(c.age(k), h.age(k))) {
			h = c
		}
	}
	return h, payName
}

// eligible reports whether holder c may be evicted for r's charge to k.
// ownPrefix is true when r's own prefix pays. Callers hold mu.
func (l *ledger) eligible(c, r *conn, k budgetKind, ownPrefix bool, now time.Time) bool {
	if k == kindRead {
		// Another, strictly heavier prefix pays whatever the age of its
		// frames (review 56a A2); inside r's prefix only an older frame does.
		return !ownPrefix || c.readStart.Before(r.readStart)
	}
	// Outbound and ephemeral: only a holder that is not keeping up. Its
	// oldest frame waits behind everything in its buffer, mail and presence
	// alike (one FIFO), so the allowance counts both budgets whichever pays
	// (review 63 S-1). The drain reservation adds to what it holds, but its
	// own age does not count.
	if len(c.queue) == 0 {
		return false
	}
	return now.Sub(c.queue[0].at) >= evictStale(c.hold[kindOutbound]+c.hold[kindEphemeral])
}

// oldest is the time of the oldest charge of k in p. Callers hold mu.
func (p *prefixUse) oldest(k budgetKind) time.Time {
	var t time.Time
	for c := range p.holders {
		if a := c.age(k); !a.IsZero() && (t.IsZero() || a.Before(t)) {
			t = a
		}
	}
	return t
}

// older reports whether a is an older charge than b; a zero time (no frame
// waiting) is the newest.
func older(a, b time.Time) bool {
	return !a.IsZero() && (b.IsZero() || a.Before(b))
}
