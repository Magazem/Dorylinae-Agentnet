package relay_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
)

func (p peer) typed(typ, to, id string, payload []byte) []byte {
	e := p.env(to, id, payload)
	e.Type = typ
	frame, err := e.Marshal()
	if err != nil {
		panic(err)
	}
	return frame
}

func writeFrame(t *testing.T, c *websocket.Conn, frame []byte) {
	t.Helper()
	if err := c.Write(ctx(t), websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
}

func TestReadyAdvertisesEphemeral(t *testing.T) {
	_, url := start(t, relay.Options{})
	p := newPeer(t)
	c, nonce := rawDial(t, url)
	writeFrame(t, c, authFrame(t, p, nonce))
	_, raw, err := c.Read(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	var ready envelope.Control
	if err := json.Unmarshal(raw, &ready); err != nil {
		t.Fatal(err)
	}
	if ready.Op != envelope.OpReady || len(ready.Features) != 1 || ready.Features[0] != "ephemeral" {
		t.Fatalf("ready = %s, want features [ephemeral]", raw)
	}
}

func TestEphemeralToOfflinePeerIsDroppedSilently(t *testing.T) {
	s, url := start(t, relay.Options{})
	a, ghost := newPeer(t), newPeer(t)
	ca := rawAuthed(t, url, a)

	writeFrame(t, ca, a.typed("presence", ghost.key, "p-1", []byte("x")))
	// The next frame from the relay must answer the mail below, so presence
	// produced neither queued nor error.
	sendQueued(t, ca, a, ghost.key, "m-1", nil)
	if n, err := s.Queued(ghost.key); err != nil || n != 1 {
		t.Fatalf("Queued = %d, %v; want only the mail frame", n, err)
	}
}

func TestEphemeralForwardedToConnectedPeerWithBacklog(t *testing.T) {
	s, url := start(t, relay.Options{})
	a, b := newPeer(t), newPeer(t)
	ca := rawAuthed(t, url, a)
	const backlog = 20
	for _, id := range ids("m", backlog) {
		sendQueued(t, ca, a, b.key, id, nil)
	}
	cb := rawAuthed(t, url, b)
	writeFrame(t, ca, a.typed("presence", b.key, "p-live", nil))

	seen := map[string]bool{}
	for len(seen) < backlog+1 {
		id := readID(t, cb)
		if !seen[id] {
			seen[id] = true
			if id != "p-live" {
				ack(t, cb, a.key, id)
			}
		}
	}
	if !seen["p-live"] {
		t.Fatal("presence was not forwarded")
	}
	waitQueued(t, s, b.key, 0)
}

func TestEphemeralOversizeDropped(t *testing.T) {
	s, url := start(t, relay.Options{EphemeralMaxBytes: 1024})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)

	writeFrame(t, ca, a.typed("presence", b.key, "p-big", bytes.Repeat([]byte("x"), 2048)))
	writeFrame(t, ca, a.typed("presence", b.key, "p-ok", []byte("x")))
	if got := readID(t, cb); got != "p-ok" {
		t.Fatalf("first frame = %s, want p-ok (oversized dropped)", got)
	}
	if n, _ := s.Queued(b.key); n != 0 {
		t.Fatalf("Queued = %d, want 0", n)
	}
}

func TestEphemeralRateLimit(t *testing.T) {
	clock := newClock()
	_, url := start(t, relay.Options{Now: clock.Now, SendQueue: 2048})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)

	const sent = 601
	for i := range sent {
		writeFrame(t, ca, a.typed("presence", b.key, fmt.Sprintf("p-%04d", i), nil))
	}
	writeFrame(t, ca, a.typed("ping", b.key, "sentinel", nil))
	n := 0
	for {
		id := readID(t, cb)
		if id == "sentinel" {
			break
		}
		n++
	}
	if n != 600 {
		t.Fatalf("forwarded %d presence envelopes, want 600 (601st dropped)", n)
	}

	// The next minute starts a fresh window.
	clock.Advance(time.Minute)
	writeFrame(t, ca, a.typed("presence", b.key, "p-next", nil))
	if got := readID(t, cb); got != "p-next" {
		t.Fatalf("got %s, want p-next", got)
	}
}

func TestEphemeralLogsNoPayload(t *testing.T) {
	var logs syncBuffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	_, url := start(t, relay.Options{Logger: logger})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)
	marker := []byte("EPHEMERAL-SECRET-MARKER-0123456789")
	writeFrame(t, ca, a.typed("presence", b.key, "p-log", marker))
	readID(t, cb)
	time.Sleep(50 * time.Millisecond)
	out := logs.String()
	if strings.Contains(out, string(marker)) || strings.Contains(out, "payload") {
		t.Fatalf("log contains payload:\n%s", out)
	}
}
