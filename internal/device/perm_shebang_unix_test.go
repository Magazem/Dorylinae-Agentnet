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

// Review 55 R55-097: a "#!" script on a private path is refused when its
// interpreter, or the interpreter of that interpreter, can be changed by
// others, or is found through PATH; with a private interpreter it is
// accepted, at scope-set time and by CheckTarget at each run start.
func TestCheckProgramOwnerChecksInterpreter(t *testing.T) {
	dir := testutil.PrivateDir(t)
	write := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o700); err != nil { //nolint:gosec // an executable test file
			t.Fatal(err)
		}
	}
	interp := filepath.Join(dir, "interp")
	write(interp, "x")
	script := filepath.Join(dir, "ci.sh")
	write(script, "#!"+interp+" -e\nexit 0\n")
	if err := CheckProgramOwner(script); err != nil {
		t.Fatalf("a script with a private interpreter: %v", err)
	}
	if err := CheckTarget(script, dir); err != nil {
		t.Fatalf("CheckTarget, a script with a private interpreter: %v", err)
	}

	// The interpreter sits in a world-writable directory.
	shared := filepath.Join(dir, "shared")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	sharedInterp := filepath.Join(shared, "node")
	write(sharedInterp, "x")
	write(script, "#!"+sharedInterp+"\n")
	var we *WritableError
	if err := CheckProgramOwner(script); !errors.As(err, &we) || we.Path != shared {
		t.Fatalf("a script whose interpreter others can change: %v", err)
	}
	if err := CheckTarget(script, dir); !errors.As(err, &we) {
		t.Fatalf("CheckTarget, a script whose interpreter others can change: %v", err)
	}
	sc := baseScope()
	sc.Commands[0].Argv = []string{script}
	var se *ScopeError
	if _, err := ValidateScope(sc, scopeNow, Resolver{RepoPath: func(raw string) (string, error) { return raw, nil }}); !errors.As(err, &se) ||
		se.Field != "commands[0].argv[0]" || !strings.HasPrefix(se.Reason, ReasonWritableByOthers+":") {
		t.Fatalf("scope with a script whose interpreter others can change: %v", err)
	}

	// The interpreter is itself a script whose interpreter others can change.
	mid := filepath.Join(dir, "mid")
	write(mid, "#!"+sharedInterp+"\n")
	write(script, "#!"+mid+"\n")
	if err := CheckProgramOwner(script); !errors.As(err, &we) || we.Path != shared {
		t.Fatalf("a script through a nested interpreter others can change: %v", err)
	}

	// env finds its program through PATH when the run starts.
	write(script, "#!/usr/bin/env sh\n")
	if err := CheckProgramOwner(script); err == nil || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("a script run through env: %v", err)
	}
}
