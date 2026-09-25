package decision

import (
	"fmt"
	"strings"
	"testing"
)

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

// Review 48 L3: graphic but invisible runes (review 46 H1's set) are escaped
// too; titles, file names and petnames never go through debate's filter.
func TestVisibleEscapesGraphicInvisible(t *testing.T) {
	for _, r := range []rune{0x3164, 0x115f, 0x1160, 0xffa0, 0x034f, 0xfe0f, 0xe0100, 0x00a0, 0x2003, 0x3000, 0x2800} {
		want := fmt.Sprintf(`a\u{%X}b`, r)
		if got := Visible("a"+string(r)+"b", false); got != want {
			t.Errorf("Visible(U+%04X) = %q, want %q", r, got, want)
		}
	}
	if got := Visible("a b", false); got != "a b" {
		t.Errorf("U+0020 must be kept, got %q", got)
	}
}

// Review 48 H1: TemplateInert leaves no "{{", "{%" or "{#" for a static
// site generator's template pass (Liquid, Hugo shortcodes, Nunjucks).
func TestTemplateInert(t *testing.T) {
	cases := []struct{ in, want string }{
		{"no braces", "no braces"},
		{"{a} {} }}", "{a} {} }}"},
		{"{{ site.github }}", `\u{7B}{ site.github }}`},
		{"{% raw %}", `\u{7B}% raw %}`},
		{"{# c #}", `\u{7B}# c #}`},
		{"{{{", `\u{7B}\u{7B}{`},
		{"{{<youtube x>}}", `\u{7B}{<youtube x>}}`},
		{`\u{{`, `\u\u{7B}{`},
		{"{", "{"},
	}
	for _, c := range cases {
		got := TemplateInert(c.in)
		if got != c.want {
			t.Errorf("TemplateInert(%q) = %q, want %q", c.in, got, c.want)
		}
		for _, bad := range []string{"{{", "{%", "{#"} {
			if strings.Contains(got, bad) {
				t.Errorf("TemplateInert(%q) = %q still holds %q", c.in, got, bad)
			}
		}
	}
}
