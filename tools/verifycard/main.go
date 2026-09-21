// Command verifycard verifies a signed Agent Card read from stdin.
//
// It deliberately shares no code with the daemon: only the standard library
// and the format in Docs/protocol/agent-card.md. Input is the signed-card
// envelope ({"card": ..., "signature": ...}) or the output of
// `agentnet identity --json`; other top-level members are ignored.
//
//	agentnet identity --json | go run ./tools/verifycard
//
// Exit codes: 0 signature valid, 1 signature invalid, 2 unreadable or malformed input.
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
	"unicode/utf16"
	"unicode/utf8"
)

const (
	domain     = "dorylinae-agent-card-v1\n"
	maxInput   = 1 << 20
	maxSafeInt = 1 << 53
)

var (
	utf8BOM = []byte{0xEF, 0xBB, 0xBF}

	b64        = base64.RawURLEncoding
	intPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

	errInvalid   = errors.New("signature does not verify")
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
	data = bytes.TrimPrefix(data, utf8BOM) // Windows PowerShell 5.1 prepends one when piping
	name, pub, err := verify(data)
	switch {
	case err == nil:
		_, _ = fmt.Fprintf(stdout, "OK %q %s\n", name, pub)
		return 0
	case errors.Is(err, errInvalid):
		_, _ = fmt.Fprintf(stderr, "verifycard: INVALID: %v\n", err)
		return 1
	default:
		_, _ = fmt.Fprintf(stderr, "verifycard: %v\n", err)
		return 2
	}
}

// verify returns the card name and public key when the signature is valid.
func verify(data []byte) (name, pubKey string, err error) {
	doc, err := parse(data)
	if err != nil {
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
	pubKey, _ = card["public_key"].(string)
	pub, err := b64.DecodeString(pubKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return "", "", fmt.Errorf("%w: card.public_key must be 32 bytes, base64url without padding", errMalformed)
	}
	sig, err := b64.DecodeString(sigText)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return "", "", fmt.Errorf("%w: signature must be 64 bytes, base64url without padding", errMalformed)
	}
	var canon bytes.Buffer
	if err := canonical(&canon, card); err != nil {
		return "", "", fmt.Errorf("%w: %w", errMalformed, err)
	}
	if !ed25519.Verify(pub, append([]byte(domain), canon.Bytes()...), sig) {
		return "", "", errInvalid
	}
	name, _ = card["name"].(string)
	return name, pubKey, nil
}

// parse decodes one JSON document, rejecting invalid UTF-8, duplicate keys and trailing data.
func parse(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("input is not valid UTF-8")
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
