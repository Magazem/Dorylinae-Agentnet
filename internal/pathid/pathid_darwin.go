package pathid

import (
	"golang.org/x/sys/unix"
)

// isMountPoint reports whether p is where its filesystem is mounted:
// statfs names the mount point of the filesystem holding p.
func isMountPoint(p string) (bool, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(p, &st); err != nil {
		return false, err
	}
	return unix.ByteSliceToString(st.Mntonname[:]) == p, nil
}
