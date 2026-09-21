package mail

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// AnnouncementLifetime is how long a fresh mailbox key is announced for
// (Docs/protocol/mail.md §Lifecycle).
const AnnouncementLifetime = 14 * 24 * time.Hour

// Announcement is a verified mailbox key announcement.
type Announcement struct {
	Identity string    // Ed25519 identity key, wire form
	KeyID    KeyID     // SHA-256(Pub)[0:8]
	Pub      []byte    // X25519 mailbox public key, 32 bytes
	Created  time.Time // whole seconds, UTC
	NotAfter time.Time
}

// SignAnnouncement returns the canonical signed announcement of the X25519
// mailbox public key pub, made by the identity key whose public half is
// identity and whose signatures sign produces:
// {"announcement":{...},"signature":"..."}. created is truncated to seconds.
func SignAnnouncement(identity ed25519.PublicKey, sign func(msg []byte) ([]byte, error), pub []byte, created time.Time) ([]byte, error) {
	if len(identity) != ed25519.PublicKeySize {
		return nil, errors.New("mail: bad identity public key")
	}
	if len(pub) != encLen {
		return nil, errors.New("mail: mailbox public key must be 32 bytes")
	}
	created = created.UTC().Truncate(time.Second)
	ann := map[string]any{
		"v":         json.Number("1"),
		"identity":  b64u.EncodeToString(identity),
		"key_id":    KeyIDOf(pub).String(),
		"pub":       b64u.EncodeToString(pub),
		"created":   created.Format(timeFmt),
		"not_after": created.Add(AnnouncementLifetime).Format(timeFmt),
	}
	canon, err := agentcard.CanonicalValue(ann)
	if err != nil {
		return nil, fmt.Errorf("mail: canonicalize announcement: %w", err)
	}
	sig, err := sign(append([]byte(annTag), canon...))
	if err != nil {
		return nil, fmt.Errorf("mail: sign announcement: %w", err)
	}
	return agentcard.CanonicalValue(map[string]any{"announcement": ann, "signature": b64u.EncodeToString(sig)})
}

// ParseAnnouncement runs the checks of Docs/protocol/mail.md §Announcement on
// raw (strict parse, identity must equal identity, signature, times against
// now). It returns the announcement and its canonical wire form, which is what
// the pairing transcript and peers.mailbox_keys hold.
func ParseAnnouncement(raw []byte, identity string, now time.Time) (*Announcement, []byte, error) {
	v, err := agentcard.ParseStrict(raw)
	if err != nil {
		return nil, nil, err
	}
	if err := verifyAnnouncement(v, identity, now); err != nil {
		return nil, nil, err
	}
	canon, err := agentcard.CanonicalValue(v)
	if err != nil {
		return nil, nil, err
	}
	ann := v.(map[string]any)["announcement"].(map[string]any)
	pub, _ := b64u.DecodeString(ann["pub"].(string))
	created, _ := parseTime(ann["created"])
	notAfter, _ := parseTime(ann["not_after"])
	return &Announcement{
		Identity: identity, KeyID: KeyIDOf(pub), Pub: pub, Created: created, NotAfter: notAfter,
	}, canon, nil
}
