package paths

import (
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
	"path/filepath"
	"testing"
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
