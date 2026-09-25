package debate

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// Debate text (Docs/protocol/debate.md §Messages, review 43 M7 and L1): in
// every debate string "control characters" are C0, U+007F and C1, and
// U+2028/U+2029 are refused as well. The C0/DEL and C1 checks are the request
// package's (hasControl, hasC1), shared rather than duplicated.

const (
	lineSeparator      = rune(0x2028)
	paragraphSeparator = rune(0x2029)
)

// badDebateText reports whether s has a debate-text control character other
// than those in allowed ("\n\t" for multi-line text).
func badDebateText(s, allowed string) bool {
	return request.HasControl(s, allowed) || request.HasC1(s) || strings.ContainsRune(s, lineSeparator) || strings.ContainsRune(s, paragraphSeparator)
}

// checkLine is Line(limit): valid UTF-8, 1-limit code points, no control
// characters at all, no leading or trailing white space (the free-text rule).
func checkLine(field, s string, limit int) error {
	if !utf8.ValidString(s) {
		return fieldErr(field, "must be valid UTF-8")
	}
	if n := utf8.RuneCountInString(s); n < 1 || n > limit {
		return fieldErr(field, "must be 1-%d code points", limit)
	}
	if badDebateText(s, "") {
		return fieldErr(field, "must be a single line without control characters (only argument may hold multi-line text)")
	}
	return checkTrimmed(field, s)
}

func checkTrimmed(field, s string) error {
	first, _ := utf8.DecodeRuneInString(s)
	last, _ := utf8.DecodeLastRuneInString(s)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return fieldErr(field, "must not start or end with white space")
	}
	return nil
}

// checkArgument is the one multi-line string: 1-4000 code points, \n and \t
// allowed, no other control character.
func checkArgument(field, s string) error {
	if !utf8.ValidString(s) {
		return fieldErr(field, "must be valid UTF-8")
	}
	if n := utf8.RuneCountInString(s); n < 1 || n > maxArgument {
		return fieldErr(field, "must be 1-%d code points", maxArgument)
	}
	if badDebateText(s, "\n\t") {
		return fieldErr(field, "must not contain control characters other than \\n and \\t")
	}
	return nil
}

// ValidateConstraintText checks a human constraint's text
// (Docs/protocol/debate.md §Human constraints, review 43 H3): Line(500) and
// visible characters only, every rune graphic or U+0020 and none of format
// class Cf (bidi controls, zero-width characters, U+FEFF, tag characters).
// Errors name the field "text".
func ValidateConstraintText(s string) error {
	if err := checkLine("text", s, maxConstraint); err != nil {
		return err
	}
	for _, r := range s {
		if (r != ' ' && !unicode.IsGraphic(r)) || unicode.Is(unicode.Cf, r) {
			return fieldErr("text", "must hold visible characters only (U+%04X is not)", r)
		}
	}
	return nil
}

// ValidateTopic checks the topic of a debate request (its brief): the request
// brief rules (1-16384 bytes, \n and \t the only C0 controls) plus the debate
// text rule (no C1, U+2028 or U+2029). Errors name the field "brief".
func ValidateTopic(s string) error {
	if !utf8.ValidString(s) {
		return fieldErr("brief", "must be valid UTF-8")
	}
	if len(s) < minTopicBytes || len(s) > maxTopicBytes {
		return fieldErr("brief", "must be %d-%d bytes", minTopicBytes, maxTopicBytes)
	}
	if badDebateText(s, "\n\t") {
		return fieldErr("brief", "must not contain control characters other than \\n and \\t")
	}
	return nil
}
