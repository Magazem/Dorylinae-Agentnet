package daemon

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 37 M1 and L1: issuance resolves the branch exactly (no DWIM, no
// symbolic ref) and accepts a bare repository only at its top.
func TestGrantGitIssuanceExact(t *testing.T) {
	repo := testGitRepo(t)
	gitPath, err := lookupGit()
	if err != nil {
		t.Skip("git not available")
	}
	run := func(dir string, args ...string) string {
		t.Helper()
		//nolint:gosec // test helper; gitPath from lookupGit, args are fixed literals
		cmd := exec.Command(gitPath, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	head := run(repo, "rev-parse", "HEAD")
	run(repo, "update-ref", "refs/tags/refs/heads/gone", head)
	run(repo, "symbolic-ref", "refs/heads/alias", "refs/heads/main")

	if ierr := validateGitResource(repo, "main"); ierr != nil {
		t.Fatalf("main refused: %v", ierr)
	}
	for _, b := range []string{"gone", "alias"} {
		if ierr := validateGitResource(repo, b); ierr == nil || ierr.Code != CodeForbiddenResource {
			t.Errorf("branch %q: %v, want forbidden_resource", b, ierr)
		}
	}

	bare := filepath.Join(testutil.TempDir(t), "bare.git")
	run(repo, "clone", "-q", "--bare", repo, bare)
	if ierr := validateGitResource(bare, "main"); ierr != nil {
		t.Fatalf("bare repository refused: %v", ierr)
	}
	for _, sub := range []string{"objects", "refs"} {
		if ierr := validateGitResource(filepath.Join(bare, sub), "main"); ierr == nil || ierr.Code != CodeForbiddenResource {
			t.Errorf("inside the bare repository (%s): %v, want forbidden_resource", sub, ierr)
		}
	}
}
