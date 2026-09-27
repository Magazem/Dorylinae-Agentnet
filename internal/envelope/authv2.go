package envelope

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// Relay authentication versions, as listed in a challenge's "auth" member
// (Docs/protocol/relay-hosted.md §1). A challenge without the member offers v1
// only (a Phase 0–3 relay).
const (
	AuthV1 = "v1"
	AuthV2 = "v2"
)

const authDomainV2 = "dorylinae-relay-auth-v2\n"

// ErrAuthV1Refused is returned by VerifyAuthV1 on a relay that requires v2,
// when the v1 signature itself is valid (an old daemon, not an attacker).
var ErrAuthV1Refused = errors.New("this relay requires relay auth v2; update agentnet")

// AuthMessageV2 is the byte string a daemon signs to answer a challenge with
// relay auth v2:
//
//	"dorylinae-relay-auth-v2\n" ‖ nonce(32) ‖ u16be(len(origin)) ‖ origin
//
// origin must already be in canonical form (see Origin).
func AuthMessageV2(nonce []byte, origin string) ([]byte, error) {
	if len(nonce) != NonceSize {
		return nil, errors.New("auth v2: nonce must be 32 bytes")
	}
	if origin == "" || len(origin) > 0xffff {
		return nil, errors.New("auth v2: origin is empty or too long")
	}
	msg := make([]byte, 0, len(authDomainV2)+NonceSize+2+len(origin))
	msg = append(msg, authDomainV2...)
	msg = append(msg, nonce...)
	msg = binary.BigEndian.AppendUint16(msg, uint16(len(origin))) //nolint:gosec // bounded above
	return append(msg, origin...), nil
}

// SignAuthV2 builds the v2 auth frame answering nonce for the relay at origin.
func SignAuthV2(pub ed25519.PublicKey, nonce []byte, origin string, sign func([]byte) ([]byte, error)) (Control, error) {
	msg, err := AuthMessageV2(nonce, origin)
	if err != nil {
		return Control{}, err
	}
	sig, err := sign(msg)
	if err != nil {
		return Control{}, err
	}
	return Control{Op: OpAuth, V: 2, PublicKey: KeyString(pub), Signature: b64.EncodeToString(sig)}, nil
}

// VerifyAuthV2 checks a v2 auth frame against the challenge nonce and the
// relay's own origins (canonical form), and returns the authenticated key.
func VerifyAuthV2(c Control, nonce []byte, origins []string) (ed25519.PublicKey, error) {
	if c.Op != OpAuth || c.V != 2 {
		return nil, errors.New("not a v2 auth frame")
	}
	pub, sig, err := authParts(c)
	if err != nil {
		return nil, err
	}
	for _, o := range origins {
		msg, err := AuthMessageV2(nonce, o)
		if err == nil && ed25519.Verify(pub, msg, sig) {
			return pub, nil
		}
	}
	return nil, errors.New("signature does not verify for this relay's origin")
}

func authParts(c Control) (ed25519.PublicKey, []byte, error) {
	pub, err := ParseKey(c.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	sig, err := b64.DecodeString(c.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, nil, errors.New("malformed signature")
	}
	return pub, sig, nil
}

// Origin returns the canonical origin of a relay URL, the value auth v2 binds:
// lowercase(scheme "://" host [":" port]), where the scheme is ws or wss, an
// IDN host is in its ASCII (punycode) form, a trailing dot is dropped, an IPv6
// literal keeps its brackets, and the port is omitted exactly when it is the
// scheme default (wss 443, ws 80). Path and query are not part of the origin;
// user info is refused.
func Origin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("origin: %w", err)
	}
	return OriginOf(u)
}

// OriginOf is Origin for a parsed URL.
func OriginOf(u *url.URL) (string, error) {
	scheme := strings.ToLower(u.Scheme)
	var defPort string
	switch scheme {
	case "ws":
		defPort = "80"
	case "wss":
		defPort = "443"
	default:
		return "", fmt.Errorf("origin: scheme must be ws or wss, got %q", u.Scheme)
	}
	if u.User != nil {
		return "", errors.New("origin: user info is not allowed in a relay URL")
	}
	if u.Opaque != "" {
		return "", errors.New("origin: relay URL has no host")
	}
	host, err := canonicalHost(u.Hostname())
	if err != nil {
		return "", err
	}
	port := u.Port()
	if port == "" && strings.HasSuffix(u.Host, ":") {
		return "", errors.New("origin: empty port")
	}
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return "", fmt.Errorf("origin: bad port %q", port)
		}
		port = strconv.FormatUint(n, 10)
		if port == defPort {
			port = ""
		}
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, nil
}

func canonicalHost(h string) (string, error) {
	if strings.Contains(h, "%") {
		return "", errors.New("origin: IPv6 zones are not allowed")
	}
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return "", errors.New("origin: relay URL has no host")
	}
	if ip := net.ParseIP(h); ip != nil {
		return strings.ToLower(h), nil
	}
	for i := 0; i < len(h); i++ {
		if h[i] >= 0x80 {
			a, err := idna.Lookup.ToASCII(h)
			if err != nil {
				return "", fmt.Errorf("origin: host %q: %w", h, err)
			}
			return strings.ToLower(a), nil
		}
	}
	return strings.ToLower(h), nil
}

// IsLoopbackHost reports whether host (no port, no brackets) names this
// machine: "localhost", 127.0.0.0/8 or ::1.
func IsLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
