package main

import (
	"bytes"
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
