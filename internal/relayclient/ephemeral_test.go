package relayclient_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// Presence is handed up without touching the seen-set and is never acked, so a
// heartbeat flood cannot evict a session.* id.
func TestPresenceBypassesSeenSetAndIsNotAcked(t *testing.T) {
	_, priv := newKey(t)
	senderPub, _ := newKey(t)
	from := envelope.KeyString(senderPub)
	recvPub, _ := newKey(t)
	to := envelope.KeyString(recvPub)
	mk := func(typ, id string) []byte {
		raw, err := envelope.Envelope{From: from, To: to, Type: typ, ID: id, TS: time.Now().UTC().Format(time.RFC3339Nano), Payload: []byte("x")}.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	const flood = 10000
	acks := make(chan envelope.Control, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		ctx := r.Context()
		challenge, _ := json.Marshal(envelope.Control{Op: envelope.OpChallenge, Version: 1, Nonce: envelope.EncodeNonce(make([]byte, envelope.NonceSize))})
		_ = ws.Write(ctx, websocket.MessageText, challenge)
		if _, _, err := ws.Read(ctx); err != nil {
			return
		}
		ready, _ := json.Marshal(envelope.Control{Op: envelope.OpReady, Features: []string{envelope.FeatureEphemeral}})
		_ = ws.Write(ctx, websocket.MessageText, ready)
		_ = ws.Write(ctx, websocket.MessageText, mk("session.ping", "s-1"))
		for i := range flood {
			_ = ws.Write(ctx, websocket.MessageText, mk("presence", fmt.Sprintf("p-%05d", i)))
		}
		_ = ws.Write(ctx, websocket.MessageText, mk("session.ping", "s-1")) // duplicate
		_ = ws.Write(ctx, websocket.MessageText, mk("session.ping", "s-2")) // sentinel
		for {
			_, frame, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var c envelope.Control
			if json.Unmarshal(frame, &c) == nil {
				acks <- c
			}
		}
	}))
	defer srv.Close()

	var presence int
	var sessions []string
	done := make(chan struct{})
	c, err := relayclient.New(relayclient.Config{
		URL:    "ws" + strings.TrimPrefix(srv.URL, "http"),
		Signer: relayclient.NewKeySigner(priv),
		OnEnvelope: func(e envelope.Envelope) {
			if e.Type == "presence" {
				presence++
				return
			}
			sessions = append(sessions, e.ID)
			if e.ID == "s-2" {
				close(done)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("never received the sentinel")
	}
	if presence != flood {
		t.Fatalf("handed up %d presence envelopes, want %d", presence, flood)
	}
	if got := strings.Join(sessions, ","); got != "s-1,s-2" {
		t.Fatalf("session envelopes handed up = %s, want s-1,s-2 (duplicate s-1 still suppressed)", got)
	}
	if !c.HasFeature(envelope.FeatureEphemeral) || len(c.Features()) != 1 {
		t.Fatalf("Features = %v, want [ephemeral]", c.Features())
	}

	// Acks: s-1, s-1 (duplicate), s-2 and nothing for presence.
	for i := range 3 {
		select {
		case a := <-acks:
			if strings.HasPrefix(a.Ref, "p-") {
				t.Fatalf("ack %d is for presence %s", i, a.Ref)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("ack %d never arrived", i)
		}
	}
	select {
	case a := <-acks:
		t.Fatalf("unexpected extra ack %+v", a)
	case <-time.After(200 * time.Millisecond):
	}
}
