// Package testutil holds helpers shared by tests.
package testutil

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// removeTimeout bounds how long cleanup retries on Windows.
const removeTimeout = 3 * time.Second

// TempDir is t.TempDir with a Windows-tolerant cleanup. On Windows an
// antivirus scanner or the search indexer can hold just-written files
// delete-pending for a few milliseconds, so RemoveAll transiently fails with
// "directory is not empty". The cleanup retries for a short while; a directory
// that is still not removable after that (a genuine leaked handle) fails the test.
// Close databases and stop daemons in cleanups registered after this call, so
// they run first.
func TempDir(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dn-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err := os.RemoveAll(dir)
		if runtime.GOOS == "windows" {
			deadline := time.Now().Add(removeTimeout)
			for err != nil && time.Now().Before(deadline) {
				time.Sleep(25 * time.Millisecond)
				err = os.RemoveAll(dir)
			}
		}
		if err != nil {
			t.Errorf("TempDir RemoveAll cleanup: %v", err)
		}
	})
	return dir
}
