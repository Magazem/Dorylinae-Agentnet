package decision

import "testing"

// Ticket 3.3b (review 43 M1): Visible keeps \n and \t only in multi-line
// mode, and escapes C0/C1 controls, Unicode format characters and any other
// non-graphic rune (space excepted) as \u{XXXX}.
func TestVisible(t *testing.T) {
	zeroWidthSpace := string(rune(0x200b))
	bidiOverride := string(rune(0x202e))
	bom := string(rune(0xfeff))
	tagChar := string(rune(0xE0041))
	c1 := string(rune(0x0085))
	del := string(rune(0x007f))

	cases := []struct {
		name      string
		in        string
		multiLine bool
		want      string
	}{
		{"plain ASCII", "hello world", false, "hello world"},
		{"newline single-line", "a\nb", false, `a\u{A}b`},
		{"newline multi-line kept", "a\nb", true, "a\nb"},
		{"tab multi-line kept", "a\tb", true, "a\tb"},
		{"tab single-line escaped", "a\tb", false, `a\u{9}b`},
		{"zero-width space", "a" + zeroWidthSpace + "b", true, `a\u{200B}b`},
		{"bidi override", "a" + bidiOverride + "b", true, `a\u{202E}b`},
		{"U+FEFF", bom + "a", true, `\u{FEFF}a`},
		{"tag character", "a" + tagChar + "b", true, `a\u{E0041}b`},
		{"C1 control", "a" + c1 + "b", true, `a\u{85}b`},
		{"plain space kept", "a b", false, "a b"},
		{"del", "a" + del + "b", false, `a\u{7F}b`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Visible(c.in, c.multiLine); got != c.want {
				t.Errorf("Visible(%q, %v) = %q, want %q", c.in, c.multiLine, got, c.want)
			}
		})
	}
}

func TestVisibleNeverPassesThroughInvisibleRunes(t *testing.T) {
	invisible := []rune{0x200b, 0x200c, 0x200d, 0x202a, 0x202e, 0xfeff, 0xe0041, 0x00ad}
	for _, r := range invisible {
		s := Visible(string(r), true)
		if s == string(r) {
			t.Errorf("U+%04X passed through Visible unescaped", r)
		}
	}
}
