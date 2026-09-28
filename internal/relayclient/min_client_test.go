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

// ready.min_client (4.4a) is kept for status/doctor only when it is a
// release version; anything else is relay-supplied junk and is dropped.
func TestReadyMinClient(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ready string
		want  string
	}{
		{"none", `{"op":"ready","public_key":"k"}`, ""},
		{"release", `{"op":"ready","public_key":"k","min_client":"1.2.3"}`, "1.2.3"},
		{"escape junk", `{"op":"ready","public_key":"k","min_client":"1.2.3\u001b[31m"}`, ""},
		{"not a version", `{"op":"ready","public_key":"k","min_client":"latest"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, priv := newKey(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = ws.CloseNow() }()
				challenge, _ := json.Marshal(envelope.Control{Op: envelope.OpChallenge, Version: 1, Nonce: envelope.EncodeNonce(make([]byte, envelope.NonceSize))})
				_ = ws.Write(r.Context(), websocket.MessageText, challenge)
				if _, _, err := ws.Read(r.Context()); err != nil {
					return
				}
				_ = ws.Write(r.Context(), websocket.MessageText, []byte(tc.ready))
				_, _, _ = ws.Read(r.Context()) // hold the connection open
			}))
			defer srv.Close()
			ready := make(chan struct{}, 1)
			c, err := relayclient.New(relayclient.Config{
				URL:     "ws" + strings.TrimPrefix(srv.URL, "http"),
				Signer:  relayclient.NewKeySigner(priv),
				OnReady: func() { ready <- struct{}{} },
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() { _ = c.Run(ctx) }()
			select {
			case <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("never became ready")
			}
			if got := c.State().MinClient; got != tc.want {
				t.Fatalf("State().MinClient = %q, want %q", got, tc.want)
			}
		})
	}
}
