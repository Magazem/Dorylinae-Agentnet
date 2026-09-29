package relay_test

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Abuse limits, Docs/protocol/relay-hosted.md §2 (ticket 4.0b): one test
// per limit table row. Each triggers the limit, checks the error code or
// HTTP status, checks that other keys or prefixes are unaffected, and checks
// the event=limit log line names the limit and a truncated key or prefix
// and holds no payload.

const (
	limOrigin   = "wss://relay.test"
	limIPHeader = "X-Test-Client-IP"
	// limMarker is every test payload; it must never reach a log.
	limMarker = "the-limit-test-payload-must-never-be-logged"
)

type limitEnv struct {
	t     *testing.T
	s     *relay.Server
	url   string
	logs  *syncBuffer
	clock *fakeClock
	// client dials the relay; nil is the default client.
	client *http.Client
}

// newLimitEnv starts a public relay that trusts 127.0.0.1's limIPHeader, so
// a test can connect from any client prefix. Unset options keep the spec
// defaults.
func newLimitEnv(t *testing.T, opts relay.Options) *limitEnv {
	t.Helper()
	return newLimitEnvWith(t, opts, nil)
}

// newLimitEnvWith is newLimitEnv serving the relay with serve, which
// returns its URL; nil serves it as start does.
func newLimitEnvWith(t *testing.T, opts relay.Options, serve func(*relay.Server) string) *limitEnv {
	t.Helper()
	e := &limitEnv{t: t, logs: &syncBuffer{}, clock: newClock()}
	opts.Public = true
	opts.Origins = []string{limOrigin}
	if opts.ClientIPHeader == "" {
		opts.ClientIPHeader = limIPHeader
	}
	if opts.TrustedProxies == nil {
		opts.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	}
	if opts.Now == nil {
		opts.Now = e.clock.Now
	}
	opts.Logger = slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if serve == nil {
		e.s, e.url = start(t, opts)
		return e
	}
	e.s = relay.New(opts)
	t.Cleanup(e.s.Close)
	e.url = serve(e.s)
	return e
}

// dialHeader opens a WebSocket with the given extra header. It returns nil
// and the HTTP status when the relay refuses the upgrade.
func (e *limitEnv) dialHeader(h http.Header) (*websocket.Conn, int) {
	e.t.Helper()
	c, resp, err := websocket.Dial(ctx(e.t), e.url, &websocket.DialOptions{HTTPHeader: h, HTTPClient: e.client}) //nolint:bodyclose // closed below when present
	if err != nil {
		if resp == nil {
			e.t.Fatalf("dial: %v", err)
		}
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, resp.StatusCode
	}
	e.t.Cleanup(func() { _ = c.CloseNow() })
	return c, http.StatusSwitchingProtocols
}

// dial opens a WebSocket from client IP ip.
func (e *limitEnv) dial(ip string) (*websocket.Conn, int) {
	e.t.Helper()
	h := http.Header{}
	h.Set(limIPHeader, ip)
	return e.dialHeader(h)
}

// auth dials from ip and answers the challenge as p with auth v2. It returns
// the connection and the relay's reply (ready or an error).
func (e *limitEnv) auth(p peer, ip string) (*websocket.Conn, envelope.Control) {
	e.t.Helper()
	c, status := e.dial(ip)
	if c == nil {
		e.t.Fatalf("dial from %s: HTTP %d", ip, status)
	}
	ch := readControl(e.t, c)
	nonce, err := envelope.DecodeNonce(ch.Nonce)
	if err != nil {
		e.t.Fatal(err)
	}
	writeFrame(e.t, c, authFrameV2(e.t, p, nonce, limOrigin))
	return c, readControl(e.t, c)
}

// authed is auth that must succeed.
func (e *limitEnv) authed(p peer, ip string) *websocket.Conn {
	e.t.Helper()
	c, reply := e.auth(p, ip)
	if reply.Op != envelope.OpReady {
		e.t.Fatalf("auth of %s from %s: %+v, want ready", p.key[:8], ip, reply)
	}
	return c
}

// send writes an envelope from p to `to` and returns the relay's reply
// (queued or an error); `to` must be offline, so every envelope is answered.
func (e *limitEnv) send(c *websocket.Conn, p peer, to, id string, payload []byte) envelope.Control {
	e.t.Helper()
	writeFrame(e.t, c, []byte(mustJSON(p.env(to, id, payload))))
	return readControl(e.t, c)
}

// count reports how many event=limit lines for limit name subject.
func (e *limitEnv) count(limit, subject string) int {
	n := 0
	for _, line := range strings.Split(e.logs.String(), "\n") {
		if strings.Contains(line, "event=limit") && strings.Contains(line, "limit="+limit+" ") && strings.Contains(line, subject) {
			n++
		}
	}
	return n
}

// logged asserts an event=limit line for limit naming subject exists, and
// that the log holds no payload (literal or base64) and no limit line an id.
func (e *limitEnv) logged(limit, subject string) {
	e.t.Helper()
	logs := e.logs.String()
	found := false
	for _, line := range strings.Split(logs, "\n") {
		if !strings.Contains(line, "event=limit") {
			continue
		}
		if strings.Contains(line, " id=") || strings.Contains(line, "payload") {
			e.t.Errorf("limit line carries an id or payload: %s", line)
		}
		if strings.Contains(line, "limit="+limit+" ") && strings.Contains(line, subject) {
			found = true
		}
	}
	if !found {
		e.t.Errorf("no event=limit line for %s with %s in:\n%s", limit, subject, logs)
	}
	if strings.Contains(logs, limMarker) || strings.Contains(logs, base64.StdEncoding.EncodeToString([]byte(limMarker))[:40]) {
		e.t.Error("a payload reached the log")
	}
}

