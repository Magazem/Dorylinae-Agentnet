// Package paths locates the per-user config directory and the files and
// endpoints inside it.
package paths

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// HomeEnv overrides the config directory when set.
const HomeEnv = "DORYLINAE_HOME"

// Paths holds every on-disk location and IPC endpoint derived from one config dir.
type Paths struct {
	// Dir is the config directory.
	Dir string
	// DB is the SQLite database file.
	DB string
	// RelayQueueDB is the relay's SQLite file of envelopes queued for offline peers.
	RelayQueueDB string
	// Endpoint is the IPC address: a socket path (Unix) or pipe name (Windows).
	Endpoint string
}

// Default resolves paths from $DORYLINAE_HOME or the OS user config directory.
func Default() (Paths, error) {
	if dir := os.Getenv(HomeEnv); dir != "" {
		return In(dir)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("locate user config dir: %w", err)
	}
	return In(filepath.Join(base, "dorylinae"))
}

// In resolves paths for an explicit config directory.
func In(dir string) (Paths, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve config dir: %w", err)
	}
	ep, err := endpoint(abs)
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		Dir:          abs,
		DB:           filepath.Join(abs, "dorylinae.db"),
		RelayQueueDB: filepath.Join(abs, "relay-queue.db"),
		Endpoint:     ep,
	}, nil
}

// Canonical returns one spelling for every spelling of dir, for deriving
// names from it (the Windows pipe name, keychain accounts): absolute, with
// the longest existing prefix resolved through symlinks (on Windows also
// junctions, subst drives and 8.3 names, in the on-disk case) and the rest
// appended as given. If resolution fails for another reason than a missing
// path, it returns the absolute path unchanged.
func Canonical(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Clean(dir)
	}
	var rest []string
	for p := abs; ; {
		r, err := resolveExisting(p)
		if err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				r = filepath.Join(r, rest[i])
			}
			return r
		}
		parent := filepath.Dir(p)
		if !errors.Is(err, fs.ErrNotExist) || parent == p {
			return abs
		}
		rest = append(rest, filepath.Base(p))
		p = parent
	}
}

// Ensure creates the config directory with owner-only permissions: mode 0700,
// or on Windows a protected DACL for the current user when anyone else had
// access. A directory owned by another user is refused (review 55 R55-089).
// On Windows the access list of an existing directory that is not empty is
// never replaced: it may be a shared folder $DORYLINAE_HOME points at, and
// the rewrite would lock everyone else out of it (review 87 L4). Ensure then
// fails with a *SharedDirError naming the fix.
func (p Paths) Ensure() error {
	_, err := os.Stat(p.Dir)
	created := errors.Is(err, fs.ErrNotExist)
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := secureDir(p.Dir, created); err != nil {
		return fmt.Errorf("secure config dir: %w", err)
	}
	return nil
}

// NotPrivateError means a path that must be private to the current user is
// not: Who can read or change it, or (Owner) it belongs to someone else.
type NotPrivateError struct {
	Path  string
	Who   string
	Owner bool
}

func (e *NotPrivateError) Error() string {
	return e.Path + " is not private to the current user: accessible by " + e.Who
}

// SharedDirError means Ensure found an existing config directory with
// contents that others can access, and left its access list alone.
type SharedDirError struct {
	*NotPrivateError
}

func (e *SharedDirError) Error() string {
	return e.NotPrivateError.Error() + "; it already holds files, so its access list is not changed: restrict it to your own user " +
		"(icacls \"" + e.Path + "\" /inheritance:r /grant:r \"%USERNAME%:(OI)(CI)F\") or set " + HomeEnv + " to a new directory"
}

func (e *SharedDirError) Unwrap() error { return e.NotPrivateError }
