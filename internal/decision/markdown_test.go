package decision_test

// Ticket 3.3b: the Markdown renderer's golden files, determinism and
// repository-safety tests. Repository safety (decision.md §Markdown, review
// 43 L8: "Markdown inertness is the security property") is checked by
// rendering the output with a real CommonMark + GFM parser,
// github.com/yuin/goldmark, a TEST-ONLY dependency pinned in go.mod: no
// non-test file imports it (grep below), and TestBinariesDoNotLinkGoldmark
// checks the three shipped binaries do not link it either.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/decision"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

func position(claim, argument string) map[string]any {
	return map[string]any{
		"claim":       claim,
		"argument":    argument,
		"assumptions": []any{"Clock skew is under 5 s"},
		"evidence": []any{
			map[string]any{"kind": "file", "ref": "internal/mail/outbox.go", "note": "the retry loop"},
		},
		"rejected_alternatives": []any{
			map[string]any{"option": "Fixed 30 s retry", "reason": "Floods the relay after an outage"},
		},
	}
}

func baseDecision(id, outcome, reason string) map[string]any {
	return map[string]any{
		"v":       1,
		"id":      id,
		"session": "s-36375782ceb6baea9cee4d4273dfb035",
		"request": "r-0123456789abcdef0123456789abcdef",
		"team":    "t-00112233445566778899aabbccddeeff",
		"participants": map[string]any{
			"initiator":  "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg",
			"respondent": "Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc",
		},
		"problem": map[string]any{
			"title": "Outbox retry policy",
			"topic": "How should the outbox retry?",
			"context": []any{
				map[string]any{"name": "outbox.go", "bytes": 4096, "sha256": strings.Repeat("ab", 32)},
			},
		},
		"rounds_max": 1,
		"positions": map[string]any{
			"initiator":  map[string]any{"initial": position("Use capped exponential backoff for outbox retries", "Retries should back off exponentially, capped at 10 minutes.")},
			"respondent": map[string]any{"initial": position("Add full jitter to the existing backoff", "Jitter matters more than the curve.")},
		},
		"outcome": outcome,
		"reason":  reason,
		"opened":  "2026-10-01T09:00:00Z",
		"closed":  "2026-10-01T09:20:00Z",
	}
}

func agreedDecision() map[string]any {
	d := baseDecision("d-7eaeb0b78e6bd96981357b500af94044", decision.OutcomeAgreed, decision.ReasonAccepted)
	d["rounds"] = []any{
		map[string]any{"n": 1,
			"initiator":  map[string]any{"challenges": []any{}},
			"respondent": map[string]any{"challenges": []any{}},
		},
	}
	d["converge"] = map[string]any{
		"proposal": map[string]any{"agreement": map[string]any{"decision": "Capped exponential backoff with full jitter"}},
		"answer":   map[string]any{"accept": true},
	}
	d["final_agreement"] = map[string]any{"decision": "Capped exponential backoff with full jitter"}
	d["human_decisions"] = []any{
		map[string]any{"id": "c-00000000000000000000000000000001", "by": "initiator", "at": "2026-10-01T09:10:00Z", "text": "Ship behind a feature flag"},
	}
	return d
}

func escalatedDecision() map[string]any {
	d := baseDecision("d-ffeeb0b78e6bd96981357b500af940aa", decision.OutcomeEscalated, decision.ReasonTimeout)
	d["rounds"] = []any{
		map[string]any{"n": 1,
			"initiator": map[string]any{"challenges": []any{
				map[string]any{"targets": []any{"claim"}, "argument": "Jitter without a cap can still storm the relay."},
			}},
		},
	}
	d["remaining_disagreement"] = []any{
		map[string]any{"point": "Cap duration", "initiator": "10 minutes", "respondent": "unbounded"},
	}
	return d
}

func mdSignaturesFor(complete bool) (sigI, sigR string) {
	sigI = "oU23UZtZYRFm7RVkDdqPfoFfL6ZFlJj8K4Xqq4ySy1EtDdeO58GEo148fZbERBYbW-75C5erU6AhqoDwiDBnAw"
	if complete {
		sigR = "xCdaoO0EujU6LbZJOgvIX_FvFlEikdlftxbUmUF9ef_1xTMrqScV_9UDGrsQu8aO_H7adF331lOzjs6PTJv-Cg"
	}
	return
}

var testNames = map[string]string{"initiator": "alice-agent", "respondent": "bob-agent"}
var testFPs = map[string]string{"initiator": "AB12-CD34-EF56", "respondent": "12AB-34CD-56EF"}

