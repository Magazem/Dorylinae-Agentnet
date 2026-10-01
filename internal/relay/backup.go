package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const backupOpTimeout = 30 * time.Second

// Backup writes an online, consistent copy of the relay database at dbPath to
// outPath using SQLite's VACUUM INTO, which may run concurrently with the
// relay's own writes (Docs/protocol/relay-hosted.md §3). dbPath must be an
// existing database, opened read-only (a typo must not create an empty one),
// and outPath must not already exist: it is never replaced.
func Backup(dbPath, outPath string) error {
	if dbPath == "" {
		return errors.New("relay database path is empty")
	}
	if info, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("relay database: %w", err)
	} else if info.IsDir() {
		return fmt.Errorf("relay database %s is a directory", dbPath)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	// Docs/protocol/relay-hosted.md §3 "On the host": the unencrypted copy is
	// mode 0600 in the database's directory before the caller encrypts it. It
	// is created 0600 here, not chmodded afterwards, so it is never readable
	// by others while VACUUM INTO fills it (R55-199); VACUUM INTO accepts an
	// existing empty file. O_EXCL refuses an existing --out (R55-040).
	f, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // outPath is an operator-supplied CLI path (relay backup --out), as intended
	if err != nil {
		return fmt.Errorf("create backup file (it must not already exist): %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(outPath)
		return fmt.Errorf("create backup file: %w", err)
	}
	if err := vacuumInto(dbPath, outPath); err != nil {
		_ = os.Remove(outPath) // the empty or partial file this call created
		return err
	}
	return nil
}

func vacuumInto(dbPath, outPath string) error {
	db, err := sql.Open("sqlite", fileDSN(dbPath, "?mode=ro&_pragma=busy_timeout(5000)"))
	if err != nil {
		return fmt.Errorf("open relay database: %w", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), backupOpTimeout)
	defer cancel()
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, outPath); err != nil {
		return fmt.Errorf("vacuum into %s: %w", outPath, err)
	}
	return nil
}

// Restore copies the backup at fromPath to dbPath, refusing to overwrite a
// non-empty existing database unless force is true. The copy is made beside
// dbPath, synced, opened to run PRAGMA integrity_check and apply any pending
// relay migrations, and only then moved over dbPath, so a bad backup leaves
// the existing database untouched (Docs/protocol/relay-hosted.md §3, R55-039).
// Restore refuses while the relay holds <db>.lock, i.e. while it runs.
func Restore(fromPath, dbPath string, force bool) error {
	if info, err := os.Stat(dbPath); err == nil {
		if info.Size() > 0 && !force {
			return fmt.Errorf("refusing to overwrite non-empty database %s without --force", dbPath)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", dbPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	// The relay holds <db>.lock while it runs (review 88 F5).
	lock, err := lockDB(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	matchOwner(dbPath+".lock", dbPath) // so the relay's service user can lock it later
	tmp := dbPath + ".restoring"
	cleanup := func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(tmp + suffix)
		}
	}
	cleanup() // a leftover of an interrupted restore
	defer cleanup()
	if err := copyFile(fromPath, tmp); err != nil {
		return fmt.Errorf("copy backup: %w", err)
	}
	if err := checkRestored(tmp); err != nil {
		return err
	}
	matchOwner(tmp, dbPath) // checkRestored may have created -wal/-shm; they are removed by now
	// A restore target's stale WAL/SHM files must not mix with the copied
	// file's own state.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s%s: %w", dbPath, suffix, err)
		}
	}
	if err := os.Rename(tmp, dbPath); err != nil {
		return fmt.Errorf("move restored database into place: %w", err)
	}
	return nil
}

// checkRestored opens the copy at path (applying pending migrations), runs
// integrity_check, and closes it so the file stands alone.
func checkRestored(path string) error {
	q, err := openQueue(path, defaultQueueTTL, defaultQueueMaxEnvelope, defaultQueueMaxBytes, time.Now)
	if err != nil {
		return fmt.Errorf("open restored database: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), backupOpTimeout)
	defer cancel()
	var integrity string
	if err := q.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		_ = q.close()
		return fmt.Errorf("integrity_check: %w", err)
	}
	if integrity != "ok" {
		_ = q.close()
		return fmt.Errorf("restored database failed integrity_check: %s", integrity)
	}
	if err := q.close(); err != nil {
		return fmt.Errorf("close restored database: %w", err)
	}
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() > 0 {
		return fmt.Errorf("restored database left a non-empty write-ahead log")
	}
	return nil
}

func copyFile(from, to string) error {
	src, err := os.Open(from) //nolint:gosec // from is an operator-supplied CLI path (relay restore --from), as intended
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // to is an operator-supplied CLI path (relay restore --db), as intended
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}
