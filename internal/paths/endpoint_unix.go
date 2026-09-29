//go:build !windows

package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// endpoint is the socket inside dir; every spelling of dir reaches the same
// file.
func endpoint(dir string) (string, error) {
	return filepath.Join(dir, "agentnetd.sock"), nil
}

// resolveExisting resolves symlinks in the existing path p, then the case of
// each component: on a case-insensitive volume (macOS by default) EvalSymlinks
// keeps the case as given, so /Users/A and /users/a would differ.
func resolveExisting(p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	return onDiskCase(r), nil
}

// onDiskCase replaces each component of the absolute, symlink-free path p
// that is not listed in its parent as spelled with the entry that matches it
// case-insensitively. On a case-sensitive volume every component is listed
// as spelled, so p is unchanged; so is any component whose parent cannot be
// read.
func onDiskCase(p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	out := string(filepath.Separator)
	for _, name := range strings.Split(strings.Trim(p, string(filepath.Separator)), string(filepath.Separator)) {
		if name != "" {
			out = filepath.Join(out, entryName(out, name))
		}
	}
	return out
}

func entryName(dir, name string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return name
	}
	match := name
	for _, e := range entries {
		switch {
		case e.Name() == name:
			return name
		case match == name && strings.EqualFold(e.Name(), name):
			match = e.Name()
		}
	}
	return match
}
