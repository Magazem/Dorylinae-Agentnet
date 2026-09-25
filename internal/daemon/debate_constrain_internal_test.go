package daemon

import (
	"strings"
	"testing"
)

// The approval summary shows the constraint text in full with the
// DisplayQuote rule (review 43 H3): even if a text got past the visible-only
// check, bidi controls, zero-width and tag characters would show as escapes.
func TestConstraintSummaryDisplayQuote(t *testing.T) {
	long := strings.Repeat("é", 500)
	s := constraintSummary("bob", "s-0123456789abcdef0123456789abcdef", long)
	if !strings.Contains(s, `"`+long+`"`) || !strings.Contains(s, "bob") || !strings.Contains(s, "s-0123456789abcdef0123456789abcdef") {
		t.Fatalf("summary does not show the full text, peer and session: %q", s)
	}
	hidden := "No new" + string(rune(0x200b)) + string(rune(0x202e)) + "dep" + string(rune(0xfeff)) + string(rune(0xe0041))
	s = constraintSummary("bob", "s-0123456789abcdef0123456789abcdef", hidden)
	for _, esc := range []string{"200b", "202e", "feff", "db40" + string(rune(0x5c)) + "udc41"} {
		esc = string(rune(0x5c)) + "u" + esc
		if !strings.Contains(s, esc) {
			t.Errorf("summary lacks %s: %q", esc, s)
		}
	}
	for _, r := range []rune{0x200b, 0x202e, 0xfeff, 0xe0041} {
		if strings.ContainsRune(s, r) {
			t.Errorf("summary holds U+%04X raw", r)
		}
	}
}
