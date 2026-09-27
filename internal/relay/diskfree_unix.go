//go:build unix

package relay

import "golang.org/x/sys/unix"

// freeDiskSpace reports the bytes available to this process on the file
// system holding dir.
func freeDiskSpace(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:gosec,unconvert // field types differ by OS; both are non-negative
}
