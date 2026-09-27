package relay_test

import (
	"crypto/ed25519"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

const yOrigin = "wss://y.example"

func authFrameV2(t *testing.T, p peer, nonce []byte, origin string) []byte {
	t.Helper()
	a, err := envelope.SignAuthV2(p.priv.Public().(ed25519.PublicKey), nonce, origin, relayclient.NewKeySigner(p.priv).Sign)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(a)
	return raw
}

// challengeOffer dials url and returns the challenge's auth list.
func challengeOffer(t *testing.T, url string) []string {
	t.Helper()
	c, _, err := websocket.Dial(ctx(t), url, nil) //nolint:bodyclose // successful WebSocket dials have no body to close
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	return readControl(t, c).Auth
}

func TestAuthOffer(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts relay.Options
		want []string
	}{
		{"not public, no origin", relay.Options{}, []string{"v1"}},
		{"not public, origin", relay.Options{Origins: []string{"ws://127.0.0.1:1"}}, []string{"v1", "v2"}},
		{"public", relay.Options{Public: true, Origins: []string{yOrigin}}, []string{"v2"}},
		{"public, --allow-auth-v1", relay.Options{Public: true, AllowAuthV1: true, Origins: []string{yOrigin}}, []string{"v1", "v2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, url := start(t, tc.opts)
			if got := challengeOffer(t, url); !slices.Equal(got, tc.want) {
				t.Errorf("auth = %q, want %q", got, tc.want)
			}
		})
	}
	if _, err := relay.Open(relay.Options{Public: true}); err == nil {
		t.Error("public relay without an origin opened")
	}
	if _, err := relay.Open(relay.Options{Origins: []string{"https://y.example"}}); err == nil {
		t.Error("bad origin accepted")
	}
}

// A Phase 0-3 daemon (v1 only) against a public relay gets auth_failed with
// a message that says what to do, and is not registered.
func TestPublicRelayRefusesV1(t *testing.T) {
	var logs syncBuffer
	a := newPeer(t)
	s, url := start(t, relay.Options{Public: true, Origins: []string{yOrigin}, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	c, nonce := rawDial(t, url)
	if err := c.Write(ctx(t), websocket.MessageText, authFrame(t, a, nonce)); err != nil {
		t.Fatal(err)
	}
	ctl := readControl(t, c)
	if ctl.Op != envelope.OpError || ctl.Code != envelope.CodeAuthFailed || !strings.Contains(ctl.Message, "requires relay auth v2") {
		t.Fatalf("got %+v, want auth_failed naming auth v2", ctl)
	}
	if _, _, err := c.Read(ctx(t)); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("read after refusal: %v, want close 1008", err)
	}
	if s.Connected(a.key) {
		t.Fatal("v1 peer registered on a public relay")
	}
	// Told apart from a failed authentication (review 50 H1: an old daemon
	// must not count towards a per-prefix lockout).
	if !strings.Contains(logs.String(), "reason=auth_v1_refused") {
		t.Errorf("log does not mark the v1 refusal: %s", logs.String())
	}

	// --allow-auth-v1 lets it in.
	_, url = start(t, relay.Options{Public: true, AllowAuthV1: true, Origins: []string{yOrigin}})
	rawAuthed(t, url, a)
}

func TestAuthV2OriginBinding(t *testing.T) {
	a := newPeer(t)
	opts := relay.Options{Public: true, Origins: []string{"wss://Y.example:443", "wss://y-alt.example"}}

	for _, origin := range []string{yOrigin, "wss://y-alt.example"} {
		_, url := start(t, opts)
		c, nonce := rawDial(t, url)
		if err := c.Write(ctx(t), websocket.MessageText, authFrameV2(t, a, nonce, origin)); err != nil {
			t.Fatal(err)
		}
		if ctl := readControl(t, c); ctl.Op != envelope.OpReady {
			t.Fatalf("origin %s: got %+v, want ready", origin, ctl)
		}
	}
	for _, origin := range []string{"wss://x.example", "wss://y.example:8443", "ws://y.example"} {
		s, url := start(t, opts)
		c, nonce := rawDial(t, url)
		if err := c.Write(ctx(t), websocket.MessageText, authFrameV2(t, a, nonce, origin)); err != nil {
			t.Fatal(err)
		}
		expectRejected(t, c)
		if s.Connected(a.key) {
			t.Fatalf("signature for %s registered the peer", origin)
		}
	}
}

func TestAuthV2Rejections(t *testing.T) {
	a := newPeer(t)

	t.Run("v2 on a relay that offers only v1", func(t *testing.T) {
		_, url := start(t, relay.Options{})
		c, nonce := rawDial(t, url)
		if err := c.Write(ctx(t), websocket.MessageText, authFrameV2(t, a, nonce, "ws://127.0.0.1")); err != nil {
			t.Fatal(err)
		}
		expectRejected(t, c)
	})

	t.Run("unknown version", func(t *testing.T) {
		_, url := start(t, relay.Options{Public: true, Origins: []string{yOrigin}})
		c, nonce := rawDial(t, url)
		var f envelope.Control
		_ = json.Unmarshal(authFrameV2(t, a, nonce, yOrigin), &f)
		f.V = 3
		raw, _ := json.Marshal(f)
		if err := c.Write(ctx(t), websocket.MessageText, raw); err != nil {
			t.Fatal(err)
		}
		expectRejected(t, c)
	})

	// Replay: a v2 answer is bound to its connection's nonce, so a captured
	// answer is worthless on any other connection.
	t.Run("replayed v2 answer", func(t *testing.T) {
		s, url := start(t, relay.Options{Public: true, Origins: []string{yOrigin}})
		c1, nonce1 := rawDial(t, url)
		captured := authFrameV2(t, a, nonce1, yOrigin)
		if err := c1.Write(ctx(t), websocket.MessageText, captured); err != nil {
			t.Fatal(err)
		}
		if ctl := readControl(t, c1); ctl.Op != envelope.OpReady {
			t.Fatalf("legit auth failed: %+v", ctl)
		}
		_ = c1.CloseNow()
		waitConnected(t, s, a.key, false)

		c2, _ := rawDial(t, url)
		if err := c2.Write(ctx(t), websocket.MessageText, captured); err != nil {
			t.Fatal(err)
		}
		expectRejected(t, c2)
	})
}

func TestHealthz(t *testing.T) {
	s := relay.New(relay.Options{})
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	t.Cleanup(s.Close)

	get := func() (int, map[string]any) {
		t.Helper()
		resp, err := http.Get(ts.URL + relay.HealthPath) //nolint:noctx // test
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body
	}
	if code, body := get(); code != http.StatusOK || body["ok"] != true || body["version"] == "" || len(body) != 2 {
		t.Fatalf("healthy: %d %v", code, body)
	}
	resp, err := http.Post(ts.URL+relay.HealthPath, "text/plain", nil) //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", resp.StatusCode)
	}

	s.Close() // closes the queue database
	if code, body := get(); code != http.StatusServiceUnavailable || body["ok"] != false {
		t.Fatalf("DB closed: %d %v, want 503", code, body)
	}
}
