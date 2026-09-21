package version

import (
	"bytes"
	"strings"
	"testing"
)

func TestMain_Flags(t *testing.T) {
	tests := []struct {
		args     []string
		wantCode int
		wantOut  string
	}{
		{[]string{"--version"}, 0, "demo " + Version},
		{[]string{"--help"}, 0, "Usage:"},
		{[]string{"-h"}, 0, "Usage:"},
		{nil, 0, "demo " + Version},
		{[]string{"--bogus"}, 2, ""},
	}
	for _, tc := range tests {
		var out, errb bytes.Buffer
		code := Main("demo", "Demo binary.", tc.args, &out, &errb)
		if code != tc.wantCode {
			t.Errorf("%v: code = %d, want %d", tc.args, code, tc.wantCode)
		}
		if !strings.Contains(out.String(), tc.wantOut) {
			t.Errorf("%v: stdout = %q, want substring %q", tc.args, out.String(), tc.wantOut)
		}
	}
}
