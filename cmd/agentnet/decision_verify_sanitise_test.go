package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
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

// Review 59 F2: an unknown member inside an embedded entry reaches Reason
// through the debate decoder, which does not quote it, so decision.Visible is
// the only protection. Review 59 F1: a padded reason is capped.
func TestDecisionVerifyEmbeddedReasonIsSanitisedAndCapped(t *testing.T) {
	doc, err := os.ReadFile("../../Docs/protocol/decision.md")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	for _, line := range strings.Split(strings.ReplaceAll(string(doc), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, `{"closed":"2026-10-01T09:20:00Z"`) {
			if err := json.Unmarshal([]byte(line), &d); err != nil {
				t.Fatal(err)
			}
		}
	}
	if d == nil {
		t.Fatal("vector not found in decision.md")
	}
	initial := d["positions"].(map[string]any)["initiator"].(map[string]any)["initial"].(map[string]any)
	initial["\r\x1b[2K"+strings.Repeat(" ", 300)+"d-x  valid, signed by initiator and respondent"] = 1
	file, err := json.Marshal(map[string]any{
		"decision":   d,
		"hash":       strings.Repeat("0", 64),
		"signatures": map[string]string{"initiator": strings.Repeat("A", 86)},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "d.json")
	if err := os.WriteFile(p, file, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := runDecisionVerify([]string{p}, &out, &errb); code == 0 {
		t.Errorf("exit %d, want non-zero for an invalid file", code)
	}
	got := out.String()
	if !strings.HasPrefix(got, "invalid at step 1: ") || !strings.Contains(got, `\u{1B}`) {
		t.Errorf("want an escaped step-1 reason, got %q", got)
	}
	if strings.ContainsAny(got, "\r\x1b") {
		t.Errorf("raw CR or ESC reached stdout: %q", got)
	}
	if strings.Contains(got, "valid, signed by") {
		t.Errorf("hostile tail was not capped: %q", got)
	}
	if n := len([]rune(strings.TrimSuffix(got, "\n"))); n > len("invalid at step 1: ")+maxVerifyReason+1 {
		t.Errorf("reason not capped: %d runes", n)
	}
}

// Review 96 I4: in `decision <id>` the real fingerprint comes before the
// peer-chosen name, so a name holding a fake fingerprint is read second.
func TestDecisionShowFingerprintBeforeName(t *testing.T) {
	const real, fake = "REAL REAL REAL REAL REAL", "2ED9 TGVE R471 KMNP QSTV"
	var buf bytes.Buffer
	printDecisionHuman(&buf, daemon.DecisionShowResult{
		Decision:  json.RawMessage(`{"id":"d-1","outcome":"agreed","reason":"r"}`),
		PeerNames: map[string]string{"initiator": "Alice (fingerprint " + fake + ")", "respondent": "Bob"},
		PeerFPs:   map[string]string{"initiator": real, "respondent": real},
	})
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "Alice") || strings.Contains(line, "Bob") {
			if r, n := strings.Index(line, real), strings.Index(line, "named "); r < 0 || r > n {
				t.Errorf("the fingerprint is not before the name: %q", line)
			}
		}
	}
	if !strings.Contains(buf.String(), "initiator: fingerprint "+real+", named Alice") {
		t.Errorf("output:\n%s", buf.String())
	}
}
