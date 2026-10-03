package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// R55-F29 test 6 (R55-071, review 92b F2/F6/F7): the timeout gate, with
// injected clocks and a fake relay client. No real sleeps.

type fakeRelay struct {
	mu         sync.Mutex
	st         relayclient.State
	reconnects int
}

func (f *fakeRelay) State() relayclient.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}

func (f *fakeRelay) Reconnect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconnects++
}

func (f *fakeRelay) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reconnects
}

// gateClock drives the gate: mono is the monotonic offset (now), and wall
// adds skew to it (a wall-clock jump the monotonic clock does not see).
type gateClock struct {
	base       time.Time // carries a monotonic reading
	mono, skew time.Duration
}

func (c *gateClock) now() time.Time  { return c.base.Add(c.mono) }
func (c *gateClock) wall() time.Time { return c.base.Add(c.mono + c.skew).Round(0) }

func newTestGate(t *testing.T, connectedFor time.Duration) (*debateTimeoutGate, *gateClock, *fakeRelay) {
	t.Helper()
	c := &gateClock{base: time.Now()}
	g := newDebateTimeoutGate(false, nil)
	g.now, g.wall = c.now, c.wall
	c.mono = time.Hour
	fr := &fakeRelay{st: relayclient.State{Connected: true, Since: c.now().Add(-connectedFor)}}
	g.setClient(fr)
	return g, c, fr
}

func noSweep(context.Context) {}

func TestDebateGateGrace(t *testing.T) {
	g, c, fr := newTestGate(t, 119*time.Second)
	if g.Ready() {
		t.Fatal("ready after 119 s of connection")
	}
	c.mono += time.Second
	if !g.Ready() {
		t.Fatal("not ready after 120 s of connection")
	}
	fr.mu.Lock()
	fr.st.Connected = false
	fr.mu.Unlock()
	if g.Ready() {
		t.Fatal("ready while disconnected")
	}
	g.setClient(nil)
	if g.Ready() {
		t.Fatal("ready with no client yet")
	}

	plain := newDebateTimeoutGate(true, nil)
	if !plain.Ready() {
		t.Fatal("no relay configured: the plain rule must apply")
	}
}

func TestDebateGateResumeOnTick(t *testing.T) {
	g, c, fr := newTestGate(t, time.Hour)
	g.tick(context.Background(), noSweep)
	if !g.Ready() {
		t.Fatal("not ready on a long-lived connection")
	}
	// A 25 s gap changes nothing.
	c.mono += 25 * time.Second
	g.tick(context.Background(), noSweep)
	if !g.Ready() || fr.count() != 0 {
		t.Fatalf("25 s gap: ready %v, reconnects %d", g.Ready(), fr.count())
	}
	// A 61 s gap is a resume: one re-dial, not ready for 120 s.
	c.mono += 61 * time.Second
	g.tick(context.Background(), noSweep)
	if fr.count() != 1 || g.resumeAt.IsZero() {
		t.Fatalf("reconnects %d, resumeAt %v", fr.count(), g.resumeAt)
	}
	resumed := g.resumeAt
	for d := time.Duration(0); d < 120*time.Second; d += 17 * time.Second { // ticks under the gap
		c.mono = resumed.Add(d).Sub(c.base)
		g.tick(context.Background(), noSweep)
		if g.Ready() {
			t.Fatalf("ready %v after the resume", d)
		}
	}
	c.mono = resumed.Add(120 * time.Second).Sub(c.base)
	g.tick(context.Background(), noSweep)
	if !g.Ready() || fr.count() != 1 {
		t.Fatalf("120 s after the resume: ready %v, reconnects %d", g.Ready(), fr.count())
	}
}

// Review 92b F2: after a wake, debate_show can call Ready before any tick.
// The old socket still reads Connected with an old Since.
func TestDebateGateResumeSeenByIPCFirst(t *testing.T) {
	g, c, fr := newTestGate(t, 2*time.Hour)
	g.tick(context.Background(), noSweep)
	c.mono += 10 * time.Minute // asleep: no tick in between
	if g.Ready() {
		t.Fatal("ready on the first call after a resume")
	}
	if fr.count() != 1 {
		t.Fatalf("reconnects = %d, want 1", fr.count())
	}
	c.mono += time.Second
	if g.Ready() || fr.count() != 1 {
		t.Fatalf("1 s later: ready %v, reconnects %d", g.Ready(), fr.count())
	}
}

// Review 92b F7: a sweep that takes 70 s, followed by a normal tick, is not a
// resume.
func TestDebateGateSlowSweepIsNotResume(t *testing.T) {
	g, c, fr := newTestGate(t, time.Hour)
	g.tick(context.Background(), noSweep)
	c.mono += 20 * time.Second
	g.tick(context.Background(), func(context.Context) { c.mono += 70 * time.Second })
	c.mono += 20 * time.Second
	g.tick(context.Background(), noSweep)
	if fr.count() != 0 || !g.Ready() {
		t.Fatalf("slow sweep: reconnects %d, ready %v", fr.count(), g.Ready())
	}
}

// A backward wall-clock jump of 1 h while connected neither looks like a
// resume nor delays readiness (monotonic subtraction).
func TestDebateGateBackwardWallJump(t *testing.T) {
	g, c, fr := newTestGate(t, 100*time.Second)
	g.tick(context.Background(), noSweep)
	c.skew = -time.Hour
	c.mono += 20 * time.Second
	g.tick(context.Background(), noSweep)
	if fr.count() != 0 || !g.Ready() {
		t.Fatalf("backward jump: reconnects %d, ready %v (connected 120 s)", fr.count(), g.Ready())
	}
}
