// Command verifycard verifies a signed Agent Card read from stdin.
//
// It deliberately shares no code with the daemon: only the standard library
// and the format in Docs/protocol/agent-card.md. It performs every step of
// §Verification, the schema step included. Input is the signed-card envelope
// ({"card": ..., "signature": ...}) or the output of `agentnet identity
// --json`; other top-level members are ignored, but they are still parsed
// under the strict rules (rules 4 and 6-8).
//
//	agentnet identity --json | go run ./tools/verifycard
//
// One leading UTF-8 byte order mark is stripped from stdin before parsing.
// That is a transport allowance of this tool only (Windows PowerShell 5.1
// prepends one when piping); the spec treats a BOM as malformed.
//
// Exit codes: 0 valid; 1 invalid (the signature does not verify, or it
// verifies but the card does not match the v1 schema); 2 unreadable or
// malformed input (Verification steps 1-3: strict parse, strict base64url,
// canonical form).
package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	domain     = "dorylinae-agent-card-v1\n"
	maxInput   = 1 << 20
	maxSafeInt = 1 << 53
	maxText    = 128
	alphabet   = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
)

var (
	utf8BOM = []byte{0xEF, 0xBB, 0xBF}

	b64        = base64.RawURLEncoding
	intPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
	createdRE  = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)

	errInvalid   = errors.New("signature does not verify")
	errSchema    = errors.New("card does not match the v1 schema")
	errMalformed = errors.New("malformed input")
)

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr))
}

func run(stdin io.Reader, stdout, stderr io.Writer) int {
	data, err := io.ReadAll(io.LimitReader(stdin, maxInput+1))
	if err != nil || len(data) > maxInput {
		_, _ = fmt.Fprintln(stderr, "verifycard: cannot read input (or larger than 1 MiB)")
		return 2
	}
	data = bytes.TrimPrefix(data, utf8BOM) // transport allowance, see the package comment
	name, pub, err := verify(data)
	switch {
	case err == nil:
		_, _ = fmt.Fprintf(stdout, "OK %q %s\n", name, pub)
		return 0
	case errors.Is(err, errInvalid), errors.Is(err, errSchema):
		_, _ = fmt.Fprintf(stderr, "verifycard: INVALID: %v\n", err)
		return 1
	default:
		_, _ = fmt.Fprintf(stderr, "verifycard: %v\n", err)
		return 2
	}
}

// verify returns the card name and public key when the card is valid.
func verify(data []byte) (name, pubKey string, err error) {
	// Step 1: the strict parse of the whole envelope, and rule 4 on every number.
	doc, err := parse(data)
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", errMalformed, err)
	}
	if err := canonical(&bytes.Buffer{}, doc); err != nil {
		return "", "", fmt.Errorf("%w: %w", errMalformed, err)
	}
	top, ok := doc.(map[string]any)
	if !ok {
		return "", "", fmt.Errorf("%w: envelope must be a JSON object", errMalformed)
	}
	card, ok := top["card"].(map[string]any)
	if !ok {
		return "", "", fmt.Errorf("%w: missing card object", errMalformed)
	}
	sigText, ok := top["signature"].(string)
	if !ok {
		return "", "", fmt.Errorf("%w: missing signature", errMalformed)
	}
	// Step 2: strict base64url.
	pubKey, _ = card["public_key"].(string)
	pub, err := strictB64(pubKey, ed25519.PublicKeySize)
	if err != nil {
		return "", "", fmt.Errorf("%w: card.public_key must be 32 bytes, strict base64url: %w", errMalformed, err)
	}
	sig, err := strictB64(sigText, ed25519.SignatureSize)
	if err != nil {
		return "", "", fmt.Errorf("%w: signature must be 64 bytes, strict base64url: %w", errMalformed, err)
	}
	// Step 3: the canonical form of the card as parsed generically.
	var canon bytes.Buffer
	if err := canonical(&canon, card); err != nil {
		return "", "", fmt.Errorf("%w: %w", errMalformed, err)
	}
	// Step 4: the signature.
	if !ed25519.Verify(pub, append([]byte(domain), canon.Bytes()...), sig) {
		return "", "", errInvalid
	}
	// Step 5: the schema, on the same generic object, by exact member name.
	if err := schema(card); err != nil {
		return "", "", fmt.Errorf("%w: %w", errSchema, err)
	}
	name, _ = card["name"].(string)
	return name, pubKey, nil
}

