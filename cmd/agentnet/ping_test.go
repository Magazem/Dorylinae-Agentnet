package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/session"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// wsProxy sits between one daemon and the relay, records every envelope it
// forwards and can tamper with or inject frames towards the daemon.
type wsProxy struct {
	upstream string

	mu       sync.Mutex
	captured []envelope.Envelope
	tamper   func(*envelope.Envelope) bool // relay -> daemon; true if changed
	toDaemon func(context.Context, []byte) error
}

func (p *wsProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	down, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = down.CloseNow() }()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	up, _, err := websocket.Dial(ctx, p.upstream, nil) //nolint:bodyclose // coder/websocket owns the response body
	if err != nil {
		return
	}
	defer func() { _ = up.CloseNow() }()
	down.SetReadLimit(envelope.MaxFrameBytes)
	up.SetReadLimit(envelope.MaxFrameBytes)
	p.mu.Lock()
	p.toDaemon = func(ctx context.Context, f []byte) error { return down.Write(ctx, websocket.MessageText, f) }
	p.mu.Unlock()

	pipe := func(from, to *websocket.Conn, toDaemon bool) {
		defer cancel()
		for {
			typ, frame, err := from.Read(ctx)
			if err != nil {
				return
			}
			if e, err := envelope.Parse(frame); err == nil {
				p.mu.Lock()
				p.captured = append(p.captured, e)
				fn := p.tamper
				p.mu.Unlock()
				if toDaemon && fn != nil && fn(&e) {
					frame, _ = json.Marshal(e)
				}
			}
			if to.Write(ctx, typ, frame) != nil {
				return
			}
		}
	}
	go pipe(up, down, true)
	pipe(down, up, false)
}

