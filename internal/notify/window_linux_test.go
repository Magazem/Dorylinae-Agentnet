//go:build linux

package notify

import (
	"strings"
	"testing"
)

// review 30 M1/M2: every exit yields an answer, and only a real OK or
// Reject press counts as "already exited with a valid answer".
func TestDecodeExit(t *testing.T) {
	cases := []struct {
		zenity    bool
		code      int
		line      string
		wantKind  string
		wantValid bool
	}{
		{true, 0, "123456", "approve", true},
		{true, 1, "Reject", "reject", true},
		{true, 1, "", "dismiss", false}, // Cancel, or GTK could not open the display
		{true, 5, "", "dismiss", false}, // timeout
		{true, -1, "", "dismiss", false},
		{true, 0, strings.Repeat("9", maxAnswerLine+1), "dismiss", false},
		{false, 0, "123456", "approve", true},
		{false, 1, "", "dismiss", false},
		{false, 0, strings.Repeat("9", maxAnswerLine+1), "dismiss", false},
	}
	for _, c := range cases {
		got, valid := decodeExit(c.zenity, c.code, c.line)
		if got.kind != c.wantKind || valid != c.wantValid {
			t.Errorf("decodeExit(%v, %d, %.10q) = %s/%v, want %s/%v", c.zenity, c.code, c.line, got.kind, valid, c.wantKind, c.wantValid)
		}
	}
}
