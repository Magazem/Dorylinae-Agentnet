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

// Review 65 F6S-1 (R55-F6b): fingerprint-shaped text survives neither the
// mathematical, enclosed, Lisu, Cherokee or small-capital look-alikes nor a
// joiner letter (Lm, or a bar- or dot-shaped Lo) between its groups.
func TestNameBlanksMoreConfusableFingerprints(t *testing.T) {
	for _, name := range []string{
		"Desktop 𝟐𝐄𝐃𝟗 𝐓𝐆𝐕𝐄 𝐑𝟒𝟕𝟏",              // mathematical bold
		"Desktop 𝟸𝙴𝙳𝟿 𝚃𝙶𝚅𝙴",                   // mathematical monospace
		"Desktop 2\U0001D6ACD9 TGV\U0001D6AC", // mathematical bold capital epsilon
		"Desktop ②ⒺⒹ⑨ ⓉⒼⓋⒺ",                   // enclosed
		"Desktop ❷🅴🅳❾ 🅃🄶🅅🄴",                   // dingbat and supplement
		"Desktop 2ꓰD9 TꓖVꓰ R471",              // Lisu
		"Desktop 2ᎬᎠ9 ᎢᏀᏙᎬ R471",              // Cherokee
		"Desktop 2ꭼꭰ9 ꭲꮐꮩꭼ",                   // small Cherokee
		"Desktop 2ᴇᴅ9 ᴛɢᴠᴇ ʀ471",              // Latin small capitals
		"Desktop 2ℰⅅ9 TGVℰ",                   // Letterlike
		"Desktop 2ED9ㆍTGVEㆍR471",              // U+318D (Lo)
		"Desktop 2ED9ǀTGVEǀR471",              // U+01C0 (Lo)
		"Desktop 2ED9ˑTGVEˑR471",              // U+02D1 (Lm)
	} {
		if got := Name(name); got != "Desktop …" {
			t.Errorf("Name(%q) = %q: the fingerprint survives", name, got)
		}
	}
}

// Review 65 F6S-2 (R55-F6b): a decoy code survives neither joiner letters
// between its digits nor a digit look-alike letter that stands alone.
func TestNameBlanksLetterDecoyCodes(t *testing.T) {
	for _, name := range []string{
		"Code 4ǀ8ǀ2ǀ9ǀ1ǀ3", // U+01C0 (Lo)
		"Code 4ˑ8ˑ2ˑ9ˑ1ˑ3", // U+02D1 (Lm)
		"Code 4ː8ː2ː9ː1ː3", // U+02D0 (Lm)
		"Code 4ㆍ8ㆍ2ㆍ9ㆍ1ㆍ3", // U+318D (Lo)
		"Code 4ǀǀ8ǀǀ2ǀǀ9ǀǀ1ǀǀ3",
		"Code 48291З", // Cyrillic Ze
		"Code З48291",
		"Code 48291б", // Cyrillic be
		"Code 4829l3", // lower-case L
		"Code 4 O 8 2 9 1",
		"Code 4́ ĺ 8 2 9 1", // marks around the look-alike
	} {
		if got := Name(name); got != "Code …" {
			t.Errorf("Name(%q) = %q: the decoy survives", name, got)
		}
	}
	// A look-alike inside a word, a run with no number of category N, and
	// letters of an ordinary script between numbers end nothing new.
	for _, in := range []string{
		"12345 lol", "Room 12345 Ok", "l l l l l l", "O-I-O-I-O-I",
		"3층 2호 1234", "1号楼2单元1", "laptop 2024", "v12345",
	} {
		if got := Name(in); got != in {
			t.Errorf("Name(%q) = %q, want it unchanged", in, got)
		}
	}
}

// D66 (review 65 F6S-3, R55-F6b): a chain of 2 or 3 groups is blanked only
// if it holds a digit; a chain of 4 or more groups always is.
func TestNameFingerprintChainsNeedADigit(t *testing.T) {
	for in, want := range map[string]string{
		// Shown: 2 and 3 groups without a digit.
		"Mary & Jake":        "Mary & Jake",
		"Zack.Hart":          "Zack.Hart",
		"Nate Kent":          "Nate Kent",
		"MARY-JAKE":          "MARY-JAKE",
		"Mary & Jake & Zack": "Mary & Jake & Zack",
		"Тeam Mary Jake":     "Тeam Mary Jake",
		// Blanked: a digit anywhere in a 2- or 3-group chain.
		"Mar7 & Jake":         "…",
		"Mary & Jake & Zac4":  "…",
		"Desktop TGVE R471":   "Desktop …",
		"TGVE R471 63MC":      "…",
		"Mary & JakЗ":         "…", // Cyrillic Ze folds to 3
		"Mary 𝟕ake":           "…", // mathematical digit
		"Mary ⑦ake":           "…", // circled digit
		"x 2ED9 TGVE y":       "x … y",
		"Desktop 2ED9 / TGVE": "Desktop …",
		// Blanked: 4 or more groups, digit or not.
		"Our Mary & Jake & Zack & Hart": "Our …",
		"ABCD EFGH JKMN PQRS TVWX":      "…",
		// Not chains: one group, or groups not joined by 1 to 3 separators.
		"Mary 7":        "Mary 7",
		"Mar7 and Jake": "Mar7 and Jake",
		"TGVE    R471":  "…", // spaces collapse first
	} {
		if got := Name(in); got != want {
			t.Errorf("Name(%q) = %q, want %q", in, got, want)
		}
	}
}

// fold: the arithmetic ranges land where the code charts put them, and every
// table entry is a letter that folds to an ASCII character.
func TestFold(t *testing.T) {
	for r, want := range map[rune]rune{
		0x1D400: 'A', 0x1D41A: 'A', 0x1D433: 'Z', 0x1D6A3: 'Z', // bold A, a, z; monospace z
		0x1D6A8: 'A', 0x1D6AC: 'E', 0x1D6C2: 'A', 0x1D6B9: 0, 0x1D6C1: 0, 0x1D7C9: 0, // Greek
		0x1D7CE: '0', 0x1D7FF: '9',
		0x2460: '1', 0x2468: '9', 0x2469: 0, 0x2474: '1', 0x2488: '1',
		0x249C: 'A', 0x24B6: 'A', 0x24CF: 'Z', 0x24D0: 'A', 0x24E9: 'Z', 0x24EA: '0', 0x24FF: '0',
		0x24F5: '1', 0x24FE: 0, 0x2776: '1', 0x277F: 0, 0x2780: '1', 0x2792: '9', 0x2793: 0,
		0x1F100: '0', 0x1F101: '0', 0x1F10A: '9', 0x1F110: 'A', 0x1F12A: 0, 0x1F130: 'A',
		0x1F150: 'A', 0x1F170: 'A', 0x1F189: 'Z', 0x1F1E6: 'A', 0x1F1FF: 'Z',
		0xFF10: '0', 0xFF41: 'A', 'q': 'Q', '7': '7', '-': 0, 0x00E9: 0,
	} {
		if got := fold(r); got != want {
			t.Errorf("fold(U+%04X) = %q, want %q", r, got, want)
		}
	}
	for r, c := range fpConfusables {
		if !unicode.IsLetter(r) {
			t.Errorf("fpConfusables: U+%04X is not a letter", r)
		}
		if fold(r) != c {
			t.Errorf("fold(U+%04X) = %q, want %q", r, fold(r), c)
		}
	}
	for r := range joinerLetters {
		if !unicode.Is(unicode.Lo, r) || fold(r) != 0 {
			t.Errorf("joinerLetters: U+%04X", r)
		}
	}
}
