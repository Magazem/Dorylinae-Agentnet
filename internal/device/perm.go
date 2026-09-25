package device

import (
	"errors"
	"fmt"
	"path/filepath"
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
)

// CheckProgramOwner refuses a program that users other than this device's
// user (or root / Administrators, SYSTEM, TrustedInstaller) can change: the
// file, the directory holding it and every directory above it, both along
// the path as given and along the path with symbolic links resolved
// (review 40 L11). The content is not pinned, so a toolchain upgrade made by
// the same user or an administrator keeps working.
func CheckProgramOwner(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("device: the program path is not absolute")
	}
	chains := [][]string{pathChain(filepath.Clean(path))}
	if resolved, err := filepath.EvalSymlinks(path); err != nil {
		return fmt.Errorf("device: resolve the program: %w", err)
	} else if resolved != filepath.Clean(path) {
		chains = append(chains, pathChain(resolved))
	}
	seen := map[string]bool{}
	for _, chain := range chains {
		for i, p := range chain {
			if seen[p] {
				continue
			}
			seen[p] = true
			role := roleAncestor
			switch i {
			case 0:
				role = roleProgram
			case 1:
				role = roleParent
			}
			if err := checkPathOwner(p, role); err != nil {
				return err
			}
		}
	}
	return nil
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
