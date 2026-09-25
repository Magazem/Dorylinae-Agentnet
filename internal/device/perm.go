package device

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ReasonWritableByOthers starts the reason of a scope or run refused because
// its program, or a directory on the program's path, can be changed by
// someone other than this device's user or an administrator
// (Docs/protocol/device.md §Scope, review 40 L11).
const ReasonWritableByOthers = "writable_by_others"

// WritableError is a program that others can swap: Path is the file or
// directory whose owner or permissions allow it, Who says by whom.
type WritableError struct {
	Path string
	Who  string
}

func (e *WritableError) Error() string {
	return fmt.Sprintf("%s: %s can be changed by %s", ReasonWritableByOthers, DisplayQuote(e.Path), e.Who)
}

// pathRole is what a checked path is to the program, which decides the
// rights that count on Windows.
type pathRole int

const (
	roleProgram  pathRole = iota // the file itself
	roleParent                   // the directory holding it
	roleAncestor                 // any directory above that
	roleLink                     // a symbolic link or junction met on the way
)

// maxLinks bounds how many symbolic links CheckProgramOwner follows, like the
// kernel's own limit (ELOOP).
const maxLinks = 40

// CheckProgramOwner refuses a program that users other than this device's
// user (or root / Administrators, SYSTEM, TrustedInstaller) can change: the
// file, the directory holding it and every directory above it, along the
// path as given and, for every symbolic link met on the way, along the
// link's target too. So a link that sits in a directory others can write is
// caught even when that directory is neither on the given nor on the fully
// resolved path (review 40 L11, review 41 M1). The content is not pinned, so
// a toolchain upgrade made by the same user or an administrator keeps
// working.
func CheckProgramOwner(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("device: the program path is not absolute")
	}
	if _, err := filepath.EvalSymlinks(path); err != nil {
		return fmt.Errorf("device: resolve the program: %w", err)
	}
	w := ownerWalk{seen: map[string]bool{}}
	return w.check(filepath.Clean(path), 0)
}

// ownerWalk is one CheckProgramOwner: what was checked, and how many links
// were followed.
type ownerWalk struct {
	seen  map[string]bool
	links int
}

// check checks p and every directory above it; depth is p's distance from
// the program (0: the program, 1: its directory), which sets the role. A
// symbolic link (or, on Windows, a junction) on the way is checked as a link
// and followed: its target is checked the same way, at the link's depth.
func (w *ownerWalk) check(p string, depth int) error {
	for i, q := range pathChain(p) {
		role := roleAncestor
		switch depth + i {
		case 0:
			role = roleProgram
		case 1:
			role = roleParent
		}
		target, isLink, err := linkTarget(q)
		if err != nil {
			return err
		}
		if isLink {
			role = roleLink
		}
		if key := fmt.Sprint(int(role), ":", q); !w.seen[key] {
			w.seen[key] = true
			if err := checkPathOwner(q, role); err != nil {
				return err
			}
		}
		if !isLink {
			continue
		}
		if w.links++; w.links > maxLinks {
			return errors.New("device: check the program's path: too many symbolic links")
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(q), target)
		}
		if err := w.check(filepath.Clean(target), depth+i); err != nil {
			return err
		}
	}
	return nil
}

// linkTarget reports whether p is a symbolic link (or, on Windows, a
// junction) and, if so, what it points to.
func linkTarget(p string) (string, bool, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return "", false, fmt.Errorf("device: check the program's path: %w", err)
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		t, err := os.Readlink(p)
		if err != nil {
			return "", false, fmt.Errorf("device: check the program's path: %w", err)
		}
		return t, true, nil
	case runtime.GOOS == "windows" && fi.Mode()&os.ModeIrregular != 0:
		// A junction (mount point) is irregular, and so are reparse points
		// that are not links (dedup, cloud files): only a junction reads
		// back as a target; the others are checked as they are.
		if t, err := os.Readlink(p); err == nil {
			return t, true, nil
		}
	}
	return "", false, nil
}

// pathChain returns p and every directory above it, up to the root.
func pathChain(p string) []string {
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
