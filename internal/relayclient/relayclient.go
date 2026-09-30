// Package relayclient is the daemon's side of the relay protocol
// (Docs/protocol/envelope.md): one persistent WebSocket connection, signed
// challenge authentication, and reconnection with exponential backoff.
package relayclient

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
)

// ErrNotConnected is returned by Send while there is no authenticated connection.
var ErrNotConnected = errors.New("relayclient: not connected to the relay")

// ErrNoAuthV2 is the connection error when a relay at a non-loopback URL
// does not offer relay auth v2. The client then signs nothing: a v1 signature
// does not name the relay and could be replayed at another one
// (Docs/protocol/relay-hosted.md §1, "Daemon rule, no downgrade").
var ErrNoAuthV2 = errors.New("relayclient: relay does not support auth v2")

// errRedirect is the dial error for a relay that answers the upgrade with a
// redirect. The *url.Error around it quotes the relay's Location, so
// connError reports it as fixed text (review 67b F9R-2).
var errRedirect = errors.New("relay redirects are not followed")

// MailType is the envelope type of sealed mail, which bypasses the seen-set.
const MailType = "mail"

const (
	defaultMinBackoff = 500 * time.Millisecond
	defaultMaxBackoff = 30 * time.Second
	// defaultStableAfter is how long a connection must stay up after ready
	// before the backoff resets (OD-R55F9-5); defaultTryAgainFloor is the
	// least delay after a close with status 1013 (OD-R55F9-9).
	defaultStableAfter   = 30 * time.Second
	defaultTryAgainFloor = 5 * time.Second
	// maxLastError bounds last_error and the relay_disconnect log line.
	maxLastError     = 256
	handshakeTimeout = 15 * time.Second
	writeTimeout     = 10 * time.Second
)

// Config configures a Client.
type Config struct {
	// URL is the relay's WebSocket URL, e.g. ws://127.0.0.1:8787/v1/connect.
	// A URL with an empty path gets envelope.ConnectPath.
	URL string
	// Signer proves the daemon's identity to the relay.
	Signer Signer
	// OnEnvelope receives each envelope forwarded by the relay. It runs on the
	// read loop and must not block. Nil drops envelopes.
	OnEnvelope func(envelope.Envelope)
	// OnError receives error frames from the relay (peer_offline and so on). Same rules.
	OnError func(envelope.ErrorFrame)
	// OnQueued receives the relay's confirmation that it stored envelope ref
	// for an offline peer. Same rules as OnEnvelope. Nil drops it.
	OnQueued func(ref string)
	// OnControl receives control frames other than error and queued (pair_code,
	// pair_peer). Same rules as OnEnvelope. Nil drops them.
	OnControl func(envelope.Control)
	// OnReady is called each time the relay accepts the authentication (also
	// after a reconnect). Same rules as OnEnvelope. Nil ignores it.
	OnReady func()
	// Logger receives connection events; it never sees payloads. Nil discards them.
	Logger *slog.Logger
	// MinBackoff and MaxBackoff bound the reconnect delay. Defaults 500ms and 30s.
	MinBackoff, MaxBackoff time.Duration
	// RootCAs verifies a wss:// relay's certificate. Nil uses the system
	// roots. See LoadRoots for adding a private CA (agentnetd --relay-ca).
	RootCAs *x509.CertPool

	// dialContext replaces the TCP dial (tests only, via export_test.go).
	dialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// stableAfter and tryAgainFloor replace defaultStableAfter and
	// defaultTryAgainFloor (tests only, via export_test.go).
	stableAfter, tryAgainFloor time.Duration
}

// Client keeps one authenticated connection to a relay.
type Client struct {
	cfg  Config
	url  string
	pub  ed25519.PublicKey
	http *http.Client
	log  *slog.Logger
	mu   sync.Mutex
	conn *websocket.Conn // non-nil only while authenticated
	seen *seenSet        // envelopes already handed to OnEnvelope
	bad  badFrames       // frames from the relay that could not be parsed

	features []string // from the latest ready frame; guarded by mu
	// minClient is ready.min_client from the latest ready frame ("" for
	// none); guarded by mu. Kept after a disconnect: it is what the relay
	// last said, for status/doctor.
	minClient string

	// connSince is when the current Connected() value began (a connect or a
	// disconnect), for status/doctor's "since" (Docs/review/49-phase4-tickets.md
	// §CLI contracts). lastErr is the most recent connection error as
	// connError renders it: content-free (never an envelope payload, a peer
	// identity, or the relay's error message, close reason or redirect
	// target), one line, at most 256 bytes.
	connSince time.Time
	lastErr   string

	// origin is the canonical origin of the configured URL, the value auth v2
	// signs. loopback says whether its host is this machine, the only case
	// in which a v1 signature is ever made.
	origin   string
	loopback bool
}

