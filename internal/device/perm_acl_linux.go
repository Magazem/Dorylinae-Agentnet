//go:build linux

package device

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// checkACL refuses p when its POSIX access ACL lets another user or an
// untrusted group write it (aclWriter). No ACL, or a filesystem without
// ACLs, passes.
func checkACL(p string, euid uint64, groupOK func(gid uint64) bool) error {
	buf := make([]byte, 4096)
	n, err := unix.Lgetxattr(p, "system.posix_acl_access", buf)
	switch {
	case errors.Is(err, unix.ENODATA), errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.EOPNOTSUPP):
		return nil
	case err != nil:
		return fmt.Errorf("device: read the access list of %s: %w", DisplayQuote(p), err)
	}
	if who := aclWriter(buf[:n], euid, groupOK); who != "" {
		return &WritableError{Path: p, Who: who}
	}
	return nil
}
