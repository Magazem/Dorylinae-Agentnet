package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ErrBadWebhookURL means the configured URL fails the static checks
// (Docs/protocol/notify.md §Configuration).
var ErrBadWebhookURL = errors.New("bad_webhook")

// ErrBlockedAddress means a dial-time SSRF check refused the connection
// (Docs/protocol/notify.md §Configuration "Dial-time address check").
var ErrBlockedAddress = errors.New("blocked_address")

// maxWebhookURLBytes is the URL length cap (Docs/protocol/notify.md
// §Configuration).
const maxWebhookURLBytes = 2048

// ValidateWebhookURL applies the static checks on a candidate webhook URL:
// scheme, size, no userinfo, and http only to a literal loopback host
// (Docs/protocol/notify.md §Configuration). It does not resolve the name; the
// dial-time check (resolveDial) does that.
func ValidateWebhookURL(raw string) error {
	if len(raw) > maxWebhookURLBytes {
		return fmt.Errorf("%w: over %d bytes", ErrBadWebhookURL, maxWebhookURLBytes)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBadWebhookURL, err)
	}
	if u.User != nil {
		return fmt.Errorf("%w: URL must not contain user info", ErrBadWebhookURL)
	}
	switch u.Scheme {
	case "https":
		if u.Host == "" {
			return fmt.Errorf("%w: missing host", ErrBadWebhookURL)
		}
		return nil
	case "http":
		if !isLiteralLoopbackHost(u.Hostname()) {
			return fmt.Errorf("%w: http:// is only allowed to a loopback host", ErrBadWebhookURL)
		}
		return nil
	default:
		return fmt.Errorf("%w: scheme must be https, or http to loopback", ErrBadWebhookURL)
	}
}

// isLiteralLoopbackHost reports whether host is the literal "localhost",
// an address in 127.0.0.0/8, or "::1" (Docs/protocol/notify.md
// §Configuration: "not a name that resolves there").
func isLiteralLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// checkDialAddress applies the dial-time SSRF policy to one resolved address
// (Docs/protocol/notify.md §Configuration "Dial-time address check"). scheme
// is the URL scheme the request was made with: "http" requests may reach
// loopback only; "https" requests may not reach link-local, unspecified or
// multicast addresses, and, as extra hardening (review 12), not other
// private ranges either.
func checkDialAddress(scheme string, ip net.IP) error {
	if ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("%w: unspecified or multicast address", ErrBlockedAddress)
	}
	if isLinkLocal(ip) {
		return fmt.Errorf("%w: link-local address", ErrBlockedAddress)
	}
	if scheme == "http" {
		if !ip.IsLoopback() {
			return fmt.Errorf("%w: http may only dial loopback", ErrBlockedAddress)
		}
		return nil
	}
	if ip.IsLoopback() {
		return nil
	}
	if ip.IsPrivate() {
		return fmt.Errorf("%w: private address", ErrBlockedAddress)
	}
	return nil
}

func isLinkLocal(ip net.IP) bool {
	// covers 169.254.0.0/16 (incl. the 169.254.169.254 metadata address) and fe80::/10.
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// checkProxyAddress applies the dial-time policy to the address of a proxy
// taken from the environment. The proxy is trusted by the user's
// configuration, so only the base list applies: no link-local, unspecified
// or multicast address (Docs/protocol/notify.md §Configuration: "the check
// applies to the proxy address, and the proxy is trusted by the user's
// configuration"). The private-range hardening of checkDialAddress does not
// apply: a corporate proxy commonly sits on a private address.
func checkProxyAddress(ip net.IP) error {
	if ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("%w: unspecified or multicast proxy address", ErrBlockedAddress)
	}
	if isLinkLocal(ip) {
		return fmt.Errorf("%w: link-local proxy address", ErrBlockedAddress)
	}
	return nil
}

// Resolver looks up the addresses for host, like
// net.Resolver.LookupIPAddr (tests substitute a fake one, e.g. to simulate
// DNS rebinding to a blocked address).
type Resolver func(ctx context.Context, host string) ([]net.IPAddr, error)

func defaultResolver(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// dialFunc matches net.Dialer.DialContext.
type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// resolvingDialer resolves the target itself (rather than leaving it to
// net.Dialer) so that the SSRF check runs against every candidate address
// actually returned by DNS, and the connection goes to exactly the address
// that passed it (Docs/protocol/notify.md §Configuration "Dial-time address
// check", review 12: DNS rebinding). When the transport dials a proxy
// instead of the target (isProxy reports the proxy's host:port), the proxy
// policy of checkProxyAddress applies instead (Docs/protocol/notify.md
// §Configuration: "the check applies to the proxy address").
func resolvingDialer(scheme string, resolve Resolver, isProxy func(addr string) bool, dial dialFunc) dialFunc {
	if resolve == nil {
		resolve = defaultResolver
	}
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		check := func(ip net.IP) error { return checkDialAddress(scheme, ip) }
		if isProxy != nil && isProxy(addr) {
			check = checkProxyAddress
		}
		var candidates []net.IPAddr
		if ip := net.ParseIP(host); ip != nil {
			candidates = []net.IPAddr{{IP: ip}}
		} else {
			candidates, err = resolve(ctx, host)
			if err != nil {
				return nil, err
			}
		}
		var lastErr error
		for _, c := range candidates {
			if err := check(c.IP); err != nil {
				lastErr = err
				continue
			}
			conn, err := dial(ctx, network, net.JoinHostPort(c.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("%w: no addresses for %q", ErrBlockedAddress, host)
		}
		return nil, lastErr
	}
}

// proxyTracker wraps a proxy function and remembers the host:port of every
// proxy it hands to the transport, so that the dialer can tell a proxy dial
// from a target dial.
type proxyTracker struct {
	from func(*http.Request) (*url.URL, error)

	mu    sync.Mutex
	addrs map[string]bool
}

func (p *proxyTracker) proxy(req *http.Request) (*url.URL, error) {
	u, err := p.from(req)
	if u != nil {
		p.mu.Lock()
		if p.addrs == nil {
			p.addrs = map[string]bool{}
		}
		p.addrs[proxyDialAddr(u)] = true
		p.mu.Unlock()
	}
	return u, err
}

func (p *proxyTracker) isProxy(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.addrs[net.JoinHostPort(strings.ToLower(host), port)]
}

// proxyDialAddr is the host:port net/http dials for proxy u (its
// canonicalAddr, with the scheme's default port).
func proxyDialAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

// httpClient builds an http.Client for one delivery attempt: a 10 s timeout,
// no redirects (a 3xx is a permanent failure), TLS verification on, the
// environment proxy, and the SSRF guard applied to every address actually
// dialled. resolve, when non-nil, overrides DNS resolution (tests use this to
// simulate DNS rebinding).
func httpClient(scheme string, resolve Resolver) *http.Client {
	return newHTTPClient(scheme, resolve, http.ProxyFromEnvironment, nil)
}

func newHTTPClient(scheme string, resolve Resolver, proxyFrom func(*http.Request) (*url.URL, error), dial dialFunc) *http.Client {
	tracker := &proxyTracker{from: proxyFrom}
	transport := &http.Transport{
		Proxy:       tracker.proxy,
		DialContext: resolvingDialer(scheme, resolve, tracker.isProxy, dial),
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
