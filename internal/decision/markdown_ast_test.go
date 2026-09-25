package decision_test

// Review 48 M1: the inertness test must see what a renderer would make of
// peer text. goldmark's default HTML renderer omits raw HTML ("<!-- raw HTML
// omitted -->"), so counting "<script" in its output cannot fail, and the
// adversarial Decision only filled four fields. This test fills EVERY
// peer-controlled field with hostile text carrying a marker, parses the
// Markdown to an AST (CommonMark + GFM + footnotes, the GitHub profile), and
// requires that the marker occurs only inside code spans and fenced blocks
// without an info string, and that no node kind peer text could use to
// become active (raw HTML, links, autolinks, images, tables, task boxes,
// footnotes, emphasis, strikethrough, indented code) exists at all.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/decision"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

const peerMarker = "PEERMARK"

// liquidBreakout expands, under Liquid (Jekyll, GitHub Pages, Eleventy), to
// a line of nine backticks, which would close any fence sized from the text
// as written.
const liquidBreakout = "{% for i in (1..9) %}`{% endfor %}"

var hostileSingle = peerMarker + " <script>alert(1)</script> <img src=x onerror=alert(1)> <!-- c --> [x](javascript:alert(1)) " +
	"![i](http://e/i.png) <http://evil.example> www.evil.example http://evil.example a@evil.example [^1] [r] " +
	"*em* _em_ **b** ~~s~~ ``` ~~~ |c|d| $x$ $`y`$ &lt;script&gt; &#60;b&#62; {{ site.github }} {% raw %} {{< youtube x >}} {# n #} " +
	liquidBreakout + " \\ " + string(rune(0x200b)) + string(rune(0x202e)) + string(rune(0xE0041)) + string(rune(0x3164))

var hostileMulti = "---\ntitle: " + peerMarker + "\n---\n" +
	"# " + peerMarker + " heading\n" + peerMarker + " setext\n===\n" + peerMarker + " setext2\n---\n" +
	"- [ ] " + peerMarker + " task\n> [!NOTE]\n> " + peerMarker + "\n<div>" + peerMarker + "</div>\n<script>alert(1)</script>\n" +
	"[r]: http://evil.example\n[^1]: " + peerMarker + " footnote\n| a | b |\n|---|---|\n| " + peerMarker + " | x |\n" +
	"    indented " + peerMarker + "\n```mermaid\ngraph TD; A-->B\n```\n~~~\n" + liquidBreakout + "\n" +
	"$$\n" + peerMarker + "\n$$\n" + strings.Repeat("`", 10) + "\n" + peerMarker + " after a ten-backtick line\n" +
	"{% endraw %}{{ page }}\n" + strings.Repeat("x", 20000) + peerMarker + "\n"

func hostileDecision() map[string]any {
	s, m := hostileSingle, hostileMulti
	pos := func() map[string]any {
		return map[string]any{
			"claim": s, "argument": m, "assumptions": []any{s, s},
			"evidence":              []any{map[string]any{"kind": s, "ref": s, "note": s}, map[string]any{"kind": s, "ref": s}},
			"rejected_alternatives": []any{map[string]any{"option": s, "reason": s}},
		}
	}
	d := baseDecision("d-7eaeb0b78e6bd96981357b500af94044", decision.OutcomeAgreed, decision.ReasonAccepted)
	d["problem"] = map[string]any{"title": s, "topic": m, "context": []any{
		map[string]any{"name": s, "bytes": 1, "sha256": strings.Repeat("ab", 32)},
	}}
	d["positions"] = map[string]any{
		"initiator":  map[string]any{"initial": pos(), "final": pos()},
		"respondent": map[string]any{"initial": pos(), "final": pos()},
	}
	challenge := map[string]any{"targets": []any{s, s}, "argument": m,
		"evidence": []any{map[string]any{"kind": s, "ref": s, "note": s}}}
	d["rounds"] = []any{
		map[string]any{"n": 1,
			"initiator":  map[string]any{"challenges": []any{challenge, challenge}, "revision": pos()},
			"respondent": map[string]any{"challenges": []any{challenge}, "revision": pos()},
		},
	}
	d["final_agreement"] = map[string]any{"decision": s, "points": []any{s, s}, "argument": m}
	d["remaining_disagreement"] = []any{map[string]any{"point": s, "initiator": s, "respondent": s}}
	d["human_decisions"] = []any{map[string]any{"id": "c-00000000000000000000000000000001", "by": "respondent", "at": "2026-10-01T09:10:00Z", "text": s}}
	d["affected_artifacts"] = []any{map[string]any{"kind": s, "ref": s, "note": s}}
	return d
}

