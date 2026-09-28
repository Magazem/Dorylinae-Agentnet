package relay

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// TestOpenQueueTurnsOnSecureDelete matches the daemon-side internal/store
// D30 pattern: freed pages of the relay database are overwritten with zeros
// instead of left as recoverable ciphertext-adjacent metadata.
func TestOpenQueueTurnsOnSecureDelete(t *testing.T) {
	for _, path := range []string{"", filepath.Join(testutil.TempDir(t), "sd.db")} {
		q, err := openQueue(path, defaultQueueTTL, defaultQueueMaxEnvelope, defaultQueueMaxBytes, time.Now)
		if err != nil {
			t.Fatalf("openQueue(%q): %v", path, err)
		}
		var v int
		if err := q.db.QueryRow(`PRAGMA secure_delete`).Scan(&v); err != nil || v != 1 {
			t.Errorf("openQueue(%q): secure_delete = %d, %v; want 1 (on)", path, v, err)
		}
		_ = q.close()
	}
}

// TestMigrateIsIdempotent covers the ordinary case: a queue.db that already
// has relay_migrations at R1 opens again without re-running R1's SQL (which
// is itself idempotent, but the version check should skip it anyway).
func TestMigrateIsIdempotent(t *testing.T) {
	db := filepath.Join(testutil.TempDir(t), "idempotent.db")
	q1, err := openQueue(db, defaultQueueTTL, defaultQueueMaxEnvelope, defaultQueueMaxBytes, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := q1.close(); err != nil {
		t.Fatal(err)
	}

	q2, err := openQueue(db, defaultQueueTTL, defaultQueueMaxEnvelope, defaultQueueMaxBytes, time.Now)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer func() { _ = q2.close() }()
	var count int
	if err := q2.db.QueryRow(`SELECT COUNT(*) FROM relay_migrations`).Scan(&count); err != nil || count != len(relayMigrations) {
		t.Fatalf("relay_migrations rows = %d, %v; want %d (each migration applied once)", count, err, len(relayMigrations))
	}
}
