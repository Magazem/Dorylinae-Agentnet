package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 55 C26-01 (R55-096): '#', '?' and '%xx' in the database path are
// path characters, not URI syntax.
func TestOpenPathWithURICharacters(t *testing.T) {
	root := testutil.TempDir(t)
	names := []string{"a#b", "x%41y", "plain dir"}
	if runtime.GOOS != "windows" {
		names = append(names, "q?r") // '?' is not a legal Windows file name character
	}
	for _, name := range names {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "dorylinae.db")
		s, err := Open(context.Background(), path)
		if err != nil {
			t.Errorf("%s: Open: %v", name, err)
			continue
		}
		_ = s.Close()
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: no database at the intended path: %v", name, err)
		}
		ro, err := OpenReadOnly(path)
		if err != nil {
			t.Errorf("%s: OpenReadOnly: %v", name, err)
			continue
		}
		_ = ro.Close()
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(names) {
		t.Errorf("stray files next to the intended directories: %v", entries)
	}
}

func TestFileURIEscapes(t *testing.T) {
	for in, want := range map[string]string{
		"/h/a#b/x.db": "file:/h/a%23b/x.db",
		"/h/x%41y.db": "file:/h/x%2541y.db",
		"/h/q?r.db":   "file:/h/q%3Fr.db",
		"//host/s/x":  "file:////host/s/x",
	} {
		if got := fileURI(in); got != want {
			t.Errorf("fileURI(%q) = %q, want %q", in, got, want)
		}
	}
}
