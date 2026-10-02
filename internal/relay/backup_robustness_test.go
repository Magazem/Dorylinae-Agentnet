package relay_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// R55-040: backing up a missing database fails and creates nothing (no empty
// database, no valid-looking empty backup).
func TestBackupMissingDatabaseCreatesNothing(t *testing.T) {
	dir := testutil.TempDir(t)
	db, out := filepath.Join(dir, "typo.db"), filepath.Join(dir, "out.db")
	if err := relay.Backup(db, out); err == nil {
		t.Fatal("Backup of a missing database succeeded")
	}
	for _, p := range []string{db, out} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s exists after a failed backup (stat err %v)", p, err)
		}
	}
}

// openClosed creates a relay database at path and closes it again.
func openClosed(t *testing.T, path string) {
	t.Helper()
	srv, err := relay.Open(relay.Options{QueuePath: path})
	if err != nil {
		t.Fatal(err)
	}
	srv.Close()
}

// R55-040: an existing --out is refused and left as it was.
func TestBackupRefusesExistingOut(t *testing.T) {
	dir := testutil.TempDir(t)
	db := filepath.Join(dir, "r.db")
	openClosed(t, db)
	out := filepath.Join(dir, "out.db")
	if err := os.WriteFile(out, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := relay.Backup(db, out); err == nil {
		t.Fatal("Backup overwrote or accepted an existing --out")
	}
	if b, _ := os.ReadFile(out); string(b) != "precious" { //nolint:gosec // test path
		t.Fatalf("existing --out was changed: %q", b)
	}
}

// R55-039: a corrupt backup must not destroy the database it was meant to replace.
func TestRestoreBadBackupKeepsExistingDatabase(t *testing.T) {
	dir := testutil.TempDir(t)
	db := filepath.Join(dir, "r.db")
	openClosed(t, db)
	before, err := os.ReadFile(db) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.db")
	if err := os.WriteFile(bad, []byte("this is not a sqlite database at all, just text padding"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := relay.Restore(bad, db, true); err == nil {
		t.Fatal("Restore accepted a corrupt backup")
	}
	after, err := os.ReadFile(db) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("existing database gone after a failed restore: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("existing database was modified by a failed restore")
	}
	if _, err := os.Stat(db + ".restoring"); !os.IsNotExist(err) {
		t.Fatalf("temp restore file left behind (stat err %v)", err)
	}
}
