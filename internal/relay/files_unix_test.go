//go:build unix

package relay_test

import "golang.org/x/sys/unix"

// openFilesLimit is this process's soft limit on open files (Go raises it
// to the hard limit at start-up).
func openFilesLimit() uint64 {
	var r unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &r); err != nil {
		return 0
	}
	return r.Cur
}
