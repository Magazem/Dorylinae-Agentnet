package relay_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

const wait = 5 * time.Second

type peer struct {
	priv ed25519.PrivateKey
	key  string
}

func newPeer(t *testing.T) peer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return peer{priv: priv, key: envelope.KeyString(pub)}
}

func (p peer) env(to, id string, payload []byte) envelope.Envelope {
	return envelope.Envelope{From: p.key, To: to, Team: "t", Type: "ping", ID: id, TS: time.Now().UTC().Format(time.RFC3339Nano), Payload: payload}
}

func start(t *testing.T, opts relay.Options) (*relay.Server, string) {
	t.Helper()
	s := relay.New(opts)
	t.Cleanup(s.Close)
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return s, "ws" + strings.TrimPrefix(ts.URL, "http") + envelope.ConnectPath
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), wait)
	t.Cleanup(cancel)
	return c
}

// rawConn dials and returns the connection plus the challenge nonce.
func rawDial(t *testing.T, url string) (*websocket.Conn, []byte) {
	t.Helper()
	c, _, err := websocket.Dial(ctx(t), url, nil) //nolint:bodyclose // successful WebSocket dials have no body to close
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	ch := readControl(t, c)
	if ch.Op != envelope.OpChallenge {
		t.Fatalf("first frame op = %q, want challenge", ch.Op)
	}
	nonce, err := envelope.DecodeNonce(ch.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	return c, nonce
}

func readControl(t *testing.T, c *websocket.Conn) envelope.Control {
	t.Helper()
	_, raw, err := c.Read(ctx(t))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var ctl envelope.Control
	if err := json.Unmarshal(raw, &ctl); err != nil {
		t.Fatalf("not a control frame: %v", err)
	}
	return ctl
}

func authFrame(t *testing.T, p peer, nonce []byte) []byte {
	t.Helper()
	a, err := envelope.SignAuth(ed25519.PrivateKey(p.priv).Public().(ed25519.PublicKey), nonce, relayclient.NewKeySigner(p.priv).Sign)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(a)
	return raw
}

// rawAuthed connects and completes authentication by hand.
func rawAuthed(t *testing.T, url string, p peer) *websocket.Conn {
	t.Helper()
	c, nonce := rawDial(t, url)
	if err := c.Write(ctx(t), websocket.MessageText, authFrame(t, p, nonce)); err != nil {
		t.Fatal(err)
	}
	if ctl := readControl(t, c); ctl.Op != envelope.OpReady || ctl.PublicKey != p.key {
		t.Fatalf("got %+v, want ready", ctl)
	}
	return c
}

// expectRejected asserts the relay sent auth_failed and closed with a policy violation.
func expectRejected(t *testing.T, c *websocket.Conn) {
	t.Helper()
	if ctl := readControl(t, c); ctl.Op != envelope.OpError || ctl.Code != envelope.CodeAuthFailed {
		t.Fatalf("got %+v, want auth_failed", ctl)
	}
	_, _, err := c.Read(ctx(t))
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("read after rejection: %v, want close 1008", err)
	}
}

// stillAlive proves c is open and routable by sending p an envelope to itself.
func stillAlive(t *testing.T, c *websocket.Conn, p peer) {
	t.Helper()
	frame, err := p.env(p.key, "alive-check", nil).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx(t), websocket.MessageText, frame); err != nil {
		t.Fatalf("connection closed: %v", err)
	}
	_, got, err := c.Read(ctx(t))
	if err != nil || !bytes.Equal(got, frame) {
		t.Fatalf("connection not routing: err=%v got=%s", err, got)
	}
}

