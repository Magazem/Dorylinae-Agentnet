//go:build unix

package relay

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockFileExclusive(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) //nolint:gosec // descriptor fits int
		switch {
		case err == nil:
			return nil
		case errors.Is(err, unix.EINTR):
		case errors.Is(err, unix.EWOULDBLOCK):
			return errDBLocked
		default:
			return err
		}
	}
}
