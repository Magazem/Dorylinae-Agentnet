//go:build windows || darwin

package notify

import (
	"strings"
	"testing"
)

func TestParseAnswerLine(t *testing.T) {
	cases := map[string]dialogAnswer{
		"approve 123456\r\n": {kind: "approve", code: "123456"},
		"approve 12a":        {kind: "approve", code: "12a"}, // malformed: the Store reopens
		"reject":             {kind: "reject"},
		"dismiss":            {kind: "dismiss"},
		"approve":            {kind: "dismiss"},
		"Reject":             {kind: "dismiss"},
		"":                   {kind: "dismiss"},
		"approve " + strings.Repeat("1", maxAnswerLine): {kind: "dismiss"}, // over 256 bytes
	}
	for in, want := range cases {
		if got := parseAnswerLine(in); got != want {
			t.Errorf("parseAnswerLine(%.20q) = %+v, want %+v", in, got, want)
		}
	}
}
