package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test vector from Docs/protocol/agent-card.md, written with literal characters
// and extra top-level members as `agentnet identity --json` prints them.
const vector = `{"ok":true,"key_backend":"file","card":{"version":1,"name":"Ada \"test\" <é>","public_key":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","harness":"custom","skills":[{"id":"review","name":"Code review","description":"a/b & c"}],"created":"2026-01-02T03:04:05Z"},"signature":"XN3GYSED9twF4mei-x7TUzHYzOMQU7aonCRQkebGdcXr8MvkkjLQVjZmtPiCNLTNigKIskMMBqF9hgQW5jdPDA"}`

func runOn(in string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(strings.NewReader(in), &out, &errb)
	return code, out.String(), errb.String()
}

func TestVectorVerifies(t *testing.T) {
	code, out, errs := runOn(vector)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.HasPrefix(out, `OK "Ada \"test\" <é>" A6EHv_`) {
		t.Fatalf("unexpected output %q", out)
	}
}

func TestTamperFails(t *testing.T) {
	for name, repl := range map[string][2]string{
		"name":      {`"Ada`, `"Eve`},
		"harness":   {`"custom"`, `"codex"`},
		"created":   {`03:04:05Z`, `03:04:06Z`},
		"version":   {`"version":1`, `"version":2`},
		"skill":     {`"Code review"`, `"Code hack"`},
		"extra":     {`"harness"`, `"x":"y","harness"`},
		"key":       {`A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg`, `A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbA`},
		"signature": {`XN3GYSED`, `XN3GYSEE`},
	} {
		t.Run(name, func(t *testing.T) {
			in := strings.Replace(vector, repl[0], repl[1], 1)
			if in == vector {
				t.Fatal("mutation did not apply")
			}
			if code, _, _ := runOn(in); code == 0 {
				t.Fatal("tampered card verified")
			}
		})
	}
}

func TestExitCodes(t *testing.T) {
	bad := strings.Replace(vector, `"Ada`, `"Eve`, 1)
	if code, _, _ := runOn(bad); code != 1 {
		t.Fatalf("invalid signature: exit %d, want 1", code)
	}
	for name, in := range map[string]string{
		"empty":         ``,
		"garbage":       `nope`,
		"no card":       `{"signature":"x"}`,
		"duplicate key": strings.Replace(vector, `"version":1`, `"version":1,"version":1`, 1),
		"float":         strings.Replace(vector, `"version":1`, `"version":1.0`, 1),
	} {
		if code, _, _ := runOn(in); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
}

func TestAcceptsUTF8BOM(t *testing.T) {
	if code, _, errs := runOn("\xef\xbb\xbf" + vector); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
}

// A4 (review 68): the agent-card.md vectors, read from the transcription in
// tools/verifyvectors/vectors.json. P1 and P2 verify; N1-N5 and N17 verify at
// step 4 but fail the schema (exit 1, INVALID); N6-N16 are malformed (exit 2).
// R55-F13 A2: N16 (over 16384 bytes) exits 2, N17 (33 skills) exits 1.
// R55-F10 A10: N18 and N19 (a bidi control, a line separator) exit 1; P3
// (emoji ZWJ and VS16 in the name) verifies.
func TestNegativeVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "verifyvectors", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		AgentCard struct {
			Cases []struct {
				Name     string `json:"name"`
				Envelope string `json:"envelope"`
				FailsAt  int    `json:"fails_at"`
			} `json:"cases"`
		} `json:"agent_card"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.AgentCard.Cases) != 22 {
		t.Fatalf("want 22 cases, got %d", len(v.AgentCard.Cases))
	}
	for _, c := range v.AgentCard.Cases {
		want := 2
		switch {
		case c.FailsAt == 0:
			want = 0
		case c.FailsAt >= 4:
			want = 1
		}
		code, out, errs := runOn(c.Envelope)
		if code != want {
			t.Errorf("%s: exit %d, want %d (%s%s)", c.Name, code, want, out, errs)
		}
		if want == 1 && !strings.Contains(errs, "INVALID: card does not match the v1 schema") {
			t.Errorf("%s: want a schema refusal, got %q", c.Name, errs)
		}
		if (c.Name == "P1" || c.Name == "P2") && !strings.HasPrefix(out, `OK "Ada \"test\" <é>" A6EHv_`) {
			t.Errorf("%s: output %q", c.Name, out)
		}
		if c.Name == "N16" && !strings.Contains(errs, "16727 bytes, over the limit of 16384") {
			t.Errorf("N16: want a size refusal, got %q", errs)
		}
		if c.Name == "P3" && !strings.HasPrefix(out, `OK "Ada `) {
			t.Errorf("P3: output %q", out)
		}
		if c.Name == "N18" && !strings.Contains(errs, "name contains U+202E") {
			t.Errorf("N18: want a bidi refusal, got %q", errs)
		}
		if c.Name == "N19" && !strings.Contains(errs, "description contains U+2028") {
			t.Errorf("N19: want a line separator refusal, got %q", errs)
		}
		if c.Name == "N17" && !strings.Contains(errs, "33 skills, at most 32") {
			t.Errorf("N17: want a skills refusal, got %q", errs)
		}
	}
}

