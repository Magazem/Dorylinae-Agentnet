//go:build windows

package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Security review 85 (R55-F22): fileURI must open the database at exactly
// the given path for the path spellings Windows accepts.
func TestFileURIWindowsPathForms(t *testing.T) {
	root := testutil.TempDir(t)
	long := filepath.Join(root, strings.Repeat("d", 120), strings.Repeat("e", 120))
	if err := os.MkdirAll(`\\?\`+long, 0o700); err != nil {
		t.Fatal(err)
	}
	uni := filepath.Join(root, "ünï 日本 #%41&x=y")
	if err := os.MkdirAll(uni, 0o700); err != nil {
		t.Fatal(err)
	}
	vol := filepath.VolumeName(root) // "C:"
	unc := `\\localhost\` + strings.TrimSuffix(vol, ":") + `$` + root[len(vol):]
	cases := map[string]struct{ open, real string }{
		"unicode+#%&=":      {filepath.Join(uni, "a.db"), filepath.Join(uni, "a.db")},
		"extended \\\\?\\":  {`\\?\` + filepath.Join(root, "b.db"), filepath.Join(root, "b.db")},
		"long \\\\?\\ >260": {`\\?\` + filepath.Join(long, "c.db"), `\\?\` + filepath.Join(long, "c.db")},
		"UNC admin share":   {filepath.Join(unc, "d.db"), filepath.Join(root, "d.db")},
		"forward slashes":   {filepath.ToSlash(filepath.Join(root, "e.db")), filepath.Join(root, "e.db")},
	}
	for name, c := range cases {
		t.Logf("%s: %s -> %s", name, c.open, fileURI(c.open))
		s, err := Open(context.Background(), c.open)
		if err != nil {
			t.Errorf("%s: Open(%q): %v", name, c.open, err)
			continue
		}
		_ = s.Close()
		if _, err := os.Stat(c.real); err != nil {
			t.Errorf("%s: no database at %q: %v", name, c.real, err)
		}
		ro, err := OpenReadOnly(c.open)
		if err != nil {
			t.Errorf("%s: OpenReadOnly: %v", name, err)
			continue
		}
		_ = ro.Close()
	}
}