func TestRenderEveryPeerFieldIsInert(t *testing.T) {
	names := map[string]string{"initiator": hostileSingle, "respondent": hostileSingle}
	for _, complete := range []bool{true, false} {
		sigI, sigR := mdSignaturesFor(complete)
		md, err := decision.Render(hostileDecision(), strings.Repeat("0", 64), sigI, sigR, !complete, names, testFPs)
		if err != nil {
			t.Fatal(err)
		}
		checkInertAST(t, md)
		for _, bad := range []string{"{{", "{%", "{#"} {
			if bytes.Contains(md, []byte(bad)) {
				t.Errorf("rendered Markdown holds %q: a template pass (Liquid, Hugo, Nunjucks) would run peer text", bad)
			}
		}
		if !bytes.HasPrefix(md, []byte("# Decision ")) && !bytes.HasPrefix(md, []byte("> UNCONFIRMED")) {
			t.Errorf("the file must start with the title or the banner (no front matter), got %q", md[:40])
		}
	}
}

// checkInertAST fails when peerMarker occurs outside a code span or an
// info-less fenced block, or when any node kind peer text could use exists.
func checkInertAST(t *testing.T, md []byte) {
	t.Helper()
	gm := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.Footnote))
	doc := gm.Parser().Parse(text.NewReader(md))
	forbidden := map[ast.NodeKind]string{
		ast.KindHTMLBlock: "HTML block", ast.KindRawHTML: "raw HTML", ast.KindLink: "link",
		ast.KindAutoLink: "autolink", ast.KindImage: "image", ast.KindCodeBlock: "indented code block",
		ast.KindEmphasis: "emphasis", ast.KindThematicBreak: "thematic break",
		extast.KindTable: "table", extast.KindTaskCheckBox: "task list box", extast.KindStrikethrough: "strikethrough",
		extast.KindFootnote: "footnote", extast.KindFootnoteLink: "footnote link", extast.KindFootnoteList: "footnote list",
	}
	blockquotes := 0
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if what, bad := forbidden[n.Kind()]; bad {
			t.Errorf("the Markdown has a %s node", what)
		}
		switch n.Kind() {
		case ast.KindCodeSpan:
			return ast.WalkSkipChildren, nil
		case ast.KindFencedCodeBlock:
			if fb := n.(*ast.FencedCodeBlock); fb.Info != nil {
				t.Errorf("a fenced block has an info string %q", fb.Info.Segment.Value(md))
			}
			return ast.WalkSkipChildren, nil
		case ast.KindBlockquote:
			blockquotes++
		case ast.KindText:
			if v := n.(*ast.Text).Segment.Value(md); bytes.Contains(v, []byte(peerMarker)) {
				t.Errorf("peer text outside a code span or fence: %q (in %s)", v, n.Parent().Kind())
			}
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if blockquotes > 1 {
		t.Errorf("%d block quotes; only the UNCONFIRMED banner may be one", blockquotes)
	}
	// The same document through a renderer that keeps raw HTML: nothing
	// active may come out.
	var buf bytes.Buffer
	if err := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.Footnote),
		goldmark.WithRendererOptions(html.WithUnsafe())).Convert(md, &buf); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"<script", "<img", "<a ", "<div", "<table", "<input", "<!--", "<hr", "<em", "<del"} {
		if strings.Contains(buf.String(), tag) {
			t.Errorf("rendered HTML (raw HTML kept) contains %s", tag)
		}
	}
}

// TestRenderLabelsAreOwnParagraphs: review 48 M2, a label after a list must
// not be a lazy continuation of the list's last item, and the Claim line
// must not swallow the next label.
func TestRenderLabelsAreOwnParagraphs(t *testing.T) {
	var buf bytes.Buffer
	if err := goldmark.New(goldmark.WithExtensions(extension.GFM)).Convert(renderFixture(t, agreedDecision(), true), &buf); err != nil {
		t.Fatal(err)
	}
	h := buf.String()
	for _, label := range []string{"Assumptions:", "Evidence:", "Rejected alternatives:", "Argument:"} {
		if !strings.Contains(h, "<p>"+label+"</p>") {
			t.Errorf("%q is not its own paragraph:\n%s", label, h)
		}
	}
}