// R55-F13 A2: the size is checked before the parse. A document one byte over
// the limit is refused for its size although it is not JSON; one at the limit
// reaches the parse.
func TestSizeBeforeParse(t *testing.T) {
	code, _, errs := runOn(strings.Repeat("{", maxCardBytes+1))
	if code != 2 || !strings.Contains(errs, "16385 bytes, over the limit of 16384") {
		t.Fatalf("exit %d (%s), want 2 and a size refusal", code, errs)
	}
	code, _, errs = runOn(strings.Repeat("{", maxCardBytes))
	if code != 2 || strings.Contains(errs, "over the limit") {
		t.Fatalf("exit %d (%s), want 2 and a parse error", code, errs)
	}
}

func TestStrictParse(t *testing.T) {
	for name, in := range map[string]string{
		"lone high in name": strings.Replace(vector, `"ok"`, `"o\ud800k"`, 1),
		"upper case hex":    strings.Replace(vector, `"ok":true`, `"x":"\uDC00"`, 1),
		"fraction ignored":  strings.Replace(vector, `"ok":true`, `"ok":1e400`, 1),
	} {
		if code, _, errs := runOn(in); code != 2 {
			t.Errorf("%s: exit %d, want 2 (%s)", name, code, errs)
		}
	}
	for name, in := range map[string]string{
		"escaped backslash": strings.Replace(vector, `"ok":true`, `"x":"\\ud800"`, 1),
		"pair":              strings.Replace(vector, `"ok":true`, `"x":"`+esc("d83d")+esc("de00")+`"`, 1),
		"non-surrogate":     strings.Replace(vector, `"ok":true`, `"x":"`+esc("0041")+`"`, 1),
	} {
		if code, _, errs := runOn(in); code != 0 {
			t.Errorf("%s: exit %d, want 0 (%s)", name, code, errs)
		}
	}
	for name, in := range map[string]string{
		"high then non-low": strings.Replace(vector, `"ok":true`, `"x":"`+esc("d800")+esc("0041")+`"`, 1),
		"low then high":     strings.Replace(vector, `"ok":true`, `"x":"`+esc("dc00")+esc("d800")+`"`, 1),
	} {
		if code, _, errs := runOn(in); code != 2 {
			t.Errorf("%s: exit %d, want 2 (%s)", name, code, errs)
		}
	}
}

// esc returns the six-character JSON escape of the code unit hex, built at
// run time so that no tool rewrites it in the source.
func esc(hex string) string {
	return string(rune(0x5C)) + "u" + hex
}
