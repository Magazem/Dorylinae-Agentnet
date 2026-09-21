package peers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"

	"golang.org/x/crypto/argon2"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// Pairing v2 primitives, Docs/protocol/pairing.md §Code format and §Keys and tags.

const (
	// CodeLen is the number of characters of a v2 code: 5 lookup and 10 secret.
	CodeLen = envelope.PairCodeV2Len
	// lookupLen and secretLen split a v2 code.
	lookupLen = envelope.PairLookupLen
	secretLen = CodeLen - lookupLen

	saltPrefix    = "dorylinae-pair-v2\n"
	transcriptTag = "dorylinae-pair-v2-transcript\n"
	usedCodeTag   = "dorylinae-pair-used-v2\n"

	// Argon2id parameters (RFC 9106 second recommendation with one lane).
	kdfTime    = 3
	kdfMemKiB  = 64 * 1024
	kdfThreads = 1
	kdfKeyLen  = 32

	roleIssuerTag   = "issuer\n"
	roleRedeemerTag = "redeemer\n"

	tagLen = sha256.Size
)

// b64u is the strict base64url used for pairing fields: no padding, and
// trailing bits must be zero, so every value has exactly one encoding.
var b64u = base64.RawURLEncoding.Strict()

// NewCode returns a fresh v2 code: CodeLen characters of envelope.PairAlphabet
// from crypto/rand. Characters 0-4 are the lookup, 5-14 the secret.
func NewCode() (string, error) {
	raw := make([]byte, CodeLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i, b := range raw {
		raw[i] = envelope.PairAlphabet[b&31] // 256 is a multiple of 32: no modulo bias
	}
	return string(raw), nil
}

// FormatCode renders a normalised v2 code as LLLLL-SSSSS-SSSSS.
func FormatCode(code string) string {
	if len(code) != CodeLen {
		return code
	}
	return code[:5] + "-" + code[5:10] + "-" + code[10:]
}

// NormalizeCode canonicalises user input like envelope.NormalizePairCode and
// tells a v2 code (15 characters) from a v1 code (10). Any other length or a
// character outside the alphabet gives ok=false.
func NormalizeCode(s string) (code string, v2 bool, ok bool) {
	if c, ok := envelope.NormalizePairCodeV2(s); ok {
		return c, true, true
	}
	if c, ok := envelope.NormalizePairCode(s); ok {
		return c, false, true
	}
	return "", false, false
}

// deriveK is K = Argon2id(secret, "dorylinae-pair-v2\n" || lookup).
func deriveK(lookup string, secret []byte) []byte {
	salt := append([]byte(saltPrefix), lookup...)
	return argon2.IDKey(secret, salt, kdfTime, kdfMemKiB, kdfThreads, kdfKeyLen)
}

// usedCodeHash is SHA-256("dorylinae-pair-used-v2\n" || code).
func usedCodeHash(code string) []byte {
	h := sha256.Sum256([]byte(usedCodeTag + code))
	return h[:]
}

// canonicalPart returns canonical({first: <first>, signature: <signature>}) from
// the generically parsed envelope raw, dropping any other member. first is
// "card" for an Agent Card and "announcement" for a mailbox announcement.
func canonicalPart(raw []byte, first string) ([]byte, error) {
	v, err := agentcard.ParseStrict(raw)
	if err != nil {
		return nil, err
	}
	top, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("not a JSON object")
	}
	a, aok := top[first]
	s, sok := top["signature"]
	if !aok || !sok {
		return nil, errors.New("missing " + first + " or signature")
	}
	return agentcard.CanonicalValue(map[string]any{first: a, "signature": s})
}

// transcript is T = SHA-256(tag || lookup || u32(len)||card_I || card_R || mbox_I || mbox_R).
func transcript(lookup string, cardI, cardR, mboxI, mboxR []byte) [sha256.Size]byte {
	h := sha256.New()
	h.Write([]byte(transcriptTag))
	h.Write([]byte(lookup))
	for _, part := range [][]byte{cardI, cardR, mboxI, mboxR} {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(part))) //nolint:gosec // parts are at most 16 KiB
		h.Write(n[:])
		h.Write(part)
	}
	var t [sha256.Size]byte
	h.Sum(t[:0])
	return t
}

func tagFor(k []byte, role string, t [sha256.Size]byte) []byte {
	mac := hmac.New(sha256.New, k)
	mac.Write([]byte(role))
	mac.Write(t[:])
	return mac.Sum(nil)
}

func issuerTag(k []byte, t [sha256.Size]byte) []byte   { return tagFor(k, roleIssuerTag, t) }
func redeemerTag(k []byte, t [sha256.Size]byte) []byte { return tagFor(k, roleRedeemerTag, t) }

// confirmPayload is the plaintext payload of a pair.confirm envelope.
func confirmPayload(lookup string, tag []byte) []byte {
	// Members in canonical (sorted) order: lookup, tag, v.
	b, _ := json.Marshal(struct {
		Lookup string `json:"lookup"`
		Tag    string `json:"tag"`
		V      int    `json:"v"`
	}{lookup, b64u.EncodeToString(tag), 2})
	return b
}

// parseConfirm strictly parses a pair.confirm payload and returns its lookup and tag.
func parseConfirm(payload []byte) (lookup string, tag []byte, err error) {
	v, err := agentcard.ParseStrict(payload)
	if err != nil {
		return "", nil, err
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 3 {
		return "", nil, errors.New("pair.confirm must be an object with lookup, tag and v")
	}
	if n, ok := m["v"].(json.Number); !ok || n.String() != "2" {
		return "", nil, errors.New("pair.confirm v must be 2")
	}
	lookup, ok = m["lookup"].(string)
	if !ok {
		return "", nil, errors.New("pair.confirm lookup must be a string")
	}
	ts, ok := m["tag"].(string)
	if !ok {
		return "", nil, errors.New("pair.confirm tag must be a string")
	}
	tag, err = b64u.DecodeString(ts)
	if err != nil || len(tag) != tagLen {
		return "", nil, errors.New("pair.confirm tag must be 32 bytes, base64url without padding")
	}
	return lookup, tag, nil
}
