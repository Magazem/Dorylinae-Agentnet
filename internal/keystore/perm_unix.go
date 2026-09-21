//go:build !windows

package keystore

import (
	"fmt"
	"os"
)

func restrictToOwner(path string) error { return os.Chmod(path, 0o600) }

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
