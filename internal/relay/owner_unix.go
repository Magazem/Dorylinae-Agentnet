//go:build unix

package relay

import (
	"os"
	"path/filepath"
	"syscall"
)

// matchOwner gives path the owner of the existing database, or of its
// directory when there is none yet. A restore run as root would otherwise leave
// a root-owned 0600 file the relay's service user cannot open (review 88 F2).
// Best effort: a non-root caller cannot chown, and then already owns what it made.
func matchOwner(path, dbPath string) {
	info, err := os.Stat(dbPath)
	if err != nil {
		info, err = os.Stat(filepath.Dir(dbPath))
	}
	if err != nil {
		return
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		_ = os.Chown(path, int(st.Uid), int(st.Gid))
	}
}
