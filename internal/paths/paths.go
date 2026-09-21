// Package paths locates the per-user config directory and the files and
// endpoints inside it.
package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// HomeEnv overrides the config directory when set.
const HomeEnv = "DORYLINAE_HOME"

// Paths holds every on-disk location and IPC endpoint derived from one config dir.
type Paths struct {
	// Dir is the config directory.
	Dir string
	// DB is the SQLite database file.
	DB string
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
	return Paths{
		Dir:      abs,
		DB:       filepath.Join(abs, "dorylinae.db"),
		Endpoint: endpoint(abs),
	}, nil
}

// Ensure creates the config directory with owner-only permissions.
func (p Paths) Ensure() error {
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if runtime.GOOS != "windows" {
		// Directory needs the x bit; 0700 is owner-only.
		if err := os.Chmod(p.Dir, 0o700); err != nil { //nolint:gosec // see above
			return fmt.Errorf("secure config dir: %w", err)
		}
	}
	return nil
}

func endpoint(dir string) string {
	if runtime.GOOS == "windows" {
		sum := sha256.Sum256([]byte(dir))
		return `\\.\pipe\dorylinae-` + hex.EncodeToString(sum[:8])
	}
	return filepath.Join(dir, "agentnetd.sock")
}
