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
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Version is the card schema version produced by this package.
const Version = 1

// domain is prepended to the canonical card before signing.
const domain = "dorylinae-agent-card-v1\n"

const maxTextLen = 128

// Size limits (agent-card.md §Size, review 55 R55-057).
const (
	// MaxCardBytes is the largest signed card envelope, in bytes. Verify
	// checks it before parsing.
	MaxCardBytes = 16384
	// MaxSkills is the most skills a card may declare.
	MaxSkills = 32
)

// parseEnvelope is the parse Verify runs after the size check. It is a
// variable only so a test can count the calls (agent-card.md §Size: an
// oversize document costs no parse).
var parseEnvelope = ParseStrict

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
	// The signature is not known yet; any valid one has the same length.
	if err := checkSize(Signed{Card: c, Signature: strings.Repeat("A", sigChars)}); err != nil {
		return Card{}, err
	}
	return c, nil
}

// sigChars is the length of a signature in strict base64url.
const sigChars = (ed25519.SignatureSize*8 + 5) / 6

// checkSize refuses a card whose envelope is over MaxCardBytes in the form
// internal/identity writes agent-card.json: json.MarshalIndent(s, "", "  "),
// indented and with <, >, &, U+2028 and U+2029 escaped. That is the largest
// form any component emits, so the pairing frame, the stored form and the
// file all fit too (agent-card.md §Size, review 71b F7).
func checkSize(s Signed) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("agentcard: marshal: %w", err)
	}
	if len(b) > MaxCardBytes {
		return fmt.Errorf("agentcard: the signed card is %d bytes, over the limit of %d", len(b), MaxCardBytes)
	}
	return nil
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
	if _, err := decodeStrict(c.PublicKey, ed25519.PublicKeySize); err != nil {
		return errors.New("agentcard: public_key must be 32 bytes, strict base64url without padding")
	}
	if c.Skills == nil {
		return errors.New("agentcard: skills is required")
	}
	if len(c.Skills) > MaxSkills {
		return fmt.Errorf("agentcard: %d skills, at most %d", len(c.Skills), MaxSkills)
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
	s := Signed{Card: c, Signature: b64.EncodeToString(sig)}
	if err := checkSize(s); err != nil {
		return Signed{}, err
	}
	return s, nil
}

// b64Alphabet is the base64url alphabet, in value order.
const b64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

// decodeStrict decodes s as strict base64url of exactly n bytes
// (agent-card.md Verification step 2). The length, the alphabet and the
// unused low bits of the last character are checked on the string before
// decoding, because base64.RawURLEncoding.Strict() still skips CR and LF.
func decodeStrict(s string, n int) ([]byte, error) {
	if want := (n*8 + 5) / 6; len(s) != want {
		return nil, fmt.Errorf("want %d characters, got %d", want, len(s))
	}
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(b64Alphabet, s[i]) < 0 {
			return nil, fmt.Errorf("character %d is not base64url", i)
		}
	}
	unused := len(s)*6 - n*8
	if strings.IndexByte(b64Alphabet, s[len(s)-1])&(1<<unused-1) != 0 {
		return nil, errors.New("non-zero trailing bits")
	}
	b, err := b64.DecodeString(s)
	if err != nil || len(b) != n {
		return nil, errors.New("not strict base64url")
	}
	return b, nil
}

// cardMembers are the exact member names of a v1 card.
var cardMembers = []string{"version", "name", "public_key", "harness", "skills", "created"}

// cardFromGeneric reads the card from the generic object the signature
// covered, by exact member name with exact member counts (agent-card.md
// Verification steps 5 and 6). It never decodes into a struct with
// encoding/json, which folds names and keeps the last duplicate (review 55
// R55-019). The caller still runs validate for the text and time rules.
func cardFromGeneric(card map[string]any) (Card, error) {
	if len(card) != len(cardMembers) {
		return Card{}, fmt.Errorf("card has %d members, want exactly %d", len(card), len(cardMembers))
	}
	for _, n := range cardMembers {
		if _, ok := card[n]; !ok {
			return Card{}, fmt.Errorf("card has no member %q", n)
		}
	}
	if v, ok := card["version"].(json.Number); !ok || v.String() != "1" {
		return Card{}, errors.New("version must be the integer 1")
	}
	c := Card{Version: Version}
	var err error
	for _, f := range []struct {
		name string
		dst  *string
	}{{"name", &c.Name}, {"public_key", &c.PublicKey}, {"harness", &c.Harness}, {"created", &c.Created}} {
		if *f.dst, err = stringMember(card, f.name); err != nil {
			return Card{}, err
		}
	}
	skills, ok := card["skills"].([]any)
	if !ok {
		return Card{}, errors.New("skills must be an array")
	}
	if len(skills) > MaxSkills {
		return Card{}, fmt.Errorf("%d skills, at most %d", len(skills), MaxSkills)
	}
	c.Skills = make([]Skill, 0, len(skills))
	for i, el := range skills {
		sk, ok := el.(map[string]any)
		if !ok {
			return Card{}, fmt.Errorf("skills[%d] must be an object", i)
		}
		if len(sk) != 3 {
			return Card{}, fmt.Errorf("skills[%d] has %d members, want exactly 3", i, len(sk))
		}
		var s Skill
		for _, f := range []struct {
			name string
			dst  *string
		}{{"id", &s.ID}, {"name", &s.Name}, {"description", &s.Description}} {
			if *f.dst, err = stringMember(sk, f.name); err != nil {
				return Card{}, fmt.Errorf("skills[%d]: %w", i, err)
			}
		}
		c.Skills = append(c.Skills, s)
	}
	return c, nil
}

