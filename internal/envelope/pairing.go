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
	// MaxMboxBytes is the largest mailbox key announcement the relay carries.
	MaxMboxBytes = 4 << 10
	// MaxRefLen is the longest correlation ref accepted on a pairing request.
	MaxRefLen = 128
	// PairCodeLen is the number of significant characters in a pairing code.
	PairCodeLen = 10
	// PairLookupLen is the length of the v2 lookup, the only part sent to the relay.
	PairLookupLen = 5
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
	return normalizePair(s, PairCodeLen)
}

// NormalizePairLookup canonicalises a v2 lookup with the same rules as
// NormalizePairCode and requires PairLookupLen characters.
func NormalizePairLookup(s string) (lookup string, ok bool) {
	return normalizePair(s, PairLookupLen)
}

func normalizePair(s string, n int) (string, bool) {
	out := make([]byte, 0, n)
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
		if !strings.ContainsRune(PairAlphabet, rune(c)) || len(out) == n {
			return "", false
		}
		out = append(out, c)
	}
	if len(out) != n {
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

// CheckMbox is CheckCard for a mailbox key announcement, with MaxMboxBytes.
func CheckMbox(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return errors.New("mbox is required")
	}
	if len(raw) > MaxMboxBytes {
		return errors.New("mbox is too large")
	}
	if raw[0] != '{' {
		return errors.New("mbox must be a JSON object")
	}
	return nil
}
