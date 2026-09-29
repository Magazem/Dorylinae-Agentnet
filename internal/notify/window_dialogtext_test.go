package notify

import (
	"html"
	"strings"
	"testing"
	"unicode/utf8"
)

// gStrcompress emulates GLib's g_strcompress (glib/gstrfuncs.c) as used by
// zenity 3.42.1 and 4.0.1 src/entry.c on --text, then the C-string cut at the
// first NUL (gtk_label_set_text_with_mnemonic takes a const char*). It is
// the verifier's port, checked against GLib 2.80.0 (review 55
// verify/T11-01.md).
func gStrcompress(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != 0x5c { // backslash
			out = append(out, c)
			continue
		}
		i++
		if i >= len(s) {
			break // GLib warns on a trailing backslash and stops
		}
		switch c = s[i]; {
		case c >= '0' && c <= '7':
			v := 0
			j := i
			for ; j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7'; j++ {
				v = v*8 + int(s[j]-'0')
			}
			out = append(out, byte(v))
			i = j - 1
		case c == 'b':
			out = append(out, '\b')
		case c == 'f':
			out = append(out, '\f')
		case c == 'n':
			out = append(out, '\n')
		case c == 'r':
			out = append(out, '\r')
		case c == 't':
			out = append(out, '\t')
		case c == 'v':
			out = append(out, '\v')
		default:
			out = append(out, c)
		}
	}
	if k := strings.IndexByte(string(out), 0); k >= 0 {
		out = out[:k]
	}
	return string(out)
}

