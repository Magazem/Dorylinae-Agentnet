package relay

import (
	"crypto/sha256"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// Pairing defaults, see Docs/protocol/pairing.md.
const (
	defaultPairTTL          = 10 * time.Minute
	defaultPairFailLimit    = 5
	defaultPairFailWindow   = time.Minute
	maxOutstandingPerKey    = 5
	defaultPairMaxCodes     = 10000
	maxPairNewV2PerWindow   = 10
	limiterSweepThreshold   = 1024
	pairCodeHashDomain      = "dorylinae-pair-code-v1\n"
	pairEventIssue          = "pair_issue"
	pairEventRedeem         = "pair_redeem"
	pairEventFail           = "pair_fail"
	pairEventCancel         = "pair_cancel"
	pairLookupHashDomain    = "dorylinae-pair-lookup-v2\n"
	maxRedemptionsPerLookup = 3
	pairReasonInvalid       = "invalid"
	pairReasonRateLimited   = "rate_limited"
	pairReasonIssuerAbsent  = "issuer_offline"

	// Per-prefix limits (review-08b L5, Docs/protocol/relay-hosted.md), a
	// Sybil defence layered on top of the per-key limits above: many keys
	// sharing one network prefix are bounded together. Fixed, not
	// configurable via Options (unlike the per-key limits, which predate
	// the hosted relay and are still tuned by tests).
	maxOutstandingPerPrefix     = 50
	maxPairNewPerPrefixWindow   = 20
	pairNewPrefixWindow         = 10 * time.Minute
	maxFailedRedeemPerPrefixWin = 10
)

// PairStats are the relay's pairing counters. They hold no codes or cards.
type PairStats struct {
	Issued      uint64
	Redeemed    uint64
	Invalid     uint64 // unknown, expired, used or malformed code
	RateLimited uint64
	LookupTaken uint64 // v2 pair_new refused because the lookup was outstanding
}

// pairEntry is one outstanding code. The code itself is never stored, only its hash.
type pairEntry struct {
	issuer    string // wire public key
	issuerRef string
	prefix    string // issuer's network prefix, for the per-prefix outstanding cap
	account   string // issuer's account on a relay with accounts, for the per-account cap
	card      json.RawMessage
	expires   time.Time

	// v2 entries persist across redemptions (see redemptions) until cancelled,
	// expired or exhausted. v1 entries are deleted on their one redemption.
	v2          bool
	mbox        json.RawMessage
	redemptions int
}

// pairings holds outstanding codes and the redemption failure limiter.
type pairings struct {
	ttl      time.Duration
	maxCodes int // relay-wide cap on outstanding entries

	mu      sync.Mutex
	entries map[[sha256.Size]byte]*pairEntry

	lim limiter
	// newLim counts every pair_new, v1 and v2. Without it, pair_new plus pair_cancel is
	// a free existence oracle (pair_lookup_taken) that bypasses lim.
	newLim limiter
	v1     bool // v1 frames enabled

	// Per-prefix counterparts of lim and newLim (review-08b L5): many keys
	// sharing one network prefix are bounded together, on top of the per-key
	// limits above.
	limPrefix    limiter
	newLimPrefix limiter
	// newLimAccount counts every pair_new of an account per day on a relay
	// with accounts (relay-hosted.md §2 L5).
	newLimAccount limiter

	issued, redeemed, invalid, limited, taken atomic.Uint64
}

func newPairings(ttl time.Duration, maxCodes, failLimit int, failWindow time.Duration) *pairings {
	if ttl <= 0 {
		ttl = defaultPairTTL
	}
	if maxCodes <= 0 {
		maxCodes = defaultPairMaxCodes
	}
	if failLimit <= 0 {
		failLimit = defaultPairFailLimit
	}
	if failWindow <= 0 {
		failWindow = defaultPairFailWindow
	}
	return &pairings{
		ttl:           ttl,
		maxCodes:      maxCodes,
		entries:       map[[sha256.Size]byte]*pairEntry{},
		lim:           limiter{limit: failLimit, window: failWindow, buckets: map[string]*bucket{}},
		newLim:        limiter{limit: maxPairNewV2PerWindow, window: failWindow, buckets: map[string]*bucket{}},
		limPrefix:     limiter{limit: maxFailedRedeemPerPrefixWin, window: defaultPairFailWindow, buckets: map[string]*bucket{}},
		newLimPrefix:  limiter{limit: maxPairNewPerPrefixWindow, window: pairNewPrefixWindow, buckets: map[string]*bucket{}},
		newLimAccount: limiter{limit: maxPairNewPerAccountDay, window: pairAccountWindow, buckets: map[string]*bucket{}},
	}
}

func hashCode(code string) [sha256.Size]byte {
	return sha256.Sum256([]byte(pairCodeHashDomain + code))
}

func hashLookup(lookup string) [sha256.Size]byte {
	return sha256.Sum256([]byte(pairLookupHashDomain + lookup))
}

// PairStats returns a snapshot of the pairing counters.
func (s *Server) PairStats() PairStats {
	return PairStats{
		Issued:      s.pairs.issued.Load(),
		Redeemed:    s.pairs.redeemed.Load(),
		Invalid:     s.pairs.invalid.Load(),
		RateLimited: s.pairs.limited.Load(),
		LookupTaken: s.pairs.taken.Load(),
	}
}

// handleControl serves a control frame from an authenticated peer. It returns
// false if the frame is not a permitted request and the connection must close.
func (s *Server) handleControl(c *conn, ctl *envelope.Control) bool {
	if s.acct != nil && !s.accountAllows(c, ctl.Op, ctl.Ref) {
		return true
	}
	switch ctl.Op {
	case envelope.OpBindStart, envelope.OpBindPoll, envelope.OpBindCancel, envelope.OpUnbind:
		if s.acct == nil {
			return false // not advertised: an unexpected control frame, as before accounts
		}
		s.handleAccountOp(c, ctl)
	case envelope.OpPairNew:
		s.pairNew(c, ctl)
	case envelope.OpPairRedeem:
		s.pairRedeem(c, ctl)
	case envelope.OpPairCancel:
		s.pairCancel(c, ctl)
	case envelope.OpAck:
		if err := s.q.ack(c.key, ctl.From, ctl.Ref); err != nil {
			s.log.Warn("queue failed", "event", "queue_error", "op", "ack", "peer", short(c.key), "error", err)
		}
	default:
		return false
	}
	return true
}

// checkPairRequest validates the ref and card of a pairing request, replying
// with bad_pairing on failure.
func (s *Server) checkPairRequest(c *conn, ctl *envelope.Control) bool {
	if len(ctl.Ref) > envelope.MaxRefLen {
		s.reject(c, envelope.CodeBadPairing, "ref is too long", "")
		return false
	}
	if err := envelope.CheckCard(ctl.Card); err != nil {
		s.reject(c, envelope.CodeBadPairing, err.Error(), ctl.Ref)
		return false
	}
	if ctl.Lookup != "" {
		if err := envelope.CheckMbox(ctl.Mbox); err != nil {
			s.reject(c, envelope.CodeBadPairing, err.Error(), ctl.Ref)
			return false
		}
	}
	return true
}

// v1Refused replies pair_v1_disabled and returns true if v1 is switched off.
func (s *Server) v1Refused(c *conn, ref string) bool {
	if s.pairs.v1 {
		return false
	}
	s.reject(c, envelope.CodePairV1Disabled, "pairing v1 is disabled on this relay; use a v2 code", ref)
	return true
}

// pairLookup normalises the lookup of a v2 request. It replies bad_pairing and
// returns false if the lookup is malformed. An empty lookup (v1) is fine.
func (s *Server) pairLookup(c *conn, ctl *envelope.Control) (string, bool) {
	if ctl.Lookup == "" {
		return "", true
	}
	lookup, ok := envelope.NormalizePairLookup(ctl.Lookup)
	if !ok {
		s.reject(c, envelope.CodeBadPairing, "lookup must be 5 characters of the pairing alphabet", ctl.Ref)
	}
	return lookup, ok
}

func (s *Server) pairNew(c *conn, ctl *envelope.Control) {
	if ctl.Lookup == "" && s.v1Refused(c, ctl.Ref) {
		return
	}
	if !s.checkPairRequest(c, ctl) {
		return
	}
	lookup, ok := s.pairLookup(c, ctl)
	if !ok {
		return
	}
	p := s.pairs
	now := s.now()

	account := ""
	if s.acct != nil {
		b, _ := s.acct.state(c.key) // accountAllows let only a bound key with a quota group through
		account = b.account
	}
	if account != "" {
		if !p.newLimAccount.allow(account, now) {
			p.limited.Add(1)
			s.log.Info("pairing issue refused", "event", pairEventFail, "reason", pairReasonRateLimited, "scope", "account")
			s.reject(c, envelope.CodePairRateLimited, "too many pairing codes from this account today; try again tomorrow", ctl.Ref)
			return
		}
		p.newLimAccount.fail(account, now)
	}

	// Every pair_new, v1 or v2, is charged to the per-key and per-prefix
	// limiters (pairing.md "Per-prefix limits"; R55-050).
	if !p.newLim.allow(c.key, now) {
		p.limited.Add(1)
		s.log.Info("pairing issue refused", "event", pairEventFail, "reason", pairReasonRateLimited, "peer", short(c.key))
		s.reject(c, envelope.CodePairRateLimited, "too many pairing requests; try again later", ctl.Ref)
		return
	}
	if !p.newLimPrefix.allow(c.prefix, now) {
		p.limited.Add(1)
		s.log.Info("pairing issue refused", "event", pairEventFail, "reason", pairReasonRateLimited, "scope", "prefix")
		s.reject(c, envelope.CodePairRateLimited, "too many pairing requests from this network; try again later", ctl.Ref)
		return
	}
	p.newLim.fail(c.key, now)
	p.newLimPrefix.fail(c.prefix, now)

	p.mu.Lock()
	outstanding, outstandingPrefix, outstandingAccount := 0, 0, 0
	for h, e := range p.entries {
		switch {
		case !now.Before(e.expires):
			delete(p.entries, h)
		default:
			if e.issuer == c.key {
				outstanding++
			}
			if e.prefix == c.prefix {
				outstandingPrefix++
			}
			if account != "" && e.account == account {
				outstandingAccount++
			}
		}
	}
	if outstandingAccount >= maxOutstandingPerAccount {
		p.mu.Unlock()
		s.log.Info("pairing issue refused", "event", pairEventFail, "reason", "account_limit")
		s.reject(c, envelope.CodePairLimit, "too many outstanding pairing codes for this account", ctl.Ref)
		return
	}
	if outstanding >= maxOutstandingPerKey {
		p.mu.Unlock()
		s.reject(c, envelope.CodePairLimit, "too many outstanding pairing codes", ctl.Ref)
		return
	}
	if outstandingPrefix >= maxOutstandingPerPrefix {
		p.mu.Unlock()
		s.log.Info("pairing issue refused", "event", pairEventFail, "reason", "prefix_limit")
		s.reject(c, envelope.CodePairLimit, "too many outstanding pairing codes from this network", ctl.Ref)
		return
	}
	if len(p.entries) >= p.maxCodes {
		p.mu.Unlock()
		s.log.Info("pairing issue refused", "event", pairEventFail, "reason", "relay_full", "peer", short(c.key))
		s.reject(c, envelope.CodePairLimit, "the relay has too many outstanding pairing codes; try again later", ctl.Ref)
		return
	}
	if lookup != "" {
		h := hashLookup(lookup)
		if p.entries[h] != nil {
			p.mu.Unlock()
			p.taken.Add(1)
			s.log.Info("pairing lookup refused", "event", pairEventFail, "reason", "lookup_taken", "peer", short(c.key))
			s.reject(c, envelope.CodeLookupTaken, "that lookup is already outstanding; generate a new code", ctl.Ref)
			return
		}
		expires := now.Add(p.ttl)
		p.entries[h] = &pairEntry{
			issuer: c.key, issuerRef: ctl.Ref, prefix: c.prefix, account: account, expires: expires, v2: true,
			card: append(json.RawMessage(nil), ctl.Card...), mbox: append(json.RawMessage(nil), ctl.Mbox...),
		}
		p.mu.Unlock()
		p.issued.Add(1)
		s.log.Info("pairing code issued", "event", pairEventIssue, "peer", short(c.key))
		c.send(control(envelope.Control{Op: envelope.OpPairCode, Expires: expires.UTC().Format(time.RFC3339), Ref: ctl.Ref}))
		return
	}
	var code string
	var h [sha256.Size]byte
	for {
		var err error
		if code, err = envelope.NewPairCode(); err != nil {
			p.mu.Unlock()
			s.reject(c, envelope.CodeBadPairing, "could not generate a code", ctl.Ref)
			return
		}
		if h = hashCode(code); p.entries[h] == nil {
			break
		}
	}
	expires := now.Add(p.ttl)
	p.entries[h] = &pairEntry{issuer: c.key, issuerRef: ctl.Ref, prefix: c.prefix, account: account, card: append(json.RawMessage(nil), ctl.Card...), expires: expires}
	p.mu.Unlock()

	p.issued.Add(1)
	s.log.Info("pairing code issued", "event", pairEventIssue, "peer", short(c.key))
	c.send(control(envelope.Control{
		Op:      envelope.OpPairCode,
		Code:    envelope.FormatPairCode(code),
		Expires: expires.UTC().Format(time.RFC3339),
		Ref:     ctl.Ref,
	}))
}

func (s *Server) pairRedeem(c *conn, ctl *envelope.Control) {
	if (ctl.Lookup == "") == (ctl.Code == "") {
		s.reject(c, envelope.CodeBadPairing, "exactly one of lookup and code is required", ctl.Ref)
		return
	}
	if ctl.Lookup == "" && s.v1Refused(c, ctl.Ref) {
		return
	}
	if !s.checkPairRequest(c, ctl) {
		return
	}
	lookup, ok := s.pairLookup(c, ctl)
	if !ok {
		return
	}
	p := s.pairs
	now := s.now()

	if !p.lim.allow(c.key, now) {
		p.limited.Add(1)
		s.log.Info("pairing redemption refused", "event", pairEventFail, "reason", pairReasonRateLimited, "peer", short(c.key))
		s.reject(c, envelope.CodePairRateLimited, "too many failed pairing attempts; try again later", ctl.Ref)
		return
	}
	if !p.limPrefix.allow(c.prefix, now) {
		p.limited.Add(1)
		s.log.Info("pairing redemption refused", "event", pairEventFail, "reason", pairReasonRateLimited, "scope", "prefix")
		s.reject(c, envelope.CodePairRateLimited, "too many failed pairing attempts from this network; try again later", ctl.Ref)
		return
	}

	invalid := func() {
		p.lim.fail(c.key, now)
		p.limPrefix.fail(c.prefix, now)
		p.invalid.Add(1)
		s.log.Info("pairing redemption failed", "event", pairEventFail, "reason", pairReasonInvalid, "peer", short(c.key))
		s.reject(c, envelope.CodePairInvalid, "pairing code is invalid, expired or already used", ctl.Ref)
	}

	var h [sha256.Size]byte
	if lookup != "" {
		h = hashLookup(lookup)
	} else {
		code, ok := envelope.NormalizePairCode(ctl.Code)
		if !ok {
			invalid()
			return
		}
		h = hashCode(code)
	}

	p.mu.Lock()
	e := p.entries[h]
	if e != nil && !now.Before(e.expires) {
		delete(p.entries, h)
		e = nil
	}
	if e == nil {
		p.mu.Unlock()
		invalid()
		return
	}
	if e.issuer == c.key {
		p.mu.Unlock()
		s.reject(c, envelope.CodeBadPairing, "cannot redeem your own pairing code", ctl.Ref)
		return
	}
	// Deliver to the issuer before consuming, so an offline or stalled issuer
	// does not burn the code.
	s.mu.Lock()
	issuer := s.conns[e.issuer]
	s.mu.Unlock()
	if issuer == nil {
		p.mu.Unlock()
		s.log.Info("pairing redemption failed", "event", pairEventFail, "reason", pairReasonIssuerAbsent, "peer", short(c.key))
		s.reject(c, envelope.CodePeerOffline, "the code's issuer is not connected", ctl.Ref)
		return
	}
	if !issuer.send(control(envelope.Control{Op: envelope.OpPairPeer, PublicKey: c.key, Card: ctl.Card, Mbox: ctl.Mbox, Ref: e.issuerRef})) {
		p.mu.Unlock()
		s.reject(c, envelope.CodePeerBusy, "the code's issuer is not keeping up", ctl.Ref)
		return
	}
	if e.v2 {
		e.redemptions++
		if e.redemptions >= maxRedemptionsPerLookup {
			delete(p.entries, h)
		}
	} else {
		delete(p.entries, h)
	}
	p.mu.Unlock()

	p.redeemed.Add(1)
	s.log.Info("pairing completed", "event", pairEventRedeem, "issuer", short(e.issuer), "redeemer", short(c.key))
	c.send(control(envelope.Control{Op: envelope.OpPairPeer, PublicKey: e.issuer, Card: e.card, Mbox: e.mbox, Ref: ctl.Ref}))
}

// pairCancel deletes the sender's own v2 entry. It never replies and treats
// unknown lookups and other keys' entries alike, so it is no oracle.
func (s *Server) pairCancel(c *conn, ctl *envelope.Control) {
	lookup, ok := envelope.NormalizePairLookup(ctl.Lookup)
	if !ok {
		return
	}
	h := hashLookup(lookup)
	p := s.pairs
	p.mu.Lock()
	e := p.entries[h]
	deleted := e != nil && e.v2 && e.issuer == c.key
	if deleted {
		delete(p.entries, h)
	}
	p.mu.Unlock()
	if deleted {
		s.log.Info("pairing cancelled", "event", pairEventCancel, "peer", short(c.key))
	}
}

// limiter is a fixed-window failure counter per key.
type limiter struct {
	limit  int
	window time.Duration

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	start time.Time
	n     int
}

// allow reports whether key may attempt a redemption.
func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	return b == nil || !now.Before(b.start.Add(l.window)) || b.n < l.limit
}

// fail records a failed attempt by key.
func (l *limiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) >= limiterSweepThreshold && now.Sub(l.lastSweep) >= l.window {
		for k, b := range l.buckets {
			if !now.Before(b.start.Add(l.window)) {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}
	b := l.buckets[key]
	if b == nil || !now.Before(b.start.Add(l.window)) {
		b = &bucket{start: now}
		l.buckets[key] = b
	}
	b.n++
}
