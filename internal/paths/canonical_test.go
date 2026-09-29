package paths

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 55 R55-088: two spellings of one config dir must derive the same
// names, or two daemons run on one DB (Windows) and keychain keys look lost.
func TestCanonicalOneNamePerDir(t *testing.T) {
	dir := testutil.TempDir(t)
	other := testutil.OtherSpelling(t, dir)
	if Canonical(dir) != Canonical(other) {
		t.Fatalf("Canonical(%s) = %s, Canonical(%s) = %s", dir, Canonical(dir), other, Canonical(other))
	}
	// A dir that does not exist yet resolves through its existing parent.
	if a, b := Canonical(filepath.Join(dir, "home")), Canonical(filepath.Join(other, "home")); a != b {
		t.Fatalf("missing child: %s != %s", a, b)
	}
	if runtime.GOOS == "windows" {
		// Not EqualFold(dir): CI's %TEMP% is an 8.3 name (RUNNER~1).
		if c := Canonical(dir); strings.HasPrefix(c, `\\?\`) || !filepath.IsAbs(c) {
			t.Fatalf("Canonical(%s) = %s, want a plain absolute path", dir, c)
		}
		pa, _ := In(dir)
		pb, _ := In(other)
		if pa.Endpoint != pb.Endpoint {
			t.Fatalf("two pipes for one dir: %s, %s", pa.Endpoint, pb.Endpoint)
		}
		// Case of a component that does not exist yet does not matter either.
		pc, _ := In(filepath.Join(dir, "Home"))
		pd, _ := In(filepath.Join(other, "HOME"))
		if pc.Endpoint != pd.Endpoint {
			t.Fatalf("two pipes for one missing dir: %s, %s", pc.Endpoint, pd.Endpoint)
		}
	}
}

func TestCanonicalKeepsDistinctDirs(t *testing.T) {
	a, b := testutil.TempDir(t), testutil.TempDir(t)
	if Canonical(a) == Canonical(b) {
		t.Fatal("distinct dirs share a canonical name")
	}
}
