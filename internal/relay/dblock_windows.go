//go:build windows

package relay

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// LockFileEx is mandatory on Windows: a second process cannot even read the
// locked byte, but the lock file holds no data, so that is harmless.
func lockFileExclusive(f *os.File) error {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errDBLocked
	}
	return err
}
