package displaytext

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func hasHidden(s string) bool {
	for _, r := range s {
		if Hidden(r) || r == utf8.RuneError {
			return true
		}
	}
	return false
}

// R55-F10 A1 (inverted review 55 T11-04): the graphic but invisible runes
// termSafe let through are escaped.
func TestTermEscapesInvisible(t *testing.T) {
	for in, want := range map[string]string{
		"report\u3164.txt": `report\u{3164}.txt`,
		"report\u115F.txt": `report\u{115F}.txt`,
		"report\uFE0F.txt": `report\u{FE0F}.txt`,
		"report\u2800.txt": `report\u{2800}.txt`,
		"report\u200B.txt": `report\u{200B}.txt`,
		"report\u202E.txt": `report\u{202E}.txt`,
	} {
		got := Term(in)
		if got != want {
			t.Errorf("Term(%+q) = %q, want %q", in, got, want)
		}
		if hasHidden(got) {
			t.Errorf("Term(%+q) = %+q holds a hidden rune", in, got)
		}
	}
}

// R55-F10 A2.
func TestTerm(t *testing.T) {
	for in, want := range map[string]string{
		"a\x1b[31mb":          `a\u{1B}[31mb`,
		"a\rb\nc\x00d":        `a\u{D}b\u{A}c\u{0}d`,
		"a\u0085b":            `a\u{85}b`,
		"a\uFFFDb":            `a\u{FFFD}b`,
		"a\xffb":              `a\u{FFFD}b`,
		"a\tb":                `a\u{9}b`,
		"e\u0301\u0301\u0301": "e\u0301\u0301" + `\u{301}`,
		"plain ASCII 123":     "plain ASCII 123",
		"café":                "café",
		"日本語":                 "日本語",
		"👩\u200D💻 ❤\uFE0F":    `👩\u{200D}💻 ❤\u{FE0F}`,
		`C:\path\x`:           `C:\path\x`,
	} {
		got := Term(in)
		if got != want {
			t.Errorf("Term(%+q) = %+q, want %+q", in, got, want)
		}
		if hasHidden(got) {
			t.Errorf("Term(%+q) = %+q holds a hidden rune", in, got)
		}
		if again := Term(got); again != got {
			t.Errorf("Term not idempotent on %+q: %+q", got, again)
		}
	}
}

// R55-F10 A3.
func TestBlock(t *testing.T) {
	got := Block("a\n  state done\r\n\tb\n", "    ")
	want := "a\n      state done\\u{D}\n    \tb"
	if got != want {
		t.Fatalf("Block = %q, want %q", got, want)
	}
	for _, l := range strings.Split(got, "\n") {
		if l == "  state done" {
			t.Fatalf("Block produced a line posing as a field line")
		}
	}
	if got := Block("one\x1b", "  "); got != `one\u{1B}` {
		t.Fatalf("Block single line = %q", got)
	}
	if got := Block("", "  "); got != "" {
		t.Fatalf("Block empty = %q", got)
	}
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Review 90 S90-2: an undecodable byte is written as \ufffd.
func TestJSONInvalidUTF8(t *testing.T) {
	got := JSON([]byte("{\"t\":\"a\x9b31m\xffz\"}"))
	want := `{"t":"a\ufffd31m\ufffdz"}`
	if string(got) != want {
		t.Errorf("JSON = %q, want %q", got, want)
	}
}

// R55-F10 A4.
func TestJSON(t *testing.T) {
	in := "a\u202Eb\u0085c\x7Fd\U000E0041"
	raw := encode(t, map[string]string{"n": in})
	got := JSON(raw)
	for _, esc := range []string{`\u202e`, `\u0085`, `\u007f`, `\udb40\udc41`} {
		if !bytes.Contains(got, []byte(esc)) {
			t.Errorf("JSON output %s lacks %s", got, esc)
		}
	}
	if hasHidden(strings.TrimSuffix(string(got), "\n")) {
		t.Errorf("JSON output %+q holds a hidden rune", got)
	}
	var back map[string]string
	if err := json.Unmarshal(got, &back); err != nil || back["n"] != in {
		t.Fatalf("decode = %+q, %v; want %+q", back["n"], err, in)
	}
	for _, v := range []any{
		map[string]string{"a": "plain", "b": "<&>"},
		[]string{"café", "日本語", "👩💻", "\x01\u2028"},
		map[string]any{"n": 1.5, "x": nil, "t": true},
	} {
		raw := encode(t, v)
		if got := JSON(raw); !bytes.Equal(got, raw) {
			t.Errorf("JSON changed %s to %s", raw, got)
		}
	}
}
