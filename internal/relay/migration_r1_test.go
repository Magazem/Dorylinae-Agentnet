package relay_test

import (
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// preMigrationSchema is the relay's queue table exactly as every relay before
// this ticket created it (internal/relay/queue.go's old queueSchema, before
// relay_migrations existed): no relay_migrations table, no queue_by_sender
// index, no secure_delete pragma.
const preMigrationSchema = `
CREATE TABLE queue (
	seq      INTEGER PRIMARY KEY AUTOINCREMENT,
	to_key   TEXT NOT NULL,
	from_key TEXT NOT NULL,
	id       TEXT NOT NULL,
	enqueued INTEGER NOT NULL,
	frame    BLOB NOT NULL
);
CREATE UNIQUE INDEX queue_dedupe ON queue (to_key, from_key, id);
CREATE INDEX queue_by_recipient ON queue (to_key, seq);
CREATE INDEX queue_by_age ON queue (enqueued);
`

// TestR1OpensPreMigrationQueueWithEnvelopesIntact is ticket 4.1a's headline
// acceptance: a queue file written by a relay that predates the migration
// system opens under R1 with its envelopes intact and still deliverable.
func TestR1OpensPreMigrationQueueWithEnvelopesIntact(t *testing.T) {
	db := filepath.Join(testutil.TempDir(t), "pre-migration.db")
	raw, err := sql.Open("sqlite", "file:"+db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(preMigrationSchema); err != nil {
		t.Fatal(err)
	}
	a, b := newPeer(t), newPeer(t)
	frame, err := a.env(b.key, "pre-1", []byte("hello")).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO queue (to_key, from_key, id, enqueued, frame) VALUES (?, ?, ?, ?, ?)`,
		b.key, a.key, "pre-1", time.Now().UnixMilli(), frame); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := relay.Open(relay.Options{QueuePath: db})
	if err != nil {
		t.Fatalf("Open on a pre-migration queue file: %v", err)
	}
	t.Cleanup(s.Close)

	n, err := s.Queued(b.key)
	if err != nil || n != 1 {
		t.Fatalf("Queued(b) = %d, %v; want 1 envelope carried over", n, err)
	}

	// Still deliverable: connecting B pulls the pre-migration envelope.
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + envelope.ConnectPath
	cb := rawAuthed(t, url, b)
	if got := readID(t, cb); got != "pre-1" {
		t.Fatalf("delivered id = %q, want pre-1", got)
	}

	// R1 recorded itself and added the index the 4.0b caps need, without
	// disturbing the rows or the pre-existing indexes.
	raw2, err := sql.Open("sqlite", "file:"+db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw2.Close() }()
	var version int
	if err := raw2.QueryRow(`SELECT MAX(version) FROM relay_migrations`).Scan(&version); err != nil || version != 3 {
		t.Fatalf("relay_migrations version = %d, %v; want 3 (R1, R2 accounts, R3 queue_delivered)", version, err)
	}
	var idxCount int
	if err := raw2.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'queue_by_sender'`).Scan(&idxCount); err != nil || idxCount != 1 {
		t.Fatalf("queue_by_sender index present = %d, %v; want 1", idxCount, err)
	}
}