func expectCode(t *testing.T, got envelope.Control, code, ref string) {
	t.Helper()
	if got.Op != envelope.OpError || got.Code != code || got.Ref != ref {
		t.Fatalf("got %+v, want error %s ref %q", got, code, ref)
	}
}

func expectQueued(t *testing.T, got envelope.Control, ref string) {
	t.Helper()
	if got.Op != envelope.OpQueued || got.Ref != ref {
		t.Fatalf("got %+v, want queued ref %q", got, ref)
	}
}

// expectClose reads until the relay closes c and checks the close status.
func expectClose(t *testing.T, c *websocket.Conn, want websocket.StatusCode) {
	t.Helper()
	for {
		_, _, err := c.Read(ctx(t))
		if err != nil {
			if got := websocket.CloseStatus(err); got != want {
				t.Fatalf("closed with %v (%v), want %v", got, err, want)
			}
			return
		}
	}
}

func payload(n int) []byte {
	p := []byte(strings.Repeat(limMarker, n/len(limMarker)+1))
	return p[:n]
}

// Per network source.

func TestLimitUpgradesPerPrefix(t *testing.T) {
	e := newLimitEnv(t, relay.Options{})
	for i := range 60 { // burst 60
		c, status := e.dial("10.0.1.5")
		if c == nil {
			t.Fatalf("upgrade %d refused: HTTP %d", i+1, status)
		}
		_ = c.CloseNow()
	}
	if c, status := e.dial("10.0.1.77"); c != nil || status != http.StatusTooManyRequests {
		t.Fatalf("61st upgrade from the prefix: HTTP %d, want 429", status)
	}
	if c, status := e.dial("10.0.2.5"); c == nil {
		t.Fatalf("another prefix: HTTP %d, want the upgrade", status)
	}
	e.clock.Advance(2 * time.Second) // 30/min: one more upgrade
	if c, status := e.dial("10.0.1.5"); c == nil {
		t.Fatalf("after 2 s: HTTP %d, want the upgrade", status)
	}
	if _, status := e.dial("10.0.1.5"); status != http.StatusTooManyRequests {
		t.Fatalf("second after 2 s: HTTP %d, want 429", status)
	}
	e.logged("upgrades_per_prefix", "prefix=10.0.1.0")
}