// strictB64 decodes exactly n bytes of base64url without padding. The length,
// the alphabet and the unused low bits of the last character are checked on
// the string first: the standard decoder, even with Strict, skips CR and LF.
func strictB64(s string, n int) ([]byte, error) {
	want := (n*8 + 5) / 6
	if len(s) != want {
		return nil, fmt.Errorf("%d characters, want %d", len(s), want)
	}
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(alphabet, s[i]) < 0 {
			return nil, fmt.Errorf("character %d is outside the base64url alphabet", i)
		}
	}
	unused := want*6 - n*8
	if strings.IndexByte(alphabet, s[len(s)-1])%(1<<unused) != 0 {
		return nil, errors.New("non-zero trailing bits")
	}
	b, err := b64.DecodeString(s)
	if err != nil || len(b) != n {
		return nil, errors.New("does not decode")
	}
	return b, nil
}

// schema checks agent-card.md §Card: exactly the six card members and the
// three skill members, by exact name, with their types and limits.
func schema(card map[string]any) error {
	if err := exactMembers(card, "version", "name", "public_key", "harness", "skills", "created"); err != nil {
		return fmt.Errorf("card: %w", err)
	}
	if v, ok := card["version"].(json.Number); !ok || v.String() != "1" {
		return errors.New("version must be the integer 1")
	}
	if err := text(card, "name", 1); err != nil {
		return err
	}
	if err := text(card, "harness", 1); err != nil {
		return err
	}
	skills, ok := card["skills"].([]any)
	if !ok {
		return errors.New("skills must be an array")
	}
	for i, el := range skills {
		sk, ok := el.(map[string]any)
		if !ok {
			return fmt.Errorf("skills[%d] must be an object", i)
		}
		if err := exactMembers(sk, "id", "name", "description"); err != nil {
			return fmt.Errorf("skills[%d]: %w", i, err)
		}
		for _, m := range []struct {
			name string
			min  int
		}{{"id", 1}, {"name", 1}, {"description", 0}} {
			if err := text(sk, m.name, m.min); err != nil {
				return fmt.Errorf("skills[%d]: %w", i, err)
			}
		}
	}
	created, ok := card["created"].(string)
	if !ok || !createdRE.MatchString(created) {
		return errors.New("created must be YYYY-MM-DDThh:mm:ssZ")
	}
	const layout = "2006-01-02T15:04:05Z"
	if t, err := time.Parse(layout, created); err != nil || t.Format(layout) != created {
		return errors.New("created is not a real date and time")
	}
	return nil
}

// exactMembers requires obj to have exactly the named members.
func exactMembers(obj map[string]any, names ...string) error {
	for _, n := range names {
		if _, ok := obj[n]; !ok {
			return fmt.Errorf("member %q is missing", n)
		}
	}
	if len(obj) != len(names) {
		return fmt.Errorf("%d members, want exactly %d", len(obj), len(names))
	}
	return nil
}

// text checks a text member (agent-card.md §Card): min-128 code points, no
// code point of category Cc and no U+FFFD.
func text(obj map[string]any, member string, minLen int) error {
	s, ok := obj[member].(string)
	if !ok {
		return fmt.Errorf("%s must be a string", member)
	}
	if n := utf8.RuneCountInString(s); n < minLen || n > maxText {
		return fmt.Errorf("%s must be %d-%d characters", member, minLen, maxText)
	}
	for _, r := range s {
		if r <= 0x1F || (r >= 0x7F && r <= 0x9F) || r == 0xFFFD {
			return fmt.Errorf("%s contains U+%04X", member, r)
		}
	}
	return nil
}

// parse decodes one JSON document under the strict parse (agent-card.md
// rules 6-8): valid UTF-8, surrogate escapes only in high+low pairs, no
// duplicate keys, no trailing data.
func parse(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("input is not valid UTF-8")
	}
	if err := surrogates(data); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := value(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after JSON value")
	}
	return v, nil
}

