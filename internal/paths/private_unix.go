//go:build !windows

package paths

import (
	"fmt"
	"os"
	"syscall"
)

// CheckPrivate reports whether p (the config dir, or a key file in it) is
// private to the current user: owned by this user and with no group or
// other permission bits (review 55 R55-089). It returns a *NotPrivateError
// when it is not.
func CheckPrivate(p string) error {
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return &NotPrivateError{Path: p, Who: fmt.Sprintf("its owner, uid %d", st.Uid), Owner: true}
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return &NotPrivateError{Path: p, Who: fmt.Sprintf("group or others (mode %#o)", perm)}
	}
	return nil
}

// secureDir makes the config dir owner-only. chmod fails on a dir owned by
// another user, so such a dir is refused too.
func secureDir(dir string, _ bool) error {
	// Directory needs the x bit; 0700 is owner-only.
	return os.Chmod(dir, 0o700) //nolint:gosec // see above
}