func TestLimitConcurrentConnsPerPrefix(t *testing.T) {
	e := newLimitEnv(t, relay.Options{UpgradeBurst: 1000, ChallengeTTL: time.Minute})
	var held []*websocket.Conn
	for i := range 64 {
		c, status := e.dial(fmt.Sprintf("10.0.1.%d", i+1))
		if c == nil {
			t.Fatalf("connection %d refused: HTTP %d", i+1, status)
		}
		held = append(held, c)
	}
	if _, status := e.dial("10.0.1.200"); status != http.StatusTooManyRequests {
		t.Fatalf("65th concurrent connection: HTTP %d, want 429", status)
	}
	if c, status := e.dial("10.0.2.1"); c == nil {
		t.Fatalf("another prefix: HTTP %d", status)
	}
	_ = held[0].CloseNow()
	deadline := time.Now().Add(wait)
	for {
		c, _ := e.dial("10.0.1.201")
		if c != nil {
			break // the closed connection freed its slot
		}
		if time.Now().After(deadline) {
			t.Fatal("a closed connection never freed its per-prefix slot")
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.logged("conns_per_prefix", "prefix=10.0.1.0")
}

func TestLimitFailedAuthsPerPrefix(t *testing.T) {
	e := newLimitEnv(t, relay.Options{})
	p := newPeer(t)
	for range 10 {
		c, _ := e.dial("10.0.1.7")
		readControl(t, c)
		writeFrame(t, c, authFrameV2(t, p, make([]byte, envelope.NonceSize), limOrigin)) // signs the wrong nonce
		expectRejected(t, c)
	}
	if _, status := e.dial("10.0.1.8"); status != http.StatusTooManyRequests {
		t.Fatalf("after 10 failures: HTTP %d, want 429", status)
	}
	e.authed(p, "10.0.2.7") // another prefix, and the key itself is not blocked

	// A valid v1 signature on this v2-only relay is refused but not counted.
	for range 15 {
		c, nonce := func() (*websocket.Conn, []byte) {
			c, _ := e.dial("10.0.3.7")
			ch := readControl(t, c)
			n, _ := envelope.DecodeNonce(ch.Nonce)
			return c, n
		}()
		writeFrame(t, c, authFrame(t, p, nonce))
		expectRejected(t, c)
	}
	e.authed(p, "10.0.3.7")

	e.clock.Advance(10 * time.Minute) // the window ends
	e.authed(p, "10.0.1.8")
	e.logged("auth_failures_per_prefix", "prefix=10.0.1.0")
}

func TestLimitUnauthenticatedConnsRelayWide(t *testing.T) {
	e := newLimitEnv(t, relay.Options{ChallengeTTL: time.Minute})
	a := newPeer(t)
	ca := e.authed(a, "10.1.0.1")
	var held []*websocket.Conn
	for i := range 256 {
		c, status := e.dial(fmt.Sprintf("10.0.%d.%d", i/32, i%32+1)) // 8 prefixes of 32
		if c == nil {
			t.Fatalf("unauthenticated connection %d refused: HTTP %d", i+1, status)
		}
		held = append(held, c)
	}
	if _, status := e.dial("10.9.9.9"); status != http.StatusServiceUnavailable {
		t.Fatalf("257th unauthenticated connection: HTTP %d, want 503", status)
	}
	stillAlive(t, ca, a) // authenticated connections are unaffected
	_ = held[0].CloseNow()
	deadline := time.Now().Add(wait)
	for {
		if c, _ := e.dial("10.9.9.9"); c != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a closed connection never freed its unauthenticated slot")
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.logged("unauth_conns", "prefix=10.9.9.0")
}

func TestLimitMaxConnsRelayFull(t *testing.T) {
	e := newLimitEnv(t, relay.Options{MaxConns: 3})
	a, b, c, d := newPeer(t), newPeer(t), newPeer(t), newPeer(t)
	e.authed(a, "10.0.1.1")
	cb := e.authed(b, "10.0.2.1")
	e.authed(c, "10.0.3.1")
	cd, reply := e.auth(d, "10.0.4.1")
	expectCode(t, reply, envelope.CodeRelayFull, "")
	expectClose(t, cd, websocket.StatusTryAgainLater)
	stillAlive(t, cb, b)
	ca := e.authed(a, "10.0.1.1") // a reconnect replaces, so it is admitted at the cap
	stillAlive(t, ca, a)
	e.logged("max_conns", "relay=all")
}

func TestLimitDistinctKeysPerPrefix(t *testing.T) {
	// The per-prefix connection cap (64) would refuse the 65th key over HTTP
	// first; raise it to reach this row.
	e := newLimitEnv(t, relay.Options{UpgradeBurst: 1000, MaxConnsPerPrefix: 1000})
	var keys []peer
	for i := range 64 {
		p := newPeer(t)
		keys = append(keys, p)
		e.authed(p, fmt.Sprintf("10.0.1.%d", i+1))
	}
	extra := newPeer(t)
	c, reply := e.auth(extra, "10.0.1.100")
	expectCode(t, reply, envelope.CodeRelayFull, "")
	expectClose(t, c, websocket.StatusTryAgainLater)
	e.authed(extra, "10.0.2.1")           // another prefix
	ck := e.authed(keys[0], "10.0.1.200") // a connected key reconnecting is not a new key
	stillAlive(t, ck, keys[0])
	e.logged("keys_per_prefix", "prefix=10.0.1.0")
}

func TestLimitPrefixEnvelopesPerMinute(t *testing.T) {
	e := newLimitEnv(t, relay.Options{KeyEnvelopesPerMinute: -1, QueueMaxEnvelopes: 10000, QueuePairMaxEnvelopes: -1, QueueSenderMaxEnvelopes: -1})
	a1, a2, b, victim := newPeer(t), newPeer(t), newPeer(t), newPeer(t)
	c1, c2 := e.authed(a1, "10.0.1.1"), e.authed(a2, "10.0.1.2")
	for i := range 300 {
		expectQueued(t, e.send(c1, a1, victim.key, fmt.Sprintf("a1-%d", i), []byte(limMarker)), fmt.Sprintf("a1-%d", i))
		expectQueued(t, e.send(c2, a2, victim.key, fmt.Sprintf("a2-%d", i), []byte(limMarker)), fmt.Sprintf("a2-%d", i))
	}
	expectCode(t, e.send(c2, a2, victim.key, "a2-over", []byte(limMarker)), envelope.CodeRateLimited, "a2-over")
	expectCode(t, e.send(c1, a1, victim.key, "a1-over", []byte(limMarker)), envelope.CodeRateLimited, "a1-over")
	cb := e.authed(b, "10.0.2.1")
	expectQueued(t, e.send(cb, b, victim.key, "b-1", []byte(limMarker)), "b-1")
	e.clock.Advance(time.Second) // 600/min: 10 more
	expectQueued(t, e.send(c1, a1, victim.key, "a1-later", []byte(limMarker)), "a1-later")
	e.logged("prefix_envelopes", "prefix=10.0.1.0")
}

func TestLimitPrefixBytesPerMinute(t *testing.T) {
	// The default (64 MiB/min) is checked in TestLimitDefaults; a small value
	// keeps this test fast.
	e := newLimitEnv(t, relay.Options{PrefixBytesPerMinute: 100 << 10, KeyBytesPerMinute: -1})
	a1, a2, b, victim := newPeer(t), newPeer(t), newPeer(t), newPeer(t)
	c1, c2 := e.authed(a1, "10.0.1.1"), e.authed(a2, "10.0.1.2")
	big := payload(30 << 10) // ~40 KiB frames
	expectQueued(t, e.send(c1, a1, victim.key, "a1-1", big), "a1-1")
	expectQueued(t, e.send(c2, a2, victim.key, "a2-1", big), "a2-1")
	expectCode(t, e.send(c2, a2, victim.key, "a2-2", big), envelope.CodeRateLimited, "") // refused unparsed (R55-035)
	cb := e.authed(b, "10.0.2.1")
	expectQueued(t, e.send(cb, b, victim.key, "b-1", big), "b-1")
	e.logged("prefix_bytes", "prefix=10.0.1.0")
}

func TestLimitKeyEnvelopesPerMinute(t *testing.T) {
	e := newLimitEnv(t, relay.Options{})
	a, a2, victim := newPeer(t), newPeer(t), newPeer(t)
	ca := e.authed(a, "10.0.1.1")
	for i := range 240 { // burst 240
		expectQueued(t, e.send(ca, a, victim.key, fmt.Sprintf("k-%d", i), []byte(limMarker)), fmt.Sprintf("k-%d", i))
	}
	expectCode(t, e.send(ca, a, victim.key, "k-over", []byte(limMarker)), envelope.CodeRateLimited, "k-over")
	n, err := e.s.Queued(victim.key)
	if err != nil || n != 240 {
		t.Fatalf("queued %d, %v; the refused envelope must be dropped", n, err)
	}
	c2 := e.authed(a2, "10.0.1.2") // another key, same prefix
	expectQueued(t, e.send(c2, a2, victim.key, "other-1", []byte(limMarker)), "other-1")
	e.clock.Advance(time.Second) // 120/min: two more; the connection stayed open
	expectQueued(t, e.send(ca, a, victim.key, "k-later", []byte(limMarker)), "k-later")
	e.logged("key_envelopes", "peer="+a.key[:8])
}

func TestLimitKeyBytesPerMinute(t *testing.T) {
	// The default (32 MiB/min) is checked in TestLimitDefaults.
	e := newLimitEnv(t, relay.Options{KeyBytesPerMinute: 100 << 10})
	a, a2, victim := newPeer(t), newPeer(t), newPeer(t)
	ca, c2 := e.authed(a, "10.0.1.1"), e.authed(a2, "10.0.1.2")
	big := payload(30 << 10)
	expectQueued(t, e.send(ca, a, victim.key, "b-1", big), "b-1")
	expectQueued(t, e.send(ca, a, victim.key, "b-2", big), "b-2")
	expectCode(t, e.send(ca, a, victim.key, "b-3", big), envelope.CodeRateLimited, "") // refused unparsed (R55-035)
	expectQueued(t, e.send(c2, a2, victim.key, "o-1", big), "o-1")
	e.logged("key_bytes", "peer="+a.key[:8])
}

func TestLimitControlFramesPerMinute(t *testing.T) {
	e := newLimitEnv(t, relay.Options{})
	a, b := newPeer(t), newPeer(t)
	ca, cb := e.authed(a, "10.0.1.1"), e.authed(b, "10.0.1.2")
	cancel := func(c *websocket.Conn, ref string) {
		send(t, c, envelope.Control{Op: envelope.OpPairCancel, Lookup: "x", Ref: ref}) // silently ignored
	}
	for range 100 { // acks are excluded from the limit
		send(t, ca, envelope.Control{Op: envelope.OpAck, From: b.key, Ref: "nothing"})
	}
	strike := func(round int) {
		for i := range 60 {
			cancel(ca, fmt.Sprintf("c%d-%d", round, i))
		}
		cancel(ca, fmt.Sprintf("c%d-over", round))
		expectCode(t, readControl(t, ca), envelope.CodeRateLimited, fmt.Sprintf("c%d-over", round))
	}
	strike(1)
	cancel(cb, "b-1")
	stillAlive(t, cb, b) // b's frame was not refused
	e.clock.Advance(time.Minute)
	strike(2)
	stillAlive(t, ca, a) // two windows in a row: still open
	e.clock.Advance(time.Minute)
	strike(3)
	expectClose(t, ca, websocket.StatusPolicyViolation)
	stillAlive(t, cb, b)
	e.logged("control_frames", "peer="+a.key[:8])
	e.logged("control_frames_close", "peer="+a.key[:8])
}

func TestLimitReconnectsPerMinute(t *testing.T) {
	e := newLimitEnv(t, relay.Options{})
	a, b := newPeer(t), newPeer(t)
	for range 20 {
		c := e.authed(a, "10.0.1.1")
		_ = c.CloseNow()
	}
	c, reply := e.auth(a, "10.0.1.1")
	expectCode(t, reply, envelope.CodeRateLimited, "")
	expectClose(t, c, websocket.StatusTryAgainLater)
	cb := e.authed(b, "10.0.1.2")
	stillAlive(t, cb, b)
	e.clock.Advance(3 * time.Second) // 20/min: one more
	ca := e.authed(a, "10.0.1.1")
	stillAlive(t, ca, a)
	e.logged("reconnects", "peer="+a.key[:8])
}

// A recipient that stops reading, sent 1 MiB frames by several keys, holds
// at most 4 MiB in its outbound buffer (review 50 M2); the rest takes the
// offline queue. Reconnecting without reading, the queue drain respects the
// same cap.
func TestLimitNonReadingRecipientHoldsAtMost4MiB(t *testing.T) {
	e := newLimitEnv(t, relay.Options{})
	r := newPeer(t)
	cr := e.authed(r, "10.0.9.1")
	waitDrained(t, e.s, r.key)
	const capBytes = 4 << 20
	var maxSeen atomic.Int64
	stop := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		for {
			if n := e.s.Buffered(r.key); n > maxSeen.Load() {
				maxSeen.Store(n)
			}
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	big := payload(700_000) // ~0.9 MiB frames, close to envelope.MaxFrameBytes
	for i := range 4 {
		s := newPeer(t)
		cs := e.authed(s, fmt.Sprintf("10.0.%d.1", i+1))
		for j := range 6 {
			writeFrame(t, cs, []byte(mustJSON(s.env(r.key, fmt.Sprintf("s%d-%d", i, j), big))))
		}
	}
	deadline := time.Now().Add(wait)
	for {
		n, err := e.s.Queued(r.key)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			break // past the cap, envelopes took the queue path
		}
		if time.Now().After(deadline) {
			t.Fatal("nothing was queued: the per-connection byte cap never engaged")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if n := maxSeen.Load(); n > capBytes {
		t.Fatalf("non-reading recipient held %d bytes, cap %d", n, capBytes)
	}

	// Reconnect without reading: the backlog drain honours the cap too.
	_ = cr.CloseNow()
	waitConnected(t, e.s, r.key, false)
	maxSeen.Store(0)
	e.authed(r, "10.0.9.1")
	time.Sleep(300 * time.Millisecond)
	close(stop)
	<-watched
	if n := maxSeen.Load(); n > capBytes {
		t.Fatalf("drain to a non-reading recipient held %d bytes, cap %d", n, capBytes)
	}
	if n := e.s.Inflight(); n > capBytes+64<<10 {
		t.Fatalf("relay in-flight %d bytes with one non-reading peer", n)
	}
}

// --max-inflight: past the relay-wide budget, direct sends take the queue path.
func TestLimitMaxInflightRelayWide(t *testing.T) {
	const budget = 3 << 20
	// Small socket buffers: the recipients' kernel buffers must not swallow
	// the frames that are to fill the budget (loopback autotuning allows
	// megabytes on Linux and macOS).
	e := newSmallSocketEnv(t, relay.Options{MaxInflight: budget})
	var maxSeen atomic.Int64
	stop := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		for {
			if n := e.s.Inflight(); n > maxSeen.Load() {
				maxSeen.Store(n)
			}
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	big := payload(700_000)
	var rs []peer
	for i := range 3 {
		r := newPeer(t)
		e.authed(r, fmt.Sprintf("10.0.%d.9", i+10))
		waitDrained(t, e.s, r.key)
		rs = append(rs, r)
	}
	for i := range 3 {
		s := newPeer(t)
		cs := e.authed(s, fmt.Sprintf("10.0.%d.1", i+1))
		for j, r := range rs {
			for k := range 3 {
				writeFrame(t, cs, []byte(mustJSON(s.env(r.key, fmt.Sprintf("s%d-%d-%d", i, j, k), big))))
			}
		}
	}
	waitUntil(t, wait, "the budget to send mail down the queue path", func() bool { return e.count("max_inflight", "relay=all") > 0 })
	settle(t, e.s.Inflight)
	close(stop)
	<-watched
	// Control frames are charged to the ephemeral budget (R55-F1), so the
	// outbound one holds exactly, with no slack.
	if n := maxSeen.Load(); n > budget {
		t.Fatalf("in-flight reached %d bytes, budget %d", n, budget)
	}
	if n := e.s.EphemeralInflight(); n > 1<<20 { // max(3 MiB / 8, 1 MiB)
		t.Fatalf("ephemeral budget holds %d bytes, budget 1 MiB", n)
	}
	e.logged("max_inflight", "relay=all")
}

// Offline queue.

// One stranger key cannot queue more than 300 envelopes for a victim; the
// victim's teammates can still queue (review 50 M1). Four fresh keys are
// each held to 300 and together to the per-recipient 1000 (OD-P4-21 (a):
// on a relay without accounts that residual is accepted and documented).
func TestLimitQueuePerPairStrangerCannotFillVictim(t *testing.T) {
	e := newLimitEnv(t, relay.Options{})
	victim, stranger, mate := newPeer(t), newPeer(t), newPeer(t)
	cs := e.authed(stranger, "10.0.1.1")
	for i := range 300 {
		e.clock.Advance(time.Second) // stay under the send rates
		expectQueued(t, e.send(cs, stranger, victim.key, fmt.Sprintf("s-%d", i), []byte(limMarker)), fmt.Sprintf("s-%d", i))
	}
	expectCode(t, e.send(cs, stranger, victim.key, "s-over", []byte(limMarker)), envelope.CodeQueueFull, "s-over")
	cm := e.authed(mate, "10.0.2.1")
	expectQueued(t, e.send(cm, mate, victim.key, "mate-1", []byte(limMarker)), "mate-1")
	if n, _ := e.s.Queued(victim.key); n != 301 {
		t.Fatalf("victim queue %d, want 301", n)
	}
	e.logged("queue_pair", "peer="+stranger.key[:8])

	t.Run("four fresh keys", func(t *testing.T) {
		victim2 := newPeer(t)
		total := 0
		for k := range 4 {
			p := newPeer(t)
			c := e.authed(p, fmt.Sprintf("10.1.%d.1", k))
			queued := 0
			for i := range 301 {
				e.clock.Advance(time.Second)
				reply := e.send(c, p, victim2.key, fmt.Sprintf("f%d-%d", k, i), []byte(limMarker))
				if reply.Op == envelope.OpQueued {
					queued++
				} else if reply.Code != envelope.CodeQueueFull {
					t.Fatalf("got %+v", reply)
				}
			}
			if queued > 300 {
				t.Fatalf("key %d queued %d for one victim, cap 300", k, queued)
			}
			total += queued
		}
		if n, _ := e.s.Queued(victim2.key); n != total || n != 1000 {
			t.Fatalf("victim queue %d (sent %d), want the per-recipient 1000: four fresh keys fill it (review 50 M1, R-4.0)", n, total)
		}
	})
}

func TestLimitQueuePerPairBytes(t *testing.T) {
	e := newLimitEnv(t, relay.Options{})
	victim, stranger, mate := newPeer(t), newPeer(t), newPeer(t)
	cs := e.authed(stranger, "10.0.1.1")
	big := payload(700_000) // ~0.9 MiB frames: 8 fit in 8 MiB, the 9th does not
	for i := range 8 {
		expectQueued(t, e.send(cs, stranger, victim.key, fmt.Sprintf("b-%d", i), big), fmt.Sprintf("b-%d", i))
	}
	expectCode(t, e.send(cs, stranger, victim.key, "b-over", big), envelope.CodeQueueFull, "b-over")
	cm := e.authed(mate, "10.0.2.1")
	expectQueued(t, e.send(cm, mate, victim.key, "mate-1", big), "mate-1")
	e.logged("queue_pair", "peer="+stranger.key[:8])
}

func TestLimitQueuePerSenderAllRecipients(t *testing.T) {
	e := newLimitEnv(t, relay.Options{})
	s, other := newPeer(t), newPeer(t)
	cs := e.authed(s, "10.0.1.1")
	var last peer
	for r := range 7 {
		last = newPeer(t)
		for i := range 300 {
			if r*300+i == 2000 {
				break
			}
			e.clock.Advance(time.Second)
			id := fmt.Sprintf("r%d-%d", r, i)
			expectQueued(t, e.send(cs, s, last.key, id, []byte(limMarker)), id)
		}
	}
	fresh := newPeer(t)
	expectCode(t, e.send(cs, s, fresh.key, "over", []byte(limMarker)), envelope.CodeQueueFull, "over")
	co := e.authed(other, "10.0.2.1")
	expectQueued(t, e.send(co, other, last.key, "other-1", []byte(limMarker)), "other-1")
	e.logged("queue_sender", "peer="+s.key[:8])
}

func TestLimitQueuePerSenderBytes(t *testing.T) {
	// The default (64 MiB) is checked in TestLimitDefaults.
	e := newLimitEnv(t, relay.Options{QueueSenderMaxBytes: 100 << 10})
	s, other := newPeer(t), newPeer(t)
	cs := e.authed(s, "10.0.1.1")
	big := payload(30 << 10)
	expectQueued(t, e.send(cs, s, newPeer(t).key, "a", big), "a")
	expectQueued(t, e.send(cs, s, newPeer(t).key, "b", big), "b")
	expectCode(t, e.send(cs, s, newPeer(t).key, "c", big), envelope.CodeQueueFull, "c")
	co := e.authed(other, "10.0.2.1")
	expectQueued(t, e.send(co, other, newPeer(t).key, "o", big), "o")
	e.logged("queue_sender", "peer="+s.key[:8])
}

func TestLimitQueueRelayWideTotal(t *testing.T) {
	// The default (4 GiB) is checked in TestLimitDefaults.
	e := newLimitEnv(t, relay.Options{QueueMaxTotal: 100 << 10})
	a, b, victim := newPeer(t), newPeer(t), newPeer(t)
	ca, cb := e.authed(a, "10.0.1.1"), e.authed(b, "10.0.2.1")
	big := payload(30 << 10)
	expectQueued(t, e.send(ca, a, victim.key, "a-1", big), "a-1")
	expectQueued(t, e.send(cb, b, victim.key, "b-1", big), "b-1")
	expectCode(t, e.send(cb, b, victim.key, "b-2", big), envelope.CodeQueueFull, "b-2")
	expectCode(t, e.send(ca, a, victim.key, "a-2", big), envelope.CodeQueueFull, "a-2")
	// An ack frees its bytes (the totals are adjusted on ack, not rescanned).
	cv := e.authed(victim, "10.0.3.1")
	cv.SetReadLimit(envelope.MaxFrameBytes)
	id := readID(t, cv)
	ack(t, cv, a.key, id)
	waitQueued(t, e.s, victim.key, 1)
	expectQueued(t, e.send(ca, a, newPeer(t).key, "a-3", big), "a-3")
	e.logged("queue_total", "peer=")
}

// Under 1 GiB free beneath the queue file new envelopes get internal
// ("relay storage low"); acks and deletes still work.
func TestLimitQueueLowDisk(t *testing.T) {
	var free atomic.Uint64
	free.Store(10 << 30)
	var logs syncBuffer
	a, victim := newPeer(t), newPeer(t)
	s, url, _ := startStoppable(t, relay.Options{
		QueuePath: testutil.TempDir(t) + "/queue.db",
		FreeDisk:  func(string) (uint64, error) { return free.Load(), nil },
		Logger:    slog.New(slog.NewTextHandler(&logs, nil)),
	})
	ca := rawAuthed(t, url, a)
	sendQueued(t, ca, a, victim.key, "before", []byte(limMarker))
	free.Store(512 << 20) // under 1 GiB
	time.Sleep(1100 * time.Millisecond)
	writeFrame(t, ca, []byte(mustJSON(a.env(victim.key, "low-1", []byte(limMarker)))))
	got := readControl(t, ca)
	expectCode(t, got, envelope.CodeInternal, "low-1")
	if got.Message != "relay storage low" {
		t.Fatalf("message %q", got.Message)
	}
	cv := rawAuthed(t, url, victim) // acks and deletes still work
	if id := readID(t, cv); id != "before" {
		t.Fatalf("got %s", id)
	}
	ack(t, cv, a.key, "before")
	waitQueued(t, s, victim.key, 0)
	if l := logs.String(); !strings.Contains(l, "event=limit limit=queue_disk relay=all") || strings.Contains(l, limMarker) {
		t.Fatalf("log: %s", l)
	}
}

// Client IP behind a proxy.

// A spoofed client IP header from a peer that is not a trusted proxy is
// ignored: the TCP address is used, so every spoofed value lands in the
// real peer's prefix.
func TestLimitSpoofedClientIPHeaderIgnored(t *testing.T) {
	e := newLimitEnv(t, relay.Options{
		UpgradeBurst: 2, UpgradesPerMinute: 1,
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.9.9.9/32")}, // not 127.0.0.1
	})
	for _, ip := range []string{"1.1.1.1", "2.2.2.2"} {
		if c, status := e.dial(ip); c == nil {
			t.Fatalf("dial: HTTP %d", status)
		}
	}
	if _, status := e.dial("3.3.3.3"); status != http.StatusTooManyRequests {
		t.Fatalf("third spoofed dial: HTTP %d, want 429 (all from 127.0.0.1)", status)
	}
	e.logged("upgrades_per_prefix", "prefix=127.0.0.0")

	t.Run("trusted", func(t *testing.T) {
		e := newLimitEnv(t, relay.Options{UpgradeBurst: 2, UpgradesPerMinute: 1})
		for _, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "3.3.3.4"} {
			if c, status := e.dial(ip); c == nil {
				t.Fatalf("dial as %s: HTTP %d", ip, status)
			}
		}
		if _, status := e.dial("3.3.3.5"); status != http.StatusTooManyRequests {
			t.Fatalf("third from 3.3.3.0/24: HTTP %d, want 429", status)
		}
	})

	t.Run("X-Forwarded-For last hop", func(t *testing.T) {
		e := newLimitEnv(t, relay.Options{UpgradeBurst: 1, UpgradesPerMinute: 1, ClientIPHeader: "X-Forwarded-For"})
		xff := func(v string) int {
			h := http.Header{}
			h.Set("X-Forwarded-For", v)
			_, status := e.dialHeader(h)
			return status
		}
		if st := xff("6.6.6.6, 10.0.1.5"); st != http.StatusSwitchingProtocols {
			t.Fatalf("HTTP %d", st)
		}
		// The client controls the first entries; only the last (added by
		// the trusted proxy) counts, so this is the same prefix.
		if st := xff("7.7.7.7, 10.0.1.6"); st != http.StatusTooManyRequests {
			t.Fatalf("same last hop prefix: HTTP %d, want 429", st)
		}
		if st := xff("10.0.1.5, 10.0.2.6"); st != http.StatusSwitchingProtocols {
			t.Fatalf("another last hop prefix: HTTP %d", st)
		}
	})

	t.Run("not public", func(t *testing.T) {
		var logs syncBuffer
		_, url := start(t, relay.Options{UpgradeBurst: 1, UpgradesPerMinute: 1, ClientIPHeader: limIPHeader,
			TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
		e := &limitEnv{t: t, url: url, logs: &logs}
		if c, _ := e.dial("1.1.1.1"); c == nil {
			t.Fatal("first dial refused")
		}
		if _, status := e.dial("2.2.2.2"); status != http.StatusTooManyRequests {
			t.Fatalf("a relay that is not public honoured the header: HTTP %d", status)
		}
	})
}

// 64 fresh keys from one prefix together send no more than the per-prefix
// rate (600 envelopes at once, then 10/s), whatever their per-key budgets.
func TestLimitSixtyFourFreshKeysOnePrefix(t *testing.T) {
	e := newLimitEnv(t, relay.Options{QueueMaxEnvelopes: 100000, QueuePairMaxEnvelopes: -1, QueueSenderMaxEnvelopes: -1})
	victim := newPeer(t)
	type sender struct {
		p peer
		c *websocket.Conn
	}
	var ss []sender
	for i := range 64 {
		if i == 60 {
			e.clock.Advance(10 * time.Second) // the upgrade burst is 60
		}
		p := newPeer(t)
		ss = append(ss, sender{p, e.authed(p, fmt.Sprintf("10.0.1.%d", i+1))})
	}
	queued, limited := 0, 0
	for round := range 20 { // 20 per key: well under each key's own 240 burst
		for i, s := range ss {
			id := fmt.Sprintf("k%d-%d", i, round)
			switch reply := e.send(s.c, s.p, victim.key, id, []byte(limMarker)); {
			case reply.Op == envelope.OpQueued:
				queued++
			case reply.Code == envelope.CodeRateLimited:
				limited++
			default:
				t.Fatalf("got %+v", reply)
			}
		}
	}
	if queued != 600 || limited != 64*20-600 {
		t.Fatalf("queued %d, rate limited %d; want 600 and %d", queued, limited, 64*20-600)
	}
	other := newPeer(t)
	co := e.authed(other, "10.0.2.1")
	expectQueued(t, e.send(co, other, victim.key, "other", []byte(limMarker)), "other")
	e.logged("prefix_envelopes", "prefix=10.0.1.0")
}

// R-4.0 H1: frames being read count against a relay-wide budget of the
// --max-inflight size. A peer that starts a maximum-size frame and never
// finishes it pinned up to 1 MiB of relay memory outside every cap, so a few
// hundred authenticated connections exhausted a 256–512 MB host.
//
// R55-F1 (acceptance test 8, OD-R55F1-1 (a)): when the second frame does not
// fit, the older of two equally heavy frames in different prefixes is
// evicted (1013) instead of the newcomer, and the second frame completes.
func TestLimitUnfinishedFramesShareReadBudget(t *testing.T) {
	const budget = 3 << 19 // 1.5 MiB: one unfinished ~1 MiB frame fits, a second does not
	e := newLimitEnv(t, relay.Options{MaxInflight: budget})
	victim, a, b := newPeer(t), newPeer(t), newPeer(t)
	ca := e.authed(a, "10.0.1.1")
	cb := e.authed(b, "10.0.2.1")
	const part = 600_000
	frameA := []byte(mustJSON(a.env(victim.key, "a-1", payload(700_000))))
	wa, err := ca.Writer(ctx(t), websocket.MessageText)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wa.Write(frameA[:part]); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(wait)
	for e.s.Reading() < part {
		if time.Now().After(deadline) {
			t.Fatalf("relay counts %d bytes of an unfinished %d-byte frame", e.s.Reading(), part)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// A second frame of the same size does not fit in what is left: the
	// first, older frame's connection is evicted to make room.
	frameB := []byte(mustJSON(b.env(victim.key, "b-1", payload(700_000))))
	wb, err := cb.Writer(ctx(t), websocket.MessageText)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wb.Write(frameB[:part]); err != nil {
		t.Fatal(err)
	}
	expectClose(t, ca, websocket.StatusTryAgainLater)
	if n := e.s.Reading(); n > budget {
		t.Fatalf("frames being read hold %d bytes, budget %d", n, budget)
	}
	// The second sender completes, and a routed frame is uncounted.
	if _, err := wb.Write(frameB[part:]); err != nil {
		t.Fatal(err)
	}
	if err := wb.Close(); err != nil {
		t.Fatal(err)
	}
	expectQueued(t, readControl(t, cb), "b-1")
	// The reply is sent from inside route, before serve uncharges the frame,
	// so the sender can see it first: poll for the uncharge.
	deadline = time.Now().Add(wait)
	for e.s.Reading() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d bytes still counted as being read after the frame was routed", e.s.Reading())
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.logged("evict_read", "prefix=10.0.1.0")
}

// R-4.0 H1: a queue drain charges its batch to --max-inflight before it
// reads it from the database. While the budget is spent, a reconnecting
// recipient's backlog stays on disk instead of in memory outside the budget.
func TestLimitDrainLoadsNothingWhileBudgetSpent(t *testing.T) {
	const budget = 4 << 20
	e := newLimitEnv(t, relay.Options{MaxInflight: budget})
	r, s := newPeer(t), newPeer(t)
	cs := e.authed(s, "10.0.1.1")
	for i := range 3 {
		id := fmt.Sprintf("d-%d", i)
		expectQueued(t, e.send(cs, s, r.key, id, payload(300_000)), id)
	}
	e.s.ChargeInflight(budget)
	cr := e.authed(r, "10.0.2.1")
	cr.SetReadLimit(2 << 20)
	time.Sleep(300 * time.Millisecond)
	if n := e.s.DrainHeld(); n != 0 {
		t.Fatalf("a drain holds %d bytes read from the queue while --max-inflight is spent", n)
	}
	e.s.ChargeInflight(-budget)
	for i := range 3 {
		_, raw, err := cr.Read(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		h, err := envelope.ParseHeader(raw)
		if err != nil || h.ID != fmt.Sprintf("d-%d", i) {
			t.Fatalf("frame %d: %v %q", i, err, h.ID)
		}
	}
	if n := e.s.DrainHeld(); n != 0 {
		t.Fatalf("drain holds %d bytes after delivering everything", n)
	}
}

// GET /healthz is rate limited per prefix like upgrades (relay-hosted.md §1;
// review 51 L5 left it to 4.0b), in its own bucket.
func TestLimitHealthPerPrefix(t *testing.T) {
	e := newLimitEnv(t, relay.Options{})
	url := "http" + strings.TrimPrefix(strings.TrimSuffix(e.url, envelope.ConnectPath), "ws") + relay.HealthPath
	get := func(ip string) int {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx(t), http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(limIPHeader, ip)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	for i := range 60 { // the burst
		if got := get("10.0.1.1"); got != http.StatusOK {
			t.Fatalf("health check %d: HTTP %d", i, got)
		}
	}
	if got := get("10.0.1.2"); got != http.StatusTooManyRequests {
		t.Fatalf("past the burst: HTTP %d, want 429", got)
	}
	if got := get("10.0.2.1"); got != http.StatusOK {
		t.Fatalf("other prefix: HTTP %d", got)
	}
	e.authed(newPeer(t), "10.0.1.1") // health checks spent no upgrades
	e.logged("health_per_prefix", "prefix=10.0.1.0")
}
