package request

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"time"
)

var testB64u = base64.RawURLEncoding

// testKey returns a deterministic, syntactically valid 43-character identity
// key (Docs/protocol/mail.md: base64url without padding, 43 characters).
func testKey(seed byte) string {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = seed + byte(i)
	}
	priv := ed25519.NewKeyFromSeed(s)
	return testB64u.EncodeToString(priv.Public().(ed25519.PublicKey))
}

var (
	testFrom = testKey(1)
	testTo   = testKey(2)
)

const (
	testID   = "r-0123456789abcdef0123456789abcdef"
	testTeam = "t-fedcba9876543210fedcba9876543210"
)

var testCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// validRequest returns a minimal, entirely valid Request. Tests mutate a
// field on a copy and check Validate rejects (or accepts) it.
func validRequest() *Request {
	return &Request{
		V:         1,
		ID:        testID,
		From:      testFrom,
		To:        testTo,
		Team:      testTeam,
		Type:      TypeTask,
		Title:     "A valid title",
		Brief:     "What: a thing.\nWhy: reasons.\nDone when: done.",
		Urgency:   UrgencyNormal,
		Artifacts: nil,
		Created:   testCreated,
	}
}

// repeatRunes returns a string of n copies of r.
func repeatRunes(r rune, n int) string { return strings.Repeat(string(r), n) }

// asciiFill returns a string of n 'a' bytes.
func asciiFill(n int) string { return strings.Repeat("a", n) }
