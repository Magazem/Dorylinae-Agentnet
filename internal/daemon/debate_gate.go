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
	// sweeping is true while tick runs its sweep, which calls Ready for
	// every overdue debate. Ready then measures the gap from lastSeen (the
	// sweep's start or its previous Ready call), not from lastTick: a slow
	// sweep is not a resume, but a suspend in the middle of one still is
	// (review 101 L1).
	sweeping bool
	lastSeen time.Time

	// redial runs a forced re-dial off the caller's goroutine (go f()):
	// Ready runs inside SweepOne's transaction and under mu, and Reconnect
	// may wait on the socket (review 101 L2). Tests run it inline.
	redial func(f func())

	// now is the gate's own clock (time.Now): elapsed times subtract its
	// monotonic readings, so a backward wall-clock jump cannot stretch the
	// grace. wall is the wall-clock reading the gap test uses (now().Round(0)
	// when nil): Go's monotonic clock stops during suspend on Linux and
	// macOS. Both are replaced in tests.
	now  func() time.Time
	wall func() time.Time
}

func newDebateTimeoutGate(noRelay bool, log *slog.Logger) *debateTimeoutGate {
	return &debateTimeoutGate{noRelay: noRelay, log: log, now: time.Now, redial: func(f func()) { go f() }}
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
// a re-dial. It reports whether it fired. The re-dial runs through g.redial,
// after mu is released.
func (g *debateTimeoutGate) checkResume() bool {
	g.mu.Lock()
	fired, c := g.checkResumeLocked()
	g.mu.Unlock()
	g.reconnect(c)
	return fired
}

// checkResumeLocked is checkResume under mu. It returns the client to
// re-dial (nil for none); the caller calls reconnect once mu is released.
func (g *debateTimeoutGate) checkResumeLocked() (bool, debateRelay) {
	wall := g.wallNow()
	ref := g.lastTick
	if g.sweeping {
		ref = g.lastSeen
		g.lastSeen = wall
	}
	if ref.IsZero() || wall.Sub(ref) <= debateResumeGap {
		return false, nil
	}
	g.resumeAt, g.lastTick = g.now(), wall
	if g.log != nil {
		g.log.Info("debate: resume detected, timeouts wait for the relay", "event", "debate_resume")
	}
	return true, g.client
}

func (g *debateTimeoutGate) reconnect(c debateRelay) {
	if c != nil {
		g.redial(c.Reconnect)
	}
}

// Ready reports whether timeouts may be applied now. The resume test runs
// here too (review 92b F2): after a wake, an IPC debate_show can come before
// the ticker does. While tick's sweep runs, the gap is measured between
// observations inside it (review 101 L1).
func (g *debateTimeoutGate) Ready() bool {
	g.mu.Lock()
	ready, c := g.readyLocked()
	g.mu.Unlock()
	g.reconnect(c)
	return ready
}

func (g *debateTimeoutGate) readyLocked() (bool, debateRelay) {
	fired, c := g.checkResumeLocked()
	switch {
	case g.noRelay:
		return true, c
	case fired || g.client == nil:
		return false, c
	}
	st := g.client.State()
	if !st.Connected {
		return false, c
	}
	from := st.Since
	if g.resumeAt.After(from) {
		from = g.resumeAt
	}
	return g.now().Sub(from) >= debateTimeoutGrace, c
}

// tick is one sweep tick: the resume test, then sweep, then the tick time.
// The gap counts from the end of the last run, and inside the sweep from
// the previous observation, so a slow sweep is not a resume (review 92b F7,
// review 101 L1).
func (g *debateTimeoutGate) tick(ctx context.Context, sweep func(context.Context)) {
	g.checkResume()
	g.mu.Lock()
	g.sweeping, g.lastSeen = true, g.wallNow()
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.sweeping = false
		g.lastTick = g.wallNow()
		g.mu.Unlock()
	}()
	sweep(ctx)
}
