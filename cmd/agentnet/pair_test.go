package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// testNode is one running daemon; the CLI is pointed at it by switching
// DORYLINAE_HOME, so tests using it must not run in parallel.
type testNode struct {
	p         paths.Paths
	key       string
	connected chan struct{} // closed when the daemon logs that its relay connection is up
}

// connectWatcher closes ch when the daemon logs relay_connect.
type connectWatcher struct {
	once sync.Once
	ch   chan struct{}
}

func (w *connectWatcher) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "relay_connect") {
		w.once.Do(func() { close(w.ch) })
	}
	return len(p), nil
}

func (n *testNode) waitRelay(t *testing.T) {
	t.Helper()
	select {
	case <-n.connected:
	case <-time.After(10 * time.Second):
		t.Fatal("daemon never connected to the relay")
	}
}

func startNode(t *testing.T, name, relayURL string) *testNode {
	t.Helper()
	dir, err := os.MkdirTemp("", "dn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p, err := paths.In(dir)
	if err != nil {
		t.Fatal(err)
	}
	ks, err := identity.NewKeystore(p.Dir, "file") // never touch the real keychain from tests
	if err != nil {
		t.Fatal(err)
	}
	w := &connectWatcher{ch: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- daemon.RunWithOptions(ctx, p, ready, daemon.Options{
			Logger:   slog.New(slog.NewTextHandler(w, nil)),
			Keystore: ks,
			Identity: &identity.Options{Name: name, Harness: "test-harness"},
			RelayURL: relayURL,
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon %s exited: %v", name, err)
	case <-time.After(10 * time.Second):
		t.Fatalf("daemon %s not ready", name)
	}
	var id daemon.IdentityResult
	cctx, ccancel := context.WithTimeout(ctx, 2*time.Second)
	defer ccancel()
	if err := ipc.Call(cctx, p.Endpoint, "identity", nil, &id); err != nil {
		t.Fatal(err)
	}
	return &testNode{p: p, key: id.Card.PublicKey, connected: w.ch}
}

func waitConnected(t *testing.T, srv *relay.Server, keys ...string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for _, k := range keys {
		for !srv.Connected(k) {
			if time.Now().After(deadline) {
				t.Fatal("daemon never connected to the relay")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// cli runs the CLI against n and checks the 2 s rule.
func cli(t *testing.T, n *testNode, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	t.Setenv(paths.HomeEnv, n.p.Dir)
	var out, errb bytes.Buffer
	start := time.Now()
	code = run(args, &out, &errb)
	if d := time.Since(start); d >= 2*time.Second {
		t.Errorf("agentnet %s took %v, want < 2s", strings.Join(args, " "), d)
	}
	return code, out.String(), errb.String()
}

type pairOut struct {
	OK        bool   `json:"ok"`
	PairingID string `json:"pairing_id"`
	Role      string `json:"role"`
	State     string `json:"state"`
	Code      string `json:"code"`
	Expires   string `json:"expires"`
	Peer      *struct {
		PublicKey string `json:"public_key"`
		Name      string `json:"name"`
		Harness   string `json:"harness"`
		PairedAt  string `json:"paired_at"`
	} `json:"peer"`
	Error *struct{ Code, Message string } `json:"error"`
}

func decodePair(t *testing.T, stdout string) pairOut {
	t.Helper()
	var o pairOut
	if err := json.Unmarshal([]byte(stdout), &o); err != nil {
		t.Fatalf("stdout not JSON: %q: %v", stdout, err)
	}
	return o
}

func peerKeys(t *testing.T, n *testNode) map[string]string {
	t.Helper()
	code, out, errs := cli(t, n, "peers", "--json")
	if code != exitOK {
		t.Fatalf("peers --json: code %d: %s", code, errs)
	}
	var body struct {
		OK    bool `json:"ok"`
		Peers []struct {
			PublicKey string `json:"public_key"`
			Name      string `json:"name"`
			Harness   string `json:"harness"`
			PairedAt  string `json:"paired_at"`
		} `json:"peers"`
	}
	if err := json.Unmarshal([]byte(out), &body); err != nil || !body.OK || body.Peers == nil {
		t.Fatalf("peers --json output %q: %v", out, err)
	}
	m := map[string]string{}
	for _, p := range body.Peers {
		if p.Harness != "test-harness" || p.PairedAt == "" {
			t.Errorf("peer %+v missing fields", p)
		}
		m[p.PublicKey] = p.Name
	}
	return m
}

func auditActions(t *testing.T, n *testNode, forbid ...string) []string {
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
	var out []string
	for _, e := range evs {
		if strings.HasPrefix(e.Action, "pair.") {
			for _, f := range forbid {
				if strings.Contains(string(e.Detail), f) {
					t.Errorf("audit detail leaks %q: %s", f, e.Detail)
				}
			}
			out = append(out, e.Action)
		}
	}
	return out
}

func TestPairTwoDaemons(t *testing.T) {
	srv := relay.New(relay.Options{})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http")
	a := startNode(t, "alice", url)
	b := startNode(t, "bob", url)
	a.waitRelay(t)
	b.waitRelay(t)

	// A issues a code.
	code, out, errs := cli(t, a, "pair", "--new", "--json")
	if code != exitOK {
		t.Fatalf("pair --new: code %d: %s %s", code, out, errs)
	}
	issued := decodePair(t, out)
	if !issued.OK || issued.State != "pending" || issued.Role != "issuer" || issued.PairingID == "" {
		t.Fatalf("pair --new = %+v", issued)
	}
	norm, ok := envelope.NormalizePairCode(issued.Code)
	if !ok || issued.Expires == "" {
		t.Fatalf("no code in %+v", issued)
	}
	_, human, _ := cli(t, a, "pair", "--status", issued.PairingID)
	if !strings.Contains(human, issued.PairingID) || !strings.Contains(human, issued.Code[:5]) {
		t.Errorf("human status = %q", human)
	}

	// B redeems it (lower case, split by a space: input is normalised).
	typed := strings.ToLower(norm[:5] + " " + norm[5:])
	code, out, errs = cli(t, b, "pair", typed, "--json")
	if code != exitOK {
		t.Fatalf("pair <code>: code %d: %s %s", code, out, errs)
	}
	red := decodePair(t, out)
	if !red.OK || red.State != "complete" || red.Peer == nil || red.Peer.PublicKey != a.key || red.Peer.Name != "alice" {
		t.Fatalf("redeem result = %+v", red)
	}
	if red.Code != "" {
		t.Errorf("finished pairing still carries a code")
	}

	// A's side completes and both list the peer.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, out, _ = cli(t, a, "pair", "--status", issued.PairingID, "--json")
		if decodePair(t, out).State == "complete" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("issuer never completed: %s", out)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := peerKeys(t, a); len(got) != 1 || got[b.key] != "bob" {
		t.Fatalf("alice peers = %v", got)
	}
	if got := peerKeys(t, b); len(got) != 1 || got[a.key] != "alice" {
		t.Fatalf("bob peers = %v", got)
	}

	// The same code fails a second time, from the same and from another machine.
	c := startNode(t, "carol", url)
	c.waitRelay(t)
	for _, n := range []*testNode{b, c} {
		code, out, _ = cli(t, n, "pair", issued.Code, "--json")
		if code != exitError {
			t.Fatalf("second redemption: code %d, out %s", code, out)
		}
		f := decodePair(t, out)
		if f.OK || f.State != "failed" || f.Error == nil || f.Error.Code != envelope.CodePairInvalid {
			t.Fatalf("second redemption = %+v", f)
		}
	}
	if got := peerKeys(t, c); len(got) != 0 {
		t.Fatalf("carol peers = %v, want none", got)
	}

	wantA := []string{"pair.start", "pair.complete"}
	if got := auditActions(t, a, issued.Code, "card"); strings.Join(got, ",") != strings.Join(wantA, ",") {
		t.Errorf("alice audit = %v, want %v", got, wantA)
	}
	wantB := []string{"pair.start", "pair.complete", "pair.start", "pair.fail"}
	if got := auditActions(t, b, issued.Code, "card"); strings.Join(got, ",") != strings.Join(wantB, ",") {
		t.Errorf("bob audit = %v, want %v", got, wantB)
	}
}

func TestPairBadCardRejected(t *testing.T) {
	srv := relay.New(relay.Options{})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http")
	b := startNode(t, "bob", url)
	b.waitRelay(t)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	card, err := agentcard.New(pub, "mallory", "evil", nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	signed, err := agentcard.Sign(priv, card)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*agentcard.Signed){
		"tampered after signing": func(s *agentcard.Signed) { s.Card.Name = "admin" },
		"garbage signature":      func(s *agentcard.Signed) { s.Signature = strings.Repeat("A", 86) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			bad := signed
			mutate(&bad)
			raw, err := json.Marshal(bad)
			if err != nil {
				t.Fatal(err)
			}
			code := issueFrom(t, srv, url, priv, raw)

			c, out, errs := cli(t, b, "pair", code, "--json")
			if c != exitError {
				t.Fatalf("code %d, out %s, err %s", c, out, errs)
			}
			o := decodePair(t, out)
			if o.OK || o.State != "failed" || o.Error == nil || o.Error.Code != "bad_card" {
				t.Fatalf("result = %+v", o)
			}
			if got := peerKeys(t, b); len(got) != 0 {
				t.Fatalf("a peer was stored despite the bad card: %v", got)
			}
		})
	}
	acts := auditActions(t, b)
	if strings.Join(acts, ",") != "pair.start,pair.fail,pair.start,pair.fail" {
		t.Errorf("audit = %v", acts)
	}
}

// issueFrom connects a raw relay client with priv and has it issue a code
// whose card is raw, returning the code.
func issueFrom(t *testing.T, srv *relay.Server, relayURL string, priv ed25519.PrivateKey, raw json.RawMessage) string {
	t.Helper()
	codes := make(chan string, 1)
	client, err := relayclient.New(relayclient.Config{
		URL:    relayURL,
		Signer: relayclient.NewKeySigner(priv),
		OnControl: func(c envelope.Control) {
			if c.Op == envelope.OpPairCode {
				codes <- c.Code
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = client.Run(ctx) }()
	t.Cleanup(func() { cancel(); wg.Wait() })
	waitConnected(t, srv, envelope.KeyString(priv.Public().(ed25519.PublicKey)))
	deadline := time.Now().Add(10 * time.Second)
	for !client.Connected() {
		if time.Now().After(deadline) {
			t.Fatal("raw client never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := client.SendControl(ctx, envelope.Control{Op: envelope.OpPairNew, Card: raw, Ref: "x"}); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-codes:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no pair_code")
		return ""
	}
}

func TestPairNoRelayAndUsage(t *testing.T) {
	n := startNode(t, "solo", "")

	code, out, _ := cli(t, n, "pair", "--new", "--json")
	if code != exitError {
		t.Fatalf("code %d", code)
	}
	var e struct {
		OK    bool
		Error struct{ Code string }
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil || e.OK || e.Error.Code != daemon.CodeNoRelay {
		t.Fatalf("out = %q (%v)", out, err)
	}

	for _, args := range [][]string{
		{"pair"}, {"pair", "--new", "ABCDE"}, {"pair", "a", "b"}, {"pair", "--status"}, {"peers", "extra"}, {"pair", "--bogus"},
	} {
		if code, _, _ := cli(t, n, args...); code != exitUsage {
			t.Errorf("%v: code %d, want usage", args, code)
		}
	}
	code, out, _ = cli(t, n, "pair", "not-a-code", "--json")
	if code != exitError || !strings.Contains(out, daemon.CodeBadCode) {
		t.Errorf("malformed code: code %d out %s", code, out)
	}
	code, out, _ = cli(t, n, "pair", "--status", "pair-unknown", "--json")
	if code != exitError || !strings.Contains(out, daemon.CodeUnknownPairing) {
		t.Errorf("unknown id: code %d out %s", code, out)
	}
	for _, sub := range []string{"pair", "peers"} {
		var o, eb bytes.Buffer
		if c := run([]string{sub, "--help"}, &o, &eb); c != exitOK || !strings.Contains(o.String(), "--json") || !strings.Contains(o.String(), "Exit codes") {
			t.Errorf("%s --help: code %d out %q", sub, c, o.String())
		}
	}
	code, out, _ = cli(t, n, "peers")
	if code != exitOK || !strings.Contains(out, "No peers") {
		t.Errorf("empty peers: %d %q", code, out)
	}
}

func TestPairRelayDown(t *testing.T) {
	// Nothing listens on this port, so the daemon never connects.
	n := startNode(t, "orphan", "ws://127.0.0.1:1")
	start := time.Now()
	code, out, _ := cli(t, n, "pair", "--new", "--json")
	if code != exitError || !strings.Contains(out, daemon.CodeRelayUnavailable) {
		t.Fatalf("code %d out %s", code, out)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("too slow")
	}
	if got := auditActions(t, n); strings.Join(got, ",") != "pair.start,pair.fail" {
		t.Errorf("audit = %v", got)
	}
}
