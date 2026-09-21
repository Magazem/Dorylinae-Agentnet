package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

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

func TestHelpAndVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"--help"}, &out, &errb); code != 0 {
		t.Fatalf("--help code = %d", code)
	}
	for _, want := range []string{"Usage:", "--listen", "--allow-non-loopback", "--verbose", envelope.ConnectPath, "never reads or logs envelope payloads"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if code := run(context.Background(), []string{"--version"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "relay ") {
		t.Fatalf("--version: code %d, out %q", code, out.String())
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"extra"}, {"--listen", "0.0.0.0:0"}, {"--listen", "example.com:80"}, {"--listen", "nocolon"}} {
		var out, errb bytes.Buffer
		if code := run(context.Background(), args, &out, &errb); code != 2 {
			t.Errorf("%v: code = %d, want 2 (stderr %q)", args, code, errb.String())
		}
	}
}

// startRelay runs the relay with args and returns its ws URL and a stop func
// that cancels it and asserts a clean exit.
func startRelay(t *testing.T, args ...string) (url string, stop func()) {
	t.Helper()
	var out, errb syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- run(ctx, append([]string{"--listen", "127.0.0.1:0"}, args...), &out, &errb) }()
	re := regexp.MustCompile(`listening on (127\.0\.0\.1:\d+)`)
	deadline := time.Now().Add(5 * time.Second)
	for url == "" {
		if m := re.FindStringSubmatch(out.String()); m != nil {
			url = "ws://" + m[1] + envelope.ConnectPath
		} else if time.Now().After(deadline) {
			cancel()
			t.Fatalf("relay never reported its address; stderr: %s", errb.String())
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case code := <-done:
			if code != 0 {
				t.Errorf("exit code %d; stderr %s", code, errb.String())
			}
		case <-time.After(10 * time.Second):
			t.Error("relay did not stop")
		}
	}
	t.Cleanup(stop)
	return url, stop
}

// authed dials url and completes the challenge as the peer with private key priv.
func authed(t *testing.T, url string, priv ed25519.PrivateKey) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c, _, err := websocket.Dial(ctx, url, nil) //nolint:bodyclose // successful WebSocket dials have no body to close
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	var ctl envelope.Control
	_, raw, err := c.Read(ctx)
	if err != nil || json.Unmarshal(raw, &ctl) != nil || ctl.Op != envelope.OpChallenge {
		t.Fatalf("challenge: %s, %v", raw, err)
	}
	nonce, err := envelope.DecodeNonce(ctl.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	a, err := envelope.SignAuth(priv.Public().(ed25519.PublicKey), nonce, relayclient.NewKeySigner(priv).Sign)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(a)
	if err := c.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
	if _, raw, err = c.Read(ctx); err != nil || !strings.Contains(string(raw), `"op":"ready"`) {
		t.Fatalf("ready: %s, %v", raw, err)
	}
	return c
}

func readEnvelopeID(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, frame, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h, err := envelope.ParseHeader(frame)
	if err != nil {
		t.Fatalf("got %s, want an envelope: %v", frame, err)
	}
	return h.ID
}

func TestQueuePersistsAcrossRestart(t *testing.T) {
	db := filepath.Join(testutil.TempDir(t), "sub", "relay-queue.db") // the relay creates the directory
	pubA, privA, _ := ed25519.GenerateKey(rand.Reader)
	pubB, privB, _ := ed25519.GenerateKey(rand.Reader)
	keyA, keyB := envelope.KeyString(pubA), envelope.KeyString(pubB)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url1, stop1 := startRelay(t, "--queue-db", db)
	ca := authed(t, url1, privA)
	want := []string{"m-1", "m-2", "m-3"}
	for _, id := range want {
		e := envelope.Envelope{From: keyA, To: keyB, Team: "t", Type: "ping", ID: id, TS: time.Now().UTC().Format(time.RFC3339Nano), Payload: []byte(`"x"`)}
		frame, err := e.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if err := ca.Write(ctx, websocket.MessageText, frame); err != nil {
			t.Fatal(err)
		}
		if _, raw, err := ca.Read(ctx); err != nil || !strings.Contains(string(raw), `"op":"queued"`) {
			t.Fatalf("queued ack for %s: %s, %v", id, raw, err)
		}
	}
	stop1()

	url2, stop2 := startRelay(t, "--queue-db", db)
	cb := authed(t, url2, privB)
	for _, id := range want {
		if got := readEnvelopeID(t, cb); got != id {
			t.Fatalf("got %s, want %s", got, id)
		}
		ack, _ := json.Marshal(envelope.Control{Op: envelope.OpAck, From: keyA, Ref: id})
		if err := cb.Write(ctx, websocket.MessageText, ack); err != nil {
			t.Fatal(err)
		}
	}
	// Let the last ack land before stopping.
	time.Sleep(200 * time.Millisecond)
	stop2()

	// Delivered exactly once: a third start has nothing left for B, so the
	// first frame B sees is the envelope it sends itself.
	url3, _ := startRelay(t, "--queue-db", db)
	cb = authed(t, url3, privB)
	self := envelope.Envelope{From: keyB, To: keyB, Team: "t", Type: "ping", ID: "self", TS: time.Now().UTC().Format(time.RFC3339Nano), Payload: []byte(`"x"`)}
	frame, err := self.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := cb.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}
	if got := readEnvelopeID(t, cb); got != "self" {
		t.Fatalf("first frame after third start = %s, want self (redelivery)", got)
	}
}

func TestQueueDBOpenFailure(t *testing.T) {
	dir := testutil.TempDir(t) // a directory is not a database file
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"--listen", "127.0.0.1:0", "--queue-db", dir}, &out, &errb); code != 1 {
		t.Fatalf("code = %d, want 1; stderr %q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "offline queue") {
		t.Errorf("stderr lacks a clear message: %q", errb.String())
	}
	if code := run(context.Background(), []string{"--queue-ttl", "0s"}, &out, &errb); code != 2 {
		t.Errorf("--queue-ttl 0s: code = %d, want 2", code)
	}
}

func TestServesUntilCancelled(t *testing.T) {
	t.Setenv("DORYLINAE_HOME", testutil.TempDir(t)) // keep the default queue database out of the real config dir
	var out, errb syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"--listen", "127.0.0.1:0"}, &out, &errb) }()

	re := regexp.MustCompile(`listening on (127\.0\.0\.1:\d+)`)
	var addr string
	deadline := time.Now().Add(5 * time.Second)
	for addr == "" {
		if m := re.FindStringSubmatch(out.String()); m != nil {
			addr = m[1]
		} else if time.Now().After(deadline) {
			t.Fatalf("relay never reported its address; stderr: %s", errb.String())
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}

	dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dcancel()
	c, _, err := websocket.Dial(dctx, "ws://"+addr+envelope.ConnectPath, nil) //nolint:bodyclose // successful WebSocket dials have no body to close
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	_, raw, err := c.Read(dctx)
	if err != nil || !strings.Contains(string(raw), `"op":"challenge"`) {
		t.Fatalf("first frame = %s, err %v", raw, err)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code %d; stderr %s", code, errb.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("relay did not stop")
	}
}