func stringMember(m map[string]any, name string) (string, error) {
	v, ok := m[name]
	if !ok {
		return "", fmt.Errorf("member %q is missing", name)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("member %q must be a string", name)
	}
	return s, nil
}

// Verify checks a signed-card envelope (JSON) as agent-card.md §Verification
// specifies. An envelope over MaxCardBytes is refused before any parsing, and
// a card with more than MaxSkills skills fails the schema. The whole envelope
// is read under the strict parse, and every
// number in it must be a canonical integer, in ignored members too. Members
// other than "card" and "signature" are ignored. The key and the signature
// are strict base64url. The signature is checked over the card as parsed
// generically, so any changed or added member fails. The schema is checked
// after the signature, on the same generic object and by exact member name:
// exactly six card members and three per skill. The returned card holds the
// values read there; the card is never decoded a second time.
func Verify(data []byte) (*Signed, error) {
	if len(data) > MaxCardBytes {
		return nil, fmt.Errorf("agentcard: envelope is %d bytes, over the limit of %d", len(data), MaxCardBytes)
	}
	doc, err := parseEnvelope(data)
	if err != nil {
		return nil, fmt.Errorf("agentcard: %w", err)
	}
	// Rule 4 over the whole envelope (review 68b F2); the bytes are discarded.
	if _, err := CanonicalValue(doc); err != nil {
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
	pub, err := decodeStrict(pubStr, ed25519.PublicKeySize)
	if err != nil {
		return nil, errors.New("agentcard: card.public_key must be 32 bytes, strict base64url without padding")
	}
	sig, err := decodeStrict(sigStr, ed25519.SignatureSize)
	if err != nil {
		return nil, errors.New("agentcard: signature must be 64 bytes, strict base64url without padding")
	}
	canon, err := canonicalBytes(card)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(pub, signingInput(canon), sig) {
		return nil, errors.New("agentcard: signature does not verify")
	}
	c, err := cardFromGeneric(card)
	if err != nil {
		return nil, fmt.Errorf("agentcard: card does not match schema: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &Signed{Card: c, Signature: sigStr}, nil
}

// StoredForm returns the stored and forwarded form of a card envelope: the
// canonical form of exactly {"card", "signature"}, with every other top-level
// member dropped (agent-card.md "Stored and forwarded form", review 55
// R55-073). It does not verify; callers verify the envelope first.
func StoredForm(data []byte) ([]byte, error) {
	doc, err := ParseStrict(data)
	if err != nil {
		return nil, fmt.Errorf("agentcard: %w", err)
	}
	return storedPair(doc)
}

func storedPair(doc any) ([]byte, error) {
	top, ok := doc.(map[string]any)
	if !ok {
		return nil, errors.New("agentcard: envelope must be a JSON object")
	}
	card, cok := top["card"]
	sig, sok := top["signature"]
	if !cok || !sok {
		return nil, errors.New("agentcard: missing card or signature")
	}
	b, err := CanonicalValue(map[string]any{"card": card, "signature": sig})
	if err != nil {
		return nil, fmt.Errorf("agentcard: %w", err)
	}
	return b, nil
}

// RescueStored re-reads a card stored before R55-F23 (review 68 OD-3). It
// parses raw with the legacy parse (no surrogate rule, and numbers outside
// the card are not checked), keeps exactly {card, signature}, canonicalises
// that pair and verifies the result under the current rules. It fails unless
// the card verifies and its public_key equals wantKey. On success it returns
// the canonical stored form; the caller rewrites the row when it differs. So a
// v1 row whose relay added a member holding a lone surrogate escape or a
// fraction is rescued rather than reported (review 68b F3).
func RescueStored(raw []byte, wantKey string) ([]byte, error) {
	doc, err := parseLegacy(raw)
	if err != nil {
		return nil, fmt.Errorf("agentcard: %w", err)
	}
	canon, err := storedPair(doc)
	if err != nil {
		return nil, err
	}
	sc, err := Verify(canon)
	if err != nil {
		return nil, err
	}
	if sc.Card.PublicKey != wantKey {
		return nil, errors.New("agentcard: stored card is for another key")
	}
	return canon, nil
}
