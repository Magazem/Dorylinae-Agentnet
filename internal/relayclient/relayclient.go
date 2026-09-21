// Package relayclient is the daemon's side of the relay protocol
// (Docs/protocol/envelope.md): one persistent WebSocket connection, signed
// challenge authentication, and reconnection with exponential backoff.
package relayclient

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// ErrNotConnected is returned by Send while there is no authenticated connection.
var ErrNotConnected = errors.New("relayclient: not connected to the relay")

// MailType is the envelope type of sealed mail, which bypasses the seen-set.
const MailType = "mail"

const (
	defaultMinBackoff = 500 * time.Millisecond
	defaultMaxBackoff = 30 * time.Second
	handshakeTimeout  = 15 * time.Second
	writeTimeout      = 10 * time.Second
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
	// Logger receives connection events; it never sees payloads. Nil discards them.
	Logger *slog.Logger
	// MinBackoff and MaxBackoff bound the reconnect delay. Defaults 500ms and 30s.
	MinBackoff, MaxBackoff time.Duration
}

// Client keeps one authenticated connection to a relay.
type Client struct {
	cfg  Config
	url  string
	pub  ed25519.PublicKey
	log  *slog.Logger
	mu   sync.Mutex
	conn *websocket.Conn // non-nil only while authenticated
	seen *seenSet        // envelopes already handed to OnEnvelope
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
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = defaultMinBackoff
	}
	if cfg.MaxBackoff < cfg.MinBackoff {
		cfg.MaxBackoff = max(defaultMaxBackoff, cfg.MinBackoff)
	}
	c := &Client{cfg: cfg, url: u.String(), pub: cfg.Signer.Public(), log: cfg.Logger, seen: newSeenSet(seenCapacity)}
	if c.log == nil {
		c.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return c, nil
}

// Connected reports whether the client currently holds an authenticated connection.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
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
func (c *Client) Run(ctx context.Context) error {
	delay := c.cfg.MinBackoff
	for {
		start := time.Now()
		authed, err := c.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if authed {
			delay = c.cfg.MinBackoff // a working connection resets the backoff
		}
		wait := jitter(delay)
		c.log.Warn("relay connection lost", "event", "relay_disconnect", "error", err, "retry_in", wait.Round(time.Millisecond), "connected_for", time.Since(start).Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		delay = min(delay*2, c.cfg.MaxBackoff)
	}
}

// session makes one connection attempt and serves it until it fails.
// authed reports whether authentication completed.
func (c *Client) session(ctx context.Context) (authed bool, err error) {
	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	conn, _, err := websocket.Dial(hctx, c.url, nil)
	if err != nil {
		cancel()
		return false, fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(envelope.MaxFrameBytes)

	err = c.handshake(hctx, conn)
	cancel()
	if err != nil {
		return false, err
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
	}()
	c.log.Info("relay connected", "event", "relay_connect")

	for {
		typ, frame, err := conn.Read(ctx)
		if err != nil {
			return true, err
		}
		if typ != websocket.MessageText {
			return true, errors.New("relay sent a binary frame")
		}
		c.dispatch(ctx, frame)
	}
}

func (c *Client) handshake(ctx context.Context, conn *websocket.Conn) error {
	ch, err := readControl(ctx, conn, envelope.OpChallenge)
	if err != nil {
		return err
	}
	nonce, err := envelope.DecodeNonce(ch.Nonce)
	if err != nil {
		return err
	}
	auth, err := envelope.SignAuth(c.pub, nonce, c.cfg.Signer.Sign)
	if err != nil {
		return fmt.Errorf("sign challenge: %w", err)
	}
	if err := writeControl(ctx, conn, auth); err != nil {
		return err
	}
	_, err = readControl(ctx, conn, envelope.OpReady)
	return err
}

func (c *Client) dispatch(ctx context.Context, frame []byte) {
	f, err := envelope.Classify(frame)
	if err != nil {
		c.log.Warn("ignoring malformed frame from relay")
		return
	}
	if f.Control != nil {
		switch {
		case f.Control.Op == envelope.OpError:
			if c.cfg.OnError != nil {
				c.cfg.OnError(envelope.ErrorFrame{Code: f.Control.Code, Message: f.Control.Message, Ref: f.Control.Ref})
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
		c.log.Warn("ignoring invalid envelope from relay", "error", err)
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
