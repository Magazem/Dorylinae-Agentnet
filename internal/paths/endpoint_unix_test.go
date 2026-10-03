//go:build !windows

package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 60b F8b-05: a component typed in NFC for a name stored in NFD (HFS+
// stores NFD; APFS compares both as one name), or the other way round, maps
// to the entry on disk, in any case.
func TestEntryNameFoldsNormalisation(t *testing.T) {
	const nfc, nfd = "Café", "Café"
	dir := testutil.TempDir(t)
	if err := os.Mkdir(filepath.Join(dir, nfd), 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadDir: %v, %d entries", err, len(entries))
	}
	onDisk := entries[0].Name()
	other := nfc
	if onDisk == nfc {
		other = nfd
	}
	for _, name := range []string{onDisk, other, strings.ToUpper(other)} {
		if got := entryName(dir, name); got != onDisk {
			t.Errorf("entryName(%+q) = %+q, want %+q", name, got, onDisk)
		}
	}
}
