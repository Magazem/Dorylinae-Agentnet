package daemon

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/notify"
)

// The approval summary shows the constraint text in full with the
// DisplayQuote rule (review 43 H3): even if a text got past the visible-only
// check, bidi controls, zero-width and tag characters would show as escapes.
func TestConstraintSummaryDisplayQuote(t *testing.T) {
	const sid = "s-0123456789abcdef0123456789abcdef"
	long := strings.Repeat("é", 500)
	s, err := constraintSummaryFor("bob", sid, long)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, `"`+long+`"`) || !strings.Contains(s, `named "bob"`) || !strings.Contains(s, sid) {
		t.Fatalf("summary does not show the full text, peer and session: %q", s)
	}
	hidden := "No new" + string(rune(0x200b)) + string(rune(0x202e)) + "dep" + string(rune(0xfeff)) + string(rune(0xe0041))
	s, err = constraintSummaryFor("bob", sid, hidden)
	if err != nil {
		t.Fatal(err)
	}
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

// Review 46 H2, R55-F5 A14: the approval window shows the summary in full
// for the longest constraint and the longest peer name, with the
// fingerprint. The name is 128 '"' (each quoted as \"), and the new worst
// case is a text of one letter and 499 combining marks, of which 497 are
// escaped (6 code points each). The summary stays within the window's limit
// and display-safe, so the window shows it unchanged.
func TestConstraintSummaryFitsWindow(t *testing.T) {
	const sid = "s-0123456789abcdef0123456789abcdef"
	name := strings.Repeat(`"`, 128)
	for _, text := range []string{strings.Repeat("<", 500), "a" + strings.Repeat("\u0301", 499), strings.Repeat("\u200b", 500)} {
		s, err := constraintSummaryFor(name, sid, text)
		if err != nil {
			t.Fatalf("worst case refused: %v", err)
		}
		if n := utf8.RuneCountInString(s); n > notify.MaxWindowSummary {
			t.Fatalf("worst-case summary is %d code points, the window shows %d", n, notify.MaxWindowSummary)
		}
		if !displaytext.Safe(s) {
			t.Fatal("the worst-case summary is not display-safe")
		}
		fp, _ := envelope.KeyFingerprint(summaryTestPeer("debater", name).Key)
		if !strings.Contains(s, "peer "+envelope.FormatFingerprint(fp)+" named ") {
			t.Fatalf("no fingerprint: %q", s[:200])
		}
	}
}
