package debate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

// commitTag is the commitment's domain separator (Docs/protocol/debate.md
// §Commit-reveal).
const commitTag = "dorylinae-debate-commit-v1\n"

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidNonce reports whether s is a nonce in wire form: exactly 64 lowercase
// hex characters.
func ValidNonce(s string) bool { return hex64.MatchString(s) }

// NewNonce draws a fresh nonce: 32 bytes from crypto/rand as 64 lowercase
// hex. Only the initiator's daemon draws it, never the agent.
func NewNonce() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return hex.EncodeToString(b)
}

// Commitment computes
//
//	lowercase-hex SHA-256("dorylinae-debate-commit-v1\n" ‖ sid ‖ "\n" ‖ A ‖ "\n" ‖ nonce ‖ "\n" ‖ canonical(position))
//
// with sid the derived session id, a the initiator's identity key in wire
// form, nonce in wire form and position the canonical bytes of A's opening
// position (Docs/protocol/debate.md §Commit-reveal). sid, a and nonce have
// fixed lengths and precede the one variable field, so the preimage parses
// one way only.
func Commitment(sid, a, nonce string, position []byte) string {
	h := sha256.New()
	h.Write([]byte(commitTag))
	h.Write([]byte(sid))
	h.Write([]byte("\n"))
	h.Write([]byte(a))
	h.Write([]byte("\n"))
	h.Write([]byte(nonce))
	h.Write([]byte("\n"))
	h.Write(position)
	return hex.EncodeToString(h.Sum(nil))
}
