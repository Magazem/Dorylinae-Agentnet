package pathid

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func TestResolvePlainDirUnchanged(t *testing.T) {
	dir := filepath.Join(testutil.TempDir(t), "proj")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	want, err := Resolve(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(want, "proj") {
		t.Fatalf("Resolve(%q) = %q, want %q", dir, got, filepath.Join(want, "proj"))
	}
	// Idempotent: a stored resolved path resolves to itself (recheck).
	if again, err := Resolve(got); err != nil || again != got {
		t.Fatalf("Resolve(Resolve(p)) = %q, %v; want %q", again, err, got)
	}
}

func TestResolveFollowsSymlink(t *testing.T) {
	base := testutil.TempDir(t)
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	a, err := Resolve(link)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Resolve(target)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("Resolve(link) = %q, Resolve(target) = %q", a, b)
	}
}

func TestAncestors(t *testing.T) {
	root := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		root = `C:\`
	}
	got := Ancestors(filepath.Join(root, "a", "b"))
	want := []string{filepath.Join(root, "a", "b"), filepath.Join(root, "a"), root}
	if len(got) != len(want) {
		t.Fatalf("Ancestors = %q, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Ancestors = %q, want %q", got, want)
		}
	}
}

func TestIsRoot(t *testing.T) {
	root := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		root = filepath.VolumeName(os.Getenv("SystemRoot")) + `\`
	}
	if ok, err := IsRoot(root); err != nil || !ok {
		t.Fatalf("IsRoot(%q) = %v, %v", root, ok, err)
	}
	dir, err := Resolve(testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := IsRoot(dir); err != nil || ok {
		t.Fatalf("IsRoot(%q) = %v, %v; want false", dir, ok, err)
	}
}

// An Lstat error other than "does not exist" on a middle component refuses
// the path: the guard never walks past what it could not see (review 61b
// F7b-1).
func TestResolveFailsClosedOnLstatError(t *testing.T) {
	// The guard follows links, so on macOS (/var -> /private/var) it lstats
	// the target's spelling, not the caller's: spell the path physically.
	base, err := filepath.EvalSymlinks(testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "a", "b")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	middle := filepath.Dir(dir)
	old := lstat
	t.Cleanup(func() { lstat = old })
	lstat = func(p string) (os.FileInfo, error) {
		if p == middle {
			return nil, os.ErrPermission
		}
		return old(p)
	}
	if got, err := Resolve(dir); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Resolve(%q) = %q, %v; want ErrPermission", dir, got, err)
	}
}
