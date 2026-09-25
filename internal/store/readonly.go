package store

import (
	"database/sql"
	"fmt"
	"os"
)

// OpenReadOnly opens an existing database for reading only: no migrations, no
// writes, so it works when the daemon cannot start (for example after an
// unchained audit row, Docs/protocol/audit.md §Verification). The caller closes
// the handle. It returns an error wrapping os.ErrNotExist if there is no file.
func OpenReadOnly(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("open database read-only: %w", err)
	}
	dsn := "file:" + path + "?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database read-only: %w", err)
	}
	return db, nil
}
