// Package notify shows local desktop notifications for request lifecycle
// events (Docs/protocol/notify.md). Settings live in the shared settings
// table (migration 10). The webhook channel (1.8b) is not in this package.
package notify

import (
	"strings"
	"unicode/utf8"
)

// bidi controls and paragraph separators that Clean strips, alongside every
// C0/C1 control character (Docs/protocol/notify.md §Text and sanitising).
var cleanRunes = map[rune]bool{
	0x200E: true, 0x200F: true,
	0x202A: true, 0x202B: true, 0x202C: true, 0x202D: true, 0x202E: true,
	0x2066: true, 0x2067: true, 0x2068: true, 0x2069: true,
	0x2028: true, 0x2029: true,
}

// Clean sanitises peer-supplied text before it is used in any notification:
// every control character, bidi control and U+2028/U+2029 becomes a space,
// runs of spaces collapse, the result is trimmed, and it is truncated to max
// code points with a trailing "…" when cut (Docs/protocol/notify.md §Text and
// sanitising).
func Clean(s string, maxLen int) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isControl(r) || cleanRunes[r] {
			r = ' '
		}
		b.WriteRune(r)
	}
	collapsed := collapseSpaces(b.String())
	return truncate(collapsed, maxLen)
}

func isControl(r rune) bool {
	return (r >= 0x0000 && r <= 0x001F) || (r >= 0x007F && r <= 0x009F)
}

func collapseSpaces(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 || utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n == maxLen-1 {
			break
		}
		b.WriteRune(r)
		n++
	}
	b.WriteRune('…')
	return b.String()
}
