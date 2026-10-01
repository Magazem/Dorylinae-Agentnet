package relayclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// R55-F14 (Docs/review/72-r55-f14-spec.md §5 test 5, relayclient part):
// relay_misrouted, relay_error_frame and relay_ack_failed are one limited
// line per event, whatever the relay sends. The client is not connected, so
// every ack fails.
func TestRelayDrivenLinesLimited(t *testing.T) {
	for _, lvl := range []slog.Level{slog.LevelDebug, slog.LevelInfo} {
		_, priv, _ := ed25519.GenerateKey(rand.Reader)
		other, _, _ := ed25519.GenerateKey(rand.Reader)
		sender, _, _ := ed25519.GenerateKey(rand.Reader)
		rec := &testutil.LogRecorder{Min: lvl}
		c, err := New(Config{URL: "ws://127.0.0.1:1", Signer: NewKeySigner(priv), Logger: rec.Logger()})
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		for i := range 300 {
			f, err := envelope.Envelope{
				From: envelope.KeyString(sender), To: envelope.KeyString(other), Type: MailType,
				ID: fmt.Sprintf("m-%032x", i), TS: "2026-01-02T03:04:05Z", Payload: []byte("x"),
			}.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			c.dispatch(ctx, f)
		}
		for i := range 200 {
			f, _ := json.Marshal(envelope.Control{Op: envelope.OpError, Code: "peer_offline", Ref: fmt.Sprint("r-", i)})
			c.dispatch(ctx, f)
		}
		if n := len(rec.Lines()); n != 0 {
			t.Fatalf("lvl %v: %d lines during the window: %v", lvl, n, rec.Lines())
		}
		if n := c.lines.Pending(); n != 3 {
			t.Fatalf("lvl %v: %d pending events, want 3", lvl, n)
		}
		c.lines.Flush() // what the connection end does
		ack := rec.Event("relay_ack_failed")
		if len(ack) != 1 || ack[0].Attrs["count"] != "300" || ack[0].Attrs["id"] != fmt.Sprintf("m-%032x", 0) || ack[0].Level != slog.LevelWarn {
			t.Fatalf("lvl %v: relay_ack_failed = %v", lvl, ack)
		}
		mis, ef := rec.Event("relay_misrouted"), rec.Event("relay_error_frame")
		if lvl == slog.LevelInfo {
			if len(mis) != 0 || len(ef) != 0 {
				t.Fatalf("Debug lines at Info: %v", rec.Lines())
			}
			continue
		}
		if len(mis) != 1 || mis[0].Attrs["count"] != "300" || mis[0].Attrs["type"] != MailType {
			t.Fatalf("relay_misrouted = %v", mis)
		}
		if len(ef) != 1 || ef[0].Attrs["count"] != "200" || ef[0].Attrs["code"] != "peer_offline" || ef[0].Attrs["ref"] != "r-0" {
			t.Fatalf("relay_error_frame = %v", ef)
		}
	}
}
