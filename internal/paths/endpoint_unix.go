//go:build !windows

package paths

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// SocketName is the daemon's socket in the config dir. ipc.LockInstance dials
// it to find a daemon from before the instance lock (review 99 F8c-02).
const SocketName = "agentnetd.sock"

// endpoint is the socket inside dir; every spelling of dir reaches the same
// file.
func endpoint(dir string) (string, error) {
	return filepath.Join(dir, SocketName), nil
}

// resolveExisting resolves symlinks in the existing path p, then the case and
// Unicode normalisation of each component: on a case-insensitive volume
// (macOS by default) EvalSymlinks keeps the case as given, so /Users/A and
// /users/a would differ, and macOS volumes compare NFC and NFD spellings as
// one name (review 60b F8b-05).
func resolveExisting(p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	return onDiskCase(r), nil
}

// onDiskCase replaces each component of the absolute, symlink-free path p
// that is not listed in its parent as spelled with the entry that matches it
// case-insensitively after NFC normalisation. On a case- and
// normalisation-sensitive volume every component is listed as spelled, so p
// is unchanged; so is any component whose parent cannot be read.
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
	match, nfc := name, norm.NFC.String(name)
	for _, e := range entries {
		switch {
		case e.Name() == name:
			return name
		case match == name && strings.EqualFold(norm.NFC.String(e.Name()), nfc):
			match = e.Name()
		}
	}
	return match
}