func value(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, isDelim := tok.(json.Delim)
	if !isDelim {
		return tok, nil
	}
	if d == '[' {
		arr := []any{}
		for dec.More() {
			el, err := value(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, el)
		}
		_, err := dec.Token()
		return arr, err
	}
	obj := map[string]any{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := kt.(string)
		if _, dup := obj[key]; dup {
			return nil, fmt.Errorf("duplicate object key %q", key)
		}
		if obj[key], err = value(dec); err != nil {
			return nil, err
		}
	}
	_, err = dec.Token()
	return obj, err
}

// canonical writes the deterministic JSON form (RFC 8785 subset, see the spec).
func canonical(w *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		w.WriteString("null")
	case bool:
		w.WriteString(strconv.FormatBool(x))
	case string:
		quote(w, x)
	case json.Number:
		s := x.String()
		n, err := strconv.ParseInt(s, 10, 64)
		if !intPattern.MatchString(s) || s == "-0" || err != nil || n >= maxSafeInt || n <= -maxSafeInt {
			return fmt.Errorf("number %q is not a canonical integer", s)
		}
		w.WriteString(s)
	case []any:
		w.WriteByte('[')
		for i, el := range x {
			if i > 0 {
				w.WriteByte(',')
			}
			if err := canonical(w, el); err != nil {
				return err
			}
		}
		w.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return less16(keys[i], keys[j]) })
		w.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				w.WriteByte(',')
			}
			quote(w, k)
			w.WriteByte(':')
			if err := canonical(w, x[k]); err != nil {
				return err
			}
		}
		w.WriteByte('}')
	default:
		return fmt.Errorf("unsupported JSON value %T", v)
	}
	return nil
}

func less16(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

func quote(w *bytes.Buffer, s string) {
	const hex = "0123456789abcdef"
	w.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			w.WriteString(`\"`)
		case r == '\\':
			w.WriteString(`\\`)
		case r == '\b':
			w.WriteString(`\b`)
		case r == '\t':
			w.WriteString(`\t`)
		case r == '\n':
			w.WriteString(`\n`)
		case r == '\f':
			w.WriteString(`\f`)
		case r == '\r':
			w.WriteString(`\r`)
		case r < 0x20:
			w.WriteString(`\u00`)
			w.WriteByte(hex[r>>4])
			w.WriteByte(hex[r&0xf])
		default:
			w.WriteRune(r)
		}
	}
	w.WriteByte('"')
}

// surrogates enforces rule 7: an escape of a surrogate code unit must be a
// high one immediately followed by the escape of a low one. It walks the raw
// bytes and tracks whether it is inside a string, so an escaped backslash
// followed by "ud800" is not taken for an escape.
func surrogates(data []byte) error {
	in := false
	for i := 0; i < len(data); i++ {
		switch {
		case !in:
			in = data[i] == '"'
		case data[i] == '"':
			in = false
		case data[i] == '\\':
			if i+1 < len(data) && data[i+1] == 'u' {
				u, ok := hexAt(data, i+2)
				if ok && u >= 0xDC00 && u <= 0xDFFF {
					return fmt.Errorf("lone low surrogate escape at byte %d", i)
				}
				if ok && u >= 0xD800 && u <= 0xDBFF {
					lo, lok := hexAt(data, i+8)
					// lok means data[i+8:i+12] exists, so i+6 and i+7 do too.
					if !lok || data[i+6] != '\\' || data[i+7] != 'u' || lo < 0xDC00 || lo > 0xDFFF {
						return fmt.Errorf("lone high surrogate escape at byte %d", i)
					}
					i += 11
					continue
				}
			}
			i++ // the escaped character
		}
	}
	return nil
}

// hexAt parses the four hex digits at data[at:at+4].
func hexAt(data []byte, at int) (int, bool) {
	if at < 0 || at+4 > len(data) {
		return 0, false
	}
	for _, c := range data[at : at+4] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", rune(c)) {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(string(data[at:at+4]), 16, 16)
	return int(n), err == nil
}
