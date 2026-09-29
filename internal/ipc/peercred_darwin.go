package ipc

import "golang.org/x/sys/unix"

// peerUID returns the effective uid of the process at the other end of the
// connected Unix socket fd.
func peerUID(fd int) (int, error) {
	x, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, err
	}
	return int(x.Uid), nil
}
