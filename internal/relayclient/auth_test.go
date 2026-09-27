package relayclient_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
)

// tlsHost is a name the httptest TLS certificate is valid for. A test URL
// wss://example.com is not loopback, and relayclient.WithDialer sends its
// connections to the local test server.
const tlsHost = "example.com"

func TestAuthVersionRule(t *testing.T) {
	for _, tc := range []struct {
		offered  []string
		loopback bool
		want     int
	}{
		{[]string{"v1", "v2"}, true, 2},
		{[]string{"v1", "v2"}, false, 2},
		{[]string{"v2"}, false, 2},
		{nil, true, 1},            // a relay that predates the list
		{[]string{"v1"}, true, 1}, // loopback may fall back
		{nil, false, 0},           // non-loopback: never v1
		{[]string{"v1"}, false, 0},
		{[]string{"v3"}, true, 0},
		{[]string{}, false, 0},
	} {
		if got := relayclient.AuthVersion(tc.offered, tc.loopback); got != tc.want {
			t.Errorf("AuthVersion(%q, loopback=%v) = %d, want %d", tc.offered, tc.loopback, got, tc.want)
		}
	}
}

// countingSigner records how many challenges it signed.
type countingSigner struct {
	relayclient.Signer
	n atomic.Int32
}

func (s *countingSigner) Sign(msg []byte) ([]byte, error) {
	s.n.Add(1)
	return s.Signer.Sign(msg)
}

func newSigner(t *testing.T) (*countingSigner, string) {
	t.Helper()
	pub, priv := newKey(t)
	return &countingSigner{Signer: relayclient.NewKeySigner(priv)}, envelope.KeyString(pub)
}

// syncLog is a goroutine-safe log sink for a client's Logger.
type syncLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newTextLogger(l *syncLog) *slog.Logger { return slog.New(slog.NewTextHandler(l, nil)) }

// runClient runs a client for cfg until the test ends.
func runClient(t *testing.T, cfg relayclient.Config) (*relayclient.Client, *syncLog) {
	t.Helper()
	logs := &syncLog{}
	cfg.Logger = newTextLogger(logs)
	cfg.MinBackoff, cfg.MaxBackoff = 20*time.Millisecond, 50*time.Millisecond
	c, err := relayclient.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return c, logs
}

// relayAt starts a relay whose origin is the URL it is reached at: loopback
// ws:// or, with tls, wss://example.com. It is public (v2 required).
func relayAt(t *testing.T, useTLS bool) (s *relay.Server, url string, ts *httptest.Server) {
	t.Helper()
	ts = httptest.NewUnstartedServer(nil)
	origin := "ws://" + ts.Listener.Addr().String()
	if useTLS {
		origin = "wss://" + tlsHost
	}
	s = relay.New(relay.Options{Public: true, Origins: []string{origin}})
	ts.Config.Handler = s
	if useTLS {
		ts.StartTLS()
	} else {
		ts.Start()
	}
	t.Cleanup(ts.Close)
	t.Cleanup(s.Close)
	return s, origin + envelope.ConnectPath, ts
}

func certPEM(ts *httptest.Server) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.Certificate().Raw})
}

// TestV2Direct: the positive control for the tests below. The same client
// that the middle relay cannot use logs straight into the real relay.
func TestV2Direct(t *testing.T) {
	for _, useTLS := range []bool{false, true} {
		s, url, ts := relayAt(t, useTLS)
		signer, key := newSigner(t)
		cfg := relayclient.Config{URL: url, Signer: signer}
		if useTLS {
			roots, err := relayclient.LoadRoots(certPEM(ts))
			if err != nil {
				t.Fatal(err)
			}
			cfg.RootCAs = roots
			cfg = relayclient.WithDialer(cfg, ts.Listener.Addr().String())
		}
		c, _ := runClient(t, cfg)
		eventually(t, "v2 login", func() bool { return c.Connected() && s.Connected(key) })
	}
}

// mitmResult is what the hostile relay X got from one forwarded challenge.
type mitmResult struct {
	signed bool             // the daemon answered with an auth frame
	auth   envelope.Control // that frame
	reply  envelope.Control // Y's answer to it
}

// hostileRelay is relay X: it forwards the real relay Y's challenge to each
// daemon that connects (after mutate) and the daemon's answer back to Y,
// trying to log into Y as that daemon.
func hostileRelay(t *testing.T, yURL string, mutate func(*envelope.Control)) (http.Handler, <-chan mitmResult) {
	t.Helper()
	results := make(chan mitmResult, 32)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = d.CloseNow() }()
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		y, _, err := websocket.Dial(ctx, yURL, nil) //nolint:bodyclose // successful WebSocket dials have no body to close
		if err != nil {
			t.Errorf("X cannot reach Y: %v", err)
			return
		}
		defer func() { _ = y.CloseNow() }()
		ch := readCtl(ctx, y)
		mutate(&ch)
		raw, _ := json.Marshal(ch)
		if err := d.Write(ctx, websocket.MessageText, raw); err != nil {
			return
		}
		var res mitmResult
		if _, frame, err := d.Read(ctx); err == nil {
			res.signed = json.Unmarshal(frame, &res.auth) == nil && res.auth.Op == envelope.OpAuth
			if err := y.Write(ctx, websocket.MessageText, frame); err == nil {
				res.reply = readCtl(ctx, y)
			}
		}
		select {
		case results <- res:
		default:
		}
	})
	return h, results
}

