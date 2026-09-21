package mail

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

// This file follows Docs/protocol/agent-card.md §Canonical serialisation. It
// mirrors the unexported helpers in internal/agentcard, which are not exported
// yet; keep the two in step.

// maxSafeInt is 2^53, the largest magnitude a JSON number may have here.
const maxSafeInt = 1 << 53

var intPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// parseStrict decodes one JSON document into generic values (map[string]any,
// []any, string, json.Number, bool, nil). It rejects invalid UTF-8, duplicate
// object keys and trailing data.
func parseStrict(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("input is not valid UTF-8")
	}
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

// canonical returns the deterministic form of a generic value.
func canonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := canonicalize(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

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