func renderFixture(t *testing.T, d map[string]any, complete bool) []byte {
	t.Helper()
	sigI, sigR := mdSignaturesFor(complete)
	out, err := decision.Render(d, "6ec367cd5f0f82b1d929878e678ba1aafdd3c33cb13dc22da2fba55836094ede", sigI, sigR, false, testNames, testFPs)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

func goldenPath(name string) string {
	return filepath.Join("testdata", name)
}

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := goldenPath(name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, got, 0o644); err != nil { //nolint:gosec // test fixture
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // fixed test path
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s: output does not match the golden file\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestRenderGoldenAgreed(t *testing.T) {
	checkGolden(t, "agreed.golden.md", renderFixture(t, agreedDecision(), true))
}

func TestRenderGoldenEscalated(t *testing.T) {
	checkGolden(t, "escalated.golden.md", renderFixture(t, escalatedDecision(), true))
}

// TestRenderDeterministic: decision.md §Markdown, "the same input gives the
// same bytes": no render time, no locale, LF only, one trailing newline.
func TestRenderDeterministic(t *testing.T) {
	a := renderFixture(t, agreedDecision(), true)
	b := renderFixture(t, agreedDecision(), true)
	if !bytes.Equal(a, b) {
		t.Fatal("Render is not deterministic across repeated runs")
	}
	if bytes.Contains(a, []byte("\r")) {
		t.Fatal("Render output has a CR: must be LF only")
	}
	if !bytes.HasSuffix(a, []byte("\n")) || bytes.HasSuffix(a, []byte("\n\n")) {
		t.Fatal("Render output must end with exactly one trailing newline")
	}
}

// TestRenderUnconfirmedBanner: review 43 H1, a single-signed Decision
// carries the UNCONFIRMED banner and "claimed by the initiator" wording.
func TestRenderUnconfirmedBanner(t *testing.T) {
	out := renderFixture(t, agreedDecision(), false)
	s := string(out)
	if !strings.Contains(s, "UNCONFIRMED: signed by the initiator only") {
		t.Error("missing the UNCONFIRMED banner")
	}
	if !strings.Contains(s, "claimed by the initiator") {
		t.Error("missing \"Outcome claimed by the initiator\"")
	}
	if strings.Contains(s, "refused to sign") {
		t.Error("awaiting_peer must not carry the peer_refused sentence")
	}
}

// TestRenderUnsignedBanner: review 47 L7, B's own peer_refused row has no
// signatures at all (B never signs on a mismatch); the Markdown must not
// claim "signed by the initiator only" or print an empty signature member,
// but must still carry the UNCONFIRMED banner.
func TestRenderUnsignedBanner(t *testing.T) {
	out, err := decision.Render(agreedDecision(), "6ec367cd5f0f82b1d929878e678ba1aafdd3c33cb13dc22da2fba55836094ede", "", "", true, testNames, testFPs)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "UNCONFIRMED: unsigned") {
		t.Errorf("missing the unsigned UNCONFIRMED banner:\n%s", s)
	}
	if strings.Contains(s, "signed by the initiator only") {
		t.Error("must not claim the initiator signed when neither side did")
	}
	if strings.Contains(s, "Signature (initiator):") || strings.Contains(s, "Signature (respondent):") {
		t.Errorf("must not print an empty signature line:\n%s", s)
	}
	if !strings.Contains(s, "Signed by: none (unsigned)") {
		t.Errorf("missing \"Signed by: none (unsigned)\":\n%s", s)
	}
}

func TestRenderPeerRefusedBanner(t *testing.T) {
	sigI, _ := mdSignaturesFor(false)
	out, err := decision.Render(agreedDecision(), "6ec367cd5f0f82b1d929878e678ba1aafdd3c33cb13dc22da2fba55836094ede", sigI, "", true, testNames, testFPs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "The respondent refused to sign: its record differs.") {
		t.Error("peer_refused must add the refusal sentence")
	}
}

func TestRenderHumanDecisionLabel(t *testing.T) {
	out := renderFixture(t, agreedDecision(), true)
	if !strings.Contains(string(out), "approved on the initiator's machine; the other side cannot check this") {
		t.Error("missing the human decision's machine label")
	}
}

// TestRenderKeepsMultilineWhitespace: decision.md §Markdown "a long argument
// full of newlines and tabs (kept, not collapsed)".
func TestRenderKeepsMultilineWhitespace(t *testing.T) {
	d := agreedDecision()
	arg := "Line one.\n\tIndented line two.\n\nLine four after a blank line.\n"
	pos := d["positions"].(map[string]any)["initiator"].(map[string]any)["initial"].(map[string]any)
	pos["argument"] = arg
	out := renderFixture(t, d, true)
	if !strings.Contains(string(out), "Line one.\n\tIndented line two.\n\nLine four after a blank line.") {
		t.Errorf("multi-line argument was collapsed or altered:\n%s", out)
	}
}

// adversarial is decision.md §Markdown's review 43 test set: HTML, an
// autolink-shaped URL, a javascript: link, backtick runs of 1-10, table and
// heading syntax, a leading '>', bidi overrides, zero-width characters and a
// '~~~' fence, plus (review 43 M2) a multi-line argument whose lines start
// at column 0 with '# ', '- ', '<div>' and '[x]: http://e', and (M1)
// zero-width and tag characters.
var adversarialSingleLine = "<script>alert(1)</script> <img src=x onerror=alert(1)> [x](javascript:alert(1)) https://evil.example ``` |cell| # heading --- > quote ~~~fence" +
	string(rune(0x200b)) + string(rune(0x202e)) + string(rune(0xE0041))

var adversarialMultiLine = "# heading at column 0\n- list item at column 0\n<div>raw html</div>\n[x]: http://e\nplain line with a zero-width" +
	string(rune(0x200b)) + "space and a tag" + string(rune(0xE0041)) + " character\nbacktick runs: ` `` ``` ```` ````` `` ` `` ``` (up to ten)"

func adversarialDecision() map[string]any {
	d := agreedDecision()
	pos := d["positions"].(map[string]any)
	init := pos["initiator"].(map[string]any)["initial"].(map[string]any)
	init["claim"] = adversarialSingleLine
	init["argument"] = adversarialMultiLine
	d["human_decisions"] = []any{
		map[string]any{"id": "c-00000000000000000000000000000001", "by": "respondent", "at": "2026-10-01T09:10:00Z", "text": adversarialSingleLine},
	}
	d["problem"].(map[string]any)["topic"] = adversarialMultiLine
	return d
}

// baselineHeadingCount renders a Decision whose peer-controlled fields hold
// plain safe text, so its heading/table/link/image/script counts are the
// document structure's own (daemon-written), independent of peer text.
func baselineHeadingCount(t *testing.T) map[string]int {
	t.Helper()
	return tagCounts(t, renderFixture(t, agreedDecision(), true))
}

func tagCounts(t *testing.T, md []byte) map[string]int {
	t.Helper()
	gm := goldmark.New(goldmark.WithExtensions(extension.GFM))
	var buf bytes.Buffer
	if err := gm.Convert(md, &buf); err != nil {
		t.Fatalf("goldmark: %v", err)
	}
	html := buf.String()
	return map[string]int{
		"<a":      strings.Count(html, "<a "),
		"<img":    strings.Count(html, "<img"),
		"<script": strings.Count(html, "<script"),
		"<table":  strings.Count(html, "<table"),
		"<h":      strings.Count(html, "<h1") + strings.Count(html, "<h2") + strings.Count(html, "<h3") + strings.Count(html, "<h4") + strings.Count(html, "<h5") + strings.Count(html, "<h6"),
	}
}

// TestRenderAdversarialIsInert is the security test (review 43 L8): the
// adversarial Decision, rendered and then parsed by a real CommonMark + GFM
// engine, produces no <a, <img, <script or <table from peer text at all, and
// exactly the document's own (daemon-written) heading count, never more.
func TestRenderAdversarialIsInert(t *testing.T) {
	base := baselineHeadingCount(t)
	adv := tagCounts(t, renderFixture(t, adversarialDecision(), true))
	for _, tag := range []string{"<a", "<img", "<script", "<table"} {
		if adv[tag] != 0 {
			t.Errorf("adversarial Decision produced %d %s tag(s); peer text must never become one", adv[tag], tag)
		}
	}
	if adv["<h"] != base["<h"] {
		t.Errorf("adversarial Decision produced %d headings, want the document's own %d (peer text must never become a heading)", adv["<h"], base["<h"])
	}
}

// TestRenderNoInvisibleCharacters: review 43 M1, zero-width and tag
// characters are escaped to \u{...} and never appear literally in the
// Markdown.
func TestRenderNoInvisibleCharacters(t *testing.T) {
	out := renderFixture(t, adversarialDecision(), true)
	for _, r := range []rune{0x200b, 0x202e, 0xE0041} {
		if strings.ContainsRune(string(out), r) {
			t.Errorf("rendered Markdown contains raw U+%04X; it must be escaped", r)
		}
	}
	if !strings.Contains(string(out), `\u{200B}`) {
		t.Error("expected the zero-width space escaped as \\u{200B}")
	}
}

// TestBinariesDoNotLinkGoldmark: the owner decision requires goldmark to be
// test-only. go list -deps over each shipped binary's package must not name
// it.
func TestBinariesDoNotLinkGoldmark(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	for _, pkg := range []string{
		"github.com/Magazem/Dorylinae-Agentnet/cmd/agentnet",
		"github.com/Magazem/Dorylinae-Agentnet/cmd/agentnetd",
		"github.com/Magazem/Dorylinae-Agentnet/cmd/relay",
	} {
		out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput() //nolint:gosec // pkg is one of the three fixed strings above
		if err != nil {
			t.Fatalf("go list -deps %s: %v\n%s", pkg, err, out)
		}
		if strings.Contains(string(out), "github.com/yuin/goldmark") {
			t.Errorf("%s links github.com/yuin/goldmark, which must stay test-only", pkg)
		}
	}
}
