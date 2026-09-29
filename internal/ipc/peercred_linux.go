package ipc

import "golang.org/x/sys/unix"

// peerUID returns the effective uid of the process at the other end of the
// connected Unix socket fd.
func peerUID(fd int) (int, error) {
	u, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return int(u.Uid), nil
}
