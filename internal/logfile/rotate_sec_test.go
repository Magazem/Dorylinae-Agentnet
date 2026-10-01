package logfile

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"path/filepath"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Security review 85 (R55-F22): what a held-open log costs while rotation
// keeps failing — the earlier generation and the size cap.
func TestRotateFailureSideEffects(t *testing.T) {
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
