package relay_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 88 F5: restore refuses while a relay holds the database, and works
// again once it has closed.
func TestRestoreRefusesWhileRelayRuns(t *testing.T) {
	dir := testutil.TempDir(t)
	db := filepath.Join(dir, "live.db")
	backup := filepath.Join(dir, "b.db")
	s, err := relay.Open(relay.Options{QueuePath: db})
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.Backup(db, backup); err != nil {
		t.Fatal(err)
	}
	err = relay.Restore(backup, db, true)
	if err == nil || !strings.Contains(err.Error(), "stop the relay") {
		t.Fatalf("Restore under a running relay = %v, want a stop-the-relay refusal", err)
	}
	if _, err := relay.Open(relay.Options{QueuePath: db}); err == nil {
		t.Fatal("a second relay opened the same database")
	}
	s.Close()
	if err := relay.Restore(backup, db, true); err != nil {
		t.Fatalf("Restore after Close: %v", err)
	}
	s2, err := relay.Open(relay.Options{QueuePath: db})
	if err != nil {
		t.Fatalf("relay cannot reopen after restore: %v", err)
	}
	s2.Close()
}
