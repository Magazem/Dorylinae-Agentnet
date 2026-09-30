package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVectors(t *testing.T) {
	var out bytes.Buffer
	if n := run(&out, vectorsJSON); n != 0 {
		t.Fatalf("%d check(s) failed:\n%s", n, out.String())
	}
	if !strings.Contains(out.String(), "PASS tag_R") || !strings.Contains(out.String(), "PASS HPKE open plaintext") ||
		!strings.Contains(out.String(), "PASS audit row 3 hash") || !strings.Contains(out.String(), "PASS audit row 2 chain_start counts") {
		t.Fatalf("expected checks did not run:\n%s", out.String())
	}
}

// A corrupted expected value must be reported as a failure.
func TestDetectsMismatch(t *testing.T) {
	bad := bytes.Replace(vectorsJSON, []byte(`"5f88441e`), []byte(`"5f88441f`), 1)
	if bytes.Equal(bad, vectorsJSON) {
		t.Fatal("test corruption did not apply")
	}
	var out bytes.Buffer
	if n := run(&out, bad); n == 0 {
		t.Fatalf("mismatch not detected:\n%s", out.String())
	}
}

// A corrupted audit-chain hash must be reported as a failure.
func TestDetectsAuditMismatch(t *testing.T) {
	bad := bytes.Replace(vectorsJSON, []byte(`"3daa4dae`), []byte(`"3daa4daf`), 1)
	if bytes.Equal(bad, vectorsJSON) {
		t.Fatal("test corruption did not apply")
	}
	var out bytes.Buffer
	if n := run(&out, bad); n != 1 || !strings.Contains(out.String(), "FAIL audit row 3 hash") {
		t.Fatalf("audit mismatch not detected (%d failures):\n%s", n, out.String())
	}
}

// Ticket 3.3a: the Decision checks run, and a corrupted Decision hash is
// reported as exactly that failure.
func TestDetectsDecisionMismatch(t *testing.T) {
	var out bytes.Buffer
	if n := run(&out, vectorsJSON); n != 0 || !strings.Contains(out.String(), "PASS decision sig respondent") ||
		!strings.Contains(out.String(), "PASS decision negative fails at step 5") {
		t.Fatalf("decision checks did not run:\n%s", out.String())
	}
	bad := bytes.Replace(vectorsJSON, []byte(`"6ec367cd`), []byte(`"6ec367ce`), 1)
	if bytes.Equal(bad, vectorsJSON) {
		t.Fatal("test corruption did not apply")
	}
	out.Reset()
	if n := run(&out, bad); n == 0 || !strings.Contains(out.String(), "FAIL decision hash") {
		t.Fatalf("decision mismatch not detected (%d failures):\n%s", n, out.String())
	}
}

// Review 68 A5: the agent-card cases run, and a case whose stated step is
// changed (N1 from 5 to 4) is reported as that case's failure.
func TestDetectsAgentCardMismatch(t *testing.T) {
	var out bytes.Buffer
	if n := run(&out, vectorsJSON); n != 0 || !strings.Contains(out.String(), "PASS agent_card P1 verifies") ||
		!strings.Contains(out.String(), "PASS agent_card N1 fails at step 5") ||
		!strings.Contains(out.String(), "PASS agent_card N14 fails at step 2") ||
		!strings.Contains(out.String(), "PASS agent_card N15 fails at step 1") ||
		!strings.Contains(out.String(), "PASS agent_card P2 verifies") ||
		!strings.Contains(out.String(), "PASS agent_card N16 fails at step 1") ||
		!strings.Contains(out.String(), "PASS agent_card N17 fails at step 5") {
		t.Fatalf("agent card checks did not run:\n%s", out.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(vectorsJSON, &doc); err != nil {
		t.Fatal(err)
	}
	cases := doc["agent_card"].(map[string]any)["cases"].([]any)
	n1 := cases[1].(map[string]any)
	if n1["name"] != "N1" {
		t.Fatalf("case 1 is %v", n1["name"])
	}
	n1["fails_at"] = 4
	bad, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if n := run(&out, bad); n != 1 || !strings.Contains(out.String(), "FAIL agent_card N1 fails at step 4") {
		t.Fatalf("agent card mismatch not detected (%d failures):\n%s", n, out.String())
	}
}

// R55-F13 A2: the size is checked before the parse, so a document one byte
// over the limit is refused for its size even when it is not JSON at all.
func TestCardSizeBeforeParse(t *testing.T) {
	_, err := verifyCardEnvelope(bytes.Repeat([]byte{'{'}, maxCardBytes+1))
	if err == nil || !strings.Contains(err.Error(), "step 1: 16385 bytes, over 16384") {
		t.Fatalf("got %v, want a size refusal at step 1", err)
	}
	if _, err := verifyCardEnvelope(bytes.Repeat([]byte{'{'}, maxCardBytes)); err == nil || strings.Contains(err.Error(), "over") {
		t.Fatalf("a document at the limit must reach the parse, got %v", err)
	}
}

// The strict parse applies to every vector document: a lone surrogate
// escape is refused, an escaped backslash before "u" is not an escape.
func TestPairedSurrogates(t *testing.T) {
	bs := string(rune(0x5C))
	for in, ok := range map[string]bool{
		`{"a":"` + bs + `ud800"}`:                false,
		`{"a":"` + bs + `udc00"}`:                false,
		`{"a":"` + bs + `ud800` + bs + `u0041"}`: false,
		`{"` + bs + `uD800":1}`:                  false,
		`{"a":"` + bs + bs + `ud800"}`:           true,
		`{"a":"` + bs + `ud83d` + bs + `ude00"}`: true,
		`{"a":"` + bs + `u0041` + bs + `u00e9"}`: true,
		`{"a":"x"}`:                              true,
	} {
		if _, err := canonical([]byte(in)); (err == nil) != ok {
			t.Errorf("canonical(%s): err %v, want ok=%v", in, err, ok)
		}
	}
}

// Ticket 4.0a: the relay auth v2 checks run, and a corrupted signature is
// reported as that case's failure.
func TestDetectsRelayAuthMismatch(t *testing.T) {
	var out bytes.Buffer
	if n := run(&out, vectorsJSON); n != 0 || !strings.Contains(out.String(), "PASS relay_auth frame") ||
		!strings.Contains(out.String(), "PASS relay_auth negative: case 0 signature fails as v1") {
		t.Fatalf("relay auth checks did not run:\n%s", out.String())
	}
	bad := bytes.Replace(vectorsJSON, []byte(`"5HmkkA1F`), []byte(`"5HmkkA1G`), 1)
	if bytes.Equal(bad, vectorsJSON) {
		t.Fatal("test corruption did not apply")
	}
	out.Reset()
	if n := run(&out, bad); n != 1 || !strings.Contains(out.String(), "FAIL relay_auth case 2") {
		t.Fatalf("relay auth mismatch not detected (%d failures):\n%s", n, out.String())
	}
}
