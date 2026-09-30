package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/lograte"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// R55-F14 (Docs/review/72-r55-f14-spec.md §5 test 5, session part): the
// session manager's relay-driven lines are one limited line per event.

type failSender struct{}

func (failSender) Send(context.Context, envelope.Envelope) error { return errors.New("write failed") }
func (failSender) Connected() bool                               { return true }

// bareManager has no worker, so the inbox is never drained, and writes its
// lines to rec.
func bareManager(rec *testutil.LogRecorder, cfg Config) *Manager {
	return &Manager{cfg: cfg, log: rec.Logger(), inbox: make(chan envelope.Envelope), lines: lograte.New(rec.Logger(), 0)}
}

func TestSessionDropLimited(t *testing.T) {
	rec := &testutil.LogRecorder{}
	m := bareManager(rec, Config{})
	for i := range 500 {
		m.HandleEnvelope(envelope.Envelope{From: fmt.Sprint("k", i), Type: TypeData, ID: fmt.Sprint("id-", i)})
	}
	if n := len(rec.Lines()); n != 0 {
		t.Fatalf("%d lines during the window", n)
	}
	m.lines.Flush()
	got := rec.Event("session_drop")
	if len(got) != 1 || got[0].Attrs["count"] != "500" || got[0].Attrs["type"] != TypeData || got[0].Level != slog.LevelWarn {
		t.Fatalf("lines = %v", rec.Lines())
	}
	if _, ok := got[0].Attrs["id"]; ok {
		t.Fatal("session_drop carries the envelope id")
	}
}

func TestSessionSendFailedLimited(t *testing.T) {
	rec := &testutil.LogRecorder{}
	m := bareManager(rec, Config{})
	m.sender = failSender{}
	for range 100 {
		if err := m.send(context.Background(), envelope.Envelope{Type: TypeResp}); err == nil {
			t.Fatal("want error")
		}
	}
	if n := len(rec.Lines()); n != 0 {
		t.Fatalf("%d lines during the window", n)
	}
	m.lines.Flush()
	got := rec.Event("session_send_failed")
	if len(got) != 1 || got[0].Attrs["count"] != "100" || got[0].Attrs["type"] != TypeResp || got[0].Attrs["error"] != "write failed" {
		t.Fatalf("lines = %v", rec.Lines())
	}
}

// 10 000 unpaired keys: one line with the count, a constant limiter map, no
// audit row, and unpaired is never counted for the summary.
func TestSessionRejectFloodFromManyKeys(t *testing.T) {
	rec := &testutil.LogRecorder{}
	var mu sync.Mutex
	counted := map[string]int{}
	m := bareManager(rec, Config{CountReject: func(r string) { mu.Lock(); counted[r]++; mu.Unlock() }})
	m.cfg.IsPaired = func(context.Context, string) (bool, error) { return false, nil }
	for i := range 10000 {
		m.handle(envelope.Envelope{From: fmt.Sprintf("key-%d", i), Type: TypeData, ID: fmt.Sprintf("id-%d", i), Payload: make([]byte, SIDSize+8)})
	}
	m.cfg.IsPaired = func(context.Context, string) (bool, error) { return true, nil }
	m.handle(envelope.Envelope{From: "paired", Type: TypeData, ID: "x", Payload: make([]byte, SIDSize+8)})
	if n := m.lines.Pending(); n > 9 {
		t.Fatalf("limiter holds %d entries", n)
	}
	m.lines.Flush()
	got := rec.Event("session_reject")
	if len(got) != 1 || got[0].Attrs["count"] != "10001" || got[0].Attrs["reasons"] != "unknown_session=1 unpaired=10000" ||
		got[0].Attrs["peer"] != "key-0" || got[0].Attrs["session"] != "00000000" {
		t.Fatalf("lines = %v", rec.Lines())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(counted) != 1 || counted[ReasonUnknownSession] != 1 {
		t.Fatalf("counted = %v", counted)
	}
}

// Close writes the pending line and no audit row exists.
func TestSessionCloseFlushes(t *testing.T) {
	r := &fakeRelay{nodes: map[string]*Manager{}}
	b := newNode(t, r, map[string]bool{})
	b.m.HandleEnvelope(envelope.Envelope{From: "stranger", To: b.key, Type: TypeInit, ID: "i", Payload: make([]byte, SIDSize)})
	waitFor(t, "reject counted", func() bool { return b.m.lines.Pending() == 1 })
	b.m.Close()
	if got := b.rec.Event("session_reject"); len(got) != 1 || got[0].Attrs["reasons"] != "unpaired=1" {
		t.Fatalf("lines = %v", b.rec.Lines())
	}
	b.noRejectRows(t)
}
