package relayclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// Mail envelopes bypass the seen-set (Docs/protocol/mail.md) so resends reach
// the mail layer and are re-acked; other types are still handed up once. Every
// delivery is acked to the relay either way.
func TestMailBypassesSeenSetOthersDoNot(t *testing.T) {
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
	frames := [][]byte{
		mk("mail", "m-1"), mk("mail", "m-1"), mk("mail", "m-1"),
		mk("session.ping", "p-1"), mk("session.ping", "p-1"),
	}

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
		ready, _ := json.Marshal(envelope.Control{Op: envelope.OpReady})
		_ = ws.Write(ctx, websocket.MessageText, ready)
		for _, f := range frames {
			_ = ws.Write(ctx, websocket.MessageText, f)
		}
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

	got := make(chan string, 16)
	c, err := relayclient.New(relayclient.Config{
		URL:        "ws" + strings.TrimPrefix(srv.URL, "http"),
		Signer:     relayclient.NewKeySigner(priv),
		OnEnvelope: func(e envelope.Envelope) { got <- e.Type + ":" + e.ID },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	for i := range frames {
		select {
		case <-acks:
		case <-time.After(5 * time.Second):
			t.Fatalf("ack %d never arrived", i)
		}
	}
	var handed []string
	for len(got) > 0 {
		handed = append(handed, <-got)
	}
	if want := "mail:m-1,mail:m-1,mail:m-1,session.ping:p-1"; strings.Join(handed, ",") != want {
		t.Fatalf("handed up %v, want %s", handed, want)
	}
}