func readCtl(ctx context.Context, c *websocket.Conn) envelope.Control {
	var ctl envelope.Control
	if _, raw, err := c.Read(ctx); err == nil {
		_ = json.Unmarshal(raw, &ctl)
	}
	return ctl
}

func nextResult(t *testing.T, results <-chan mitmResult) mitmResult {
	t.Helper()
	select {
	case r := <-results:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("hostile relay saw no connection")
		return mitmResult{}
	}
}

// TestRelayInTheMiddle is review 50's attack: relay X forwards relay Y's
// challenge to a daemon connected to X, then replays the answer at Y. With
// auth v2 the daemon signs X's origin, which Y refuses; with the offer
// downgraded, a loopback daemon signs v1, which a public Y refuses too.
func TestRelayInTheMiddle(t *testing.T) {
	ys, yURL, _ := relayAt(t, false)
	for _, tc := range []struct {
		name   string
		mutate func(*envelope.Control)
		want   int // auth version the daemon signs (its URL for X is loopback)
	}{
		{"challenge forwarded as is", func(*envelope.Control) {}, 2},
		{"auth list removed", func(c *envelope.Control) { c.Auth = nil }, 1},
		{"auth list set to v1", func(c *envelope.Control) { c.Auth = []string{envelope.AuthV1} }, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, results := hostileRelay(t, yURL, tc.mutate)
			x := httptest.NewServer(h)
			t.Cleanup(x.Close)
			signer, key := newSigner(t)
			runClient(t, relayclient.Config{URL: "ws" + strings.TrimPrefix(x.URL, "http"), Signer: signer})

			r := nextResult(t, results)
			wantV := 0 // a v1 frame carries no "v"
			if tc.want == 2 {
				wantV = 2
			}
			if !r.signed || r.auth.V != wantV {
				t.Fatalf("daemon answer %+v, want a v%d signature", r.auth, tc.want)
			}
			if r.reply.Op != envelope.OpError || r.reply.Code != envelope.CodeAuthFailed {
				t.Fatalf("Y answered %+v to the forwarded signature, want auth_failed", r.reply)
			}
			if ys.Connected(key) {
				t.Fatal("X logged into Y as the daemon")
			}
		})
	}
}

// TestNoDowngradeOffLoopback: a daemon whose relay URL is not loopback
// (wss://example.com) signs nothing when the challenge does not offer v2,
// and a v2 signature for X is still refused by Y.
func TestNoDowngradeOffLoopback(t *testing.T) {
	ys, yURL, _ := relayAt(t, false)
	for _, tc := range []struct {
		name   string
		mutate func(*envelope.Control)
		signs  bool
	}{
		{"challenge forwarded as is", func(*envelope.Control) {}, true},
		{"auth list removed", func(c *envelope.Control) { c.Auth = nil }, false},
		{"auth list set to v1", func(c *envelope.Control) { c.Auth = []string{envelope.AuthV1} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, results := hostileRelay(t, yURL, tc.mutate)
			x := httptest.NewTLSServer(h)
			t.Cleanup(x.Close)
			roots, err := relayclient.LoadRoots(certPEM(x))
			if err != nil {
				t.Fatal(err)
			}
			signer, key := newSigner(t)
			cfg := relayclient.WithDialer(relayclient.Config{URL: "wss://" + tlsHost, Signer: signer, RootCAs: roots}, x.Listener.Addr().String())
			_, logs := runClient(t, cfg)

			r := nextResult(t, results)
			if r.signed != tc.signs {
				t.Fatalf("daemon answered %+v; want signed=%v", r.auth, tc.signs)
			}
			if !tc.signs {
				if n := signer.n.Load(); n != 0 {
					t.Fatalf("signer used %d times against a relay without auth v2", n)
				}
				eventually(t, "the auth v2 error in the log", func() bool { return strings.Contains(logs.String(), "does not support auth v2") })
				return
			}
			if r.auth.V != 2 || r.reply.Code != envelope.CodeAuthFailed {
				t.Fatalf("answer %+v, Y reply %+v; want v2 refused by Y", r.auth, r.reply)
			}
			if ys.Connected(key) {
				t.Fatal("X logged into Y as the daemon")
			}
		})
	}
}

