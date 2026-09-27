package relayclient

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/url"
	"time"

	"github.com/coder/websocket"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// Challenge is the relay's opening frame, as read by Probe.
type Challenge struct {
	// Auth lists the relay auth versions offered (envelope.AuthV1, AuthV2).
	// Empty means v1 only (a relay that predates the list).
	Auth []string
	// Expires is when the challenge's nonce window ends, the relay's clock
	// plus its (undisclosed) challenge TTL.
	Expires time.Time
}

// Probe dials rawURL, reads its challenge frame and closes, without
// authenticating. It is the only relay contact `agentnet doctor` makes: the
// relay keeps one connection per key and replaces the older one, so a doctor
// login would kick the running daemon's connection (review 50 M4). Probe
// never signs a response, so it cannot be mistaken for that daemon.
func Probe(ctx context.Context, rawURL string, roots *x509.CertPool) (Challenge, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return Challenge{}, fmt.Errorf("relayclient: relay URL must be ws:// or wss:// with a host, got %q", rawURL)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = envelope.ConnectPath
	}
	loopback := envelope.IsLoopbackHost(u.Hostname())
	client := newHTTPClient(Config{RootCAs: roots}, loopback)

	conn, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: client}) //nolint:bodyclose // the library closes the handshake body
	if err != nil {
		return Challenge{}, fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(envelope.MaxFrameBytes)

	ch, err := readControl(ctx, conn, envelope.OpChallenge)
	if err != nil {
		return Challenge{}, err
	}
	var expires time.Time
	if ch.Expires != "" {
		expires, err = time.Parse(time.RFC3339, ch.Expires)
		if err != nil {
			return Challenge{}, fmt.Errorf("relayclient: challenge expires: %w", err)
		}
	}
	return Challenge{Auth: ch.Auth, Expires: expires}, nil
}
