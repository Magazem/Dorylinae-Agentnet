package relayclient_test

// R55-F9 (review 55 R55-014, C04-01, C28-01, T5-01, C04-03): relay-supplied
// text is converted once in relayclient, and last_error and the log line carry
// no relay message, close reason or redirect target.

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// rawJSON encodes v without HTML escaping, as a hostile relay would write it.
func rawJSON(t *testing.T, v any) []byte {
	t.Helper()
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return []byte(strings.TrimSuffix(b.String(), "\n"))
}

// fakeRelay serves each connection with serve after accepting the upgrade.
func fakeRelay(t *testing.T, serve func(ctx context.Context, ws *websocket.Conn)) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		ws.SetReadLimit(envelope.MaxFrameBytes)
		serve(r.Context(), ws)
	}))
	t.Cleanup(ts.Close)
	return "ws" + strings.TrimPrefix(ts.URL, "http")
}

// acceptAuth plays the relay's side of a v1 handshake (the client is on
// loopback) and reports whether the client answered.
func acceptAuth(ctx context.Context, ws *websocket.Conn) bool {
	ch, _ := json.Marshal(envelope.Control{Op: envelope.OpChallenge, Version: 1, Nonce: envelope.EncodeNonce(make([]byte, envelope.NonceSize))})
	if ws.Write(ctx, websocket.MessageText, ch) != nil {
		return false
	}
	if _, _, err := ws.Read(ctx); err != nil {
		return false
	}
	ready, _ := json.Marshal(envelope.Control{Op: envelope.OpReady})
	return ws.Write(ctx, websocket.MessageText, ready) == nil
}

// hostileMessage is an OSC 8 link, an erase-line and ~1 000 000 '<'.
func hostileMessage() string {
	return "\x1b]8;;https://evil.example/agentnet\x07upgrade now\x1b]8;;\x07\x1b[2K\r\nrelay: connected" + strings.Repeat("<", 1000000)
}

// lastError waits for the client's first LastError.
func lastError(t *testing.T, c *relayclient.Client) string {
	t.Helper()
	eventually(t, "LastError", func() bool { return c.State().LastError != "" })
	return c.State().LastError
}

// disconnectLine returns the first relay_disconnect log line.
func disconnectLine(t *testing.T, logs *syncLog) string {
	t.Helper()
	var line string
	eventually(t, "relay_disconnect log line", func() bool {
		sc := bufio.NewScanner(strings.NewReader(logs.String()))
		sc.Buffer(nil, 4<<20)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "event=relay_disconnect") {
				line = sc.Text()
				return true
			}
		}
		return false
	})
	return line
}

// C28-01 inverted, and test 8: an error frame in place of the challenge,
// code "x" and a 1 MiB hostile message.
func TestRelayErrorInPlaceOfChallengeIsContentFree(t *testing.T) {
	frame := rawJSON(t, map[string]string{"op": "error", "code": "x", "message": hostileMessage()})
	if len(frame) > envelope.MaxFrameBytes {
		t.Fatalf("frame %d bytes over the cap", len(frame))
	}
	url := fakeRelay(t, func(ctx context.Context, ws *websocket.Conn) {
		_ = ws.Write(ctx, websocket.MessageText, frame)
		time.Sleep(200 * time.Millisecond)
	})
	_, priv := newKey(t)
	c, logs := runClient(t, relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv)})

	le := lastError(t, c)
	if le != "relay: relay_error" {
		t.Fatalf("LastError = %q, want %q", le[:min(len(le), 120)], "relay: relay_error")
	}
	var sb strings.Builder
	_ = json.NewEncoder(&sb).Encode(map[string]string{"last_error": le}) // HTML-escaping, as IPC
	if sb.Len() >= 2048 {
		t.Fatalf("last_error is %d bytes as JSON", sb.Len())
	}

	line := disconnectLine(t, logs)
	if len(line) >= 1024 || strings.ContainsRune(line, 0x1b) || strings.Contains(line, "evil.example") {
		t.Fatalf("relay_disconnect line (%d bytes) carries relay text: %.200q", len(line), line)
	}
}

// C04-01 inverted: an unknown code (with an escape) becomes relay_error, a
// known one is kept, and the message never reaches LastError.
func TestRelayErrorCodeInLastError(t *testing.T) {
	msg := "\x1b]8;;https://evil.example/\x1b\\click\x1b]8;;\x1b\\\x1b[2K\rrelay: connected" + strings.Repeat("A", 1000*1000)
	for _, tc := range []struct{ code, want string }{
		{"internal\x1b[31m", "relay: relay_error"},
		{"internal", "relay: internal"},
	} {
		frame := rawJSON(t, map[string]string{"op": "error", "code": tc.code, "message": msg})
		url := fakeRelay(t, func(ctx context.Context, ws *websocket.Conn) {
			_ = ws.Write(ctx, websocket.MessageText, frame)
			time.Sleep(100 * time.Millisecond)
		})
		_, priv := newKey(t)
		c, _ := runClient(t, relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv)})
		if le := lastError(t, c); le != tc.want || strings.ContainsRune(le, 0x1b) {
			t.Errorf("code %q: LastError = %.120q, want %q", tc.code, le, tc.want)
		}
	}
}

