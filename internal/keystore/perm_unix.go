//go:build !windows

package keystore

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// createPrivateTemp creates a new file in dir with mode 0600.
func createPrivateTemp(dir string) (*os.File, error) {
	f, err := os.CreateTemp(dir, ".secret-*.tmp") // 0600
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, err
	}
	return f, nil
}

// syncDir flushes dir, so a rename into it survives a crash (review 55
// R55-155). A file system that cannot sync a directory (EINVAL) is accepted.
func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // dir holds the key file being written
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) {
		return err
	}
	return nil
}

func checkOwnerOnly(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("key file %s has mode %#o; it must not be accessible by group or others (chmod 600)", path, fi.Mode().Perm())
	}
	return nil
}

// OwnerOnly reports whether path is accessible by its owner only (mode 0600 or tighter).
func OwnerOnly(path string) error { return checkOwnerOnly(path) }
