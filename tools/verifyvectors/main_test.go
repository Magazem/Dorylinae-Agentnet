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
	if !strings.Contains(out.String(), "PASS tag_R") || !strings.Contains(out.String(), "PASS HPKE open plaintext") {
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
