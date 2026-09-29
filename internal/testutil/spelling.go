package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// OtherSpelling returns a second path to the existing directory dir: the
// upper-cased path on Windows (case-insensitive), a symlink elsewhere.
func OtherSpelling(t testing.TB, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		up := strings.ToUpper(dir)
		if up == dir {
			t.Skip("path has no lower-case letters")
		}
		return up
	}
	link := filepath.Join(TempDir(t), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	return link
}
