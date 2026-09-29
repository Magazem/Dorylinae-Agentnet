package relay

import "strings"

// Draining reports whether the peer with the given key is connected and still
// receiving its queued backlog. While it is, envelopes for it are queued (and
// the sender gets "queued") instead of being forwarded directly. Tests that
// need a direct route wait for this to become false.
func (s *Server) Draining(key string) bool {
	s.mu.Lock()
	c := s.conns[key]
	s.mu.Unlock()
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.draining
}

// Buffered reports the bytes waiting in the outbound buffer of the peer with
// the given key, or 0 if it is not connected (4.0b memory bound tests).
func (s *Server) Buffered(key string) int64 {
	s.mu.Lock()
	c := s.conns[key]
	s.mu.Unlock()
	if c == nil {
		return 0
	}
	return c.buffered()
}

// DrainReserve is what one queue batch charges to the outbound budget before
// it is read from the database.
const DrainReserve = drainReserve

// Inflight reports the bytes charged to the outbound budget: envelopes
// waiting in every outbound buffer, and queue-drain reservations.
func (s *Server) Inflight() int64 { return s.led.used(kindOutbound) }

// ChargeInflight adds n bytes to the relay-wide outbound budget, as if they
// were waiting in some buffer that cannot be evicted (n < 0 gives them back).
func (s *Server) ChargeInflight(n int64) { s.led.addUnowned(kindOutbound, n) }

// Reading reports the bytes of frames being read, relay-wide (R-4.0 H1).
func (s *Server) Reading() int64 { return s.led.used(kindRead) }

// EphemeralInflight reports the bytes of presence and control frames
// waiting in every outbound buffer (the ephemeral budget, R55-F1).
func (s *Server) EphemeralInflight() int64 { return s.led.used(kindEphemeral) }

// ReadingFor, InflightFor and EphemeralFor report what one prefix
// ("10.1.0.0/24" or "10.1.0.0") holds of the read, outbound and ephemeral
// budgets (R55-F1).
func (s *Server) ReadingFor(prefix string) int64 { return s.led.usedBy(kindRead, bare(prefix)) }
func (s *Server) InflightFor(prefix string) int64 {
	return s.led.usedBy(kindOutbound, bare(prefix))
}
func (s *Server) EphemeralFor(prefix string) int64 {
	return s.led.usedBy(kindEphemeral, bare(prefix))
}

// bare drops a prefix length: the relay names a prefix by its address.
func bare(prefix string) string {
	if i := strings.IndexByte(prefix, '/'); i >= 0 {
		return prefix[:i]
	}
	return prefix
}

// DrainHeld reports the bytes queue drains have read from the database and
// not yet put into an outbound buffer (R-4.0 H1).
func (s *Server) DrainHeld() int64 { return s.drainHeld.Load() }

// SetGroupForTest puts account acc in quota group group, creating the group
// (active, 8 seats) if needed; "" takes it out of any group. Ticket 4.3a
// builds the real ways into a group.
func (s *Server) SetGroupForTest(acc, group string) error {
	ctx, cancel := opCtx()
	defer cancel()
	now := s.now().UnixMilli()
	var err error
	if group != "" {
		_, err = s.q.db.ExecContext(ctx, `INSERT OR IGNORE INTO quota_groups (id, seats, created) VALUES (?, 8, ?)`, group, now)
	}
	if err == nil {
		_, err = s.q.db.ExecContext(ctx, `UPDATE accounts SET group_id = NULLIF(?, '') WHERE id = ?`, group, acc)
	}
	s.accountsChanged()
	return err
}

// BindKeyForTest binds key to account acc directly, as a confirmed bind would.
func (s *Server) BindKeyForTest(key, acc string) error {
	ctx, cancel := opCtx()
	defer cancel()
	_, err := s.q.db.ExecContext(ctx, `INSERT INTO account_keys (key, account_id, device, os, bound_at) VALUES (?, ?, 'test', 'linux', ?)`,
		key, acc, s.now().UnixMilli())
	s.accountsChanged()
	return err
}

// FillAccountPairNewForTest counts n pair_new of account acc today (the per-account day limit).
func (s *Server) FillAccountPairNewForTest(acc string, n int) {
	for range n {
		s.pairs.newLimAccount.fail(acc, s.now())
	}
}

// ChargeEphemeral adds n bytes to the ephemeral budget as if they were held
// by nobody who can be evicted (n < 0 gives them back).
func (s *Server) ChargeEphemeral(n int64) { s.led.addUnowned(kindEphemeral, n) }

// SetRouteHookForTest runs f with the sender's key at the start of every
// routed frame (R55-144: a hook that panics).
func (s *Server) SetRouteHookForTest(f func(key string)) {
	s.onRoute = func(c *conn) { f(c.key) }
}
