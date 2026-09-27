package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// Offline queue defaults, see Docs/protocol/envelope.md.
const (
	defaultQueueTTL         = 7 * 24 * time.Hour
	defaultQueueMaxEnvelope = 1000
	defaultQueueMaxBytes    = 32 << 20
	defaultSweepInterval    = time.Minute
	drainBatch              = 64
	queueOpTimeout          = 10 * time.Second
)

var errQueueFull = errors.New("recipient queue is full")

// queue persists envelopes addressed to a peer that is not connected. Frames
// are stored exactly as received; the relay never looks inside the payload.
type queue struct {
	db       *sql.DB
	ttl      time.Duration
	maxCount int
	maxBytes int64
	now      func() time.Time
}

type queued struct {
	seq   int64
	frame []byte
}

// relayMigration is one forward-only step of the relay database schema,
// numbered R1, R2, ... independent of the daemon's internal/store numbering
// (Docs/protocol/relay-hosted.md §3). Never edit an applied entry; append.
type relayMigration struct {
	version int
	name    string
	sql     string
}

// relayMigrations is the ordered relay schema history. R1 adopts the queue
// table exactly as every pre-migration relay left it (every statement is
// idempotent, so applying R1 to an old queue.db that already has the table
// only adds queue_by_sender) and adds the sender-side index the 4.0b caps
// need without scanning the table (review 50 M3).
var relayMigrations = []relayMigration{
	{1, "R1_queue_baseline", `
CREATE TABLE IF NOT EXISTS queue (
	seq      INTEGER PRIMARY KEY AUTOINCREMENT,
	to_key   TEXT NOT NULL,
	from_key TEXT NOT NULL,
	id       TEXT NOT NULL,
	enqueued INTEGER NOT NULL, -- unix milliseconds
	frame    BLOB NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS queue_dedupe ON queue (to_key, from_key, id);
CREATE INDEX IF NOT EXISTS queue_by_recipient ON queue (to_key, seq);
CREATE INDEX IF NOT EXISTS queue_by_age ON queue (enqueued);
CREATE INDEX IF NOT EXISTS queue_by_sender ON queue (from_key, enqueued);
`},
}

// openQueue opens the SQLite relay database at path; "" means a private
// in-memory database that does not survive the process.
func openQueue(path string, ttl time.Duration, maxCount int, maxBytes int64, now func() time.Time) (*queue, error) {
	dsn := "file::memory:?_pragma=busy_timeout(5000)&_pragma=secure_delete(1)"
	if path != "" {
		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=secure_delete(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open queue database: %w", err)
	}
	// One connection: serialises access, and keeps an in-memory database private and whole.
	db.SetMaxOpenConns(1)
	q := &queue{db: db, ttl: ttl, maxCount: maxCount, maxBytes: maxBytes, now: now}
	if err := q.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return q, nil
}

// migrate applies every relay migration not yet recorded in relay_migrations.
func (q *queue) migrate() error {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	if _, err := q.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS relay_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("create relay_migrations table: %w", err)
	}
	var current int
	if err := q.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM relay_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read relay schema version: %w", err)
	}
	if current > len(relayMigrations) {
		return fmt.Errorf("relay database schema version %d is newer than this binary (%d)", current, len(relayMigrations))
	}
	for _, m := range relayMigrations[current:] {
		if _, err := q.db.ExecContext(ctx, m.sql); err != nil {
			return fmt.Errorf("relay migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := q.db.ExecContext(ctx,
			`INSERT INTO relay_migrations (version, name, applied_at) VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
			m.version, m.name); err != nil {
			return fmt.Errorf("record relay migration %d: %w", m.version, err)
		}
	}
	return nil
}

func (q *queue) close() error { return q.db.Close() }

func (q *queue) cutoff() int64 { return q.now().Add(-q.ttl).UnixMilli() }

// add stores frame for h.To. Re-sending an envelope already queued (same
// sender and id) is a no-op that still succeeds.
func (q *queue) add(h envelope.Header, frame []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var one int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM queue WHERE to_key = ? AND from_key = ? AND id = ?`, h.To, h.From, h.ID).Scan(&one)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	var count int
	var size int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(LENGTH(frame)), 0) FROM queue WHERE to_key = ? AND enqueued >= ?`, h.To, q.cutoff()).Scan(&count, &size); err != nil {
		return err
	}
	if count >= q.maxCount || size+int64(len(frame)) > q.maxBytes {
		return errQueueFull
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO queue (to_key, from_key, id, enqueued, frame) VALUES (?, ?, ?, ?, ?)`,
		h.To, h.From, h.ID, q.now().UnixMilli(), frame); err != nil {
		return err
	}
	return tx.Commit()
}

// next returns up to limit unexpired envelopes for to with seq > after, oldest first.
func (q *queue) next(to string, after int64, limit int) ([]queued, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	rows, err := q.db.QueryContext(ctx, `SELECT seq, frame FROM queue WHERE to_key = ? AND seq > ? AND enqueued >= ? ORDER BY seq LIMIT ?`,
		to, after, q.cutoff(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []queued
	for rows.Next() {
		var r queued
		if err := rows.Scan(&r.seq, &r.frame); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ack deletes the envelope (from, id) addressed to to. Unknown envelopes are ignored.
func (q *queue) ack(to, from, id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	_, err := q.db.ExecContext(ctx, `DELETE FROM queue WHERE to_key = ? AND from_key = ? AND id = ?`, to, from, id)
	return err
}

// sweep deletes expired envelopes and reports how many.
func (q *queue) sweep() (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	res, err := q.db.ExecContext(ctx, `DELETE FROM queue WHERE enqueued < ?`, q.cutoff())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// count reports the unexpired envelopes waiting for to.
func (q *queue) count(to string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	var n int
	err := q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM queue WHERE to_key = ? AND enqueued >= ?`, to, q.cutoff()).Scan(&n)
	return n, err
}

// stats reports the unexpired rows and bytes across every recipient, for
// the operator metrics endpoint (Docs/protocol/relay-hosted.md §5).
func (q *queue) stats() (rows int64, bytes int64, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	err = q.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(LENGTH(frame)), 0) FROM queue WHERE enqueued >= ?`, q.cutoff()).Scan(&rows, &bytes)
	return rows, bytes, err
}
