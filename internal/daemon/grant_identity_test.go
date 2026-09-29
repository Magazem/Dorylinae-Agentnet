package daemon

// Regression tests for review 55 R55-006, R55-027 and R55-028
// (Docs/review/55-code-review/99-report.md): forbidden resources are judged
// by file identity, a UNC path is refused without being dialled, and a .git
// resource is refused.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/pathid"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// identityDirs returns an existing config dir holding a dorylinae.db and an
// allowed project directory beside it.
func identityDirs(t *testing.T) (cfg, proj string) {
	t.Helper()
	base := testutil.TempDir(t)
	cfg = filepath.Join(base, "dorylinae-config")
	proj = filepath.Join(base, "proj")
	for _, d := range []string{cfg, proj} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(cfg, "dorylinae.db"), []byte("SECRET-DB"), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg, proj
}

// A legitimate directory is still accepted, stored in its resolved form, and
// passes the approval-time recheck.
func TestValidateResourceAllowsProjectDir(t *testing.T) {
	cfg, proj := identityDirs(t)
	resolved, ierr := validateResource(cfg, proj)
	if ierr != nil {
		t.Fatalf("project dir refused: %s", ierr.Message)
	}
	want, err := pathid.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("resolved = %q, want %q", resolved, want)
	}
	if err := recheckResource(cfg, capability.Record{Path: resolved, Action: capability.ActionFSRead}); err != nil {
		t.Fatalf("recheck of an unchanged resource: %v", err)
	}
}

// The config dir, inside it, containing it, the home dir and a root.
func TestValidateResourceRefusesForbidden(t *testing.T) {
	cfg, _ := identityDirs(t)
	inside := filepath.Join(cfg, "keys")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		root = filepath.VolumeName(cfg) + `\`
	}
	for _, p := range []string{cfg, inside, filepath.Dir(cfg), home, root} {
		if resolved, ierr := validateResource(cfg, p); ierr == nil {
			t.Errorf("%q accepted as %q", p, resolved)
		} else if ierr.Code != CodeForbiddenResource {
			t.Errorf("%q: code %q, want %q", p, ierr.Code, CodeForbiddenResource)
		}
	}
}

// grant.go:148 failed open: a config dir that cannot be resolved skipped the
// config-dir rule. It now refuses every resource.
func TestValidateResourceUnresolvableConfigDirFailsClosed(t *testing.T) {
	_, proj := identityDirs(t)
	missing := filepath.Join(testutil.TempDir(t), "no-such-config")
	if resolved, ierr := validateResource(missing, proj); ierr == nil {
		t.Fatalf("accepted %q with an unresolvable config dir", resolved)
	}
}

// A symlink to the config dir is refused (identity after resolution).
func TestValidateResourceRefusesLinkToConfigDir(t *testing.T) {
	cfg, proj := identityDirs(t)
	link := filepath.Join(proj, "innocent")
	if err := os.Symlink(cfg, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	if resolved, ierr := validateResource(cfg, link); ierr == nil {
		t.Fatalf("symlink to the config dir accepted as %q", resolved)
	}
}

// R55-028: a .git directory, or a directory inside one, is refused for every
// action; a bare repository (or inside one) is refused for fs.read.
func TestValidateResourceRefusesGitDirs(t *testing.T) {
	cfg, proj := identityDirs(t)
	dotGit := filepath.Join(proj, ".git")
	objects := filepath.Join(dotGit, "objects")
	bare := filepath.Join(proj, "repo.git")
	for _, d := range []string{objects, filepath.Join(dotGit, "refs"), filepath.Join(bare, "objects", "pack"), filepath.Join(bare, "refs")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{dotGit, bare} {
		if err := os.WriteFile(filepath.Join(d, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{dotGit, objects} {
		if resolved, ierr := validateResource(cfg, p); ierr == nil {
			t.Errorf("%q accepted as %q", p, resolved)
		}
	}
	for _, p := range []string{bare, filepath.Join(bare, "objects")} {
		resolved, ierr := validateResource(cfg, p)
		if ierr != nil {
			t.Fatalf("%q refused by the common rule: %s", p, ierr.Message)
		}
		if validateFSResource(resolved) == nil {
			t.Errorf("fs.read on %q accepted", p)
		}
		if recheckResource(cfg, capability.Record{Path: resolved, Action: capability.ActionFSRead}) == nil {
			t.Errorf("recheck of fs.read on %q accepted", p)
		}
	}
	// The work tree holding .git stays allowed.
	resolved, ierr := validateResource(cfg, proj)
	if ierr != nil || validateFSResource(resolved) != nil {
		t.Fatalf("work tree %q refused", proj)
	}
}

// R55-027: a UNC resource is refused at once, without an SMB dial (TEST-NET-1
// is never reachable, so a dial would take seconds).
func TestValidateResourceRefusesUNCWithoutDial(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	cfg, _ := identityDirs(t)
	for _, p := range []string{`\\192.0.2.1\share\dir`, `\\?\UNC\192.0.2.1\share\dir`, `//192.0.2.1/share/dir`} {
		start := time.Now()
		if resolved, ierr := validateResource(cfg, p); ierr == nil {
			t.Errorf("%q accepted as %q", p, resolved)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("%q took %v: the path was dialled", p, d)
		}
	}
}

// The home rule on its own, with a home outside the config dir's tree.
func TestValidateResourceRefusesHome(t *testing.T) {
	cfg, proj := identityDirs(t)
	home := filepath.Join(testutil.TempDir(t), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, ierr := validateResource(cfg, home); ierr == nil || ierr.Message != "resource may not be the home directory" {
		t.Fatalf("home: %v, want the home-directory refusal", ierr)
	}
	if _, ierr := validateResource(cfg, proj); ierr != nil {
		t.Fatalf("project dir refused: %s", ierr.Message)
	}
}
