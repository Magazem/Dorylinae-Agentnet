//go:build !windows

package device

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 40 L11: a program others can change is refused at scope-set time
// and by CheckTarget at each run start: a world-writable directory on its
// path (including a sticky one such as /tmp), a world-writable file. A
// program on a private path is accepted.
func TestCheckProgramOwnerUnix(t *testing.T) {
	dir := testutil.PrivateDir(t)
	prog := filepath.Join(dir, "tool")
	if err := os.WriteFile(prog, []byte("x"), 0o700); err != nil { //nolint:gosec // an executable test file
		t.Fatal(err)
	}
	if err := CheckProgramOwner(prog); err != nil {
		t.Fatalf("a private program: %v", err)
	}
	if err := CheckTarget(prog, dir); err != nil {
		t.Fatalf("CheckTarget, private program: %v", err)
	}

	// The same program under a world-writable directory.
	shared := filepath.Join(dir, "shared")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	inShared := filepath.Join(shared, "tool")
	if err := os.WriteFile(inShared, []byte("x"), 0o700); err != nil { //nolint:gosec // an executable test file
		t.Fatal(err)
	}
	var we *WritableError
	if err := CheckProgramOwner(inShared); !errors.As(err, &we) || we.Path != shared {
		t.Fatalf("a program in a world-writable directory: %v", err)
	}
	if err := CheckTarget(inShared, dir); !errors.As(err, &we) {
		t.Fatalf("CheckTarget, world-writable directory: %v", err)
	}
	sc := baseScope()
	sc.Commands[0].Argv = []string{inShared}
	var se *ScopeError
	if _, err := ValidateScope(sc, scopeNow, Resolver{RepoPath: func(raw string) (string, error) { return raw, nil }}); !errors.As(err, &se) ||
		se.Field != "commands[0].argv[0]" || !strings.HasPrefix(se.Reason, ReasonWritableByOthers+":") {
		t.Fatalf("scope with a program in a world-writable directory: %v", err)
	}

	// A world-writable file in a private directory.
	if err := os.Chmod(prog, 0o777); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	if err := CheckProgramOwner(prog); !errors.As(err, &we) || we.Path != prog {
		t.Fatalf("a world-writable program: %v", err)
	}

	// A program in the sticky, world-writable temp directory.
	tmp := testutil.TempDir(t)
	inTmp := filepath.Join(tmp, "tool")
	if err := os.WriteFile(inTmp, []byte("x"), 0o700); err != nil { //nolint:gosec // an executable test file
		t.Fatal(err)
	}
	if fi, err := os.Stat(os.TempDir()); err == nil && fi.Mode().Perm()&0o002 != 0 {
		if err := CheckProgramOwner(inTmp); !errors.As(err, &we) {
			t.Fatalf("a program under %s: %v", os.TempDir(), err)
		}
	}

	// A link to the program is judged by the link's directory and by the
	// target's path.
	link := filepath.Join(dir, "link")
	if err := os.Symlink(inShared, link); err != nil {
		t.Fatal(err)
	}
	if err := CheckProgramOwner(link); !errors.As(err, &we) || we.Path != shared {
		t.Fatalf("a link to a program in a world-writable directory: %v", err)
	}
}

// The owner, group and mode rule on its own.
func TestClassifyUnix(t *testing.T) {
	const self, other = 1000, 1001
	trusted := func(gid uint64) bool { return gid == 0 || gid == 50 }
	for _, tc := range []struct {
		uid, gid uint64
		perm     os.FileMode
		ok       bool
	}{
		{self, 1000, 0o755, true},
		{0, 0, 0o755, true},
		{0, 0, 0o775, true},     // root's group
		{self, 50, 0o775, true}, // an admin or the user's private group
		{self, 1234, 0o775, false},
		{self, 1000, 0o757, false},
		{0, 0, 0o777 | os.ModeSticky, false}, // sticky and world-writable, like /tmp
		{other, 1001, 0o755, false},
		{other, 1001, 0o700, false},
	} {
		err := classifyUnix("/p", tc.uid, tc.gid, tc.perm, self, trusted)
		if (err == nil) != tc.ok {
			t.Errorf("uid %d gid %d perm %o: err = %v, want ok %v", tc.uid, tc.gid, tc.perm, err, tc.ok)
		}
	}
}

// Review 41 M1: a link to a link is judged along every hop. Here the
// middle link sits in a world-writable directory that is neither on the
// path as given nor on the fully resolved path, so whoever can write there
// could point it at another program.
func TestCheckProgramOwnerLinkHops(t *testing.T) {
	dir := testutil.PrivateDir(t)
	for _, d := range []string{"real", "open", "bin"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	prog := filepath.Join(dir, "real", "tool")
	if err := os.WriteFile(prog, []byte("x"), 0o700); err != nil { //nolint:gosec // an executable test file
		t.Fatal(err)
	}
	hop := filepath.Join(dir, "open", "hop")
	if err := os.Symlink(filepath.Join("..", "real", "tool"), hop); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "bin", "tool")
	if err := os.Symlink(hop, link); err != nil {
		t.Fatal(err)
	}
	if err := CheckProgramOwner(link); err != nil {
		t.Fatalf("links through private directories: %v", err)
	}
	open := filepath.Join(dir, "open")
	if err := os.Chmod(open, 0o777); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	var we *WritableError
	if err := CheckProgramOwner(link); !errors.As(err, &we) || we.Path != open {
		t.Fatalf("a link whose middle hop others can replace: %v", err)
	}

	// A loop of links is refused, not followed forever.
	a, b := filepath.Join(dir, "bin", "a"), filepath.Join(dir, "bin", "b")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	if err := CheckProgramOwner(a); err == nil {
		t.Fatal("a loop of links was accepted")
	}
}
