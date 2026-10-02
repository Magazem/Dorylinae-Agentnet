package paths

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func TestDefaultHonoursEnv(t *testing.T) {
	dir := testutil.TempDir(t)
	t.Setenv(HomeEnv, dir)
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if p.Dir != dir || p.DB != filepath.Join(dir, "dorylinae.db") {
		t.Fatalf("unexpected paths: %+v", p)
	}
	if p.Endpoint == "" {
		t.Fatal("empty endpoint")
	}
}

func TestEndpointDiffersPerDir(t *testing.T) {
	a, _ := In(testutil.TempDir(t))
	b, _ := In(testutil.TempDir(t))
	if a.Endpoint == b.Endpoint {
		t.Fatalf("endpoints collide: %s", a.Endpoint)
	}
}

func TestEnsure(t *testing.T) {
	p, err := In(filepath.Join(testutil.TempDir(t), "a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
}

// Review 87b N4: the fix for a shared config dir drops explicit entries too,
// and names the user by SID: no %USERNAME% (cmd only) or $env:USERNAME
// (PowerShell only), so the same text works in both shells.
func TestSharedDirErrorFixText(t *testing.T) {
	e := &SharedDirError{NotPrivateError: &NotPrivateError{Path: `C:\team\home`, Who: "S-1-5-32-545"}, Self: "S-1-5-21-1-2-3-1001"}
	msg := e.Error()
	for _, want := range []string{
		`icacls "C:\team\home" /reset`,
		`icacls "C:\team\home" /inheritance:r /grant:r "*S-1-5-21-1-2-3-1001:(OI)(CI)F"`,
		"cmd or PowerShell",
		HomeEnv,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "%") || strings.Contains(msg, "$env") {
		t.Errorf("message uses a shell-specific variable: %s", msg)
	}
}