func waitConnected(t *testing.T, s *relay.Server, key string, want bool) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for s.Connected(key) != want {
		if time.Now().After(deadline) {
			t.Fatalf("Connected(%s) never became %v", key[:8], want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTwoDaemonsExchangeEnvelope(t *testing.T) {
	s, url := start(t, relay.Options{})
	a, b := newPeer(t), newPeer(t)

	got := make(chan envelope.Envelope, 1)
	clientA, err := relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(a.priv)})
	if err != nil {
		t.Fatal(err)
	}
	clientB, err := relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(b.priv), OnEnvelope: func(e envelope.Envelope) { got <- e }})
	if err != nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = clientA.Run(rctx) }()
	go func() { _ = clientB.Run(rctx) }()
	waitConnected(t, s, a.key, true)
	waitConnected(t, s, b.key, true)

	payload := make([]byte, 4096)
	_, _ = rand.Read(payload)
	sent := a.env(b.key, "msg-1", payload)
	deadline := time.Now().Add(wait)
	for clientA.Send(ctx(t), sent) != nil { // client may not have flipped to connected yet
		if time.Now().After(deadline) {
			t.Fatal("client A never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case e := <-got:
		if e.From != sent.From || e.To != sent.To || e.ID != sent.ID || e.Type != sent.Type || e.Team != sent.Team || e.TS != sent.TS || !bytes.Equal(e.Payload, payload) {
			t.Fatalf("envelope changed in transit:\n got %+v\nwant %+v", e, sent)
		}
	case <-time.After(wait):
		t.Fatal("B never received the envelope")
	}
}

func TestForwardsFrameByteForByte(t *testing.T) {
	_, url := start(t, relay.Options{})
	a, b := newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)

	// Odd whitespace, key order and an unknown field must all survive untouched.
	frame := fmt.Sprintf(`{ "payload" : "AAEC/w==",  "x-future":{"k":[1, 2]},"id":"f-1","type":"ping","ts":"2026-01-02T03:04:05Z", "team":"", "to":%q,"from":%q }`, b.key, a.key)
	if err := ca.Write(ctx(t), websocket.MessageText, []byte(frame)); err != nil {
		t.Fatal(err)
	}
	_, got, err := cb.Read(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != frame {
		t.Fatalf("frame changed:\n got %s\nwant %s", got, frame)
	}
}

func TestAuthRejections(t *testing.T) {
	a, mallory := newPeer(t), newPeer(t)

	t.Run("bad signature", func(t *testing.T) {
		s, url := start(t, relay.Options{})
		c, nonce := rawDial(t, url)
		// Mallory signs, but claims to be A.
		forged := authFrame(t, mallory, nonce)
		var f envelope.Control
		_ = json.Unmarshal(forged, &f)
		f.PublicKey = a.key
		raw, _ := json.Marshal(f)
		if err := c.Write(ctx(t), websocket.MessageText, raw); err != nil {
			t.Fatal(err)
		}
		expectRejected(t, c)
		if s.Connected(a.key) {
			t.Fatal("rejected peer is registered")
		}
	})

	t.Run("replayed challenge response", func(t *testing.T) {
		s, url := start(t, relay.Options{})
		c1, nonce1 := rawDial(t, url)
		captured := authFrame(t, a, nonce1) // a valid answer to connection 1's challenge
		if err := c1.Write(ctx(t), websocket.MessageText, captured); err != nil {
			t.Fatal(err)
		}
		if ctl := readControl(t, c1); ctl.Op != envelope.OpReady {
			t.Fatalf("legit auth failed: %+v", ctl)
		}
		_ = c1.CloseNow()
		waitConnected(t, s, a.key, false)

		c2, _ := rawDial(t, url) // new connection, new nonce
		if err := c2.Write(ctx(t), websocket.MessageText, captured); err != nil {
			t.Fatal(err)
		}
		expectRejected(t, c2)
	})

	t.Run("expired challenge", func(t *testing.T) {
		s, url := start(t, relay.Options{ChallengeTTL: 150 * time.Millisecond})
		c, nonce := rawDial(t, url)
		time.Sleep(400 * time.Millisecond)
		// The relay has given up; whether the write or the read notices first, the peer never registers.
		_ = c.Write(ctx(t), websocket.MessageText, authFrame(t, a, nonce))
		_, _, err := c.Read(ctx(t))
		if err == nil {
			t.Fatal("connection still open after expiry")
		}
		if s.Connected(a.key) {
			t.Fatal("expired challenge registered the peer")
		}
	})

	t.Run("first frame is not auth", func(t *testing.T) {
		_, url := start(t, relay.Options{})
		c, _ := rawDial(t, url)
		env, _ := json.Marshal(a.env(a.key, "x", nil))
		if err := c.Write(ctx(t), websocket.MessageText, env); err != nil {
			t.Fatal(err)
		}
		expectRejected(t, c)
	})

	t.Run("garbage signature", func(t *testing.T) {
		_, url := start(t, relay.Options{})
		c, _ := rawDial(t, url)
		raw := fmt.Sprintf(`{"op":"auth","public_key":%q,"signature":"AAAA"}`, a.key)
		if err := c.Write(ctx(t), websocket.MessageText, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		expectRejected(t, c)
	})
}

func TestOfflinePeerGetsQueuedAck(t *testing.T) {
	_, url := start(t, relay.Options{})
	a, ghost := newPeer(t), newPeer(t)
	ca := rawAuthed(t, url, a)

	raw, _ := json.Marshal(a.env(ghost.key, "lost-1", []byte("hi")))
	if err := ca.Write(ctx(t), websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
	ctl := readControl(t, ca)
	if ctl.Op != envelope.OpQueued || ctl.Ref != "lost-1" {
		t.Fatalf("got %+v, want queued ref lost-1", ctl)
	}
	// The sender stays connected.
	stillAlive(t, ca, a)
}

func TestClientSurfacesErrorFrame(t *testing.T) {
	_, url := start(t, relay.Options{})
	a, other := newPeer(t), newPeer(t)
	errs := make(chan envelope.ErrorFrame, 1)
	c, err := relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(a.priv), OnError: func(e envelope.ErrorFrame) { errs <- e }})
	if err != nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.Run(rctx) }()
	deadline := time.Now().Add(wait)
	for c.Send(ctx(t), other.env(a.key, "id-9", nil)) != nil {
		if time.Now().After(deadline) {
			t.Fatal("never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case e := <-errs:
		if e.Code != envelope.CodeBadSender || e.Ref != "id-9" {
			t.Fatalf("got %+v", e)
		}
	case <-time.After(wait):
		t.Fatal("no error frame")
	}
}

func TestBadEnvelopeAndSenderMismatch(t *testing.T) {
	_, url := start(t, relay.Options{})
	a, b := newPeer(t), newPeer(t)
	ca := rawAuthed(t, url, a)

	for _, tc := range []struct {
		name, frame, code string
	}{
		{"not json", `not json`, envelope.CodeBadEnvelope},
		{"missing fields", `{"to":"x"}`, envelope.CodeBadEnvelope},
		{"spoofed from", mustJSON(b.env(a.key, "s-1", nil)), envelope.CodeBadSender},
	} {
		if err := ca.Write(ctx(t), websocket.MessageText, []byte(tc.frame)); err != nil {
			t.Fatal(err)
		}
		if ctl := readControl(t, ca); ctl.Code != tc.code {
			t.Errorf("%s: code = %q, want %q", tc.name, ctl.Code, tc.code)
		}
	}
	// Still usable afterwards.
	stillAlive(t, ca, a)
}

func TestControlFrameAfterAuthClosesConnection(t *testing.T) {
	_, url := start(t, relay.Options{})
	a := newPeer(t)
	c := rawAuthed(t, url, a)
	if err := c.Write(ctx(t), websocket.MessageText, []byte(`{"op":"auth"}`)); err != nil {
		t.Fatal(err)
	}
	_, _, err := c.Read(ctx(t))
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("read: %v, want close 1008", err)
	}
}

func TestNewConnectionReplacesOld(t *testing.T) {
	s, url := start(t, relay.Options{})
	a := newPeer(t)
	old := rawAuthed(t, url, a)
	fresh := rawAuthed(t, url, a)

	_, _, err := old.Read(ctx(t))
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("old connection: %v, want close 1008", err)
	}
	if !s.Connected(a.key) {
		t.Fatal("replacement was unregistered along with the old connection")
	}
	stillAlive(t, fresh, a)
}

