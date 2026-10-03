package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// The grace after downtime (Docs/protocol/debate.md §Timeouts, R55-071,
// OD-F29-3): A applies the timeout rule only once its relay connection has
// been up for debateTimeoutGrace since the later of the connect and the last
// detected resume, because B's entry may still be queued at the relay (which
// has no end-of-backlog marker). A resume is a wall-clock gap of more than
// debateResumeGap between observations; it forces a re-dial, because after a
// sleep Connected can stay true on a dead socket with a Since from before it.
const (
	debateTimeoutGrace = 120 * time.Second
	debateResumeGap    = 60 * time.Second
)

// debateRelay is the part of *relayclient.Client the gate uses.
type debateRelay interface {
	State() relayclient.State
	Reconnect()
}

// debateTimeoutGate is debate.Store.TimeoutsReady's source. It only delays
// the check, never the deadline.
type debateTimeoutGate struct {
	mu sync.Mutex
	// resumeAt is the last detected resume (a now() reading, monotonic);
	// lastTick is the wall time of the last sweep tick (Round(0)).
	resumeAt, lastTick time.Time
	client             debateRelay
	noRelay            bool // no relay configured: the plain rule
	log                *slog.Logger

	// now is the gate's own clock (time.Now): elapsed times subtract its
	// monotonic readings, so a backward wall-clock jump cannot stretch the
	// grace. wall is the wall-clock reading the gap test uses (now().Round(0)
	// when nil): Go's monotonic clock stops during suspend on Linux and
	// macOS. Both are replaced in tests.
	now  func() time.Time
	wall func() time.Time
}

func newDebateTimeoutGate(noRelay bool, log *slog.Logger) *debateTimeoutGate {
	return &debateTimeoutGate{noRelay: noRelay, log: log, now: time.Now}
}

func (g *debateTimeoutGate) wallNow() time.Time {
	if g.wall != nil {
		return g.wall()
	}
	return g.now().Round(0)
}

// setClient stores the relay client once it exists (it is created after the
// sweep starts). Until then the gate is not ready.
func (g *debateTimeoutGate) setClient(c debateRelay) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.client = c
}

// checkResume detects a resume: a wall-clock gap of more than
// debateResumeGap since the last tick. It then starts a new grace, counts
// the tick as seen (one sleep is one resume), logs debate_resume and forces
// a re-dial. It reports whether it fired.
func (g *debateTimeoutGate) checkResume() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.checkResumeLocked()
}

func (g *debateTimeoutGate) checkResumeLocked() bool {
	wall := g.wallNow()
	if g.lastTick.IsZero() || wall.Sub(g.lastTick) <= debateResumeGap {
		return false
	}
	g.resumeAt, g.lastTick = g.now(), wall
	if g.log != nil {
		g.log.Info("debate: resume detected, timeouts wait for the relay", "event", "debate_resume")
	}
	if g.client != nil {
		g.client.Reconnect()
	}
	return true
}

// Ready reports whether timeouts may be applied now. The resume test runs
// here too (review 92b F2): after a wake, an IPC debate_show can come before
// the ticker does.
func (g *debateTimeoutGate) Ready() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	fired := g.checkResumeLocked()
	switch {
	case g.noRelay:
		return true
	case fired || g.client == nil:
		return false
	}
	st := g.client.State()
	if !st.Connected {
		return false
	}
	from := st.Since
	if g.resumeAt.After(from) {
		from = g.resumeAt
	}
	return g.now().Sub(from) >= debateTimeoutGrace
}

// tick is one sweep tick: the resume test, then sweep, then the tick time.
// The gap counts from the end of the last run, so a slow sweep is not a
// resume (review 92b F7).
func (g *debateTimeoutGate) tick(ctx context.Context, sweep func(context.Context)) {
	g.checkResume()
	sweep(ctx)
	g.mu.Lock()
	g.lastTick = g.wallNow()
	g.mu.Unlock()
}
