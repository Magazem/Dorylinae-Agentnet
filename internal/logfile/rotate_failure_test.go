package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 55 C16-03 (R55-093): a failed rotation (here: another handle holds
// the log open without FILE_SHARE_DELETE, as a Windows log viewer may) must
// not leave the writer closed for the rest of the daemon's life.
func TestRotateFailureKeepsLogging(t *testing.T) {
	p := filepath.Join(testutil.TempDir(t), "d.log")
	w, err := Open(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if _, err := w.Write([]byte("0123456789")); err != nil {
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
