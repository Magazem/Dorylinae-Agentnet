package main

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
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

func TestServesUntilCancelled(t *testing.T) {
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
