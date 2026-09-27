package relay_test

import (
	"database/sql"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relay"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func integrityCheck(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var v string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestBackupDuringWritesPassesIntegrityCheck: a backup taken while the relay
// keeps writing to the same file gives a file that opens clean.
func TestBackupDuringWritesPassesIntegrityCheck(t *testing.T) {
	dir := testutil.TempDir(t)
	db := filepath.Join(dir, "live.db")
	s, err := relay.Open(relay.Options{QueuePath: db})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + envelope.ConnectPath

	a, b := newPeer(t), newPeer(t)
	ca := rawAuthed(t, url, a)
	for _, id := range ids("live", 5) {
		sendQueued(t, ca, a, b.key, id, []byte("payload-"+id))
	}

	out := filepath.Join(dir, "backup.db")
	if err := relay.Backup(db, out); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if got := integrityCheck(t, out); got != "ok" {
		t.Fatalf("integrity_check = %q, want ok", got)
	}
	if runtime.GOOS != "windows" { // Windows has no POSIX mode bits to check here
		if info, err := os.Stat(out); err != nil {
			t.Fatal(err)
		} else if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("backup file mode %v is group/other accessible", info.Mode())
		}
	}
}

// TestRestoreRefusesNonEmptyTargetWithoutForce.
func TestRestoreRefusesNonEmptyTargetWithoutForce(t *testing.T) {
	dir := testutil.TempDir(t)
	src := filepath.Join(dir, "src.db")
	s, err := relay.Open(relay.Options{QueuePath: src})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	backup := filepath.Join(dir, "backup.db")
	if err := relay.Backup(src, backup); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "target.db")
	s2, err := relay.Open(relay.Options{QueuePath: target}) // non-empty (has relay_migrations rows)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()

	if err := relay.Restore(backup, target, false); err == nil {
		t.Fatal("Restore without --force onto a non-empty database succeeded, want refusal")
	}
	if err := relay.Restore(backup, target, true); err != nil {
		t.Fatalf("Restore with --force: %v", err)
	}

	empty := filepath.Join(dir, "empty.db")
	if err := relay.Restore(backup, empty, false); err != nil {
		t.Fatalf("Restore onto a path with nothing there: %v", err)
	}
}

// TestRestoreDrillTwoDaemons is the drill from relay-hosted.md §3: a backup
// taken mid-traffic, then restored; every envelope unacked at backup time
// still reaches its recipient exactly once.
func TestRestoreDrillTwoDaemons(t *testing.T) {
	dir := testutil.TempDir(t)
	db := filepath.Join(dir, "drill.db")
	s, err := relay.Open(relay.Options{QueuePath: db})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + envelope.ConnectPath

	a, b := newPeer(t), newPeer(t)
	ca := rawAuthed(t, url, a)
	want := ids("drill", 6)
	for _, id := range want[:4] {
		sendQueued(t, ca, a, b.key, id, nil)
	}
	waitQueued(t, s, b.key, 4)

	backup := filepath.Join(dir, "drill-backup.db")
	if err := relay.Backup(db, backup); err != nil {
		t.Fatal(err)
	}

	// More traffic after the backup: this is what a restore loses (outbox
	// resends it later; not this test's concern).
	for _, id := range want[4:] {
		sendQueued(t, ca, a, b.key, id, nil)
	}
	waitQueued(t, s, b.key, 6)

	ts.Close()
	s.Close()

	restored := filepath.Join(dir, "drill-restored.db")
	if err := relay.Restore(backup, restored, false); err != nil {
		t.Fatal(err)
	}
	s2, err := relay.Open(relay.Options{QueuePath: restored})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s2.Close)
	ts2 := httptest.NewServer(s2)
	t.Cleanup(ts2.Close)
	url2 := "ws" + strings.TrimPrefix(ts2.URL, "http") + envelope.ConnectPath

	cb := rawAuthed(t, url2, b)
	got := map[string]int{}
	for range want[:4] {
		id := readID(t, cb)
		got[id]++
		ack(t, cb, a.key, id)
	}
	for _, id := range want[:4] {
		if got[id] != 1 {
			t.Errorf("envelope %s delivered %d times after restore, want exactly 1", id, got[id])
		}
	}
	waitQueued(t, s2, b.key, 0)
}
