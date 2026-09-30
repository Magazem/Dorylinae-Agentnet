package daemon

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// OD-F14-7 (b), R55-F14 (Docs/review/72-r55-f14-spec.md §5 test 9).

type summarySink struct {
	mu   sync.Mutex
	rows []string
}

func (s *summarySink) Append(_ context.Context, actor, action string, detail any) error {
	if actor != "daemon" || action != ActionRejectSummary {
		panic("unexpected audit call " + actor + " " + action)
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.rows = append(s.rows, string(b))
	s.mu.Unlock()
	return nil
}

func (s *summarySink) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.rows...)
}

func TestRejectSummaryRows(t *testing.T) {
	sink := &summarySink{}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	s := newRejectSummary(sink, nil, func() time.Time { return now })

	s.write(context.Background())
	if n := len(sink.all()); n != 0 {
		t.Fatalf("a row with all counts zero: %v", sink.all())
	}

	s.countMail("decrypt")
	s.countMail("decrypt")
	s.countMail("stale")
	s.countSession("bad_binding")
	now = now.Add(24 * time.Hour)
	s.write(context.Background())
	rows := sink.all()
	if len(rows) != 1 {
		t.Fatalf("rows = %v", rows)
	}
	var d map[string]any
	dec := json.NewDecoder(strings.NewReader(rows[0]))
	dec.UseNumber()
	if err := dec.Decode(&d); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"since": "2026-09-01T12:00:00Z", "until": "2026-09-02T12:00:00Z",
		"mail":    map[string]any{"decrypt": json.Number("2"), "stale": json.Number("1")},
		"session": map[string]any{"bad_binding": json.Number("1")},
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("detail = %s", rows[0])
	}

	// The next period starts empty and at the last write.
	now = now.Add(time.Hour)
	s.countSession("replay")
	s.write(context.Background())
	rows = sink.all()
	if len(rows) != 2 || rows[1] != `{"since":"2026-09-02T12:00:00Z","until":"2026-09-02T13:00:00Z","mail":{},"session":{"replay":1}}` {
		t.Fatalf("rows = %v", rows)
	}
}

// One row per tick and one at stop, never one for an empty period.
func TestRejectSummaryTickAndStop(t *testing.T) {
	sink := &summarySink{}
	s := newRejectSummary(sink, nil, nil)
	s.interval = 20 * time.Millisecond
	s.start(context.Background())
	s.countMail("key_miss")
	deadline := time.Now().Add(5 * time.Second)
	for len(sink.all()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no row after a tick")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(60 * time.Millisecond) // more ticks, nothing counted
	if n := len(sink.all()); n != 1 {
		t.Fatalf("%d rows, want 1", n)
	}
	s.countSession("decrypt")
	s.stop(context.Background())
	if n := len(sink.all()); n != 2 {
		t.Fatalf("%d rows after stop, want 2", n)
	}
}