// Test 3: an error frame after ready reaches OnError converted.
func TestOnErrorGetsConvertedFrame(t *testing.T) {
	unit := "\x1b[31m\r\n\u202e\u200b\u3164\ufe0f\u2800a\u0301\u0302\u0303\u0304\u0305 "
	msg := strings.Repeat(unit, (1<<20-4096)/(len(unit)+32))
	bad := rawJSON(t, map[string]string{"op": "error", "code": "no_such_code", "message": msg, "ref": "bad ref!"})
	good := rawJSON(t, map[string]string{"op": "error", "code": envelope.CodePeerOffline, "message": "peer gone", "ref": "env-1"})
	if len(bad) > envelope.MaxFrameBytes {
		t.Fatalf("frame %d bytes over the cap", len(bad))
	}
	url := fakeRelay(t, func(ctx context.Context, ws *websocket.Conn) {
		if !acceptAuth(ctx, ws) {
			return
		}
		_ = ws.Write(ctx, websocket.MessageText, bad)
		_ = ws.Write(ctx, websocket.MessageText, good)
		<-ctx.Done()
	})
	got := make(chan envelope.ErrorFrame, 4)
	_, priv := newKey(t)
	runClient(t, relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv), OnError: func(e envelope.ErrorFrame) { got <- e }})

	next := func() envelope.ErrorFrame {
		select {
		case e := <-got:
			return e
		case <-time.After(10 * time.Second):
			t.Fatal("no error frame handed up")
			return envelope.ErrorFrame{}
		}
	}
	e := next()
	if e.Code != envelope.CodeRelayError || e.Ref != "" || len(e.Message) > 200 || e.Message == "" {
		t.Fatalf("converted frame = code %q ref %q message %d bytes", e.Code, e.Ref, len(e.Message))
	}
	marks := 0
	for _, r := range e.Message {
		if displaytext.Hidden(r) {
			t.Fatalf("hidden rune %U kept in %q", r, e.Message)
		}
		if unicode.Is(unicode.Mn, r) {
			if marks++; marks > 2 {
				t.Fatalf("more than 2 marks on one base: %q", e.Message)
			}
		} else {
			marks = 0
		}
	}
	if e := next(); e != (envelope.ErrorFrame{Code: envelope.CodePeerOffline, Message: "peer gone", Ref: "env-1"}) {
		t.Fatalf("known frame changed: %+v", e)
	}
}

// Test 9: nothing of an unexpected op or a close reason is echoed.
func TestHandshakeEchoesNoRelayText(t *testing.T) {
	op := rawJSON(t, map[string]string{"op": strings.Repeat("x", 1000000)})
	url := fakeRelay(t, func(ctx context.Context, ws *websocket.Conn) {
		_ = ws.Write(ctx, websocket.MessageText, op)
		time.Sleep(100 * time.Millisecond)
	})
	_, priv := newKey(t)
	c, _ := runClient(t, relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv)})
	if le := lastError(t, c); strings.Contains(le, "xxxx") || len(le) > 256 {
		t.Errorf("LastError echoes the op: %.120q (%d bytes)", le, len(le))
	}

	url = fakeRelay(t, func(_ context.Context, ws *websocket.Conn) {
		_ = ws.Close(websocket.StatusPolicyViolation, "\x1b[2K\rrelay: connected")
	})
	c, _ = runClient(t, relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv)})
	if le := lastError(t, c); le != "closed by relay (status 1008)" {
		t.Errorf("LastError = %q, want %q", le, "closed by relay (status 1008)")
	}
}

// Test 14: the redirect target is never kept.
func TestRedirectTargetIsNotKept(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example/fix-now", http.StatusFound)
	}))
	t.Cleanup(ts.Close)
	_, priv := newKey(t)
	c, logs := runClient(t, relayclient.Config{URL: "ws" + strings.TrimPrefix(ts.URL, "http"), Signer: relayclient.NewKeySigner(priv)})
	const want = "dial: the relay answered with a redirect (not followed)"
	if le := lastError(t, c); le != want {
		t.Errorf("LastError = %q, want %q", le, want)
	}
	if line := disconnectLine(t, logs); strings.Contains(line, "evil.example") {
		t.Errorf("relay_disconnect line names the target: %s", line)
	}
}

// Test 11: an envelope for another key is not handed up, and does not touch
// the seen-set; a queued type is acked, presence is not.
func TestMisroutedEnvelopeIsDropped(t *testing.T) {
	pub, priv := newKey(t)
	me := envelope.KeyString(pub)
	senderPub, _ := newKey(t)
	from := envelope.KeyString(senderPub)
	otherPub, _ := newKey(t)
	other := envelope.KeyString(otherPub)
	mk := func(to, typ, id string) []byte {
		raw, err := envelope.Envelope{From: from, To: to, Type: typ, ID: id, TS: time.Now().UTC().Format(time.RFC3339Nano), Payload: []byte("x")}.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	acks := make(chan string, 8)
	url := fakeRelay(t, func(ctx context.Context, ws *websocket.Conn) {
		if !acceptAuth(ctx, ws) {
			return
		}
		for _, f := range [][]byte{mk(other, "session.ping", "m-1"), mk(other, envelope.TypePresence, "p-1"), mk(me, "session.ping", "m-1")} {
			_ = ws.Write(ctx, websocket.MessageText, f)
		}
		for {
			_, frame, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var c envelope.Control
			if json.Unmarshal(frame, &c) == nil && c.Op == envelope.OpAck {
				acks <- c.Ref
			}
		}
	})
	var mu sync.Mutex
	var seen []string
	runClient(t, relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(priv), OnEnvelope: func(e envelope.Envelope) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, e.To+"/"+e.ID)
	}})

	for i := range 2 {
		select {
		case ref := <-acks:
			if ref != "m-1" {
				t.Fatalf("ack %d is for %q", i, ref)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("ack %d never arrived", i)
		}
	}
	select {
	case ref := <-acks:
		t.Fatalf("unexpected ack for %q (presence is never acked)", ref)
	case <-time.After(200 * time.Millisecond):
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != me+"/m-1" {
		t.Fatalf("handed up %v, want only m-1 addressed to us", seen)
	}
}
