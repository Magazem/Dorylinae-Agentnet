package relay

import (
	"errors"
	"fmt"
	"os"
)

// errDBLocked means another process holds the database's run lock.
var errDBLocked = errors.New("locked by another process")

// dbLockPath is the advisory lock file beside the database. The relay holds it
// while it runs; restore refuses to run while it is held (review 88 F5).
func dbLockPath(dbPath string) string { return dbPath + ".lock" }

// lockDB takes the exclusive, non-blocking run lock for dbPath. The lock is
// released by closing the returned file, or by the process exiting.
func lockDB(dbPath string) (*os.File, error) {
	p := dbLockPath(dbPath)
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // beside the operator-supplied database path
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", p, err)
	}
	if err := lockFileExclusive(f); err != nil {
		_ = f.Close()
		if errors.Is(err, errDBLocked) {
			return nil, fmt.Errorf("database %s is in use (%s is %w): stop the relay first", dbPath, p, err)
		}
		return nil, fmt.Errorf("lock %s: %w", p, err)
	}
	return f, nil
}
