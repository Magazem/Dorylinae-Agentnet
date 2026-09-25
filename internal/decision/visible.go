package decision

import (
	"fmt"
	"strings"
	"unicode"
)

// Visible is the escaping rule of decision.md §Markdown (review 43 M1), the
// rule of review 40's DisplayQuote adapted for plain (not JSON-quoted) text:
// it keeps \n and \t when multiLine is true (the only place they are
// meaningful, inside a fenced block), and replaces every other rune that is
// a C0/C1 control, a Unicode format character (Cf: bidi controls, zero-width
// characters, U+FEFF, tag characters), or not unicode.IsGraphic (space
// excepted), by the visible ASCII escape \u{XXXX} (uppercase hex of the code
// point). The JSON file keeps the exact bytes; this is a view.
func Visible(s string, multiLine bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if multiLine && (r == '\n' || r == '\t') {
			b.WriteRune(r)
			continue
		}
		if isC0C1(r) || unicode.Is(unicode.Cf, r) || (r != ' ' && !unicode.IsGraphic(r)) {
			fmt.Fprintf(&b, `\u{%X}`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isC0C1(r rune) bool {
	return (r >= 0x00 && r <= 0x1F) || (r >= 0x7F && r <= 0x9F)
}
