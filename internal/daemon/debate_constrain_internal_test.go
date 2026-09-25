package daemon

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/notify"
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

// Review 46 H2: the approval window shows the summary in full for the
// longest constraint, even when every character is escaped (json.Marshal
// writes "<" as <) and the peer's name is at its 40-code-point bound.
func TestConstraintSummaryFitsWindow(t *testing.T) {
	text := strings.Repeat("<", 500)
	s := constraintSummary(strings.Repeat("n", 40), "s-0123456789abcdef0123456789abcdef", text)
	if n := utf8.RuneCountInString(s); n > notify.MaxWindowSummary {
		t.Fatalf("worst-case summary is %d code points, the window shows %d", n, notify.MaxWindowSummary)
	}
	if got := notify.Clean(s, notify.MaxWindowSummary); got != s {
		t.Fatal("the window would cut or alter the worst-case summary")
	}
}
