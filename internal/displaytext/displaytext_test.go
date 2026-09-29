package displaytext

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// hasDigitRun reports whether s holds 6 or more numbers (category N) in a
// row, ignoring every rune that is not a number, a letter or a plain
// separator: what a human could read as a code.
func hasDigitRun(s string) bool {
	n := 0
	for _, r := range s {
		switch {
		case unicode.Is(unicode.N, r):
			n++
			if n >= 6 {
				return true
			}
		case unicode.IsLetter(r) || r == '…':
			n = 0
		}
	}
	return false
}

// R55-F5 A4 (review 55 T11-02 / R55-066; the reviewer's test was filed as
// zz_review55_T11-01_reviewer_test.go and only logged): a decoy code in a
// peer name does not survive Name, however it is split or written.
func TestNameBlanksDecoyCodes(t *testing.T) {
	for _, name := range []string{
		"Code 4\u200b8\u200b2\u200b9\u200b1\u200b3",       // zero-width space (Cf)
		"Code 4\u20608\u20602\u20609\u20601\u20603",       // word joiner (Cf)
		"Code 4\ufe0f8\ufe0f2\ufe0f9\ufe0f1\ufe0f3",       // variation selector (Mn)
		"Code 4\u31648\u31642\u31649\u31641\u31643",       // Hangul filler (Lo)
		`Code \064\070\062\071\061\063`,                   // octal escapes (zenity g_strcompress)
		"Code 482913",                                     // plain
		"Code 4\u03328\u03322\u03329\u03321\u03323\u0332", // combining low line between digits
		"Code ⁴⁸²⁹¹³",                                     // superscript (No)
		"Code ④⑧②⑨①③",                                     // circled (No)
		"Code 𝟒𝟖𝟐𝟗𝟏𝟑",                                     // mathematical bold (Nd)
		"Code ４８２９１３",                                     // fullwidth (Nd)
		"Code 48 29 13",
		"Code 4 - 8 - 2 - 9 - 1 - 3", // review 58a L3
		"Bob\u202e code 482913",
	} {
		got := Name(name)
		if !strings.Contains(got, "…") || hasDigitRun(got) {
			t.Errorf("Name(%q) = %q: the decoy survives", name, got)
		}
		if strings.ContainsRune(got, 0x202e) {
			t.Errorf("Name(%q) = %q holds U+202E", name, got)
		}
	}
	for in, want := range map[string]string{
		"laptop 2024": "laptop 2024",
		"v12345":      "v12345",
		"bob":         "bob",
		"bob 123456":  "bob …",
		"a1234567b":   "a…b",
		"a 12 b 3456": "a 12 b 3456",
		"48-29-13":    "…",
		"":            "(no name)",
		"\u200b \t":   "(no name)",
		"  a \n\n b ": "a b",
	} {
		if got := Name(in); got != want {
			t.Errorf("Name(%q) = %q, want %q", in, got, want)
		}
	}
}

// R55-F5 A15 (OD-R55F5-9, review 58a H1): fingerprint-shaped text in a name
// is blanked, grouped or not, in any case, with spaces or '-'.
func TestNameBlanksFingerprints(t *testing.T) {
	const fp = "2ED9 TGVE R471 63MC C451"
	for _, name := range []string{
		"Desktop (fingerprint " + fp + ")",
		"Desktop (fingerprint " + strings.ToLower(fp) + ")",
		"Desktop (fingerprint " + strings.ReplaceAll(fp, " ", "-") + ")",
		"Desktop " + strings.ReplaceAll(fp, " ", ""),
		"Desktop TGVE R471",
	} {
		got := Name(name)
		for _, g := range []string{"2ED9", "TGVE", "R471", "63MC", "C451"} {
			if strings.Contains(strings.ToUpper(got), g) {
				t.Errorf("Name(%q) = %q keeps %s", name, got, g)
			}
		}
		if !strings.HasPrefix(got, "Desktop") {
			t.Errorf("Name(%q) = %q lost its name", name, got)
		}
	}
	if got := Name("Office laptop"); got != "Office laptop" {
		t.Errorf("Name = %q", got)
	}
}

// Review 64 F5S-2 (R55-F6): a decoy code survives neither 4 to 8 separator
// runes between its digits nor a mark on a separator nor a spacing mark
// (Mc) after each digit.
func TestNameBlanksWideDecoyCodes(t *testing.T) {
	for _, name := range []string{
		"Code 4----8----2----9----1----3",
		"Code 4 - - 8 - - 2 - - 9 - - 1 - - 3",
		"Code 4 \u0332 8 \u0332 2 \u0332 9 \u0332 1 \u0332 3", // mark on the separator
		"Code 4\u0903 8\u0903 2\u0903 9\u0903 1\u0903 3",      // spacing mark (Mc)
		"Code 4\u09038\u09032\u09039\u09031\u09033",
		"Code 4 . . . 8 . . . 2 . . . 9 . . . 1 . . . 3", // 7 separators
	} {
		got := Name(name)
		if got != "Code …" {
			t.Errorf("Name(%q) = %q: the decoy survives", name, got)
		}
	}
	// Nine separators end a run; letters always do.
	for _, in := range []string{"a 12 b 3456", "1.........2.........3.........4.........5.........6"} {
		if got := Name(in); got != in {
			t.Errorf("Name(%q) = %q, want it unchanged", in, got)
		}
	}
}

