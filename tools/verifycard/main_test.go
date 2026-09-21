package main

import (
	"bytes"
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
