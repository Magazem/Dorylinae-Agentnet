package device

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/pathid"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 55 C14-01 (R55-006): a repo directory replaced by a junction to
// another directory is refused by CheckTarget (device.md §Running, review 40
// L5). Go's EvalSymlinks does not follow junctions; pathid.Resolve does.
func TestCheckTargetRefusesJunctionSwap(t *testing.T) {
	prog := filepath.Join(os.Getenv("SystemRoot"), "System32", "whoami.exe")
	base, err := pathid.Resolve(testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "repo")
	other := filepath.Join(base, "elsewhere")
	for _, d := range []string{repo, other} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A C:\ ACL that grants others Modify makes the program-owner step fail
	// on some PCs (HANDOFF section 0), so only the directory step, which runs
	// first, is judged here.
	if err := CheckTarget(prog, repo, nil); err != nil && strings.Contains(err.Error(), "working directory") {
		t.Fatalf("unchanged repo refused: %v", err)
	}
	if err := os.Remove(repo); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", repo, other).CombinedOutput(); err != nil { //nolint:gosec // test: fixed command, temp dirs
		t.Fatalf("mklink /J: %v %s", err, out)
	}
	err = CheckTarget(prog, repo, nil)
	if err == nil || !strings.Contains(err.Error(), "working directory") {
		t.Fatalf("a repo swapped for a junction passed the directory re-check: %v", err)
	}
}

// R55-027: a UNC argv[0] is refused before lookPath touches it, and a UNC
// program or working directory at run time is refused without a dial
// (TEST-NET-1 is never reachable).
func TestUNCProgramRefusedWithoutDial(t *testing.T) {
	unc := `\\192.0.2.1\share\tool.exe`
	start := time.Now()
	called := false
	if _, err := resolveProgram(unc, func(string) (string, error) { called = true; return unc, nil }); err == nil {
		t.Error("UNC argv[0] accepted")
	}
	if called {
		t.Error("lookPath was called on a UNC argv[0]")
	}
	prog := filepath.Join(os.Getenv("SystemRoot"), "System32", "whoami.exe")
	if CheckTarget(unc, testutil.TempDir(t), nil) == nil {
		t.Error("CheckTarget accepted a UNC program")
	}
	if CheckTarget(prog, `\\192.0.2.1\share\repo`, nil) == nil {
		t.Error("CheckTarget accepted a UNC working directory")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v: a UNC path was dialled", d)
	}
}
