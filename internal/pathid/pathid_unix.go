//go:build !windows

package pathid

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func checkLocal(string) error { return nil }

func finalPath(r string) (string, error) { return r, nil }

func isRoot(p string) (bool, error) {
	p = filepath.Clean(p)
	if p == "/" {
		return true, nil
	}
	fi, err := os.Stat(p)
	if err != nil {
		return false, err
	}
	// p + "/..", not filepath.Join, which would clean the ".." away: the
	// physical parent, across a firmlink or bind mount.
	pi, err := os.Stat(p + "/..")
	if err != nil {
		return false, err
	}
	if os.SameFile(fi, pi) {
		return true, nil
	}
	a, ok1 := fi.Sys().(*syscall.Stat_t)
	b, ok2 := pi.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return false, errors.New("no device number")
	}
	return a.Dev != b.Dev, nil
}
