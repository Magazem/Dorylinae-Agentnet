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
// relay's own writes (Docs/protocol/relay-hosted.md §3). outPath must not
// already exist; VACUUM INTO refuses to overwrite a file.
func Backup(dbPath, outPath string) error {
	if dbPath == "" {
		return errors.New("relay database path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if err := os.Remove(outPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing backup file: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open relay database: %w", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), backupOpTimeout)
	defer cancel()
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, outPath); err != nil {
		return fmt.Errorf("vacuum into %s: %w", outPath, err)
	}
	// Docs/protocol/relay-hosted.md §3 "On the host": the unencrypted copy is
	// mode 0600 in the database's directory before the caller encrypts it.
	if err := os.Chmod(outPath, 0o600); err != nil {
		return fmt.Errorf("chmod backup file: %w", err)
	}
	return nil
}

// Restore copies the backup at fromPath to dbPath, refusing to overwrite a
// non-empty existing database unless force is true, then opens the copy to
// run PRAGMA integrity_check and apply any pending relay migrations before
// returning (Docs/protocol/relay-hosted.md §3).
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
	// A restore target's stale WAL/SHM files must not mix with the copied
	// file's own write-ahead state.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s%s: %w", dbPath, suffix, err)
		}
	}
	if err := copyFile(fromPath, dbPath); err != nil {
		return fmt.Errorf("copy backup: %w", err)
	}
	q, err := openQueue(dbPath, defaultQueueTTL, defaultQueueMaxEnvelope, defaultQueueMaxBytes, time.Now)
	if err != nil {
		return fmt.Errorf("open restored database: %w", err)
	}
	defer func() { _ = q.close() }()
	ctx, cancel := context.WithTimeout(context.Background(), backupOpTimeout)
	defer cancel()
	var integrity string
	if err := q.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("integrity_check: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("restored database failed integrity_check: %s", integrity)
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
	return dst.Close()
}
