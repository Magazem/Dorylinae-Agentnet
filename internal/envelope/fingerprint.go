package envelope

import (
	"crypto/sha256"
	"strings"
)

// FingerprintLen is the number of characters in a key fingerprint (100 bits).
const FingerprintLen = 20

const fingerprintDomain = "dorylinae-fingerprint-v1\n"

// Fingerprint is fp(pub) from Docs/protocol/pairing.md: the first
// FingerprintLen characters of the Crockford base32 of
// SHA-256("dorylinae-fingerprint-v1\n" || pub), without spaces.
func Fingerprint(pub []byte) string {
	h := sha256.New()
	h.Write([]byte(fingerprintDomain))
	h.Write(pub)
	digest := h.Sum(nil)
	out := make([]byte, FingerprintLen)
	var acc, bits uint
	n := 0
	for _, b := range digest {
		acc = acc<<8 | uint(b)
		bits += 8
		for bits >= 5 && n < FingerprintLen {
			bits -= 5
			out[n] = PairAlphabet[(acc>>bits)&31]
			n++
		}
	}
	return string(out)
}

// KeyFingerprint is Fingerprint for a base64url public key as used in Agent
// Cards and on the wire.
func KeyFingerprint(key string) (string, error) {
	pub, err := ParseKey(key)
	if err != nil {
		return "", err
	}
	return Fingerprint(pub), nil
}

// FormatFingerprint renders a 20-character fingerprint as five groups of four
// separated by spaces, e.g. "2ED9 TGVE R471 63MC C451".
func FormatFingerprint(fp string) string {
	if len(fp) != FingerprintLen {
		return fp
	}
	parts := make([]string, 0, FingerprintLen/4)
	for i := 0; i < FingerprintLen; i += 4 {
		parts = append(parts, fp[i:i+4])
	}
	return strings.Join(parts, " ")
}

// NormalizeFingerprint canonicalises user input like a pairing code (case,
// '-' and spaces ignored, aliases O->0 and I,L->1). ok is false unless the
// result is exactly FingerprintLen alphabet characters.
func NormalizeFingerprint(s string) (fp string, ok bool) {
	out := make([]byte, 0, FingerprintLen)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '-' || c == ' ':
			continue
		case c >= 'a' && c <= 'z':
			c -= 'a' - 'A'
		}
		switch c {
		case 'O':
			c = '0'
		case 'I', 'L':
			c = '1'
		}
		if !strings.ContainsRune(PairAlphabet, rune(c)) || len(out) == FingerprintLen {
			return "", false
		}
		out = append(out, c)
	}
	if len(out) != FingerprintLen {
		return "", false
	}
	return string(out), true
}
