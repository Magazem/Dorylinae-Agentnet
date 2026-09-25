package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// MkdirPrivate creates a new directory under the user's cache directory
// (%LOCALAPPDATA%, ~/.cache, ~/Library/Caches) for a program the device
// helper must accept: review 40 L11 refuses a program on a path that other
// users can change, such as /tmp (world-writable) or a %TEMP% whose access
// list grants others Modify. The caller removes it.
func MkdirPrivate(prefix string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	base = filepath.Join(base, "dorylinae-test")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, prefix)
}

// PrivateDir is MkdirPrivate with the same cleanup as TempDir.
func PrivateDir(t testing.TB) string {
	t.Helper()
	dir, err := MkdirPrivate("dn-test-")
	if err != nil {
		t.Fatal(err)
	}
	removeOnCleanup(t, dir, "PrivateDir")
	return dir
}
