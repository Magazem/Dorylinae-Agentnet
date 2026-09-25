//go:build !windows

package device

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// adminGroups are the group names whose members administer the machine
// (macOS admin, BSD/Linux wheel, Debian sudo): their write access is an
// administrator's.
var adminGroups = map[string]bool{"root": true, "wheel": true, "admin": true, "sudo": true}

// checkPathOwner refuses p when it is owned by anyone but root or this user,
// is writable by every user (sticky directories such as /tmp included), or is
// writable by a group that is not root's, an admin group or this user's own
// private group. A symbolic link itself is skipped: its permissions are never
// used, and its directory, and what it points to, are checked.
func checkPathOwner(p string, _ pathRole) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("device: check the program's path: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return &WritableError{Path: p, Who: "an unknown owner"}
	}
	return classifyUnix(p, uint64(st.Uid), uint64(st.Gid), fi.Mode().Perm(), uint64(os.Geteuid()), trustedGroup) //nolint:gosec // user ids are never negative
}

// classifyUnix applies the rule of checkPathOwner to one owner, group and
// mode.
func classifyUnix(p string, uid, gid uint64, perm os.FileMode, euid uint64, groupOK func(gid uint64) bool) error {
	if uid != 0 && uid != euid {
		return &WritableError{Path: p, Who: "its owner, user id " + strconv.FormatUint(uid, 10)}
	}
	if perm&0o002 != 0 {
		return &WritableError{Path: p, Who: "every user"}
	}
	if perm&0o020 != 0 && !groupOK(gid) {
		return &WritableError{Path: p, Who: "the members of group id " + strconv.FormatUint(gid, 10)}
	}
	return nil
}

// trustedGroup reports whether members of gid may change the program: root's
// group, an admin group, or this user's private group (same id as the user's
// primary group and named after the user).
func trustedGroup(gid uint64) bool {
	if gid == 0 {
		return true
	}
	g, err := user.LookupGroupId(strconv.FormatUint(gid, 10))
	if err != nil {
		return false
	}
	if adminGroups[g.Name] {
		return true
	}
	u, err := user.Current()
	return err == nil && gid == uint64(os.Getgid()) && g.Name == u.Username //nolint:gosec // group ids are never negative
}
