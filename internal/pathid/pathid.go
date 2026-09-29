// Package pathid resolves local directories to one canonical path and
// compares them by file identity, not by spelling (Docs/protocol/grant.md
// §Issuance step 3, review 55 R55-006). A path that could reach another host
// (UNC, a device-namespace path, a network drive) is refused before the
// filesystem is touched, so that resolving a caller-chosen path never opens an
// SMB connection (R55-027).
package pathid

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrRemote is returned for a UNC, device-namespace or network-drive path.
var ErrRemote = errors.New("UNC, device-namespace and network paths are refused")

// maxLinks bounds the links followed by the pre-resolution guard.
const maxLinks = 40

// CheckLocal reports ErrRemote, without touching the filesystem, when p is
// spelled as a path that could reach another host or bypass Win32 path rules.
// On Windows that is any path starting with two separators (UNC, \\?\, \\.\,
// volume GUIDs, GLOBALROOT) and a drive letter the OS reports as a network
// drive. On other systems nothing is refused.
func CheckLocal(p string) error {
	return checkLocal(p)
}

// Resolve returns the canonical form of the absolute path p: symbolic links
// resolved with filepath.EvalSymlinks and, on Windows, everything else that
// makes two spellings name one directory (junctions and other mount points,
// subst drives, 8.3 names, case) resolved from an open handle. Before any
// link is followed, every link target on the way is checked with CheckLocal,
// so a link to a UNC path is refused without being dialled.
func Resolve(p string) (string, error) {
	if err := checkLocal(p); err != nil {
		return "", err
	}
	if err := guardLinks(filepath.Clean(p)); err != nil {
		return "", err
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	return finalPath(r)
}

// Same reports whether a and b are the same file or directory by identity.
func Same(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b)
}

// Ancestors returns p and every directory above it, lexically, ending with
// the root: for a path from Resolve these are its real ancestors.
func Ancestors(p string) []string {
	p = filepath.Clean(p)
	out := []string{p}
	for {
		d := filepath.Dir(p)
		if d == p {
			return out
		}
		out = append(out, d)
		p = d
	}
}

// IsRoot reports whether the resolved directory p is the root of a
// filesystem: a volume root or any other mount point (Unix: its parent is
// on another device or is itself; Windows: p is its own volume mount path).
// A mount point counts as a root because it can be another spelling of one
// (macOS /System/Volumes/Data is "/" under a firmlink, review 55 T13-01).
func IsRoot(p string) (bool, error) {
	return isRoot(p)
}

// guardLinks walks p component by component with Lstat and, at every link,
// checks the target with CheckLocal before continuing through it. It only
// guards; resolution is left to EvalSymlinks and finalPath. A component that
// does not exist ends the walk (EvalSymlinks then reports it).
func guardLinks(p string) error {
	vol := filepath.VolumeName(p)
	cur := vol + string(filepath.Separator)
	rest := splitComponents(p[len(vol):])
	for hops := 0; len(rest) > 0; {
		c := rest[0]
		rest = rest[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, c)
		fi, err := os.Lstat(next)
		if err != nil {
			return nil
		}
		if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
			cur = next
			continue
		}
		t, err := os.Readlink(next)
		if err != nil {
			// A reparse point that is not a link (a cloud file, dedup):
			// the kernel does not redirect through it.
			cur = next
			continue
		}
		if hops++; hops > maxLinks {
			return errors.New("too many links")
		}
		if err := checkLocal(t); err != nil {
			return err
		}
		switch {
		case filepath.IsAbs(t):
		case filepath.VolumeName(t) == "" && len(t) > 0 && os.IsPathSeparator(t[0]):
			t = filepath.VolumeName(cur) + t // rooted on the current drive
		default:
			t = filepath.Join(cur, t) // relative to the link's directory
		}
		t = filepath.Clean(t)
		if err := checkLocal(t); err != nil {
			return err
		}
		vol = filepath.VolumeName(t)
		cur = vol + string(filepath.Separator)
		rest = append(splitComponents(t[len(vol):]), rest...)
	}
	return nil
}

func splitComponents(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || os.IsPathSeparator(s[i]) {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}
