package paths

import (
	"path/filepath"
	"testing"
)

func TestDefaultHonoursEnv(t *testing.T) {
	dir := t.TempDir()
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
	a, _ := In(t.TempDir())
	b, _ := In(t.TempDir())
	if a.Endpoint == b.Endpoint {
		t.Fatalf("endpoints collide: %s", a.Endpoint)
	}
}

func TestEnsure(t *testing.T) {
	p, err := In(filepath.Join(t.TempDir(), "a", "b"))
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
