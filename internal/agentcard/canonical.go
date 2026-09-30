package agentcard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// maxSafeInt is 2^53, the largest magnitude a JSON number may have here.
const maxSafeInt = 1 << 53

var intPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// ParseStrict decodes one JSON document into generic values (map[string]any,
// []any, string, json.Number, bool, nil) under the strict parse of
// agent-card.md §Canonical serialisation: it rejects invalid UTF-8 (rule 6),
// surrogate escapes that do not form a high+low pair (rule 7), duplicate
// object keys compared after unescaping with no folding (rule 8), and
// trailing data. Numbers are kept as json.Number; CanonicalValue applies
// rule 4.
//
// Code rule (review 55 R55-019): never decode data that passed ParseStrict
// with encoding/json into a struct. encoding/json folds member names (ASCII
// case, U+212A, U+017F) and keeps the last duplicate, so it can return values
// that were never bound under that exact name. Read the generic value by
// exact name instead.
func ParseStrict(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("input is not valid UTF-8")
	}
	if err := checkSurrogates(data); err != nil {
		return nil, err
	}
	return parseDocument(data)
}

// parseLegacy is ParseStrict without rule 7: a lone surrogate escape decodes
// to U+FFFD, as it did before R55-F23. Only RescueStored uses it, to read
// cards stored before the strict parse (review 68b F3).
func parseLegacy(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("input is not valid UTF-8")
	}
	return parseDocument(data)
}

// checkSurrogates enforces rule 7 on the raw bytes: a \u escape in D800-DFFF
// must be a high surrogate immediately followed by a \u escape of a low one.
// It tracks the in-string state, so an escaped backslash followed by "u" is
// not an escape. Malformed escapes are left to the JSON decoder.
func checkSurrogates(data []byte) error {
	inString := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			continue
		}
		if c == '"' {
			inString = false
			continue
		}
		if c != '\\' {
			continue
		}
		if i+1 >= len(data) {
			return nil
		}
		if data[i+1] != 'u' {
			i++ // one escaped character
			continue
		}
		v, ok := hex4(data, i+2)
		if !ok {
			return nil // the decoder reports the bad escape
		}
		start := i
		i += 5 // the last hex digit
		switch {
		case v >= 0xDC00 && v <= 0xDFFF:
			return fmt.Errorf("lone surrogate escape at byte %d", start)
		case v >= 0xD800 && v <= 0xDBFF:
			if i+2 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
				return fmt.Errorf("lone surrogate escape at byte %d", start)
			}
			lo, ok := hex4(data, i+3)
			if !ok || lo < 0xDC00 || lo > 0xDFFF {
				return fmt.Errorf("lone surrogate escape at byte %d", start)
			}
			i += 6
		}
	}
	return nil
}

// hex4 reads four hex digits, either case, at data[at:].
func hex4(data []byte, at int) (rune, bool) {
	if at+4 > len(data) {
		return 0, false
	}
	var v rune
	for _, c := range data[at : at+4] {
		switch {
		case c >= '0' && c <= '9':
			v = v<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			v = v<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			v = v<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return v, true
}

func parseDocument(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after JSON value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil // string, json.Number, bool or nil
	}
	if d == '[' {
		arr := []any{}
		for dec.More() {
			el, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, el)
		}
		if _, err := dec.Token(); err != nil { // ']'
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return arr, nil
	}
	obj := map[string]any{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		key, _ := kt.(string)
		if _, dup := obj[key]; dup {
			return nil, fmt.Errorf("duplicate object key %q", key)
		}
		if obj[key], err = parseValue(dec); err != nil {
			return nil, err
		}
	}
	if _, err := dec.Token(); err != nil { // '}'
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return obj, nil
}

// canonicalize writes the deterministic form of a generic value; the rules are
// in Docs/protocol/agent-card.md.
func canonicalize(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		buf.WriteString(strconv.FormatBool(x))
	case string:
		writeString(buf, x)
	case json.Number:
		s := x.String()
		if !intPattern.MatchString(s) || s == "-0" {
			return fmt.Errorf("number %q is not a canonical integer", s)
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n >= maxSafeInt || n <= -maxSafeInt {
			return fmt.Errorf("number %q is out of range", s)
		}
		buf.WriteString(s)
	case []any:
		buf.WriteByte('[')
		for i, el := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := canonicalize(buf, el); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return utf16Less(keys[i], keys[j]) })
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeString(buf, k)
			buf.WriteByte(':')
			if err := canonicalize(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("unsupported JSON value of type %T", v)
	}
	return nil
}

// utf16Less orders strings by UTF-16 code units, as RFC 8785 requires.
func utf16Less(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

const hexDigits = "0123456789abcdef"

func writeString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\t':
			buf.WriteString(`\t`)
		case '\n':
			buf.WriteString(`\n`)
		case '\f':
			buf.WriteString(`\f`)
		case '\r':
			buf.WriteString(`\r`)
		default:
			if r < 0x20 {
				buf.WriteString(`\u00`)
				buf.WriteByte(hexDigits[r>>4])
				buf.WriteByte(hexDigits[r&0xf])
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}

// CanonicalValue returns the deterministic JSON form (agent-card.md §Canonical
// serialisation) of a value produced by ParseStrict. It fails on numbers that
// are not integers within +-2^53.
func CanonicalValue(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := canonicalize(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
