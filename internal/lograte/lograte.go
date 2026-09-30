// Package lograte limits the daemon's relay-driven log lines to one line a
// minute per event, written at the end of the minute as a count
// (Docs/protocol/envelope.md §Relay-driven log lines (daemon), R55-F14).
package lograte

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultWindow is how often at most one event is written.
const DefaultWindow = time.Minute

// Limiter counts occurrences of fixed events and writes one line per event per
// window, at the end of the window, with the count, the count per reason and
// the fields of the first occurrence. Memory is one entry per distinct event
// name plus one counter per distinct reason, whatever the relay sends.
type Limiter struct {
	log    *slog.Logger
	window time.Duration
	// after schedules f after d and returns a function that cancels it
	// (time.AfterFunc outside tests).
	after func(d time.Duration, f func()) (stop func() bool)

	mu      sync.Mutex
	pending map[string]*entry // by event
}

type entry struct {
	level   slog.Level
	msg     string
	first   []any
	count   int
	reasons map[string]int
	stop    func() bool
}

// New returns a Limiter writing to log (nil: slog.Default). window 0 means
// DefaultWindow.
func New(log *slog.Logger, window time.Duration) *Limiter {
	if log == nil {
		log = slog.Default()
	}
	if window <= 0 {
		window = DefaultWindow
	}
	return &Limiter{
		log: log, window: window, pending: map[string]*entry{},
		after: func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop },
	}
}

// Note counts one occurrence of event. The first in a window keeps level, msg
// and the key/value pairs first for the line, and starts the window. reason
// (may be "") is counted per event. Counting does not depend on the handler's
// level. event and reason must be constants of the code, never input: they
// are map keys, so input would let the sender choose the map's size.
func (l *Limiter) Note(level slog.Level, event, msg, reason string, first ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.pending[event]
	if !ok {
		e = &entry{level: level, msg: msg, first: first}
		l.pending[event] = e
		e.stop = l.after(l.window, func() { l.flushEvent(event, e) })
	}
	e.count++
	if reason != "" {
		if e.reasons == nil {
			e.reasons = map[string]int{}
		}
		e.reasons[reason]++
	}
}

// Flush writes every pending line now (daemon stop, connection end).
func (l *Limiter) Flush() {
	l.mu.Lock()
	events := make([]string, 0, len(l.pending))
	for ev := range l.pending {
		events = append(events, ev)
	}
	slices.Sort(events)
	out := make([]*entry, 0, len(events))
	for _, ev := range events {
		e := l.pending[ev]
		e.stop()
		delete(l.pending, ev)
		out = append(out, e)
	}
	l.mu.Unlock()
	for i, e := range out {
		l.write(events[i], e)
	}
}

// Pending returns how many events have a line waiting (tests: the map stays
// bounded by the number of distinct events).
func (l *Limiter) Pending() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pending)
}

// flushEvent writes e if it is still the pending entry of event: a Flush may
// have written it already and a new window may have started since.
func (l *Limiter) flushEvent(event string, e *entry) {
	l.mu.Lock()
	if l.pending[event] != e {
		l.mu.Unlock()
		return
	}
	delete(l.pending, event)
	l.mu.Unlock()
	l.write(event, e)
}

func (l *Limiter) write(event string, e *entry) {
	args := make([]any, 0, 6+len(e.first))
	args = append(args, "event", event, "count", e.count)
	if r := formatReasons(e.reasons); r != "" {
		args = append(args, "reasons", r)
	}
	args = append(args, e.first...)
	l.log.Log(context.Background(), e.level, e.msg, args...)
}

// formatReasons renders the counts as "a=1 b=2", sorted by reason.
func formatReasons(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(strconv.Itoa(m[k]))
	}
	return b.String()
}
