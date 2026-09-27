package relay_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func TestJournalWriterRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	jw := relay.NewJournalWriter(&buf)
	if err := jw.Append("unbind", map[string]string{"account": "acc-1"}); err != nil {
		t.Fatal(err)
	}
	if err := jw.Append("invite_redeem", map[string]string{"invite": "inv-1"}); err != nil {
		t.Fatal(err)
	}
	entries, err := relay.ReadJournal(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Event != "unbind" || entries[1].Event != "invite_redeem" {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Fields["account"] != "acc-1" {
		t.Fatalf("fields = %+v", entries[0].Fields)
	}
}

// TestReplayJournalSkipsUnregisteredEvents is the extension-point contract
// this ticket leaves for 4.2a/4.3a: an event with no registered handler is
// skipped, not an error, since accounts/invites are not wired up here.
func TestReplayJournalSkipsUnregisteredEvents(t *testing.T) {
	dir := testutil.TempDir(t)
	db := filepath.Join(dir, "j.db")
	s, err := relay.Open(relay.Options{QueuePath: db})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	journalPath := filepath.Join(dir, "journal.jsonl")
	f, err := os.OpenFile(journalPath, os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // path under testutil.TempDir(t)
	if err != nil {
		t.Fatal(err)
	}
	jw := relay.NewJournalWriter(f)
	if err := jw.Append("unbind", map[string]string{"account": "acc-1"}); err != nil {
		t.Fatal(err)
	}
	if err := jw.Append("invite_redeem", map[string]string{"invite": "inv-1"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	applied, skipped, err := relay.ReplayJournal(db, journalPath, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 0 || skipped != 2 {
		t.Fatalf("applied=%d skipped=%d, want 0, 2 (no handlers registered yet)", applied, skipped)
	}
}

// TestReplayJournalAppliesRegisteredHandlerAfterSince covers the mechanism
// a future ticket relies on: a registered handler runs for entries at or
// after since, and entries before it are left alone.
func TestReplayJournalAppliesRegisteredHandlerAfterSince(t *testing.T) {
	dir := testutil.TempDir(t)
	db := filepath.Join(dir, "j2.db")
	s, err := relay.Open(relay.Options{QueuePath: db})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	var applied []string
	relay.RegisterJournalHandler("test_event", func(_ *sql.DB, e relay.JournalEntry) error {
		applied = append(applied, e.Fields["id"])
		return nil
	})

	// Written directly (not through JournalWriter, which always stamps
	// time.Now()) so the fixture controls the timestamps "since" filters on.
	cutoff := time.Now()
	entries := []relay.JournalEntry{
		{Time: cutoff.Add(-time.Hour), Event: "test_event", Fields: map[string]string{"id": "before"}},
		{Time: cutoff.Add(time.Minute), Event: "test_event", Fields: map[string]string{"id": "after"}},
	}
	journalPath := filepath.Join(dir, "journal.jsonl")
	f, err := os.OpenFile(journalPath, os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // path under testutil.TempDir(t)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	appliedN, skippedN, err := relay.ReplayJournal(db, journalPath, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if appliedN != 1 || skippedN != 0 {
		t.Fatalf("applied=%d skipped=%d, want 1, 0", appliedN, skippedN)
	}
	if len(applied) != 1 || applied[0] != "after" {
		t.Fatalf("handler saw %v, want [after]", applied)
	}
}
