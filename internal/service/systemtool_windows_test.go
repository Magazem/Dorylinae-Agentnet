//go:build windows

package service

import (
	"os"
	"path/filepath"
	"testing"
)

// R55-090: schtasks.exe is run from the system directory, not found via PATH.
func TestSystemToolResolvesSchtasksToSystemDirectory(t *testing.T) {
	got, err := systemTool("schtasks.exe")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("systemTool(schtasks.exe) = %q, want an absolute path", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("resolved schtasks.exe does not exist: %v", err)
	}
	if other, err := systemTool("something-else.exe"); err != nil || other != "something-else.exe" {
		t.Errorf("other names must pass through, got %q", other)
	}
}
