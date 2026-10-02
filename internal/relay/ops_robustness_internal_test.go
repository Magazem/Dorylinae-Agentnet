package relay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// R55-036: Close must not close the queue under a read loop that is still
// handling a frame, and no read loop may start once Close has begun.
func TestCloseWaitsForReadLoops(t *testing.T) {
	s := New(Options{})
	if !s.beginServe() {
		t.Fatal("beginServe refused on an open server")
	}
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()

	// While the loop is registered the queue must stay usable.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatal("Close returned while a read loop was still running")
		default:
		}
		if _, err := s.q.count("k"); err != nil {
			t.Fatalf("queue closed under a running read loop: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.serveWG.Done()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the read loop finished")
	}
	if s.beginServe() {
		t.Fatal("beginServe accepted a read loop after Close")
	}
}

// R55-038: a migration that fails half-way rolls back whole.
func TestRelayMigrationIsAtomic(t *testing.T) {
	saved := relayMigrations
	t.Cleanup(func() { relayMigrations = saved })
	bad := relayMigration{version: len(saved) + 1, name: "RX_bad", sql: `CREATE TABLE half_applied (x INTEGER); THIS IS NOT SQL;`}
	relayMigrations = append(append([]relayMigration(nil), saved...), bad)

	path := filepath.Join(testutil.TempDir(t), "r.db")
	if db, err := openRelayDB(path); err == nil {
		_ = db.Close()
		t.Fatal("a failing migration did not fail openRelayDB")
	}
	relayMigrations = saved
	db, err := openRelayDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'half_applied'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("the failed migration's first statement was left applied")
	}
}

// R55-038: concurrent openers of a database with a pending migration (an
// upgrade: `relay admin` next to the starting relay) all succeed with the
// full schema, each migration applied once.
func TestRelayMigrationConcurrentOpeners(t *testing.T) {
	saved := relayMigrations
	t.Cleanup(func() { relayMigrations = saved })
	path := filepath.Join(testutil.TempDir(t), "r.db")
	relayMigrations = saved[:len(saved)-1]
	old, err := openRelayDB(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = old.Close()
	relayMigrations = saved
	const openers = 6
	var wg sync.WaitGroup
	errs := make(chan error, openers)
	for range openers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, err := openRelayDB(path)
			if err != nil {
				errs <- err
				return
			}
			errs <- db.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent opener failed: %v", err)
		}
	}
	db, err := openRelayDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var v, rows int
	if err := db.QueryRow(`SELECT MAX(version), COUNT(*) FROM relay_migrations`).Scan(&v, &rows); err != nil {
		t.Fatal(err)
	}
	if v != len(relayMigrations) || rows != len(relayMigrations) {
		t.Fatalf("schema version %d with %d rows, want %d each", v, rows, len(relayMigrations))
	}
}

// R55-096: '#', '%' and '?' in the database path are not URI syntax.
func TestRelayDBPathWithURICharacters(t *testing.T) {
	for _, name := range []string{"a#b", "x%41y", "q?z"} {
		dir := filepath.Join(testutil.TempDir(t), name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Logf("%q is not a valid directory name here: %v", name, err)
			continue
		}
		path := filepath.Join(dir, "relay.db")
		q, err := openQueue(path, defaultQueueTTL, defaultQueueMaxEnvelope, defaultQueueMaxBytes, time.Now)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_ = q.close()
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s: database not at the intended path: %v", name, err)
		}
		entries, _ := os.ReadDir(filepath.Dir(dir))
		if len(entries) != 1 {
			t.Fatalf("%s: stray siblings created beside the directory: %v", name, entries)
		}
		backup := filepath.Join(dir, "b.db")
		if err := Backup(path, backup); err != nil {
			t.Fatalf("%s: backup: %v", name, err)
		}
		if err := Restore(backup, filepath.Join(dir, "restored.db"), false); err != nil {
			t.Fatalf("%s: restore: %v", name, err)
		}
	}
}

// R55-050: v1 pair_new is charged to the per-prefix limiter like v2.
func TestPairNewV1ChargedPerPrefix(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := New(Options{Now: func() time.Time { return now }, AllowPairingV1: true})
	t.Cleanup(s.Close)
	send := func(i int) envelope.Control {
		c := newConn(nil, testKey(t), 4, "203.0.113.0")
		s.pairNew(c, &envelope.Control{Op: envelope.OpPairNew, Card: json.RawMessage(`{}`), Ref: fmt.Sprintf("r%d", i)})
		var ctl envelope.Control
		if err := json.Unmarshal(<-c.out, &ctl); err != nil {
			t.Fatal(err)
		}
		return ctl
	}
	for i := 0; i < maxPairNewPerPrefixWindow; i++ {
		if r := send(i); r.Op != envelope.OpPairCode {
			t.Fatalf("v1 pair_new %d: got %+v, want pair_code", i, r)
		}
	}
	if r := send(maxPairNewPerPrefixWindow); r.Op != envelope.OpError || r.Code != envelope.CodePairRateLimited {
		t.Fatalf("21st v1 pair_new in the prefix = %+v, want pair_rate_limited", r)
	}
}
