package envelope

import "crypto/rand"

// Binding limits, see Docs/protocol/accounts.md.
const (
	// BindCodeLen is the number of significant characters of a bind user
	// code: 8 characters of PairAlphabet (Crockford base32), 40 bits.
	BindCodeLen = 8
	// MaxDeviceLen is the longest device label a bind_start may carry.
	MaxDeviceLen = 32
	// MaxOSLen is the longest os a bind_start may carry.
	MaxOSLen = 16
)

// NewBindCode returns a fresh normalised bind user code from crypto/rand.
func NewBindCode() (string, error) {
	raw := make([]byte, BindCodeLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i, b := range raw {
		raw[i] = PairAlphabet[b&31] // 256 is a multiple of 32: no modulo bias
	}
	return string(raw), nil
}

// FormatBindCode renders a normalised bind code as XXXX-XXXX.
func FormatBindCode(code string) string {
	if len(code) != BindCodeLen {
		return code
	}
	return code[:BindCodeLen/2] + "-" + code[BindCodeLen/2:]
}

// NormalizeBindCode canonicalises a typed bind code with the pairing code
// rules (case, '-' and spaces ignored, O->0, I,L->1).
func NormalizeBindCode(s string) (code string, ok bool) {
	return normalizePair(s, BindCodeLen)
}

// ValidDevice reports whether s is a device label a bind_start may carry:
// 1 to MaxDeviceLen characters of [A-Za-z0-9 ._-].
func ValidDevice(s string) bool {
	return len(s) >= 1 && len(s) <= MaxDeviceLen && allBytes(s, func(c byte) bool {
		return isAlnum(c) || c == ' ' || c == '.' || c == '_' || c == '-'
	})
}

// ValidOS reports whether s is an os a bind_start may carry: 1 to MaxOSLen
// characters of [a-z0-9] (a runtime.GOOS value).
func ValidOS(s string) bool {
	return len(s) >= 1 && len(s) <= MaxOSLen && allBytes(s, func(c byte) bool {
		return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
	})
}

func isAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
