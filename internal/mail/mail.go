// Package mail seals and opens application mail between paired daemons: an
// Ed25519-signed message, HPKE-sealed to the recipient's mailbox key. The
// specification is Docs/protocol/mail.md; change that first.
//
// This package is pure crypto and validation. Mailbox key storage (1.0b),
// dedupe and ack (1.0d) and the outbox (1.0e) live elsewhere and reach it
// through the Peers and Keys interfaces.
package mail

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hpke"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// Wire constants from Docs/protocol/mail.md.
const (
	// MaxMailPlaintext is the largest canonical signed plaintext.
	MaxMailPlaintext = 716800
	// MinPayload is the shortest valid binary payload: version, key_id, enc, tag.
	MinPayload = 1 + KeyIDLen + encLen + tagLen
	// MaxPayload is the longest valid binary payload.
	MaxPayload = MinPayload + MaxMailPlaintext
	// KeyIDLen is the length of a raw mailbox key_id.
	KeyIDLen = 8

	// PayloadVersion is the first payload byte.
	PayloadVersion = 0x01

	// MaxAge and MaxSkew bound msg.created against the receiver clock.
	MaxAge  = 30 * 24 * time.Hour
	MaxSkew = 10 * time.Minute

	encLen  = 32
	tagLen  = 16
	msgTag  = "dorylinae-mail-v1\n"
	annTag  = "dorylinae-mailbox-key-v1\n"
	timeFmt = "2006-01-02T15:04:05Z"
)

var (
	b64u = base64.RawURLEncoding.Strict() // rejects non-canonical trailing bits

	idPattern   = regexp.MustCompile(`^m-[0-9a-f]{32}$`)
	kindPattern = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)
)

// KeyID is the raw 8-byte mailbox key identifier, SHA-256(pub)[0:8].
type KeyID [KeyIDLen]byte

// KeyIDOf returns the key_id of an X25519 mailbox public key.
func KeyIDOf(pub []byte) KeyID {
	sum := sha256.Sum256(pub)
	return KeyID(sum[:KeyIDLen])
}

// String is the 16-character lowercase hex form used in JSON.
func (k KeyID) String() string { return hex.EncodeToString(k[:]) }

// NewID returns a fresh mail id: "m-" and 32 lowercase hex characters.
func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return "m-" + hex.EncodeToString(b)
}

// ValidID reports whether s has the mail id format.
func ValidID(s string) bool { return idPattern.MatchString(s) }

// Msg is the verified message object.
type Msg struct {
	V       int
	ID      string
	From    string
	To      string
	Created time.Time
	Kind    string
	Body    map[string]any // generic JSON; numbers are json.Number
}

// SealInput describes one mail to seal.
type SealInput struct {
	Priv       ed25519.PrivateKey // sender identity key
	To         string             // recipient identity key, wire form
	MailboxPub []byte             // recipient's newest X25519 mailbox public key, 32 bytes
	ID         string             // "" picks a fresh one
	Kind       string
	Body       any // JSON-marshalled; integers only. nil means {}
	Created    time.Time
}

// Sealed is the output of Seal.
type Sealed struct {
	ID      string
	KeyID   KeyID
	Signed  []byte // canonical signed plaintext, kept by the outbox for re-sealing
	Payload []byte // binary envelope payload (the envelope carries it as base64)
}

// Seal signs and seals one mail. HPKE draws a random ephemeral key, so the
// result is not reproducible.
func Seal(in SealInput) (Sealed, error) {
	if len(in.Priv) != ed25519.PrivateKeySize {
		return Sealed{}, errors.New("mail: bad identity private key")
	}
	if len(in.MailboxPub) != encLen {
		return Sealed{}, errors.New("mail: mailbox public key must be 32 bytes")
	}
	if !kindPattern.MatchString(in.Kind) {
		return Sealed{}, errors.New("mail: kind must be 1-64 characters from [a-z0-9._-]")
	}
	if in.ID == "" {
		in.ID = NewID()
	}
	if !ValidID(in.ID) {
		return Sealed{}, errors.New("mail: bad id format")
	}
	if _, err := decodeKey(in.To); err != nil {
		return Sealed{}, fmt.Errorf("mail: bad recipient key: %w", err)
	}
	from := b64u.EncodeToString(in.Priv.Public().(ed25519.PublicKey))

	body := in.Body
	if body == nil {
		body = map[string]any{}
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return Sealed{}, fmt.Errorf("mail: marshal body: %w", err)
	}
	genBody, err := agentcard.ParseStrict(rawBody)
	if err != nil {
		return Sealed{}, fmt.Errorf("mail: body: %w", err)
	}
	if _, ok := genBody.(map[string]any); !ok {
		return Sealed{}, errors.New("mail: body must be a JSON object")
	}
	msg := map[string]any{
		"v":       json.Number("1"),
		"id":      in.ID,
		"from":    from,
		"to":      in.To,
		"created": in.Created.UTC().Truncate(time.Second).Format(timeFmt),
		"kind":    in.Kind,
		"body":    genBody,
	}
	canonMsg, err := agentcard.CanonicalValue(msg)
	if err != nil {
		return Sealed{}, fmt.Errorf("mail: canonicalize msg: %w", err)
	}
	sig := ed25519.Sign(in.Priv, append([]byte(msgTag), canonMsg...))
	plain, err := agentcard.CanonicalValue(map[string]any{"msg": msg, "sig": b64u.EncodeToString(sig)})
	if err != nil {
		return Sealed{}, fmt.Errorf("mail: canonicalize signed: %w", err)
	}
	if len(plain) > MaxMailPlaintext {
		return Sealed{}, fmt.Errorf("mail: plaintext is %d bytes, over the %d limit", len(plain), MaxMailPlaintext)
	}

	kid := KeyIDOf(in.MailboxPub)
	ecPub, err := ecdh.X25519().NewPublicKey(in.MailboxPub)
	if err != nil {
		return Sealed{}, fmt.Errorf("mail: mailbox public key: %w", err)
	}
	pk, err := hpke.NewDHKEMPublicKey(ecPub)
	if err != nil {
		return Sealed{}, fmt.Errorf("mail: hpke public key: %w", err)
	}
	enc, sender, err := hpke.NewSender(pk, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), buildInfo(from, in.To, kid))
	if err != nil {
		return Sealed{}, fmt.Errorf("mail: hpke sender: %w", err)
	}
	ct, err := sender.Seal([]byte(in.ID), plain)
	if err != nil {
		return Sealed{}, fmt.Errorf("mail: hpke seal: %w", err)
	}
	payload := make([]byte, 0, 1+KeyIDLen+len(enc)+len(ct))
	payload = append(payload, PayloadVersion)
	payload = append(payload, kid[:]...)
	payload = append(payload, enc...)
	payload = append(payload, ct...)
	return Sealed{ID: in.ID, KeyID: kid, Signed: plain, Payload: payload}, nil
}

// buildInfo is the HPKE info string: tag, from, to and the raw key_id.
func buildInfo(from, to string, kid KeyID) []byte {
	info := make([]byte, 0, len(msgTag)+len(from)+len(to)+2+KeyIDLen)
	info = append(info, msgTag...)
	info = append(info, from...)
	info = append(info, '\n')
	info = append(info, to...)
	info = append(info, '\n')
	return append(info, kid[:]...)
}

func decodeKey(s string) (ed25519.PublicKey, error) {
	raw, err := b64u.DecodeString(s)
	if err != nil || len(s) != 43 || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("not a base64url Ed25519 public key")
	}
	return ed25519.PublicKey(raw), nil
}
