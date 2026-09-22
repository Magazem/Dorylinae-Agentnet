package relay

import (
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

const (
	defaultEphemeralPerMinute = 600
	defaultEphemeralMaxBytes  = 8 << 10
	ephemeralWindowLen        = time.Minute
)

// ephemeralLimiter is a fixed-window counter per sending key.
type ephemeralLimiter struct {
	limit int
	now   func() time.Time

	mu        sync.Mutex
	windows   map[string]*ephemeralWindow
	lastPrune time.Time
}

type ephemeralWindow struct {
	start   time.Time
	count   int
	dropped int
}

// limitReport is the number of envelopes one key had dropped in a window that
// has ended.
type limitReport struct {
	key     string
	dropped int
}

func newEphemeralLimiter(limit int, now func() time.Time) *ephemeralLimiter {
	return &ephemeralLimiter{limit: limit, now: now, windows: map[string]*ephemeralWindow{}}
}

// allow counts one envelope from key. ok is false when key is over its limit.
// reports lists the keys whose ended windows dropped any envelopes, each
// once, so the caller logs one line per key a minute. Ended windows are
// removed at most once a window, so the map holds only keys that sent in about
// the last two minutes, and a key that never sends again is still reported.
func (l *ephemeralLimiter) allow(key string) (ok bool, reports []limitReport) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastPrune) >= ephemeralWindowLen {
		l.lastPrune = now
		for k, w := range l.windows {
			if now.Sub(w.start) >= ephemeralWindowLen {
				if w.dropped > 0 {
					reports = append(reports, limitReport{k, w.dropped})
				}
				delete(l.windows, k)
			}
		}
	}
	w := l.windows[key]
	if w == nil || now.Sub(w.start) >= ephemeralWindowLen {
		if w != nil && w.dropped > 0 {
			reports = append(reports, limitReport{key, w.dropped})
		}
		w = &ephemeralWindow{start: now}
		l.windows[key] = w
	}
	if w.count >= l.limit {
		w.dropped++
		return false, reports
	}
	w.count++
	return true, reports
}

// routeEphemeral forwards an ephemeral envelope to a connected recipient, or
// drops it silently: no queue, no queued or error frame, no queue accounting.
func (s *Server) routeEphemeral(sender *conn, h envelope.Header, frame []byte) {
	ok, reports := s.eph.allow(sender.key)
	for _, r := range reports {
		s.log.Info("ephemeral rate limited", "event", "ephemeral_limited", "peer", short(r.key), "dropped", r.dropped)
	}
	if !ok || len(frame) > s.ephMax {
		return
	}
	s.mu.Lock()
	dst := s.conns[h.To]
	s.mu.Unlock()
	if dst != nil && dst.directEphemeral(frame) {
		s.log.Debug("routed", "event", "route", "from", short(h.From), "to", short(h.To), "type", h.Type, "id", h.ID, "bytes", len(frame))
	}
}
