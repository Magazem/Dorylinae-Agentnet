//go:build darwin

package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
)

// Review 55 T13-01 (R55-006): /Users is a firmlink to
// /System/Volumes/Data/Users (macOS 10.15+), which EvalSymlinks does not
// resolve. The Data-volume spellings of the config dir, the home dir and the
// data volume itself must be refused, by identity.
func TestValidateResourceRefusesFirmlinkSpellings(t *testing.T) {
	const data = "/System/Volumes/Data"
	home, err := os.UserHomeDir()
	if err != nil || !strings.HasPrefix(home, "/Users/") {
		t.Skipf("home %q is not under /Users", home)
	}
	if _, err := os.Stat(data + home); err != nil {
		t.Skipf("no firmlinked data volume: %v", err)
	}
	// A config dir under the real home, like the default
	// ~/Library/Application Support/dorylinae.
	cfg, err := os.MkdirTemp(home, "dn-test-firmlink-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cfg) })
	if err := os.WriteFile(filepath.Join(cfg, "dorylinae.db"), []byte("SECRET-DB"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := os.Stat(home)
	b, _ := os.Stat(data + home)
	if !os.SameFile(a, b) {
		t.Fatalf("%s and %s are not the same directory", home, data+home)
	}
	for _, p := range []string{home, cfg, "/", data + cfg, data + home, data, data + "/Users"} {
		if resolved, ierr := validateResource(cfg, p); ierr == nil {
			t.Errorf("%q accepted as %q", p, resolved)
		} else {
			t.Logf("refused %q: %s", p, ierr.Message)
		}
	}
	// The approval recheck on a stored Data-volume spelling refuses too.
	if err := recheckResource(cfg, capability.Record{Path: data + cfg, Action: capability.ActionFSRead}); err == nil {
		t.Errorf("recheckResource accepted %q", data+cfg)
	}
	// A project dir under the Data spelling stays allowed.
	proj := cfg + "-proj"
	if err := os.Mkdir(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(proj) })
	if _, ierr := validateResource(cfg, data+proj); ierr != nil {
		t.Errorf("project dir %q refused: %s", data+proj, ierr.Message)
	}
}
