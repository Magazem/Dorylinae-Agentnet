package relayclient_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNewValidatesURLAndSigner(t *testing.T) {
	_, priv := newKey(t)
	s := relayclient.NewKeySigner(priv)
	for _, u := range []string{"", "http://x", "ws://", "not a url", "127.0.0.1:8787"} {
		if _, err := relayclient.New(relayclient.Config{URL: u, Signer: s}); err == nil {
			t.Errorf("URL %q accepted", u)
		}
	}
	if _, err := relayclient.New(relayclient.Config{URL: "ws://127.0.0.1:1"}); err == nil {
		t.Error("nil signer accepted")
	}
	if _, err := relayclient.New(relayclient.Config{URL: "ws://127.0.0.1:1", Signer: s}); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestSendWhileDisconnected(t *testing.T) {
	pub, priv := newKey(t)
	c, err := relayclient.New(relayclient.Config{URL: "ws://127.0.0.1:1", Signer: relayclient.NewKeySigner(priv)})
	if err != nil {
		t.Fatal(err)
	}
	e := envelope.Envelope{From: envelope.KeyString(pub), To: envelope.KeyString(pub), Type: "ping", ID: "1", TS: "2026-01-02T03:04:05Z"}
	if err := c.Send(context.Background(), e); !errors.Is(err, relayclient.ErrNotConnected) {
		t.Fatalf("Send = %v, want ErrNotConnected", err)
	}
}

// TestReconnectsWithBackoff starts the client before any relay exists, then
// restarts the relay on the same port and checks the client comes back each time.
func TestReconnectsWithBackoff(t *testing.T) {
	pub, priv := newKey(t)
	key := envelope.KeyString(pub)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // relay not up yet

	c, err := relayclient.New(relayclient.Config{
		URL:        "ws://" + addr,
		Signer:     relayclient.NewKeySigner(priv),
		MinBackoff: 20 * time.Millisecond,
		MaxBackoff: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after cancel")
		}
	}()

	time.Sleep(150 * time.Millisecond) // let a few attempts fail
	if c.Connected() {
		t.Fatal("connected to nothing")
	}

	for round := 0; round < 3; round++ {
		srv := relay.New(relay.Options{})
		hs := &http.Server{Handler: srv, ReadHeaderTimeout: time.Second}
		var l net.Listener
		eventually(t, "port to be free", func() bool {
			l, err = net.Listen("tcp", addr)
			return err == nil
		})
		go func() { _ = hs.Serve(l) }()

		eventually(t, "client to connect", func() bool { return c.Connected() && srv.Connected(key) })

		// Take the relay down; the client must notice and go back to retrying.
		_ = hs.Close()
		srv.Close()
		eventually(t, "client to notice the relay is gone", func() bool { return !c.Connected() })
	}
}

func TestKeystoreSigner(t *testing.T) {
	pub, priv := newKey(t)
	ks := keystore.New(keystore.NewFile(filepath.Join(testutil.TempDir(t), "identity.key")))
	if _, _, err := ks.Save(priv.Seed()); err != nil {
		t.Fatal(err)
	}
	msg := []byte("hello")
	sig, err := relayclient.NewKeystoreSigner(ks, pub).Sign(msg)
	if err != nil || !ed25519.Verify(pub, msg, sig) {
		t.Fatalf("Sign: %v", err)
	}
	other, _ := newKey(t)
	if _, err := relayclient.NewKeystoreSigner(ks, other).Sign(msg); err == nil {
		t.Fatal("signed for a public key that does not match the stored key")
	}
}

// TestDuplicateDeliveriesAreAckedButHandedUpOnce plays a relay that redelivers
// an envelope (as it does after a reconnect mid-flush).
func TestDuplicateDeliveriesAreAckedButHandedUpOnce(t *testing.T) {
	_, priv := newKey(t)
	senderPub, senderPriv := newKey(t)
	from := envelope.KeyString(senderPub)
	_ = senderPriv
	recvPub, _ := newKey(t)
	to := envelope.KeyString(recvPub)
	mk := func(id string) []byte {
		raw, err := envelope.Envelope{From: from, To: to, Type: "ping", ID: id, TS: time.Now().UTC().Format(time.RFC3339Nano)}.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	acks := make(chan envelope.Control, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		ctx := r.Context()
		nonce := envelope.EncodeNonce(make([]byte, envelope.NonceSize))
		challenge, _ := json.Marshal(envelope.Control{Op: envelope.OpChallenge, Version: 1, Nonce: nonce})
		_ = ws.Write(ctx, websocket.MessageText, challenge)
		if _, _, err := ws.Read(ctx); err != nil { // auth
			return
		}
		ready, _ := json.Marshal(envelope.Control{Op: envelope.OpReady})
		_ = ws.Write(ctx, websocket.MessageText, ready)
		for _, id := range []string{"e-1", "e-2", "e-1", "e-3", "e-2"} {
			_ = ws.Write(ctx, websocket.MessageText, mk(id))
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

	got := make(chan string, 8)
	c, err := relayclient.New(relayclient.Config{
		URL:        "ws" + strings.TrimPrefix(srv.URL, "http"),
		Signer:     relayclient.NewKeySigner(priv),
		OnEnvelope: func(e envelope.Envelope) { got <- e.ID },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	for i, want := range []string{"e-1", "e-2", "e-1", "e-3", "e-2"} {
		select {
		case a := <-acks:
			if a.Op != envelope.OpAck || a.From != from || a.Ref != want {
				t.Fatalf("ack %d = %+v, want ack of %s from %s", i, a, want, from[:8])
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("ack %d never arrived", i)
		}
	}
	var handed []string
	for len(got) > 0 {
		handed = append(handed, <-got)
	}
	if strings.Join(handed, ",") != "e-1,e-2,e-3" {
		t.Fatalf("OnEnvelope saw %v, want each of e-1, e-2, e-3 once", handed)
	}
}