// State is relayclient's connection state, reported by "status" and "doctor"
// (Docs/review/49-phase4-tickets.md §CLI contracts).
type State struct {
	// Connected is Client.Connected() at the time of the call.
	Connected bool
	// Since is when Connected last changed.
	Since time.Time
	// LastError is the most recent connection error's message, or "" before
	// any failure.
	LastError string
	// MinClient is the relay's ready.min_client, as last received ("" when
	// the relay sent none or never answered). Untrusted relay text: it is
	// only ever reported if it parses as a release version.
	MinClient string
}

// New validates cfg and returns a Client. Call Run to connect.
func New(cfg Config) (*Client, error) {
	if cfg.Signer == nil {
		return nil, errors.New("relayclient: Signer is required")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return nil, fmt.Errorf("relayclient: relay URL must be ws:// or wss:// with a host, got %q", cfg.URL)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = envelope.ConnectPath
	}
	origin, err := envelope.OriginOf(u)
	if err != nil {
		return nil, fmt.Errorf("relayclient: relay URL %q: %w", cfg.URL, err)
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = defaultMinBackoff
	}
	if cfg.MaxBackoff < cfg.MinBackoff {
		cfg.MaxBackoff = max(defaultMaxBackoff, cfg.MinBackoff)
	}
	if cfg.stableAfter <= 0 {
		cfg.stableAfter = defaultStableAfter
	}
	if cfg.tryAgainFloor <= 0 {
		cfg.tryAgainFloor = defaultTryAgainFloor
	}
	c := &Client{cfg: cfg, url: u.String(), pub: cfg.Signer.Public(), log: cfg.Logger, seen: newSeenSet(seenCapacity),
		origin: origin, loopback: envelope.IsLoopbackHost(u.Hostname()), connSince: time.Now()}
	c.http = newHTTPClient(cfg, c.loopback)
	if c.log == nil {
		c.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	c.bad.log = c.log
	return c, nil
}

// Connected reports whether the client currently holds an authenticated connection.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// State returns the client's current connection state for status/doctor.
func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return State{Connected: c.conn != nil, Since: c.connSince, LastError: c.lastErr, MinClient: c.minClient}
}

// Send validates and sends e. It does not wait for delivery: a relay refusal
// (for example peer_offline) arrives later through Config.OnError with Ref == e.ID.
func (c *Client) Send(ctx context.Context, e envelope.Envelope) error {
	frame, err := e.Marshal()
	if err != nil {
		return err
	}
	return c.write(ctx, frame)
}

// SendControl sends a control frame (for example pair_new). Like Send it does
// not wait for a reply; replies arrive through OnControl and OnError.
func (c *Client) SendControl(ctx context.Context, ctl envelope.Control) error {
	frame, err := json.Marshal(ctl)
	if err != nil {
		return fmt.Errorf("relayclient: marshal control frame: %w", err)
	}
	return c.write(ctx, frame)
}

func (c *Client) write(ctx context.Context, frame []byte) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return ErrNotConnected
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := conn.Write(wctx, websocket.MessageText, frame); err != nil {
		return fmt.Errorf("relayclient: send: %w", err)
	}
	return nil
}

