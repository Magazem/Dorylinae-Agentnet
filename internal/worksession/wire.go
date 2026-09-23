package worksession

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// timeFmt is the wire time format: RFC 3339 UTC, "Z", whole seconds
// (Docs/protocol/work-session.md, conventions of request.md).
const timeFmt = "2006-01-02T15:04:05Z"

func wireTime(t time.Time) string { return t.UTC().Truncate(time.Second).Format(timeFmt) }

func storeTime(t time.Time) string { return t.UTC().Format(mail.StoreTimeFmt) }

func parseWireTime(s string) time.Time {
	t, _ := time.Parse(timeFmt, s)
	return t
}

func parseStoreTime(s string) time.Time {
	t, _ := time.Parse(mail.StoreTimeFmt, s)
	return t
}

// strictMembers rejects any key of body not in allowed.
func strictMembers(body map[string]any, allowed ...string) error {
	set := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		set[a] = true
	}
	for k := range body {
		if !set[k] {
			return badBody("unknown member %q", k)
		}
	}
	return nil
}

func decodeString(body map[string]any, field string, optional bool) (string, error) {
	raw, ok := body[field]
	if !ok {
		if optional {
			return "", nil
		}
		return "", badBody("%s is required", field)
	}
	s, ok := raw.(string)
	if !ok {
		return "", badBody("%s must be a string", field)
	}
	if !optional && s == "" {
		return "", badBody("%s must not be empty", field)
	}
	return s, nil
}

// decodeOptionalNonEmpty decodes an optional string member that, when
// present, must not be empty (optional members are absent, never null or
// empty).
func decodeOptionalNonEmpty(body map[string]any, field string) (string, error) {
	raw, ok := body[field]
	if !ok {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", badBody("%s must be a string", field)
	}
	if s == "" {
		return "", badBody("%s must be absent, not empty", field)
	}
	return s, nil
}

func decodeInt(body map[string]any, field string) (int, error) {
	raw, ok := body[field]
	if !ok {
		return 0, badBody("%s is required", field)
	}
	n, ok := raw.(json.Number)
	if !ok {
		return 0, badBody("%s must be an integer", field)
	}
	i64, err := n.Int64()
	if err != nil {
		return 0, badBody("%s must be an integer", field)
	}
	return int(i64), nil
}

func decodeTime(field string, raw any) (time.Time, error) {
	s, ok := raw.(string)
	if !ok {
		return time.Time{}, badBody("%s must be a string", field)
	}
	t, err := time.Parse(timeFmt, s)
	if err != nil || t.Format(timeFmt) != s {
		return time.Time{}, badBody("%s must be RFC 3339 UTC with Z and whole seconds", field)
	}
	return t, nil
}

// checkCodePoints validates s is valid UTF-8, its code point count is in
// [lo, hi], and it has no control characters except those in allowed.
func checkCodePoints(field, s string, lo, hi int, allowed string) error {
	if !utf8.ValidString(s) {
		return fieldErr(field, "must be valid UTF-8")
	}
	n := utf8.RuneCountInString(s)
	if n < lo || n > hi {
		return fieldErr(field, "must be %d-%d code points", lo, hi)
	}
	if hasControl(s, allowed) {
		return fieldErr(field, "must not contain control characters")
	}
	return nil
}

func hasControl(s, allowed string) bool {
	for _, r := range s {
		if r == utf8.RuneError {
			return true
		}
		if (r <= 0x1F || r == 0x7F) && !strings.ContainsRune(allowed, r) {
			return true
		}
	}
	return false
}
