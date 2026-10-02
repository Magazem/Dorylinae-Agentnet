package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// R55-087: the --*-from-file readers are bounded and refuse a non-regular
// file (a directory stands in for a FIFO, which cannot be made on every OS).
func TestReadBounded(t *testing.T) {
	dir := t.TempDir()
	if _, err := readBounded(dir, nil, 10, "x"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory: %v", err)
	}
	big := filepath.Join(dir, "big")
	if err := os.WriteFile(big, bytes.Repeat([]byte("a"), 11), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBounded(big, nil, 10, "x"); err == nil || !strings.Contains(err.Error(), "over 10 bytes") {
		t.Fatalf("over limit: %v", err)
	}
	if b, err := readBounded(big, nil, 11, "x"); err != nil || len(b) != 11 {
		t.Fatalf("at limit: %d %v", len(b), err)
	}
	if _, err := readBounded("-", strings.NewReader(strings.Repeat("a", 11)), 10, "x"); err == nil {
		t.Fatal("stdin over limit accepted")
	}
}

func TestReadOutputFileIsBounded(t *testing.T) {
	big := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(big, bytes.Repeat([]byte("a"), maxOutputFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readOutputFile(big); err == nil {
		t.Fatal("an over-limit output file was read")
	}
	if _, err := readOutputFile(t.TempDir()); err == nil {
		t.Fatal("a directory was read as an output file")
	}
	if _, err := readBriefFile(t.TempDir()); err == nil {
		t.Fatal("a directory was read as a brief file")
	}
}