// TestRedirectFailsDial: a relay URL answering 301 is a connection error, so
// the origin signed is always the one dialled.
func TestRedirectFailsDial(t *testing.T) {
	ys, yURL, _ := relayAt(t, false)
	var hits atomic.Int32
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, strings.Replace(yURL, "ws://", "http://", 1), http.StatusMovedPermanently)
	}))
	t.Cleanup(redir.Close)
	signer, key := newSigner(t)
	c, logs := runClient(t, relayclient.Config{URL: "ws" + strings.TrimPrefix(redir.URL, "http"), Signer: signer})

	eventually(t, "two dial attempts", func() bool { return hits.Load() >= 2 })
	if c.Connected() || ys.Connected(key) || signer.n.Load() != 0 {
		t.Fatalf("followed the redirect: connected=%v at Y=%v signatures=%d", c.Connected(), ys.Connected(key), signer.n.Load())
	}
	if !strings.Contains(logs.String(), "redirect") {
		t.Errorf("log does not name the redirect: %s", logs.String())
	}
}

// TestRelayCA: a relay with a self-signed certificate is reachable with its
// CA added (agentnetd --relay-ca) and not with the system roots alone.
func TestRelayCA(t *testing.T) {
	s, url, ts := relayAt(t, true)

	signer, key := newSigner(t)
	c, logs := runClient(t, relayclient.WithDialer(relayclient.Config{URL: url, Signer: signer}, ts.Listener.Addr().String()))
	eventually(t, "a certificate error", func() bool { return strings.Contains(logs.String(), "certificate") })
	if c.Connected() || s.Connected(key) || signer.n.Load() != 0 {
		t.Fatal("system roots trusted the test relay")
	}

	roots, err := relayclient.LoadRoots(certPEM(ts))
	if err != nil {
		t.Fatal(err)
	}
	signer2, key2 := newSigner(t)
	c2, _ := runClient(t, relayclient.WithDialer(relayclient.Config{URL: url, Signer: signer2, RootCAs: roots}, ts.Listener.Addr().String()))
	eventually(t, "login with --relay-ca", func() bool { return c2.Connected() && s.Connected(key2) })

	if _, err := relayclient.LoadRoots([]byte("not a certificate")); err == nil {
		t.Error("LoadRoots accepted junk")
	}
}

func TestCheckURL(t *testing.T) {
	for _, tc := range []struct {
		url           string
		allowInsecure bool
		insecure, ok  bool
	}{
		{"ws://127.0.0.1:8787", false, false, true},
		{"ws://localhost", false, false, true},
		{"ws://[::1]:1", false, false, true},
		{"wss://relay.example.com", false, false, true},
		{"ws://relay.example.com", false, false, false},
		{"ws://relay.example.com", true, true, true},
		{"ftp://relay.example.com", true, false, false},
		{"wss://u@relay.example.com", false, false, false},
	} {
		insecure, err := relayclient.CheckURL(tc.url, tc.allowInsecure)
		if insecure != tc.insecure || (err == nil) != tc.ok {
			t.Errorf("CheckURL(%q, %v) = %v, %v", tc.url, tc.allowInsecure, insecure, err)
		}
	}
}

// TestLoopbackURLMustReachLoopback (review 51 M1): a URL is classed loopback
// by its text, but "localhost" or "127.0.0.1." is resolved by the hosts file
// or DNS. When the answer is another machine, the daemon must not connect:
// a loopback URL gets plain ws:// and a v1 signature, which does not name the
// relay. The relay here offers v1 only, as a hostile one would.
func TestLoopbackURLMustReachLoopback(t *testing.T) {
	x := relay.New(relay.Options{})
	ts := httptest.NewServer(x)
	t.Cleanup(ts.Close)
	t.Cleanup(x.Close)
	if offer := challengeAuth(t, "ws"+strings.TrimPrefix(ts.URL, "http")+envelope.ConnectPath); len(offer) != 1 || offer[0] != envelope.AuthV1 {
		t.Fatalf("test relay offers %q, want v1 only", offer)
	}
	_, port, _ := strings.Cut(ts.Listener.Addr().String(), ":")
	for _, host := range []string{"localhost", "127.0.0.1."} {
		t.Run(host, func(t *testing.T) {
			signer, key := newSigner(t)
			remote := &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 80}
			c, logs := runClient(t, relayclient.WithDialerAs(relayclient.Config{URL: "ws://" + host + ":" + port, Signer: signer}, ts.Listener.Addr().String(), remote))
			eventually(t, "a signature or a dial error naming the peer", func() bool {
				return signer.n.Load() > 0 || strings.Contains(logs.String(), "203.0.113.7")
			})
			if n := signer.n.Load(); n != 0 || c.Connected() || x.Connected(key) {
				t.Fatalf("signed %d challenges for a non-loopback peer (connected=%v)", n, c.Connected())
			}
		})
	}
	// Control: the same URL whose name really reaches this machine works.
	signer, key := newSigner(t)
	c, _ := runClient(t, relayclient.WithDialer(relayclient.Config{URL: "ws://localhost:" + port, Signer: signer}, ts.Listener.Addr().String()))
	eventually(t, "v1 login on a real loopback peer", func() bool { return c.Connected() && x.Connected(key) })
}

// challengeAuth returns the auth list of the relay's challenge at url.
func challengeAuth(t *testing.T, url string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, url, nil) //nolint:bodyclose // the library closes the handshake body
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.CloseNow() }()
	return readCtl(ctx, ws).Auth
}