func (p *wsProxy) inject(t *testing.T, e envelope.Envelope) {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	send := p.toDaemon
	p.mu.Unlock()
	if err := send(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
}

func (p *wsProxy) setTamper(fn func(*envelope.Envelope) bool) {
	p.mu.Lock()
	p.tamper = fn
	p.mu.Unlock()
}

// logBuf collects log output safely.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// pairNodes pairs a and b through the CLI.
func pairNodes(t *testing.T, a, b *testNode) {
	t.Helper()
	_, out, _ := cli(t, a, "pair", "--new", "--json")
	issued := decodePair(t, out)
	if issued.Code == "" {
		t.Fatalf("pair --new = %s", out)
	}
	// Key derivation and the confirmation round trip can outlast the daemon's
	// one-second wait, so the redeemer may still be pending here.
	if code, out, errs := cli(t, b, "pair", issued.Code, "--json"); code != exitOK || decodePair(t, out).State == "failed" {
		t.Fatalf("redeem: %d %s %s", code, out, errs)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(peerKeys(t, a)) == 0 || len(peerKeys(t, b)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("a side never stored the peer")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type pingOut struct {
	OK        bool     `json:"ok"`
	PingID    string   `json:"ping_id"`
	State     string   `json:"state"`
	RTT       *float64 `json:"rtt_ms"`
	Handshake bool     `json:"handshake"`
	Peer      struct {
		PublicKey string `json:"public_key"`
		Name      string `json:"name"`
	} `json:"peer"`
	Error *struct{ Code, Message string } `json:"error"`
}

func ping(t *testing.T, n *testNode, args ...string) (int, pingOut) {
	t.Helper()
	code, out, _ := cli(t, n, append([]string{"ping", "--json"}, args...)...)
	var o pingOut
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("ping stdout not JSON: %q", out)
	}
	return code, o
}

func sessionEvents(t *testing.T, n *testNode, action string) []map[string]string {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, n.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	evs, err := audit.New(st.DB()).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]string
	for _, e := range evs {
		if e.Action == action {
			var d map[string]string
			if err := json.Unmarshal(e.Detail, &d); err != nil {
				t.Fatal(err)
			}
			out = append(out, d)
		}
	}
	return out
}

func waitEvents(t *testing.T, n *testNode, action string, want int) []map[string]string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		evs := sessionEvents(t, n, action)
		if len(evs) >= want {
			return evs
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s events = %v, want %d", action, evs, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPingEncryptedThroughRelay(t *testing.T) {
	relayLog := &logBuf{}
	srv := relay.New(relay.Options{Logger: slog.New(slog.NewTextHandler(relayLog, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http")
	proxy := &wsProxy{upstream: url + envelope.ConnectPath}
	pts := httptest.NewServer(proxy)
	t.Cleanup(pts.Close)

	a := startNode(t, "alice", url)
	b := startNode(t, "bob", "ws"+strings.TrimPrefix(pts.URL, "http"))
	a.waitRelay(t)
	b.waitRelay(t)
	waitConnected(t, srv, a.key, b.key)
	pairNodes(t, a, b)

	// A pings B by name: handshake plus encrypted round trip.
	code, o := ping(t, a, "@bob")
	if code != exitOK || !o.OK || o.State != session.StateComplete || o.RTT == nil || !o.Handshake || o.Peer.PublicKey != b.key {
		t.Fatalf("ping @bob = %d %+v", code, o)
	}
	firstID := o.PingID
	// Human output, and by public key.
	if code, out, _ := cli(t, a, "ping", b.key); code != exitOK || !strings.Contains(out, "pong from @bob") {
		t.Fatalf("human ping: %d %q", code, out)
	}
	// B pings A back over the existing session.
	if code, o := ping(t, b, "@alice"); code != exitOK || o.State != session.StateComplete || o.Handshake {
		t.Fatalf("reverse ping = %d %+v", code, o)
	}
	if code, o := ping(t, a, "--status", firstID); code != exitOK || o.State != session.StateComplete {
		t.Fatalf("--status = %d %+v", code, o)
	}

	// Payloads seen in transit never contain the plaintext.
	proxy.mu.Lock()
	var data int
	for _, e := range proxy.captured {
		if !strings.HasPrefix(e.Type, "session.") && e.Type != "pair.confirm" {
			t.Errorf("non-session envelope %q in transit", e.Type)
		}
		if e.Type == session.TypeData {
			data++
		}
		for _, secret := range []string{firstID, `"ping"`, `"pong"`, `"type"`, "alice", "bob"} {
			if bytes.Contains(e.Payload, []byte(secret)) {
				t.Errorf("%s payload contains %q", e.Type, secret)
			}
		}
	}
	proxy.mu.Unlock()
	if data < 6 {
		t.Errorf("captured %d session.data envelopes, want >= 6", data)
	}
	if l := relayLog.String(); strings.Contains(l, firstID) || strings.Contains(l, "pong") || !strings.Contains(l, "session.data") {
		t.Errorf("relay log leaks plaintext or lacks routing events:\n%s", l)
	}

	// Flip one ciphertext byte of the next A -> B message: B rejects and audits it.
	var original envelope.Envelope
	proxy.setTamper(func(e *envelope.Envelope) bool {
		if e.Type != session.TypeData || original.ID != "" {
			return false
		}
		original = *e
		original.Payload = bytes.Clone(e.Payload)
		e.Payload[len(e.Payload)-3] ^= 0x40
		return true
	})
	if code, o := ping(t, a, "@bob"); code != exitOK || o.State != session.StatePending || o.PingID == "" {
		t.Fatalf("tampered ping = %d %+v (want pending)", code, o)
	}
	rej := waitEvents(t, b, session.ActionReject, 1)
	if rej[0]["reason"] != session.ReasonDecrypt || rej[0]["peer"] != a.key || rej[0]["type"] != session.TypeData {
		t.Fatalf("reject = %v", rej[0])
	}
	proxy.setTamper(nil)

	// The session keeps working for valid messages.
	if code, o := ping(t, a, "@bob"); code != exitOK || o.State != session.StateComplete || o.Handshake {
		t.Fatalf("ping after tamper = %d %+v", code, o)
	}

	// Replaying an envelope B already accepted, and the untampered original, are rejected.
	proxy.mu.Lock()
	var accepted envelope.Envelope
	for _, e := range proxy.captured {
		if e.Type == session.TypeData && e.To == b.key {
			accepted = e
		}
	}
	proxy.mu.Unlock()
	// Same envelope id would be dropped earlier by the relay client's duplicate
	// suppression (ticket 0.7); a fresh id gets the replay to the session layer.
	accepted.ID, original.ID = "replay-accepted", "replay-original"
	proxy.inject(t, accepted)
	proxy.inject(t, original)
	rej = waitEvents(t, b, session.ActionReject, 3)
	for _, d := range rej[1:] {
		if d["reason"] != session.ReasonReplay {
			t.Fatalf("replay reject = %v", d)
		}
	}
	if code, o := ping(t, a, "@bob"); code != exitOK || o.State != session.StateComplete {
		t.Fatalf("ping after replay = %d %+v", code, o)
	}
	if n := len(sessionEvents(t, a, session.ActionOpen)); n != 1 {
		t.Errorf("alice opened %d sessions, want 1", n)
	}
	for _, d := range sessionEvents(t, b, session.ActionReject) {
		if len(d) > 4 {
			t.Errorf("reject detail has unexpected fields: %v", d)
		}
	}

	// A handshake from a key B never paired with is refused without an answer.
	_, mpriv, _ := ed25519.GenerateKey(rand.Reader)
	mkey := envelope.KeyString(mpriv.Public().(ed25519.PublicKey))
	got := make(chan string, 4)
	mc, err := relayclient.New(relayclient.Config{URL: url, Signer: relayclient.NewKeySigner(mpriv),
		OnEnvelope: func(e envelope.Envelope) { got <- e.Type }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = mc.Run(ctx) }()
	t.Cleanup(func() { cancel(); wg.Wait() })
	waitConnected(t, srv, mkey)
	for !mc.Connected() {
		time.Sleep(10 * time.Millisecond)
	}
	init := append(make([]byte, session.SIDSize), make([]byte, 32)...) // sid + a Noise msg1-sized ephemeral
	_, _ = rand.Read(init)
	env := envelope.Envelope{From: mkey, To: b.key, Type: session.TypeInit, ID: "m1", TS: time.Now().UTC().Format(time.RFC3339Nano), Payload: init}
	if err := mc.Send(ctx, env); err != nil {
		t.Fatal(err)
	}
	rej = waitEvents(t, b, session.ActionReject, 4)
	if d := rej[3]; d["reason"] != session.ReasonUnpaired || d["peer"] != mkey || d["type"] != session.TypeInit {
		t.Fatalf("unpaired reject = %v", d)
	}
	select {
	case typ := <-got:
		t.Fatalf("B answered the unpaired key with %s", typ)
	case <-time.After(300 * time.Millisecond):
	}
	if n := len(sessionEvents(t, b, session.ActionOpen)); n != 1 {
		t.Errorf("bob opened %d sessions, want 1", n)
	}
}

func TestPingUsageAndErrors(t *testing.T) {
	n := startNode(t, "solo", "")
	for _, args := range [][]string{{"ping"}, {"ping", "a", "b"}, {"ping", "--status"}, {"ping", "--status", "x", "@a"}, {"ping", "--bogus"}} {
		if code, _, _ := cli(t, n, args...); code != exitUsage {
			t.Errorf("%v: code %d, want usage", args, code)
		}
	}
	for args, want := range map[string]string{
		"@nobody":             "unknown_peer",
		"--status=ping-00000": "unknown_ping",
	} {
		code, out, _ := cli(t, n, "ping", args, "--json")
		if code != exitError || !strings.Contains(out, want) {
			t.Errorf("ping %s: %d %s", args, code, out)
		}
	}
	var o, eb bytes.Buffer
	if c := run([]string{"ping", "--help"}, &o, &eb); c != exitOK || !strings.Contains(o.String(), "--json") || !strings.Contains(o.String(), "rtt_ms") || !strings.Contains(o.String(), "Exit codes") {
		t.Errorf("ping --help: %d %q", c, o.String())
	}
}
