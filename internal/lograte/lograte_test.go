package lograte

import (
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// fakeAfter replaces time.AfterFunc: fire runs every scheduled function that
// was not stopped, as if the window had passed.
type fakeAfter struct {
	mu    sync.Mutex
	funcs []*fakeTimer
	durs  []time.Duration
}

type fakeTimer struct {
	f       func()
	stopped bool
}

func (a *fakeAfter) after(d time.Duration, f func()) func() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := &fakeTimer{f: f}
	a.funcs = append(a.funcs, t)
	a.durs = append(a.durs, d)
	return func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		was := !t.stopped
		t.stopped = true
		return was
	}
}

func (a *fakeAfter) fire() {
	a.mu.Lock()
	var run []func()
	for _, t := range a.funcs {
		if !t.stopped {
			t.stopped = true
			run = append(run, t.f)
		}
	}
	a.mu.Unlock()
	for _, f := range run {
		f()
	}
}

func newTest(lvl slog.Level) (*Limiter, *testutil.LogRecorder, *fakeAfter) {
	rec := &testutil.LogRecorder{Min: lvl}
	fa := &fakeAfter{}
	l := New(rec.Logger(), 0)
	l.after = fa.after
	return l, rec, fa
}

func TestTrailingCountWithReasons(t *testing.T) {
	l, rec, fa := newTest(slog.LevelDebug)
	l.Note(slog.LevelInfo, "mail_reject", "mail envelopes rejected", "unpaired", "reason", "unpaired", "step", 1, "peer", "p0")
	for i := 1; i < 1000; i++ {
		l.Note(slog.LevelInfo, "mail_reject", "mail envelopes rejected", "unpaired", "reason", "unpaired", "step", 1, "peer", fmt.Sprint("p", i))
	}
	l.Note(slog.LevelInfo, "mail_reject", "mail envelopes rejected", "bad_signature", "reason", "bad_signature", "step", 7, "peer", "px")
	if n := len(rec.Lines()); n != 0 {
		t.Fatalf("%d lines written during the window, want 0", n)
	}
	if len(fa.durs) != 1 || fa.durs[0] != time.Minute {
		t.Fatalf("scheduled %v, want one 1m window", fa.durs)
	}
	fa.fire()
	lines := rec.Event("mail_reject")
	if len(lines) != 1 {
		t.Fatalf("%d lines, want 1", len(lines))
	}
	a := lines[0].Attrs
	if a["count"] != "1001" || a["reasons"] != "bad_signature=1 unpaired=1000" || a["reason"] != "unpaired" || a["peer"] != "p0" || a["step"] != "1" {
		t.Fatalf("line = %v", a)
	}
	if lines[0].Level != slog.LevelInfo || lines[0].Msg != "mail envelopes rejected" {
		t.Fatalf("level/msg = %v %q", lines[0].Level, lines[0].Msg)
	}
	if l.Pending() != 0 {
		t.Fatal("event still pending after its line")
	}
}

func TestWindowRollsOver(t *testing.T) {
	l, rec, fa := newTest(slog.LevelDebug)
	l.Note(slog.LevelWarn, "session_drop", "dropped", "", "type", "session.data")
	fa.fire()
	l.Note(slog.LevelWarn, "session_drop", "dropped", "", "type", "session.init")
	l.Note(slog.LevelWarn, "session_drop", "dropped", "", "type", "session.fin")
	fa.fire()
	lines := rec.Event("session_drop")
	if len(lines) != 2 {
		t.Fatalf("%d lines, want 2", len(lines))
	}
	if lines[0].Attrs["count"] != "1" || lines[1].Attrs["count"] != "2" || lines[1].Attrs["type"] != "session.init" {
		t.Fatalf("lines = %v", lines)
	}
	if _, ok := lines[0].Attrs["reasons"]; ok {
		t.Fatal("reasons written for an event without reasons")
	}
}

func TestFlushWritesPendingOnce(t *testing.T) {
	l, rec, fa := newTest(slog.LevelDebug)
	l.Note(slog.LevelWarn, "a", "a", "x")
	l.Note(slog.LevelWarn, "b", "b", "")
	l.Flush()
	if len(rec.Event("a")) != 1 || len(rec.Event("b")) != 1 {
		t.Fatalf("lines = %v", rec.Lines())
	}
	// The stopped timers must not write the lines again, and a late firing of
	// an old timer must not flush a new window.
	l.Note(slog.LevelWarn, "a", "a", "x")
	for _, tm := range fa.funcs[:2] {
		tm.f()
	}
	if len(rec.Event("a")) != 1 {
		t.Fatalf("old timer flushed the new window: %v", rec.Lines())
	}
	l.Flush()
	l.Flush()
	if len(rec.Event("a")) != 2 {
		t.Fatalf("lines = %v", rec.Lines())
	}
}

// The count does not depend on the handler's level: at Info a Debug event
// writes nothing, and the next window starts from zero either way.
func TestCountsAtEveryLevel(t *testing.T) {
	for _, lvl := range []slog.Level{slog.LevelDebug, slog.LevelInfo} {
		l, rec, fa := newTest(lvl)
		for range 5 {
			l.Note(slog.LevelDebug, "relay_misrouted", "misrouted", "", "type", "mail", "id", "m-1")
		}
		if l.Pending() != 1 {
			t.Fatalf("lvl %v: pending %d", lvl, l.Pending())
		}
		fa.fire()
		got := rec.Event("relay_misrouted")
		if lvl == slog.LevelDebug && (len(got) != 1 || got[0].Attrs["count"] != "5") {
			t.Fatalf("debug: %v", got)
		}
		if lvl == slog.LevelInfo && len(got) != 0 {
			t.Fatalf("info: %v", got)
		}
		if l.Pending() != 0 {
			t.Fatalf("lvl %v: still pending", lvl)
		}
	}
}

func TestMapBoundedByEvents(t *testing.T) {
	l, _, _ := newTest(slog.LevelDebug)
	events := []string{"relay_misrouted", "relay_error_frame", "relay_ack_failed", "mail_reject", "mail_ack_failed", "session_reject", "session_drop", "session_send_failed"}
	for i := range 10000 {
		ev := events[i%len(events)]
		l.Note(slog.LevelInfo, ev, "m", "unpaired", "peer", fmt.Sprintf("key-%d", i))
	}
	if n := l.Pending(); n > 9 {
		t.Fatalf("limiter map holds %d entries, want <= 9", n)
	}
}

func TestConcurrentNotes(t *testing.T) {
	rec := &testutil.LogRecorder{}
	l := New(rec.Logger(), time.Millisecond)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				l.Note(slog.LevelInfo, "e", "m", "r")
			}
		})
	}
	wg.Wait()
	l.Flush()
	total := 0
	deadline := time.Now().Add(5 * time.Second)
	for {
		total = 0
		for _, ln := range rec.Event("e") {
			var n int
			_, _ = fmt.Sscan(ln.Attrs["count"], &n)
			total += n
		}
		if total == 1600 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if total != 1600 {
		t.Fatalf("counted %d, want 1600", total)
	}
}
