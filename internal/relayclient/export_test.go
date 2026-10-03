package relayclient

import (
	"context"
	"net"
	"time"
)

// AuthVersion exposes authVersion to the external tests.
var AuthVersion = authVersion

// WithDialer makes every connection of cfg go to addr, whatever the URL's
// host, so a test can use a non-loopback URL against a local server.
func WithDialer(cfg Config, addr string) Config {
	cfg.dialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	return cfg
}

// WithDialerAs is WithDialer, except the connection reports peer as its
// remote address: a name such as "localhost" that the hosts file or DNS
// resolved to another machine.
func WithDialerAs(cfg Config, addr string, peer *net.TCPAddr) Config {
	cfg.dialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		c, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return peerConn{c, peer}, nil
	}
	return cfg
}

type peerConn struct {
	net.Conn
	peer *net.TCPAddr
}

func (c peerConn) RemoteAddr() net.Addr { return c.peer }

// WithBackoffTiming sets how long a connection must stay up after ready
// before the backoff resets, and the least delay after a close with 1013.
func WithBackoffTiming(cfg Config, stableAfter, tryAgainFloor time.Duration) Config {
	cfg.stableAfter, cfg.tryAgainFloor = stableAfter, tryAgainFloor
	return cfg
}

// MarkRedial sets the flag Reconnect sets, without closing the connection,
// so a test can end the session from the relay side with the flag stale
// (review 101 L3).
func MarkRedial(c *Client) {
	c.mu.Lock()
	c.redial = true
	c.mu.Unlock()
}
