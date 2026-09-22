package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Ticket 1.5: `agentnet request --help` carries the brief template.
func TestRequestHelpHasBriefTemplate(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"request", "--help"}, &out, &errb); code != exitOK {
		t.Fatalf("request --help: code %d", code)
	}
	for _, want := range []string{"What: <one sentence", "Why: <one or two", "Done when: <how", "--brief-from-file", "Exit codes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("request --help lacks %q", want)
		}
	}
}

// Ticket 1.5: `--brief-from-file -` reads stdin; input is bounded and must be UTF-8.
func TestReadBriefFile(t *testing.T) {
	old := requestStdin
	t.Cleanup(func() { requestStdin = old })

	requestStdin = strings.NewReader("What: x\r\nWhy: y\r\n")
	got, err := readBriefFile("-")
	if err != nil || got != "What: x\r\nWhy: y\r\n" {
		t.Fatalf("stdin brief = %q, %v", got, err)
	}

	requestStdin = strings.NewReader(strings.Repeat("a", maxBriefFileBytes))
	if _, err := readBriefFile("-"); err != nil {
		t.Errorf("brief of %d bytes: %v", maxBriefFileBytes, err)
	}
	requestStdin = strings.NewReader(strings.Repeat("a", maxBriefFileBytes+1))
	if _, err := readBriefFile("-"); err == nil {
		t.Error("over-long stdin brief: want an error")
	}
	requestStdin = strings.NewReader("What: caf\xe9\n") // Latin-1, not UTF-8
	if _, err := readBriefFile("-"); err == nil {
		t.Error("invalid UTF-8 brief: want an error")
	}

	p := filepath.Join(testutil.TempDir(t), "brief.md")
	if err := os.WriteFile(p, []byte("What: from a file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := readBriefFile(p); err != nil || got != "What: from a file\n" {
		t.Errorf("file brief = %q, %v", got, err)
	}
	if _, err := readBriefFile(filepath.Join(filepath.Dir(p), "missing.md")); err == nil {
		t.Error("missing file: want an error")
	}
}

// A --brief-from-file problem is a bad flag value: exit 2 with code usage
// (Docs/cli/request.md §Exit codes), before the daemon is contacted.
func TestRequestBriefFileErrorIsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	missing := filepath.Join(testutil.TempDir(t), "missing.md")
	code := run([]string{"request", "@bob", "task", "--title", "t", "--brief-from-file", missing, "--json"}, &out, &errb)
	if code != exitUsage || !strings.Contains(out.String(), `"usage"`) {
		t.Errorf("missing brief file: code %d, out %q; want %d and code usage", code, out.String(), exitUsage)
	}
}