// Run connects and stays connected, reconnecting with backoff, until ctx is
// cancelled. It always returns ctx.Err().
//
// The backoff resets only after a connection stayed up stableAfter (30 s)
// after ready, and never after a close with status 1013, which instead sets
// the next delay to at least tryAgainFloor (Docs/protocol/envelope.md
// §Client behaviour, R55-F9).
func (c *Client) Run(ctx context.Context) error {
	delay := c.cfg.MinBackoff
	for {
		start := time.Now()
		readyAt, err := c.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		switch {
		case shedding(err):
			delay = max(delay, c.cfg.tryAgainFloor)
		case closedWith(err, websocket.StatusGoingAway):
			// A relay drain or restart: no reset, so the daemons it dropped
			// do not all come back at MinBackoff together (review 75 F9S-1).
		case !readyAt.IsZero() && time.Since(readyAt) >= c.cfg.stableAfter:
			delay = c.cfg.MinBackoff // a lasting connection resets the backoff
		}
		var msg string
		if err != nil {
			msg = connError(err)
			c.mu.Lock()
			c.lastErr = msg
			c.mu.Unlock()
		}
		wait := jitter(delay)
		c.log.Warn("relay connection lost", "event", "relay_disconnect", "error", msg, "retry_in", wait.Round(time.Millisecond), "connected_for", time.Since(start).Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		delay = min(delay*2, c.cfg.MaxBackoff)
	}
}

// shedding reports whether err is the relay shedding load: a close with 1013
// (Try Again Later), or relay_full / rate_limited in place of ready, which
// the relay sends just before its 1013 close (review 75 F9S-1). The backoff
// then never resets and the next wait is at least tryAgainFloor.
func shedding(err error) bool {
	var ef envelope.ErrorFrame
	if errors.As(err, &ef) {
		return ef.Code == envelope.CodeRelayFull || ef.Code == envelope.CodeRateLimited
	}
	return closedWith(err, websocket.StatusTryAgainLater)
}

// closedWith reports whether err is a close by the relay with status code.
func closedWith(err error, code websocket.StatusCode) bool {
	var ce websocket.CloseError
	return errors.As(err, &ce) && ce.Code == code
}

// tlsRejected reports whether err is the relay's certificate failing
// verification. These errors quote certificate names the relay chose.
func tlsRejected(err error) bool {
	var hn x509.HostnameError
	var ua x509.UnknownAuthorityError
	var ci x509.CertificateInvalidError
	var cv *tls.CertificateVerificationError
	return errors.As(err, &hn) || errors.As(err, &ua) || errors.As(err, &ci) || errors.As(err, &cv)
}

// connError renders a connection error for last_error and the
// relay_disconnect log line (Docs/protocol/envelope.md, "last_error"): no
// relay message, close reason, redirect target, upgrade header value or
// certificate name (review 75 F9S-2), one line, at most 256 bytes.
func connError(err error) string {
	var ef envelope.ErrorFrame
	var ce websocket.CloseError
	switch {
	case errors.As(err, &ef):
		return ef.Error()
	case errors.As(err, &ce):
		return fmt.Sprintf("closed by relay (status %d)", int(ce.Code))
	case errors.Is(err, errRedirect):
		return "dial: the relay answered with a redirect (not followed)"
	case tlsRejected(err):
		return "dial: the relay's TLS certificate was rejected"
	case strings.Contains(err.Error(), "WebSocket protocol violation"):
		// coder/websocket quotes the relay's upgrade header values here.
		return "dial: the relay's upgrade response is invalid"
	default:
		return displaytext.Line(err.Error(), maxLastError)
	}
}

// session makes one connection attempt and serves it until it fails.
// readyAt is when the relay's ready was read, zero if it never was.
func (c *Client) session(ctx context.Context) (readyAt time.Time, err error) {
	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	conn, _, err := websocket.Dial(hctx, c.url, &websocket.DialOptions{HTTPClient: c.http}) //nolint:bodyclose // the library closes the handshake body
	if err != nil {
		cancel()
		return time.Time{}, fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(envelope.MaxFrameBytes)

	ready, err := c.handshake(hctx, conn)
	cancel()
	if err != nil {
		return time.Time{}, err
	}
	readyAt = time.Now()

	c.mu.Lock()
	c.conn = conn
	c.features = ready.Features
	c.minClient = validMinClient(ready.MinClient)
	c.connSince = time.Now()
	minClient := c.minClient
	c.mu.Unlock()
	if minClient != "" {
		if meets, ok := version.MeetsMinimum(version.Version, minClient); ok && !meets {
			c.log.Warn("this agentnetd is older than the relay's minimum supported version; please upgrade",
				"event", "relay_min_client", "version", version.Version, "min_client", minClient)
		}
	}
	defer c.bad.flush()
	defer func() {
		c.mu.Lock()
		c.conn = nil
		c.features = nil
		c.connSince = time.Now()
		c.mu.Unlock()
	}()
	c.log.Info("relay connected", "event", "relay_connect")
	if c.cfg.OnReady != nil {
		c.cfg.OnReady()
	}

	for {
		typ, frame, err := conn.Read(ctx)
		if err != nil {
			return readyAt, err
		}
		if typ != websocket.MessageText {
			return readyAt, errors.New("relay sent a binary frame")
		}
		c.dispatch(ctx, frame)
	}
}

// handshake authenticates and returns the relay's ready frame.
func (c *Client) handshake(ctx context.Context, conn *websocket.Conn) (envelope.Control, error) {
	ch, err := readControl(ctx, conn, envelope.OpChallenge)
	if err != nil {
		return envelope.Control{}, err
	}
	nonce, err := envelope.DecodeNonce(ch.Nonce)
	if err != nil {
		return envelope.Control{}, err
	}
	var auth envelope.Control
	switch authVersion(ch.Auth, c.loopback) {
	case 2:
		auth, err = envelope.SignAuthV2(c.pub, nonce, c.origin, c.cfg.Signer.Sign)
	case 1:
		auth, err = envelope.SignAuth(c.pub, nonce, c.cfg.Signer.Sign)
	default:
		return envelope.Control{}, ErrNoAuthV2
	}
	if err != nil {
		return envelope.Control{}, fmt.Errorf("sign challenge: %w", err)
	}
	if err := writeControl(ctx, conn, auth); err != nil {
		return envelope.Control{}, err
	}
	return readControl(ctx, conn, envelope.OpReady)
}

// validMinClient returns s if it is a release version (MAJOR.MINOR.PATCH),
// else "": ready.min_client is relay-supplied text and reaches logs, status
// and doctor output, so anything else is dropped rather than shown.
func validMinClient(s string) string {
	if _, ok := version.ParseRelease(s); !ok {
		return ""
	}
	return s
}

// authVersion picks the relay authentication version to answer a challenge
// offering offered (nil: a relay that predates the list, v1 only), or 0 for
// none. v2 whenever it is offered; v1 only when the relay URL is loopback.
// A non-loopback URL never gets a v1 signature, whatever the challenge says:
// a hostile relay could otherwise strip v2 from a challenge it forwards and
// replay the v1 answer at the real relay.
func authVersion(offered []string, loopback bool) int {
	if slices.Contains(offered, envelope.AuthV2) {
		return 2
	}
	if loopback && (len(offered) == 0 || slices.Contains(offered, envelope.AuthV1)) {
		return 1
	}
	return 0
}

// newHTTPClient is the client for the WebSocket handshake: HTTP/1.1, the
// configured roots, and no redirects. A redirect is a connection error, so
// the origin the daemon signs is always the one it dialled. For a loopback
// URL the peer must be a loopback address (see loopbackOnly).
func newHTTPClient(cfg Config, loopback bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = false
	tr.TLSClientConfig = &tls.Config{RootCAs: cfg.RootCAs, MinVersion: tls.VersionTLS12}
	if cfg.dialContext != nil {
		tr.DialContext = cfg.dialContext
	}
	if loopback {
		tr.DialContext = loopbackOnly(tr.DialContext)
	}
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errRedirect
		},
	}
}