func TestUnknownPathIs404(t *testing.T) {
	s := relay.New(relay.Options{})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestLogsContainNoPayload(t *testing.T) {
	var logs syncBuffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	_, url := start(t, relay.Options{Logger: logger})
	a, b, ghost := newPeer(t), newPeer(t), newPeer(t)
	ca, cb := rawAuthed(t, url, a), rawAuthed(t, url, b)

	marker := []byte("TOP-SECRET-PAYLOAD-MARKER-0123456789")
	frame, err := a.env(b.key, "log-1", marker).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := ca.Write(ctx(t), websocket.MessageText, frame); err != nil { // routed
		t.Fatal(err)
	}
	if _, _, err := cb.Read(ctx(t)); err != nil {
		t.Fatal(err)
	}
	dropped, _ := a.env(ghost.key, "log-2", marker).Marshal()
	if err := ca.Write(ctx(t), websocket.MessageText, dropped); err != nil { // queued: peer offline
		t.Fatal(err)
	}
	readControl(t, ca)
	// Malformed frames that embed the marker must not be echoed into logs either.
	if err := ca.Write(ctx(t), websocket.MessageText, append([]byte(`{"from":`), marker...)); err != nil {
		t.Fatal(err)
	}
	readControl(t, ca)
	// A failed auth attempt.
	bad, _ := rawDial(t, url)
	_ = bad.Write(ctx(t), websocket.MessageText, append([]byte(`{"op":"auth","signature":"`), marker...))
	_, _, _ = bad.Read(ctx(t))
	_ = ca.CloseNow()
	_ = cb.CloseNow()
	time.Sleep(100 * time.Millisecond)

	out := logs.String()
	for _, needle := range []string{
		string(marker),
		base64.StdEncoding.EncodeToString(marker),
		base64.RawURLEncoding.EncodeToString(marker),
	} {
		if strings.Contains(out, needle) {
			t.Fatalf("log output contains payload bytes (%q):\n%s", needle, out)
		}
	}
	if !strings.Contains(out, "log-1") || !strings.Contains(out, "event=route") || !strings.Contains(out, "event=queue") {
		t.Fatalf("expected routing events in the log, got:\n%s", out)
	}
	if strings.Contains(out, "payload") {
		t.Fatalf("log mentions the payload field:\n%s", out)
	}
}

func mustJSON(e envelope.Envelope) string {
	raw, err := json.Marshal(e)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
