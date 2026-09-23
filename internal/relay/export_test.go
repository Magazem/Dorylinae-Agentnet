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
