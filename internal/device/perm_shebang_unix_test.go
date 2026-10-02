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
// others; with a private interpreter it is
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
	if err := CheckTarget(script, dir, nil); err != nil {
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
	if err := CheckTarget(script, dir, nil); !errors.As(err, &we) {
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

}

// D71 (review 86 M1): "#!/usr/bin/env X" finds X on the PATH of the run's
// environment, not the daemon's, and the program found is checked like an
// interpreter. npm's "#!/usr/bin/env node" passes with node in a private
// directory and is refused, naming the program found, with node in one
// others can write, or behind a directory others can write that is
// searched first; X that is not found, or a relative PATH entry searched
// first, is refused.
func TestCheckProgramOwnerResolvesEnvOnRunPath(t *testing.T) {
	if _, err := os.Stat("/usr/bin/env"); err != nil {
		t.Skip("no /usr/bin/env")
	}
	dir := testutil.PrivateDir(t)
	write := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o700); err != nil { //nolint:gosec // an executable test file
			t.Fatal(err)
		}
	}
	mkdir := func(name string, perm os.FileMode) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, perm); err != nil {
			t.Fatal(err)
		}
		return p
	}
	private := mkdir("private", 0o700)
	shared := mkdir("shared", 0o777)
	sharedEmpty := mkdir("shared-empty", 0o777)
	empty := mkdir("empty", 0o700)
	write(filepath.Join(private, "node"), "x")
	write(filepath.Join(shared, "node"), "x")
	script := filepath.Join(dir, "npm-cli.js")
	write(script, "#!/usr/bin/env node\nrequire('npm')\n")

	runEnv := func(path string) []string { return []string{"HOME=" + dir, "PATH=" + path} }
	// The daemon's own PATH holds the shared node: only the run's counts.
	t.Setenv("PATH", shared)
	if err := CheckProgramOwnerEnv(script, runEnv(private)); err != nil {
		t.Fatalf("env node, node in a private directory: %v", err)
	}
	if err := CheckTarget(script, dir, runEnv(empty+":"+private)); err != nil {
		t.Fatalf("CheckTarget, env node, node in a private directory: %v", err)
	}
	if err := CheckProgramOwnerEnv(filepath.Join(dir, "plain"), nil); err == nil {
		t.Fatal("a missing program passed")
	}

	var we *WritableError
	err := CheckProgramOwnerEnv(script, runEnv(shared+":"+private))
	if !errors.As(err, &we) || we.Path != shared || !strings.Contains(err.Error(), filepath.Join(shared, "node")) {
		t.Fatalf("env node, node in a directory others can write: %v", err)
	}
	if err := CheckTarget(script, dir, runEnv(shared)); !errors.As(err, &we) {
		t.Fatalf("CheckTarget, env node, node in a directory others can write: %v", err)
	}
	if err := CheckProgramOwnerEnv(script, runEnv(sharedEmpty+":"+private)); !errors.As(err, &we) || we.Path != sharedEmpty {
		t.Fatalf("env node, a directory others can write searched first: %v", err)
	}
	if err := CheckProgramOwnerEnv(script, runEnv(empty)); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("env node, node not on PATH: %v", err)
	}
	if err := CheckProgramOwnerEnv(script, []string{"HOME=" + dir}); err == nil || !strings.Contains(err.Error(), "no PATH") {
		t.Fatalf("env node, no PATH: %v", err)
	}
	if err := CheckProgramOwnerEnv(script, runEnv("bin:"+private)); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("env node, a relative PATH entry first: %v", err)
	}

	// The program found is a script too: its own interpreter is checked.
	write(filepath.Join(private, "node"), "#!"+filepath.Join(shared, "node")+"\n")
	if err := CheckProgramOwnerEnv(script, runEnv(private)); !errors.As(err, &we) || we.Path != shared {
		t.Fatalf("env node, node a script whose interpreter others can change: %v", err)
	}
	write(filepath.Join(private, "node"), "x")

	// An absolute program after env is checked as it is.
	write(script, "#!/usr/bin/env -S "+filepath.Join(shared, "node")+" --flag\n")
	if err := CheckProgramOwnerEnv(script, runEnv(private)); !errors.As(err, &we) || we.Path != shared {
		t.Fatalf("env -S /abs/node in a directory others can write: %v", err)
	}
	write(script, "#!/usr/bin/env -S node --flag\n")
	if err := CheckProgramOwnerEnv(script, runEnv(private)); err != nil {
		t.Fatalf("env -S node, node in a private directory: %v", err)
	}

	// At scope-set time, the PATH is the one runs get: the daemon's.
	sc := baseScope()
	sc.Commands[0].Argv = []string{script}
	res := Resolver{RepoPath: func(raw string) (string, error) { return raw, nil }}
	var se *ScopeError
	if _, err := ValidateScope(sc, scopeNow, res); !errors.As(err, &se) ||
		se.Field != "commands[0].argv[0]" || !strings.HasPrefix(se.Reason, ReasonWritableByOthers+":") {
		t.Fatalf("scope, env node, node on the daemon's PATH in a directory others can write: %v", err)
	}
	t.Setenv("PATH", private)
	if _, err := ValidateScope(sc, scopeNow, res); err != nil {
		t.Fatalf("scope, env node, node in a private directory: %v", err)
	}
}