// loopbackOnly wraps dial so that it fails unless the peer is a loopback
// address (review 51 M1). A URL is classed loopback by its text, but a name
// such as "localhost" or "127.0.0.1." is resolved by the hosts file or DNS,
// and a loopback URL is allowed plain ws:// and a v1 signature, which does
// not name the relay.
func loopbackOnly(dial func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		if ta, ok := conn.RemoteAddr().(*net.TCPAddr); !ok || !ta.IP.IsLoopback() {
			_ = conn.Close()
			return nil, fmt.Errorf("relay %s is a loopback URL but the connection went to %s", addr, conn.RemoteAddr())
		}
		return conn, nil
	}
}

// LoadRoots returns the system roots plus the certificates in pemCA, for a
// relay behind a private or self-signed CA. It fails if pemCA holds no
// certificate.
func LoadRoots(pemCA []byte) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pemCA) {
		return nil, errors.New("relayclient: no PEM certificate found in the relay CA file")
	}
	return pool, nil
}

// CheckURL applies the daemon's relay URL rule (Docs/protocol/relay-hosted.md
// §1): a ws:// URL must name a loopback host; a remote relay must use wss://.
// allowInsecure (DORYLINAE_ALLOW_INSECURE_RELAY=1, LAN tests) lets a remote
// ws:// URL through; insecure then reports that it did so, for a warning.
func CheckURL(raw string, allowInsecure bool) (insecure bool, err error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return false, fmt.Errorf("relay URL must be ws:// or wss:// with a host, got %q", raw)
	}
	if _, err := envelope.OriginOf(u); err != nil {
		return false, fmt.Errorf("relay URL %q: %w", raw, err)
	}
	if u.Scheme == "wss" || envelope.IsLoopbackHost(u.Hostname()) {
		return false, nil
	}
	if !allowInsecure {
		return false, fmt.Errorf("a remote relay must use wss:// (got %q)", raw)
	}
	return true, nil
}

