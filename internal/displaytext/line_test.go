package displaytext

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// R55-F9 test 4: Line, the one-line rendering (Docs/protocol/approval.md
// §Sanitising, displayLine).

func TestLine(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		max      int
		want     string
	}{
		{"empty", "", 200, ""},
		{"plain", "dial tcp 127.0.0.1:8787: connection refused", 200, "dial tcp 127.0.0.1:8787: connection refused"},
		{"digits kept", "code 482913", 200, "code 482913"},
		{"OSC 8 and erase line", "\x1b]8;;https://evil.example/\x07click\x1b]8;;\x07\x1b[2K", 200, "]8;;https://evil.example/ click ]8;; [2K"},
		{"CR LF TAB", "a\r\nb\tc", 200, "a b c"},
		{"bidi and zero width", "a\u202eb\u200bc\u2066d\ufeffe", 200, "abcde"},
		{"R46 set", "a\u3164b\u115fc\ufe0fd\u2800e\u00a0f", 200, "abcd e f"},
		{"invalid UTF-8", "a\xffb", 200, "a\ufffdb"},
		{"stacked marks", "a\u0301\u0302\u0303\u0304b", 200, "a\u0301\u0302b"},
		{"spaces collapse and trim", "  a    b  ", 200, "a b"},
		{"only hidden", "\u200b\u202e", 200, ""},
		{"cut", strings.Repeat("a", 10), 8, "aaaaa…"},
		{"cut after a space", "abcd efgh", 8, "abcd…"},
		{"cut through a 4-byte rune", "ab\U0001F600\U0001F600", 8, "ab…"},
		{"exact fit", "abcdefgh", 8, "abcdefgh"},
		{"minimum", "abcdef", 4, "a…"},
	} {
		got := Line(tc.in, tc.max)
		if got != tc.want {
			t.Errorf("%s: Line(%q, %d) = %q, want %q", tc.name, tc.in, tc.max, got, tc.want)
		}
	}
}

func TestLineBoundAndSafety(t *testing.T) {
	inputs := []string{
		"",
		strings.Repeat("\x1b[2K\r\n\u202e\u200b\u3164\ufe0f\u2800x\u0301\u0302\u0303\u0304\u0305 ", 5000),
		strings.Repeat("\U0001F600", 2000),
		strings.Repeat("é ", 3000),
		strings.Repeat("\xff", 9000),
		"abc def ghi " + strings.Repeat("é", 300),
	}
	for _, n := range []int{4, 64, 200, 256} {
		for i, in := range inputs {
			out := Line(in, n)
			if len(out) > n || !utf8.ValidString(out) {
				t.Errorf("input %d, max %d: %d bytes, valid %v", i, n, len(out), utf8.ValidString(out))
			}
			for _, r := range out {
				if Hidden(r) {
					t.Errorf("input %d, max %d: hidden rune %U", i, n, r)
				}
			}
			if strings.HasSuffix(out, " …") || strings.HasPrefix(out, " ") || strings.HasSuffix(out, " ") {
				t.Errorf("input %d, max %d: untrimmed %q", i, n, out)
			}
		}
	}
}

// An input longer than what Line reads is a cut even if the part read renders
// empty: never "" without "…".
func TestLineLongHiddenInput(t *testing.T) {
	in := strings.Repeat("\u200b", 5*1024/3+1) + "abc"
	got := Line(in, 200)
	if got != "…" && got != "abc" {
		t.Fatalf("Line = %q, want \"…\" (pre-cut) or \"abc\" (scan on)", got)
	}
	if got := Line(strings.Repeat("a", 5000), 256); len(got) > 256 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long input: %d bytes %q", len(got), got[len(got)-5:])
	}
}
