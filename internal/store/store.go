// Package store wraps the SQLite persistence layer and its schema migrations.
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go driver, keeps cross-compilation static
)

// migration is one forward-only schema step. Versions are consecutive from 1.
type migration struct {
	version int
	name    string
	sql     string
}

// migrations is the ordered schema history. Never edit an applied entry; append.
var migrations = []migration{
	{1, "audit_events", `
CREATE TABLE audit_events (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	ts     TEXT NOT NULL,
	actor  TEXT NOT NULL,
	action TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(detail))
);
CREATE INDEX audit_events_ts ON audit_events (ts);
CREATE TRIGGER audit_events_no_update BEFORE UPDATE ON audit_events
BEGIN SELECT RAISE(ABORT, 'audit_events is append-only'); END;
CREATE TRIGGER audit_events_no_delete BEFORE DELETE ON audit_events
BEGIN SELECT RAISE(ABORT, 'audit_events is append-only'); END;
`},
	{2, "peers", `
CREATE TABLE peers (
	public_key TEXT PRIMARY KEY,
	name       TEXT NOT NULL,
	harness    TEXT NOT NULL,
	skills     TEXT NOT NULL CHECK (json_valid(skills)),
	card       TEXT NOT NULL CHECK (json_valid(card)),
	paired_at  TEXT NOT NULL
);
`},
	{3, "peers_trust", `
ALTER TABLE peers ADD COLUMN trust TEXT NOT NULL DEFAULT 'relay'
	CHECK (trust IN ('relay', 'code', 'fingerprint'));
ALTER TABLE peers ADD COLUMN mailbox_keys TEXT NOT NULL DEFAULT '[]'
	CHECK (json_valid(mailbox_keys));
`},
	{4, "pair_used_codes", `
CREATE TABLE pair_used_codes (
	hash    BLOB PRIMARY KEY,
	used_at INTEGER NOT NULL
);
`},
	{5, "mail_seen_inbox", `
CREATE TABLE mail_seen (
	from_key    TEXT NOT NULL,
	id          TEXT NOT NULL,
	received_at TEXT NOT NULL,
	PRIMARY KEY (from_key, id)
) WITHOUT ROWID;
CREATE INDEX mail_seen_received ON mail_seen (received_at);
CREATE TABLE mail_inbox (
	from_key    TEXT NOT NULL,
	id          TEXT NOT NULL,
	kind        TEXT NOT NULL,
	created     TEXT NOT NULL,
	received_at TEXT NOT NULL,
	signed      TEXT NOT NULL,
	PRIMARY KEY (from_key, id)
);
`},
	{6, "mailbox_keys_own", `
CREATE TABLE mailbox_keys_own (
	key_id       TEXT PRIMARY KEY,
	pub          TEXT NOT NULL,
	created      TEXT NOT NULL,
	not_after    TEXT NOT NULL,
	retired      TEXT,
	deleted      TEXT,
	announcement TEXT NOT NULL CHECK (json_valid(announcement))
);
`},
	{7, "outbox", `
CREATE TABLE outbox (
	id           TEXT PRIMARY KEY,
	to_key       TEXT NOT NULL,
	kind         TEXT NOT NULL,
	created      TEXT NOT NULL,
	key_id       TEXT,
	signed       TEXT,
	frame        TEXT,
	state        TEXT NOT NULL CHECK (state IN ('queued','relayed','delivered','expired','failed')),
	attempts     INTEGER NOT NULL DEFAULT 0,
	next_attempt TEXT,
	updated      TEXT NOT NULL,
	error        TEXT
);
CREATE INDEX outbox_due ON outbox (state, next_attempt);
`},
	// Rebuild: SQLite cannot change a CHECK. Safe inside the migration transaction
	// because through migration 7 nothing refers to peers; re-check sqlite_master
	// if a later migration adds such a reference before this one.
	{8, "peers_trust_team", `
CREATE TABLE peers_new (
	public_key    TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	harness       TEXT NOT NULL,
	skills        TEXT NOT NULL CHECK (json_valid(skills)),
	card          TEXT NOT NULL CHECK (json_valid(card)),
	paired_at     TEXT NOT NULL,
	trust         TEXT NOT NULL DEFAULT 'relay'
	              CHECK (trust IN ('relay', 'team', 'code', 'fingerprint')),
	mailbox_keys  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(mailbox_keys)),
	introduced_by TEXT
);
INSERT INTO peers_new (public_key, name, harness, skills, card, paired_at, trust, mailbox_keys)
	SELECT public_key, name, harness, skills, card, paired_at, trust, mailbox_keys FROM peers;
DROP TABLE peers;
ALTER TABLE peers_new RENAME TO peers;
`},
}

// Store is an open SQLite database with migrations applied.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One writer keeps SQLITE_BUSY out of the daemon's simple access pattern.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// DB exposes the handle for packages that own tables (e.g. audit).
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this binary (%d)", current, len(migrations))
	}
	for _, m := range migrations[current:] {
		if err := s.apply(ctx, m); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

func (s *Store) apply(ctx context.Context, m migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO migrations (version, name, applied_at) VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		m.version, m.name); err != nil {
		return err
	}
	return tx.Commit()
}
