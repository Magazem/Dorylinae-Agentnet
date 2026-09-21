package envelope

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
)

// Pairing limits, see Docs/protocol/pairing.md.
const (
	// MaxCardBytes is the largest Agent Card the relay carries in a pairing frame.
	MaxCardBytes = 16 << 10
	// MaxRefLen is the longest correlation ref accepted on a pairing request.
	MaxRefLen = 128
	// PairCodeLen is the number of significant characters in a pairing code.
	PairCodeLen = 10
	// PairAlphabet is the Crockford base32 alphabet used for pairing codes.
	PairAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

// NewPairCode returns a fresh normalised pairing code: PairCodeLen characters
// from PairAlphabet, 50 bits of entropy from crypto/rand.
func NewPairCode() (string, error) {
	raw := make([]byte, PairCodeLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i, b := range raw {
		raw[i] = PairAlphabet[b&31] // 256 is a multiple of 32: no modulo bias
	}
	return string(raw), nil
}

// FormatPairCode renders a normalised code as XXXXX-XXXXX.
func FormatPairCode(code string) string {
	if len(code) != PairCodeLen {
		return code
	}
	return code[:PairCodeLen/2] + "-" + code[PairCodeLen/2:]
}

// NormalizePairCode canonicalises user input: case-insensitive, '-' and spaces
// ignored, Crockford aliases O->0 and I,L->1. ok is false if the result is not
// a well-formed code.
func NormalizePairCode(s string) (code string, ok bool) {
	out := make([]byte, 0, PairCodeLen)
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
		if !strings.ContainsRune(PairAlphabet, rune(c)) || len(out) == PairCodeLen {
			return "", false
		}
		out = append(out, c)
	}
	if len(out) != PairCodeLen {
		return "", false
	}
	return string(out), true
}

// CheckCard checks that raw looks like a card the relay may carry: a JSON
// object of bounded size. The relay does not verify it; daemons do.
func CheckCard(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return errors.New("card is required")
	}
	if len(raw) > MaxCardBytes {
		return errors.New("card is too large")
	}
	if raw[0] != '{' {
		return errors.New("card must be a JSON object")
	}
	return nil
}
