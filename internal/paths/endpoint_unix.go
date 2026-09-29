//go:build !windows

package paths

import "path/filepath"

// endpoint is the socket inside dir; every spelling of dir reaches the same
// file.
func endpoint(dir string) (string, error) {
	return filepath.Join(dir, "agentnetd.sock"), nil
}

func resolveExisting(p string) (string, error) { return filepath.EvalSymlinks(p) }
