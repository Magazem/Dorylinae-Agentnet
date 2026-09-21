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
	defaultPairTTL         = 10 * time.Minute
	defaultPairFailLimit   = 5
	defaultPairFailWindow  = time.Minute
	maxOutstandingPerKey   = 5
	limiterSweepThreshold  = 1024
	pairCodeHashDomain     = "dorylinae-pair-code-v1\n"
	pairEventIssue         = "pair_issue"
	pairEventRedeem        = "pair_redeem"
	pairEventFail          = "pair_fail"
	pairReasonInvalid      = "invalid"
	pairReasonRateLimited  = "rate_limited"
	pairReasonIssuerAbsent = "issuer_offline"
)

// PairStats are the relay's pairing counters. They hold no codes or cards.
type PairStats struct {
	Issued      uint64
	Redeemed    uint64
	Invalid     uint64 // unknown, expired, used or malformed code
	RateLimited uint64
}

// pairEntry is one outstanding code. The code itself is never stored, only its hash.
type pairEntry struct {
	issuer    string // wire public key
	issuerRef string
	card      json.RawMessage
	expires   time.Time
}

// pairings holds outstanding codes and the redemption failure limiter.
type pairings struct {
	ttl time.Duration

	mu      sync.Mutex
	entries map[[sha256.Size]byte]*pairEntry

	lim limiter

	issued, redeemed, invalid, limited atomic.Uint64
}

func newPairings(ttl time.Duration, failLimit int, failWindow time.Duration) *pairings {
	if ttl <= 0 {
		ttl = defaultPairTTL
	}
	if failLimit <= 0 {
		failLimit = defaultPairFailLimit
	}
	if failWindow <= 0 {
		failWindow = defaultPairFailWindow
	}
	return &pairings{
		ttl:     ttl,
		entries: map[[sha256.Size]byte]*pairEntry{},
		lim:     limiter{limit: failLimit, window: failWindow, buckets: map[string]*bucket{}},
	}
}

func hashCode(code string) [sha256.Size]byte {
	return sha256.Sum256([]byte(pairCodeHashDomain + code))
}

// PairStats returns a snapshot of the pairing counters.
func (s *Server) PairStats() PairStats {
	return PairStats{
		Issued:      s.pairs.issued.Load(),
		Redeemed:    s.pairs.redeemed.Load(),
		Invalid:     s.pairs.invalid.Load(),
		RateLimited: s.pairs.limited.Load(),
	}
}

// handleControl serves a control frame from an authenticated peer. It returns
// false if the frame is not a permitted request and the connection must close.
func (s *Server) handleControl(c *conn, ctl *envelope.Control) bool {
	switch ctl.Op {
	case envelope.OpPairNew:
		s.pairNew(c, ctl)
	case envelope.OpPairRedeem:
		s.pairRedeem(c, ctl)
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
	return true
}

func (s *Server) pairNew(c *conn, ctl *envelope.Control) {
	if !s.checkPairRequest(c, ctl) {
		return
	}
	p := s.pairs
	now := s.now()

	p.mu.Lock()
	outstanding := 0
	for h, e := range p.entries {
		switch {
		case !now.Before(e.expires):
			delete(p.entries, h)
		case e.issuer == c.key:
			outstanding++
		}
	}
	if outstanding >= maxOutstandingPerKey {
		p.mu.Unlock()
		s.reject(c, envelope.CodePairLimit, "too many outstanding pairing codes", ctl.Ref)
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
	p.entries[h] = &pairEntry{issuer: c.key, issuerRef: ctl.Ref, card: append(json.RawMessage(nil), ctl.Card...), expires: expires}
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
	if !s.checkPairRequest(c, ctl) {
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

	invalid := func() {
		p.lim.fail(c.key, now)
		p.invalid.Add(1)
		s.log.Info("pairing redemption failed", "event", pairEventFail, "reason", pairReasonInvalid, "peer", short(c.key))
		s.reject(c, envelope.CodePairInvalid, "pairing code is invalid, expired or already used", ctl.Ref)
	}

	code, ok := envelope.NormalizePairCode(ctl.Code)
	if !ok {
		invalid()
		return
	}
	h := hashCode(code)

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
	if !issuer.send(control(envelope.Control{Op: envelope.OpPairPeer, PublicKey: c.key, Card: ctl.Card, Ref: e.issuerRef})) {
		p.mu.Unlock()
		s.reject(c, envelope.CodePeerBusy, "the code's issuer is not keeping up", ctl.Ref)
		return
	}
	delete(p.entries, h)
	p.mu.Unlock()

	p.redeemed.Add(1)
	s.log.Info("pairing completed", "event", pairEventRedeem, "issuer", short(e.issuer), "redeemer", short(c.key))
	c.send(control(envelope.Control{Op: envelope.OpPairPeer, PublicKey: e.issuer, Card: e.card, Ref: ctl.Ref}))
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
