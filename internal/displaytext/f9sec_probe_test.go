package displaytext

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

// Review 75 (R55-F9 security) probe: random inputs from a hostile alphabet,
// every bound 4..300; Line's output is always <= max bytes, valid UTF-8, free
// of hidden runes, and a prefix-cut of clean() ending in "…" when cut.
func TestF9SecLineProperties(t *testing.T) {
	alphabet := []string{"a", " ", "\x1b", "\x9b", "\u009b", "\xff", "\xe2\x80", "‮", "​", "́", "ः",
		"\U0001F600", "é", "\r\n", " ", "ㅤ", "️", "\U000E0041", "…", "\x00", "؜", "א"}
	r := rand.New(rand.NewPCG(1, 2))
	for iter := 0; iter < 20000; iter++ {
		var b strings.Builder
		n := r.IntN(6000)
		for b.Len() < n {
			b.WriteString(alphabet[r.IntN(len(alphabet))])
		}
		in := b.String()
		max := 4 + r.IntN(297)
		out := Line(in, max)
		if len(out) > max || !utf8.ValidString(out) {
			t.Fatalf("max %d: %d bytes, valid %v", max, len(out), utf8.ValidString(out))
		}
		for _, x := range out {
			if Hidden(x) {
				t.Fatalf("hidden %U in output", x)
			}
		}
		full := string(clean(in))
		if out != full {
			if !strings.HasSuffix(out, "…") {
				t.Fatalf("changed without ellipsis: %q", out)
			}
			if !strings.HasPrefix(full, strings.TrimSuffix(out, "…")) {
				t.Fatalf("not a prefix of clean(): %q", out)
			}
		}
	}
}
