package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// wsConn returns a live client-side WebSocket, so a conn built around it can
// be kicked or closed without a real relay session.
func wsConn(t *testing.T) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		_, _, _ = ws.Read(r.Context())
	}))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil) //nolint:bodyclose // successful WebSocket dials have no body to close
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	return ws
}

// A drain armed before Close must be counted by Close's drain wait: Close
// returns only once the backlog has been handed to the connection, never
// with a drain goroutine still about to read the (now closed) queue. INV-5:
// drainWG.Add used to run inside the drain goroutine, so it raced with
// Close's Wait (the CI race report) and Close could miss the drain entirely.
func TestCloseWaitsForDrainArmedBeforeIt(t *testing.T) {
	for i := range 50 {
		s := New(Options{})
		const key = "recipient"
		if err := s.q.add(envelope.Header{To: key, From: "sender", ID: "m-1"}, []byte("frame")); err != nil {
			t.Fatal(err)
		}
		c := newConn(wsConn(t), key, 4, "")
		c.ctx = context.Background()
		c.draining = false

		s.arm(c)
		s.Close()
		if n := len(c.out); n != 1 {
			t.Fatalf("iteration %d: %d frames delivered before Close returned, want the 1 queued", i, n)
		}
	}
}

// Once Close has begun, arming a connection must not start a drain Close no
// longer waits for (and whose drainWG.Add would race with Close's Wait).
func TestArmAfterCloseStartsNoDrain(t *testing.T) {
	s := New(Options{})
	s.Close()
	if s.startDrain(newConn(nil, "k", 4, "")) {
		t.Fatal("startDrain started a drain on a closed server")
	}
}
