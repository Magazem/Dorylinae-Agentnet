// Package agentcard defines the signed A2A-style Agent Card: its schema, the
// deterministic JSON used for signing, and signature verification. The wire
// format is specified in Docs/protocol/agent-card.md.
package agentcard

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"
)

// Version is the card schema version produced by this package.
const Version = 1

// domain is prepended to the canonical card before signing.
const domain = "dorylinae-agent-card-v1\n"

const maxTextLen = 128

// Strict: no padding, and trailing bits must be zero, so a value has one encoding.
var b64 = base64.RawURLEncoding.Strict()

// Skill is one declared skill.
type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Card is the signed payload.
type Card struct {
	Version   int     `json:"version"`
	Name      string  `json:"name"`
	PublicKey string  `json:"public_key"`
	Harness   string  `json:"harness"`
	Skills    []Skill `json:"skills"`
	Created   string  `json:"created"`
}

// Signed is a card with its signature (the wire envelope).
type Signed struct {
	Card      Card   `json:"card"`
	Signature string `json:"signature"`
}

// New builds a validated card for pub. created is truncated to whole seconds UTC.
func New(pub ed25519.PublicKey, name, harness string, skills []Skill, created time.Time) (Card, error) {
	if len(pub) != ed25519.PublicKeySize {
		return Card{}, errors.New("agentcard: bad public key length")
	}
	if skills == nil {
		skills = []Skill{}
	}
	c := Card{
		Version:   Version,
		Name:      name,
		PublicKey: b64.EncodeToString(pub),
		Harness:   harness,
		Skills:    skills,
		Created:   created.UTC().Truncate(time.Second).Format(time.RFC3339),
	}
	if err := c.validate(); err != nil {
		return Card{}, err
	}
	return c, nil
}

func (c Card) validate() error {
	if c.Version != Version {
		return fmt.Errorf("agentcard: unsupported version %d", c.Version)
	}
	if err := checkText("name", c.Name, true); err != nil {
		return err
	}
	if err := checkText("harness", c.Harness, true); err != nil {
		return err
	}
	pub, err := b64.DecodeString(c.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("agentcard: public_key must be 32 bytes, base64url without padding")
	}
	if c.Skills == nil {
		return errors.New("agentcard: skills is required")
	}
	for i, s := range c.Skills {
		if err := checkText(fmt.Sprintf("skills[%d].id", i), s.ID, true); err != nil {
			return err
		}
		if err := checkText(fmt.Sprintf("skills[%d].name", i), s.Name, true); err != nil {
			return err
		}
		if err := checkText(fmt.Sprintf("skills[%d].description", i), s.Description, false); err != nil {
			return err
		}
	}
	t, err := time.Parse(time.RFC3339, c.Created)
	if err != nil || t.UTC().Format(time.RFC3339) != c.Created {
		return errors.New("agentcard: created must be RFC 3339 UTC with Z, whole seconds")
	}
	return nil
}

func checkText(field, s string, required bool) error {
	if required && s == "" {
		return fmt.Errorf("agentcard: %s is required", field)
	}
	if utf8.RuneCountInString(s) > maxTextLen {
		return fmt.Errorf("agentcard: %s longer than %d characters", field, maxTextLen)
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return fmt.Errorf("agentcard: %s contains a control or invalid character", field)
		}
	}
	return nil
}

// Canonical returns the deterministic JSON form of c.
func Canonical(c Card) ([]byte, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("agentcard: marshal: %w", err)
	}
	v, err := ParseStrict(raw)
	if err != nil {
		return nil, fmt.Errorf("agentcard: %w", err)
	}
	return canonicalBytes(v)
}

func canonicalBytes(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := canonicalize(&buf, v); err != nil {
		return nil, fmt.Errorf("agentcard: canonicalize: %w", err)
	}
	return buf.Bytes(), nil
}

func signingInput(canonical []byte) []byte {
	return append([]byte(domain), canonical...)
}

// Sign validates c and signs it with priv, whose public half must match c.
func Sign(priv ed25519.PrivateKey, c Card) (Signed, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return Signed{}, errors.New("agentcard: bad private key length")
	}
	if err := c.validate(); err != nil {
		return Signed{}, err
	}
	if c.PublicKey != b64.EncodeToString(priv.Public().(ed25519.PublicKey)) {
		return Signed{}, errors.New("agentcard: card public key does not match signing key")
	}
	canon, err := Canonical(c)
	if err != nil {
		return Signed{}, err
	}
	sig := ed25519.Sign(priv, signingInput(canon))
	return Signed{Card: c, Signature: b64.EncodeToString(sig)}, nil
}

// Verify checks a signed-card envelope (JSON). Members other than "card" and
// "signature" are ignored. The signature is checked over the card as parsed
// generically, so any changed or added field fails; the schema is checked
// after the signature.
func Verify(data []byte) (*Signed, error) {
	doc, err := ParseStrict(data)
	if err != nil {
		return nil, fmt.Errorf("agentcard: %w", err)
	}
	top, ok := doc.(map[string]any)
	if !ok {
		return nil, errors.New("agentcard: envelope must be a JSON object")
	}
	card, ok := top["card"].(map[string]any)
	if !ok {
		return nil, errors.New("agentcard: missing card object")
	}
	sigStr, ok := top["signature"].(string)
	if !ok {
		return nil, errors.New("agentcard: missing signature")
	}
	pubStr, _ := card["public_key"].(string)
	pub, err := b64.DecodeString(pubStr)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("agentcard: card.public_key must be 32 bytes, base64url without padding")
	}
	sig, err := b64.DecodeString(sigStr)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, errors.New("agentcard: signature must be 64 bytes, base64url without padding")
	}
	canon, err := canonicalBytes(card)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(pub, signingInput(canon), sig) {
		return nil, errors.New("agentcard: signature does not verify")
	}
	var c Card
	dec := json.NewDecoder(bytes.NewReader(canon))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("agentcard: card does not match schema: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &Signed{Card: c, Signature: sigStr}, nil
}