// Features returns the optional features the relay advertised in its latest
// ready frame; nil while disconnected or when the relay advertised none.
func (c *Client) Features() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.features)
}

// HasFeature reports whether the connected relay advertised feature.
func (c *Client) HasFeature(feature string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Contains(c.features, feature)
}

func (c *Client) dispatch(ctx context.Context, frame []byte) {
	f, err := envelope.Classify(frame)
	if err != nil {
		c.bad.add(false)
		return
	}
	if f.Control != nil {
		switch {
		case f.Control.Op == envelope.OpError:
			ef := errorFrame(*f.Control)
			// The relay's message is shown nowhere else (OD-R55F9-10).
			c.log.Debug("relay error frame", "event", "relay_error_frame", "code", ef.Code, "ref", ef.Ref, "message", ef.Message)
			if c.cfg.OnError != nil {
				c.cfg.OnError(ef)
			}
		case f.Control.Op == envelope.OpQueued:
			if c.cfg.OnQueued != nil {
				c.cfg.OnQueued(f.Control.Ref)
			}
		case c.cfg.OnControl != nil:
			c.cfg.OnControl(*f.Control)
		}
		return
	}
	e, err := envelope.Parse(frame)
	if err != nil {
		c.badEnvelope(ctx, frame)
		return
	}
	// An envelope for another key is never handed up and does not enter the
	// seen-set. A queued type is acked so the relay does not redeliver it; an
	// ephemeral one is never acked (Docs/protocol/envelope.md §Client
	// behaviour, R55-F9).
	if e.To != envelope.KeyString(c.pub) {
		c.log.Debug("dropping envelope addressed to another key", "event", "relay_misrouted", "type", e.Type, "id", e.ID)
		if !envelope.IsEphemeral(e.Type) {
			c.ack(ctx, e)
		}
		return
	}
	// Ephemeral envelopes (presence) are never queued by the relay, so there is
	// nothing to ack, and they must not touch the seen-set: a heartbeat flood
	// would evict the session.* ids it protects. The presence layer has its own
	// replay rule.
	if envelope.IsEphemeral(e.Type) {
		if c.cfg.OnEnvelope != nil {
			c.cfg.OnEnvelope(e)
		}
		return
	}
	// The relay may redeliver an envelope whose ack it never saw (for example
	// after a reconnect mid-flush). Hand each one up once, but always ack.
	// Mail is the exception (Docs/protocol/mail.md): repeats must reach the mail
	// layer so a resend after a lost ack is re-acked, so it bypasses the
	// seen-set and the mail layer's (from, id) dedupe handles repeats.
	if (e.Type == MailType || c.seen.add(e.From, e.ID)) && c.cfg.OnEnvelope != nil {
		c.cfg.OnEnvelope(e)
	}
	c.ack(ctx, e)
}

// ack tells the relay e arrived so it can delete its queued copy. A lost ack is
// harmless: the relay redelivers and dispatch drops the duplicate.
func (c *Client) ack(ctx context.Context, e envelope.Envelope) {
	if err := c.SendControl(ctx, envelope.Control{Op: envelope.OpAck, From: e.From, Ref: e.ID}); err != nil {
		c.log.Warn("could not ack envelope", "event", "relay_ack_failed", "id", e.ID, "error", err)
	}
}

func jitter(d time.Duration) time.Duration {
	// 75%-125% of d, so many daemons don't reconnect in lockstep.
	return d*3/4 + rand.N(d/2+1) //nolint:gosec // jitter needs no crypto randomness
}
