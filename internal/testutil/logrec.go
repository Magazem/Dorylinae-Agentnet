package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// LogLine is one record captured by a LogRecorder, its attributes rendered
// with fmt's %v.
type LogLine struct {
	Level slog.Level
	Msg   string
	Attrs map[string]string
}

// LogRecorder is a slog.Handler that keeps every record at or above Min. It is
// safe for concurrent use.
type LogRecorder struct {
	Min slog.Level

	mu    sync.Mutex
	lines []LogLine
}

// Logger returns a logger writing to r.
func (r *LogRecorder) Logger() *slog.Logger { return slog.New(r) }

// Enabled implements slog.Handler.
func (r *LogRecorder) Enabled(_ context.Context, l slog.Level) bool { return l >= r.Min }

// Handle implements slog.Handler.
func (r *LogRecorder) Handle(_ context.Context, rec slog.Record) error {
	ln := LogLine{Level: rec.Level, Msg: rec.Message, Attrs: map[string]string{}}
	rec.Attrs(func(a slog.Attr) bool {
		ln.Attrs[a.Key] = fmt.Sprint(a.Value.Any())
		return true
	})
	r.mu.Lock()
	r.lines = append(r.lines, ln)
	r.mu.Unlock()
	return nil
}

// WithAttrs implements slog.Handler; the recorder ignores logger attributes.
func (r *LogRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }

// WithGroup implements slog.Handler; the recorder ignores groups.
func (r *LogRecorder) WithGroup(string) slog.Handler { return r }

// Lines returns a copy of the records so far.
func (r *LogRecorder) Lines() []LogLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]LogLine(nil), r.lines...)
}

// Event returns the records whose event attribute is ev.
func (r *LogRecorder) Event(ev string) []LogLine {
	var out []LogLine
	for _, ln := range r.Lines() {
		if ln.Attrs["event"] == ev {
			out = append(out, ln)
		}
	}
	return out
}
