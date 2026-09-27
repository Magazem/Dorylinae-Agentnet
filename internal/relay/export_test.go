package relay

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

// Inflight reports the bytes waiting in every outbound buffer together.
func (s *Server) Inflight() int64 { return s.lim.inflight.used.Load() }

// ChargeInflight adds n bytes to the relay-wide outbound budget, as if they
// were waiting in some buffer (n < 0 gives them back).
func (s *Server) ChargeInflight(n int64) { s.lim.inflight.add(n) }

// Reading reports the bytes of frames being read, relay-wide (R-4.0 H1).
func (s *Server) Reading() int64 { return s.lim.reading.used.Load() }

// DrainHeld reports the bytes queue drains have read from the database and
// not yet put into an outbound buffer (R-4.0 H1).
func (s *Server) DrainHeld() int64 { return s.drainHeld.Load() }
