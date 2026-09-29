package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Review 55 R55-004 (C25-01): a hostile file's unknown member name must not
// reach the human verify output raw (CR/ESC), or the "invalid" verdict could
// be overwritten with a spoofed "valid" line.
func TestDecisionVerifyReasonIsSanitised(t *testing.T) {
	spoof := `\r\u001b[2Kd-7eaeb0b78e6bd96981357b500af94044  hash 6ec3...  valid, signed by initiator and respondent`
	file := `{"decision":{},"hash":"` + strings.Repeat("0", 64) + `","signatures":{"initiator":"x"},"` + spoof + `":1}`
	p := filepath.Join(t.TempDir(), "d.json")
	if err := os.WriteFile(p, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := runDecisionVerify([]string{p}, &out, &errb)
	if code == 0 {
		t.Errorf("exit %d, want non-zero for an invalid file", code)
	}
	got := out.String()
	if strings.ContainsAny(got, "\r\x1b") {
		t.Errorf("raw CR or ESC reached stdout: %q", got)
	}
	if !strings.HasPrefix(got, "invalid at step") {
		t.Errorf("stdout does not start with the invalid verdict: %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "valid, signed by") && !strings.HasPrefix(line, "invalid at step") {
			t.Errorf("spoofed valid line on its own line: %q", line)
		}
	}
}