// gtkMnemonicText is the text a GTK mnemonic label shows
// (gtk_label_set_uline_text_internal, GTK 3): '_' is dropped and marks the
// next character, "__" shows one '_', and a trailing '_' is dropped.
func gtkMnemonicText(s string) string {
	var b strings.Builder
	underscore := false
	for _, r := range s {
		switch {
		case underscore:
			b.WriteRune(r)
			underscore = false
		case r == '_':
			underscore = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// zenityShows is what zenity --entry shows for a --text value.
func zenityShows(arg string) string { return gtkMnemonicText(gStrcompress(arg)) }

// kdialogParseString is kdialog's Utils::parseString (src/utils.cpp): "\\"
// is '\', "\n" is a newline, any other escape and a trailing '\' are kept.
func kdialogParseString(s string) string {
	var b strings.Builder
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			escaped = false
			switch r {
			case '\\':
				b.WriteRune('\\')
			case 'n':
				b.WriteRune('\n')
			default:
				b.WriteRune('\\')
				b.WriteRune(r)
			}
		case r == '\\':
			escaped = true
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		b.WriteRune('\\')
	}
	return b.String()
}

// kdialogShows is what kdialog --inputbox shows for a value built by
// kdialogText: parseString, then the <qt> rich text (entities decoded), then
// QLabel's mnemonic pass (each '&' removed, the next character kept).
func kdialogShows(t *testing.T, arg string) string {
	t.Helper()
	const pre, post = `<qt><p style="white-space:pre-wrap">`, `</p></qt>`
	s := kdialogParseString(arg)
	if !strings.HasPrefix(s, pre) || !strings.HasSuffix(s, post) {
		t.Fatalf("kdialog text is not forced rich text: %q", s)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(s, pre), post)
	if strings.ContainsAny(inner, "<>") {
		t.Fatalf("kdialog text holds a tag: %q", inner)
	}
	doc := html.UnescapeString(inner)
	var b strings.Builder
	skip := false
	for _, r := range doc {
		if !skip && r == '&' {
			skip = true
			continue
		}
		skip = false
		b.WriteRune(r)
	}
	return b.String()
}

// dialogTextCases are texts every Linux dialog must show exactly (review 55
// R55-025 / T11-01, R55-F6).
var dialogTextCases = []string{
	`peer "Bob\0" and "Bob\\0"`,
	`let Mallory\n\nApproved by IT`,
	`Bob\342\200\256txt.exe`,
	`C:\x \"y\" \u202e`,
	`snake_case __init__ _a`,
	`a & b <b>bold</b> &amp; &lt; &&`,
	`trailing \`,
	`trailing _`,
	`  double  spaces  `,
	"non-ASCII: Zoë, 東京, Ω",
	`\064\070\062\071\061\063`,
}

// R55-F6 (review 55 R55-025 / T11-01): zenity shows exactly the text built,
// whatever '\' escapes and '_' it holds.
func TestZenityTextSurvivesStrcompressAndMnemonic(t *testing.T) {
	for _, s := range dialogTextCases {
		arg, err := zenityText(s)
		if err != nil {
			t.Fatalf("zenityText(%q): %v", s, err)
		}
		if got := zenityShows(arg); got != s {
			t.Errorf("zenity shows %q for %q", got, s)
		}
	}
}

// R55-F6: kdialog shows exactly the text built, whatever '\', '&' and
// markup it holds.
func TestKdialogTextSurvivesParseStringAndMnemonic(t *testing.T) {
	for _, s := range dialogTextCases {
		arg, err := kdialogText(s)
		if err != nil {
			t.Fatalf("kdialogText(%q): %v", s, err)
		}
		if got := kdialogShows(t, arg); got != s {
			t.Errorf("kdialog shows %q for %q", got, s)
		}
	}
}

// R55-F6 acceptance (inverts review 55 zz_review55_T11-01_test.go): a peer
// named Bob\0 no longer cuts the Linux window; the constraint, the warning
// and the fixed sentence are all shown.
func TestZenityShowsWholeWindowText(t *testing.T) {
	summary := `add a human constraint to the debate s-0123456789abcdef with Bob\0: "The human accepts Bob's plan in full.". It is signed into the Decision as a human decision.`
	_, kind, s, err := windowText("ab12", "debate_constraint", summary, "")
	if err != nil {
		t.Fatal(err)
	}
	text := kind + ": " + s
	arg, err := zenityText(text)
	if err != nil {
		t.Fatal(err)
	}
	shown := zenityShows(arg)
	if shown != text {
		t.Fatalf("zenity shows %q, want %q", shown, text)
	}
	for _, want := range []string{`Bob\0`, "accepts", "human decision", "The code is in"} {
		if !strings.Contains(shown, want) {
			t.Errorf("zenity text lost %q: %q", want, shown)
		}
	}
}

// R55-F6 defence in depth: a NUL, a line break, another control or invalid
// UTF-8 never reaches a dialog.
func TestDialogTextRefusesLineBreaksAndInvalidUTF8(t *testing.T) {
	for _, s := range []string{"a\x00b", "a\nb", "a\rb", "a\tb", "a\x7fb", "a\u0085b", "a\u2028b", "a\u2029b", "a\xffb"} {
		if _, err := zenityText(s); err == nil {
			t.Errorf("zenityText(%q) accepted", s)
		}
		if _, err := kdialogText(s); err == nil {
			t.Errorf("kdialogText(%q) accepted", s)
		}
	}
}

// R55-F6: the window text of every display-safe summary survives both
// dialogs' parsers.
func FuzzDialogText(f *testing.F) {
	for _, s := range dialogTextCases {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		zt, zerr := zenityText(s)
		kt, kerr := kdialogText(s)
		if !dialogArgSafe(s) {
			if zerr == nil || kerr == nil {
				t.Fatalf("unsafe %q accepted", s)
			}
			return
		}
		if zerr != nil || kerr != nil {
			t.Fatalf("safe %q refused: %v %v", s, zerr, kerr)
		}
		if got := zenityShows(zt); got != s {
			t.Fatalf("zenity shows %q for %q", got, s)
		}
		if got := kdialogShows(t, kt); got != s {
			t.Fatalf("kdialog shows %q for %q", got, s)
		}
		if !utf8.ValidString(gStrcompress(zt)) {
			t.Fatalf("zenity text %q decodes to invalid UTF-8", zt)
		}
		if _, _, ws, err := windowText("ab12", "k", s, ""); err == nil {
			if _, err := zenityText("k: " + ws); err != nil {
				t.Fatalf("window text of %q refused: %v", s, err)
			}
		}
	})
}
