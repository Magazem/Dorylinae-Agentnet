package notify

import "testing"

func TestClean(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"plain", "hello world", 80, "hello world"},
		{"control chars become spaces", "a\x00b\x01c\x1fd\x7fe\u009ff", 80, "a b c d e f"},
		{"collapses runs of control-derived spaces", "a\x00\x01\x02b", 80, "a b"},
		{"tab and newline are control chars", "a\tb\nc", 80, "a b c"},
		{"leading and trailing spaces are trimmed", "  hi  ", 80, "hi"},
		{"multiple internal spaces collapse to one", "a    b", 80, "a b"},
		{"empty string", "", 80, ""},
		{"exact length is not truncated", "abcde", 5, "abcde"},
		{"over length is truncated with ellipsis", "abcdef", 5, "abcd…"},
		{"truncation counts code points not bytes", "\U0001F600\U0001F600\U0001F600\U0001F600\U0001F600\U0001F600", 5, "\U0001F600\U0001F600\U0001F600\U0001F600…"},
		{"max of 1 is just the ellipsis", "abcdef", 1, "…"},
		{"max of 0 is unbounded", "abcdef", 0, "abcdef"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Clean(c.in, c.max)
			if got != c.want {
				t.Errorf("Clean(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
			}
		})
	}
}

// bidiAndSeparatorCodePoints are the bidi controls (U+200E, U+200F, U+202A,
// U+202E, U+2066, U+2069) and the line/paragraph separators (U+2028,
// U+2029) that Clean must turn into spaces (Docs/protocol/notify.md §Text
// and sanitising). Built from code points, not written as literal
// characters, so the source file carries no raw bidi-control or
// format-control bytes (gosec G116, staticcheck ST1018).
var bidiAndSeparatorCodePoints = []rune{0x200E, 0x200F, 0x202A, 0x202E, 0x2066, 0x2069, 0x2028, 0x2029}

func TestCleanBidiAndSeparatorControls(t *testing.T) {
	for _, r := range bidiAndSeparatorCodePoints {
		in := "a" + string(r) + "b"
		want := "a b"
		if got := Clean(in, 80); got != want {
			t.Errorf("Clean(%q, 80) = %q, want %q (U+%04X)", in, got, want, r)
		}
	}
}

func TestCleanTitleAndNameLimits(t *testing.T) {
	long := make([]rune, 200)
	for i := range long {
		long[i] = 'a'
	}
	if got := len([]rune(Clean(string(long), 80))); got != 80 {
		t.Errorf("title clean length = %d, want 80", got)
	}
	if got := len([]rune(Clean(string(long), 40))); got != 40 {
		t.Errorf("name clean length = %d, want 40", got)
	}
}
