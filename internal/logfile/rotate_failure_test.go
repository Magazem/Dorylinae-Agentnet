package logfile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 55 C16-03 (R55-093): a failed rotation (here: another handle holds
// the log open without FILE_SHARE_DELETE, as a Windows log viewer may) must
// not leave the writer closed for the rest of the daemon's life.
func TestRotateFailureKeepsLogging(t *testing.T) {
	p := filepath.Join(testutil.TempDir(t), "d.log")
	w, err := Open(p, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if _, err := w.Write([]byte(strings.Repeat("0123456789", 10))); err != nil {
		t.Fatal(err)
	}
	viewer, err := os.Open(p) //nolint:gosec // test-owned temp path
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("rotate-now\n"))
	_ = viewer.Close()
	if _, err := w.Write([]byte("after viewer closed\n")); err != nil {
		t.Fatalf("log writer stays dead after one failed rotation: %v", err)
	}
	var all strings.Builder
	for _, name := range []string{p, p + ".1"} {
		if b, err := os.ReadFile(name); err == nil { //nolint:gosec // test-owned temp path
			all.Write(b)
		}
	}
	for _, want := range []string{"rotate-now", "after viewer closed"} {
		if !strings.Contains(all.String(), want) {
			t.Errorf("log lost %q: %q", want, all.String())
		}
	}
}

// Review 85 (R55-F22 F1, F2): a held-open log must not cost the earlier
// generation, and the log stays within twice the limit while rotation fails.
func TestRotateFailureKeepsGenerationAndCap(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the rename only fails under Windows sharing rules")
	}
	p := filepath.Join(testutil.TempDir(t), "d.log")
	if err := os.WriteFile(p+".1", []byte("previous generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := Open(p, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if _, err := w.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	viewer, err := os.Open(p) //nolint:gosec // test-owned temp path
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = viewer.Close() }()
	line := strings.Repeat("x", 31) + "\n"
	for range 100 {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if _, err := os.Stat(p + ".1"); err == nil {
		t.Log(".1 kept")
	} else {
		t.Errorf("the previous generation was deleted by the failed rotation: %v", err)
	}
	fi, _ := os.Stat(p)
	t.Logf("log size with viewer open: %d bytes (limit 64)", fi.Size())
	if fi.Size() > 2*64 {
		t.Errorf("log grew to %d bytes, past twice the limit", fi.Size())
	}
}
