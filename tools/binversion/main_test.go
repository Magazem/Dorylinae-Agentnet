package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReleaseTargets links cmd/relay for every release target as the release
// workflow's check does, and reads the version back; an unversioned build and
// a stripped one must not pass.
func TestReleaseTargets(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-builds cmd/relay six times")
	}
	dir := t.TempDir()
	build := func(goos, goarch, ldflags string) string {
		t.Helper()
		out := filepath.Join(dir, goos+"-"+goarch+"-"+strings.ReplaceAll(ldflags, " ", ""))
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, "../../cmd/relay") //nolint:gosec // test builds a fixed package
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0", "GOFLAGS=")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s/%s: %v\n%s", goos, goarch, err, b)
		}
		return out
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			got, err := read(build(goos, goarch, "-w -X "+symbol+"=1.2.3"))
			if err != nil || got != "1.2.3" {
				t.Errorf("%s/%s: read = %q, %v; want 1.2.3", goos, goarch, got, err)
			}
			if got, err := read(build(goos, goarch, "-w")); err != nil || got == "1.2.3" {
				t.Errorf("%s/%s unversioned: read = %q, %v; want the default", goos, goarch, got, err)
			}
			if got, err := read(build(goos, goarch, "-s -w -X "+symbol+"=1.2.3")); err == nil {
				t.Errorf("%s/%s stripped: read = %q, want an error", goos, goarch, got)
			}
		}
	}
}

func TestNotABinary(t *testing.T) {
	f := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(f, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := read(f); err == nil {
		t.Fatal("read accepted a non-binary")
	}
}
