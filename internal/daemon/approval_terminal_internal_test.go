package daemon

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
)

// review 30 M8: an over-long stdin line is refused and skipped; the reader
// keeps going (a bufio.Scanner would stop for good on ErrTooLong).
func TestTerminalReaderSurvivesOverlongLine(t *testing.T) {
	as, err := approval.NewStore(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := strings.Repeat("9", 5000) + "\n" + strings.Repeat("8", terminalLineLimit+1) + "\nreject a-abcdef\nreject a-fedcba"
	var out bytes.Buffer
	runTerminalApprovalReader(context.Background(), strings.NewReader(in), &out, as)
	got := out.String()
	if n := strings.Count(got, "ignored"); n != 2 {
		t.Fatalf("want 2 over-long lines refused, got %d: %q", n, got)
	}
	for _, tag := range []string{"a-abcdef", "a-fedcba"} {
		if !strings.Contains(got, `no pending approval matches "`+tag+`"`) {
			t.Fatalf("line after the over-long one not handled (%s): %q", tag, got)
		}
	}
}