// Review 64 F5S-1 (R55-F6): fingerprint-shaped text survives neither other
// separators (. _ / — -- " - ") nor look-alike letters (Cyrillic, Greek,
// fullwidth), nor combining marks inside a group.
func TestNameBlanksConfusableFingerprints(t *testing.T) {
	const fp = "2ED9 TGVE R471 63MC C451"
	names := []string{
		"Desktop 2ЕD9 TGVЕ R471 63МС С451", // Cyrillic Е, М, С
		"Desktop 2ΕD9 TGVΕ R471 63ΜC C451", // Greek Ε, Μ
		"Desktop ２ＥＤ９ ＴＧＶＥ",                // fullwidth
		"Desktop ２ｅｄ９ ｔｇｖｅ",                // fullwidth lower case
		"Desktop 2E\u0301D9 TGVE",          // a mark inside a group
		"Desktop 2ЕD9TGVЕR471",             // Cyrillic, no separator
	}
	for _, sep := range []string{".", "_", "/", "—", "--", " - ", " / ", "·"} {
		names = append(names, "Desktop "+strings.ReplaceAll(fp, " ", sep))
	}
	for _, name := range names {
		got := Name(name)
		if !strings.HasPrefix(got, "Desktop …") {
			t.Errorf("Name(%q) = %q: the fingerprint survives", name, got)
		}
		if n := utf8.RuneCountInString(strings.TrimPrefix(got, "Desktop …")); n > 0 {
			t.Errorf("Name(%q) = %q keeps %d runes after the blank", name, got, n)
		}
	}
	// Ordinary names are left alone: a Cyrillic or Greek word, and groups
	// that are not joined by separators only.
	for _, in := range []string{"Москва office", "Αθήνα laptop", "Office laptop", "ABCD and EFGH", "ABCD 1 EFGH"} {
		if got := Name(in); got != in {
			t.Errorf("Name(%q) = %q, want it unchanged", in, got)
		}
	}
}

// R55-F5 A17 (review 58a M4): at most 2 combining marks stay on a base;
// Quote keeps the value exact and escapes the rest.
func TestStackedMarks(t *testing.T) {
	in := "a" + strings.Repeat("\u0336", 100)
	if got := Name(in); got != "a\u0336\u0336" {
		t.Errorf("Name kept %d marks", utf8.RuneCountInString(got)-1)
	}
	q := Quote("/srv/" + in)
	if want := `"/srv/a` + "\u0336\u0336" + strings.Repeat(`\u0336`, 98) + `"`; q != want {
		t.Errorf("Quote = %q", q)
	}
	if !Safe(q) {
		t.Error("Quote output is not display-safe")
	}
}

// Quote: JSON without HTML escaping, the full hidden set escaped, '\' and
// '"' escaped (review 58a L1, M2).
func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		`a<b>&c`:          `"a<b>&c"`,
		`C:\x "y"`:        `"C:\\x \"y\""`,
		"a\u00a0b":        `"a\u00a0b"`,
		"a\u2800b":        `"a\u2800b"`,
		"a\ufe0fb":        `"a\ufe0fb"`,
		"a\u3164b":        `"a\u3164b"`,
		"a\x7fb":          `"a\u007fb"`,
		"a\u0085b":        `"a\u0085b"`,
		"a\U000E0041":     `"a\udb40\udc41"`,
		"a\xffb":          `"a\ufffdb"`,
		"line\nbreak\x00": `"line\nbreak\u0000"`,
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
		if !Safe(Quote(in)) {
			t.Errorf("Quote(%q) is not display-safe", in)
		}
	}
}

// Hidden is exactly decision.Visible's former set (review 46 H1, review 48
// L3): a spot check of each class and of the visible runes next to them.
func TestHidden(t *testing.T) {
	for _, r := range []rune{0, 0x1f, 0x7f, 0x9f, 0x200b, 0x200e, 0x202e, 0x2066, 0x2060, 0xfeff, 0xe0041,
		0x2028, 0x2029, 0xfe0f, 0xe0100, 0x034f, 0x115f, 0x3164, 0xffa0, 0x00a0, 0x2003, 0x3000, 0x2800, 0xe000} {
		if !Hidden(r) {
			t.Errorf("U+%04X not hidden", r)
		}
	}
	for _, r := range []rune{' ', 'a', '"', '\\', 0x00e9, 0x0301, 0x05d0, 0x4e2d, 0x1f600, 0xfffd, '…'} {
		if Hidden(r) {
			t.Errorf("U+%04X hidden", r)
		}
	}
}

// Safe: one line, valid UTF-8, no hidden rune, at most MaxSummary code
// points.
func TestSafe(t *testing.T) {
	if !Safe(strings.Repeat("é", MaxSummary)) || Safe(strings.Repeat("é", MaxSummary+1)) {
		t.Error("the limit is not 4096 code points")
	}
	for _, s := range []string{"a\nb", "a\x00", "a\u200bb", "a\xff", "a\u00a0b"} {
		if Safe(s) {
			t.Errorf("Safe(%q)", s)
		}
	}
}