// Review 86b M1, L1: env's execvp moves on to the next X on the PATH when
// one fails to run, so X is the first one this user may execute
// (access X_OK), every directory on the PATH is checked, the ones after X
// too, and a missing one is checked through its nearest existing ancestor,
// which could create it before the run starts. A relative directory
// anywhere on the PATH is refused.
func TestCheckProgramOwnerEnvWholePath(t *testing.T) {
	if _, err := os.Stat("/usr/bin/env"); err != nil {
		t.Skip("no /usr/bin/env")
	}
	dir := testutil.PrivateDir(t)
	write := func(p, body string, perm os.FileMode) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o700); err != nil { //nolint:gosec // an executable test file
			t.Fatal(err)
		}
		if err := os.Chmod(p, perm); err != nil {
			t.Fatal(err)
		}
	}
	mkdir := func(name string, perm os.FileMode) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, perm); err != nil {
			t.Fatal(err)
		}
		return p
	}
	private := mkdir("private", 0o700)
	first := mkdir("first", 0o700)
	shared := mkdir("shared", 0o777)
	write(filepath.Join(private, "node"), "x", 0o700)
	write(filepath.Join(shared, "node"), "x", 0o700)
	script := filepath.Join(dir, "npm-cli.js")
	write(script, "#!/usr/bin/env node\n", 0o700)
	runEnv := func(path string) []string { return []string{"HOME=" + dir, "PATH=" + path} }

	var we *WritableError
	if err := CheckProgramOwnerEnv(script, runEnv(private+":"+shared)); !errors.As(err, &we) || we.Path != shared ||
		!strings.Contains(err.Error(), filepath.Join(private, "node")) {
		t.Fatalf("env node, a directory others can write after node: %v", err)
	}
	missing := filepath.Join(shared, "later", "bin")
	if err := CheckProgramOwnerEnv(script, runEnv(missing+":"+private)); !errors.As(err, &we) || we.Path != shared {
		t.Fatalf("env node, a missing PATH directory others could create: %v", err)
	}
	if err := CheckProgramOwnerEnv(script, runEnv(filepath.Join(private, "later", "bin")+":"+private)); err != nil {
		t.Fatalf("env node, a missing PATH directory under a private one: %v", err)
	}
	if err := CheckProgramOwnerEnv(script, runEnv(private+":bin")); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("env node, a relative PATH entry after node: %v", err)
	}

	// A node this user may not execute (owner bits rw-, others --x) is
	// skipped as execvp skips it: the next one is the program found.
	if os.Geteuid() == 0 {
		t.Skip("root may execute a file with any execute bit")
	}
	write(filepath.Join(first, "node"), "#!"+filepath.Join(shared, "node")+"\n", 0o611)
	if err := CheckProgramOwnerEnv(script, runEnv(first+":"+shared)); !errors.As(err, &we) || we.Path != shared ||
		!strings.Contains(err.Error(), filepath.Join(shared, "node")) {
		t.Fatalf("env node, the first node not executable by this user: %v", err)
	}
	if err := CheckProgramOwnerEnv(script, runEnv(first+":"+private)); err != nil {
		t.Fatalf("env node, the first node not executable, then a private one: %v", err)
	}
}

// Review 86 L1, review 86b L2, D72: a program that can be executed but not
// read may be a script whose interpreter the kernel would still run: it is
// refused, set-user-ID (mode 4111) too.
func TestCheckProgramOwnerExecuteOnly(t *testing.T) {
	dir := testutil.PrivateDir(t)
	prog := filepath.Join(dir, "tool")
	if err := os.WriteFile(prog, []byte("#!/nonexistent/interp\n"), 0o700); err != nil { //nolint:gosec // an executable test file
		t.Fatal(err)
	}
	if err := os.Chmod(prog, 0o111); err != nil { //nolint:gosec // execute-only: the point of the test
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads every file")
	}
	if err := CheckProgramOwner(prog); err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("an execute-only program: %v", err)
	}
	if err := os.Chmod(prog, os.ModeSetuid|0o111); err != nil { //nolint:gosec // execute-only set-ID: the point of the test
		t.Fatal(err)
	}
	if fi, err := os.Stat(prog); err != nil || fi.Mode()&os.ModeSetuid == 0 {
		t.Skipf("the set-user-ID bit did not stick: %v", err)
	}
	if err := CheckProgramOwner(prog); err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("an execute-only set-user-ID program: %v", err)
	}
}
