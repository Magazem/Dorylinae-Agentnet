package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// relayAuthVectors is Docs/protocol/envelope.md §Relay auth v2 vector.
type relayAuthVectors struct {
	SeedHex   string `json:"seed_hex"`
	Key       string `json:"key"`
	NonceB64u string `json:"nonce_b64u"`
	Cases     []struct {
		URL           string `json:"url"`
		Origin        string `json:"origin"`
		MessageHex    string `json:"message_hex"`
		SignatureB64u string `json:"signature_b64u"`
	} `json:"cases"`
	AuthFrame string `json:"auth_frame"`
}

// relayAuth recomputes relay auth v2 (Docs/protocol/relay-hosted.md §1):
//
//	"dorylinae-relay-auth-v2\n" ‖ nonce(32) ‖ u16be(len(origin)) ‖ origin
//
// with the origin derived here from each URL by the text's rule, and checks
// the Ed25519 signature byte for byte (Ed25519 is deterministic).
func relayAuth(c *checker, v *vectors) {
	ra := &v.RelayAuth
	b64u := base64.RawURLEncoding
	priv := ed25519.NewKeyFromSeed(mustHex(c, "relay_auth seed_hex", ra.SeedHex))
	pub := priv.Public().(ed25519.PublicKey)
	c.eqs("relay_auth key", b64u.EncodeToString(pub), ra.Key)
	nonce, err := b64u.DecodeString(ra.NonceB64u)
	if err != nil || len(nonce) != 32 {
		c.ok("relay_auth nonce", false, "nonce is not 32 bytes of base64url")
		return
	}
	if len(ra.Cases) < 2 {
		c.ok("relay_auth cases", false, "need at least two cases in vectors.json")
		return
	}
	msgs := make([][]byte, len(ra.Cases))
	for i, tc := range ra.Cases {
		name := fmt.Sprintf("relay_auth case %d (%s)", i, tc.URL)
		origin, err := relayOrigin(tc.URL)
		if err != nil {
			c.ok(name+" origin", false, err.Error())
			continue
		}
		c.eqs(name+" origin", origin, tc.Origin)
		msg := []byte("dorylinae-relay-auth-v2\n")
		msg = append(msg, nonce...)
		msg = binary.BigEndian.AppendUint16(msg, uint16(len(origin))) //nolint:gosec // short test origins
		msg = append(msg, origin...)
		msgs[i] = msg
		c.eq(name+" message", msg, mustHex(c, name+" message_hex", tc.MessageHex))
		sig := ed25519.Sign(priv, msg)
		c.eqs(name+" signature", b64u.EncodeToString(sig), tc.SignatureB64u)
	}

	// The frame carries case 0's signature, labelled v2, for the key.
	var f struct {
		Op        string `json:"op"`
		V         int    `json:"v"`
		PublicKey string `json:"public_key"`
		Signature string `json:"signature"`
	}
	err = json.Unmarshal([]byte(ra.AuthFrame), &f)
	c.ok("relay_auth frame", err == nil && f.Op == "auth" && f.V == 2 && f.PublicKey == ra.Key && f.Signature == ra.Cases[0].SignatureB64u,
		fmt.Sprintf("frame %s does not carry case 0 as v2 (%v)", ra.AuthFrame, err))

	// Negative checks: the binding. Case 0's signature fails for another
	// origin and as a v1 signature over the same nonce.
	sig0, _ := b64u.DecodeString(ra.Cases[0].SignatureB64u)
	for i := 1; i < len(msgs); i++ {
		if ra.Cases[i].Origin != ra.Cases[0].Origin && msgs[i] != nil {
			c.ok(fmt.Sprintf("relay_auth negative: case 0 signature fails for %s", ra.Cases[i].Origin), !ed25519.Verify(pub, msgs[i], sig0), "verified for another origin")
		}
	}
	v1 := append([]byte("dorylinae-relay-auth-v1\n"), nonce...)
	c.ok("relay_auth negative: case 0 signature fails as v1", !ed25519.Verify(pub, v1, sig0), "verified as a v1 signature")
}

// relayOrigin derives the origin of a ws/wss URL by relay-hosted.md §1 for
// ASCII hosts: lowercase(scheme "://" host [":" port]), no trailing dot,
// IPv6 literals in brackets, the default port (wss 443, ws 80) omitted, no
// path, query or user info. Written from the text, not with net/url.
func relayOrigin(raw string) (string, error) {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return "", fmt.Errorf("%q has no scheme", raw)
	}
	scheme = strings.ToLower(scheme)
	def := map[string]string{"ws": "80", "wss": "443"}[scheme]
	if def == "" {
		return "", fmt.Errorf("%q: scheme is not ws or wss", raw)
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if strings.Contains(rest, "@") {
		return "", fmt.Errorf("%q has user info", raw)
	}
	host, port := rest, ""
	if strings.HasPrefix(rest, "[") {
		end := strings.Index(rest, "]")
		if end < 0 {
			return "", fmt.Errorf("%q: unclosed IPv6 literal", raw)
		}
		host, port = rest[:end+1], strings.TrimPrefix(rest[end+1:], ":")
	} else if i := strings.LastIndex(rest, ":"); i >= 0 {
		host, port = rest[:i], rest[i+1:]
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return "", fmt.Errorf("%q: bad port", raw)
		}
		if port = strconv.FormatUint(n, 10); port == def {
			port = ""
		}
	}
	if host == "" {
		return "", fmt.Errorf("%q has no host", raw)
	}
	if port != "" {
		return scheme + "://" + host + ":" + port, nil
	}
	return scheme + "://" + host, nil
}
