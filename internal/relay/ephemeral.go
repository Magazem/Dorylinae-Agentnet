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
	limiterPruneAbove         = 1024
)

// ephemeralLimiter is a fixed-window counter per sending key.
type ephemeralLimiter struct {
	limit int
	now   func() time.Time

	mu      sync.Mutex
	windows map[string]*ephemeralWindow
}

type ephemeralWindow struct {
	start   time.Time
	count   int
	dropped int
}

func newEphemeralLimiter(limit int, now func() time.Time) *ephemeralLimiter {
	return &ephemeralLimiter{limit: limit, now: now, windows: map[string]*ephemeralWindow{}}
}

// allow counts one envelope from key. ok is false when key is over its limit.
// report is the number of envelopes dropped in the window that just ended, set
// once when a window closes that dropped any, so the caller logs one line a minute.
func (l *ephemeralLimiter) allow(key string) (ok bool, report int) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.windows[key]
	if w == nil || now.Sub(w.start) >= ephemeralWindowLen {
		if w != nil {
			report = w.dropped
		} else if len(l.windows) > limiterPruneAbove {
			for k, o := range l.windows {
				if now.Sub(o.start) >= ephemeralWindowLen && o.dropped == 0 {
					delete(l.windows, k)
				}
			}
		}
		w = &ephemeralWindow{start: now}
		l.windows[key] = w
	}
	if w.count >= l.limit {
		w.dropped++
		return false, report
	}
	w.count++
	return true, report
}

// routeEphemeral forwards an ephemeral envelope to a connected recipient, or
// drops it silently: no queue, no queued or error frame, no queue accounting.
func (s *Server) routeEphemeral(sender *conn, h envelope.Header, frame []byte) {
	ok, report := s.eph.allow(sender.key)
	if report > 0 {
		s.log.Info("ephemeral rate limited", "event", "ephemeral_limited", "peer", short(sender.key), "dropped", report)
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
