package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// review 30 L1: control characters (NUL, newline) never reach a dialog's
// argv, environment or script, the summary is bounded, and the window says
// where the code is (review 29 L5).
func TestWindowTextSanitises(t *testing.T) {
	tag, kind, sum := windowText("a-012345", "grant\x00", "peer\x00 \"bob\"\n--help $(x) `y`"+strings.Repeat("z", 5000))
	for _, v := range []string{tag, kind, sum} {
		if strings.ContainsAny(v, "\x00\n\r") {
			t.Fatalf("control character survived: %q", v)
		}
	}
	if kind != "grant" {
		t.Fatalf("kind = %q", kind)
	}
	if n := utf8.RuneCountInString(sum); n > maxWindowSummary+200 {
		t.Fatalf("summary not bounded: %d code points", n)
	}
	if !strings.Contains(sum, "The code is in the AgentNet notification for a-012345.") {
		t.Fatalf("missing where-the-code-is sentence: %q", sum[len(sum)-150:])
	}
}
