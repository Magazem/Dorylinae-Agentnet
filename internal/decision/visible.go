package decision

import (
	"strings"

	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
)

// Visible is the escaping rule of decision.md §Markdown (review 43 M1), the
// rule of review 40's DisplayQuote adapted for plain (not JSON-quoted) text:
// it keeps \n and \t when multiLine is true (the only place they are
// meaningful, inside a fenced block), and replaces every other rune that is
// a C0/C1 control, a Unicode format character (Cf: bidi controls, zero-width
// characters, U+FEFF, tag characters), not unicode.IsGraphic (space
// excepted), or graphic but invisible (review 48 L3, the set of review 46
// H1: variation selectors, the other default-ignorable code points such as
// the Hangul fillers, every Zs space but U+0020, and U+2800), by the visible
// ASCII escape \u{XXXX} (uppercase hex of the code point). The JSON file
// keeps the exact bytes; this is a view. The rule is displaytext.Escape, the
// one shared set (Docs/protocol/approval.md §Sanitising; R55-F10): U+FFFD is
// kept and there is no stacked-marks cap (OD-F10-8).
func Visible(s string, multiLine bool) string {
	return displaytext.Escape(s, multiLine)
}

// TemplateInert is decision.md §Markdown's template rule (review 48 H1),
// applied after Visible: it replaces every '{' followed by '{', '%' or '#' by
// the escape \u{7B}, so the rendered text holds no "{{", "{%" or "{#". Static
// site generators run their template language over a Markdown file BEFORE
// the Markdown parser (Jekyll and GitHub Pages: Liquid, also on files without
// front matter; Eleventy: Liquid by default; Hugo: shortcodes {{< >}} and
// {{% %}}; Nunjucks, Jinja, Handlebars), and they do so inside code spans and
// fenced blocks too. Peer text such as {% for i in (1..9) %}`{% endfor %}
// would otherwise expand to a backtick run longer than the fence (which is
// sized from the text as written), close it, and turn the rest of the peer
// text into live Markdown and raw HTML on the published site; any template
// syntax error would fail the site's build. A '{' left in the output is
// always followed by a character other than '{', '%' and '#': the escape
// starts with '\' and holds "{7B}".
func TemplateInert(s string) string {
	if !strings.Contains(s, "{") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '{' && i+1 < len(s) && (s[i+1] == '{' || s[i+1] == '%' || s[i+1] == '#') {
			b.WriteString(`\u{7B}`)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
