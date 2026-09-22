package mail

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
)

// presenceIDPattern is the presence envelope id format, Docs/protocol/presence.md §Envelope.
var presenceIDPattern = regexp.MustCompile(`^p-[0-9a-f]{32}$`)

// NewPresenceID returns a fresh presence id: "p-" and 32 lowercase hex characters.
func NewPresenceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return "p-" + hex.EncodeToString(b)
}

// ValidPresenceID reports whether s has the presence id format.
func ValidPresenceID(s string) bool { return presenceIDPattern.MatchString(s) }

// SealPresence signs and seals one presence message: like Seal, but the id has
// the presence format ("p-" + 32 hex) instead of "m-", reusing the mail seal
// and signature (Docs/protocol/presence.md §Envelope). in.Kind must be
// "presence".
func SealPresence(in SealInput) (Sealed, error) {
	if in.Kind != "presence" {
		return Sealed{}, errors.New("mail: SealPresence requires kind presence")
	}
	if in.ID == "" {
		in.ID = NewPresenceID()
	}
	if !ValidPresenceID(in.ID) {
		return Sealed{}, errors.New("mail: bad presence id format")
	}
	return sealMsg(in)
}
