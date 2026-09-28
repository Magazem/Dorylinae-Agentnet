package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
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
	drainBatchBytes         = 1 << 20
	queueOpTimeout          = 10 * time.Second
)

// Offline queue caps added by 4.0b (Docs/protocol/relay-hosted.md §2
// "Offline queue"). The per-recipient caps above stay.
const (
	defaultQueuePairMaxEnvelopes   = 300
	defaultQueuePairMaxBytes       = 8 << 20
	defaultQueueSenderMaxEnvelopes = 2000
	defaultQueueSenderMaxBytes     = 64 << 20
	defaultQueueMaxTotal           = 4 << 30
	defaultQueueMinFreeDisk        = 1 << 30
	// diskCheckEvery bounds how often add asks the OS for free space.
	diskCheckEvery = time.Second
)

var errQueueFull = errors.New("recipient queue is full")

// errStorageLow refuses new envelopes while the disk under the queue file
// has less than the configured free space; acks and deletes still work.
var errStorageLow = errors.New("relay storage low")

// queueFullError is errQueueFull naming the cap that refused the envelope.
type queueFullError struct{ limit string }

func (e *queueFullError) Error() string        { return "offline queue full (" + e.limit + ")" }
func (e *queueFullError) Is(target error) bool { return target == errQueueFull }

// queueLimits are the 4.0b caps. A zero field means no limit.
type queueLimits struct {
	pairCount   int   // per sender -> recipient
	pairBytes   int64 //
	senderCount int   // per sender, all recipients
	senderBytes int64 //
	totalBytes  int64 // relay-wide
	minFree     int64 // free disk under the queue file
	// freeDisk reports the free bytes of the file system holding dir.
	freeDisk func(dir string) (uint64, error)
}

// usage is a count of queued envelopes and their bytes.
type usage struct {
	n     int
	bytes int64
}

// queue persists envelopes addressed to a peer that is not connected. Frames
// are stored exactly as received; the relay never looks inside the payload.
type queue struct {
	db       *sql.DB
	path     string
	ttl      time.Duration
	maxCount int
	maxBytes int64
	now      func() time.Time
	lim      queueLimits

	// mu serialises add, ack and sweep with the in-memory totals below, so no
	// cap check scans the table (review 50 M3): the per-sender and relay-wide
	// totals are rebuilt by one scan at open and adjusted on add, ack and
	// sweep. They count every stored row, expired or not, until the sweep
	// deletes it.
	mu          sync.Mutex
	senders     map[string]*usage
	total       usage
	diskChecked time.Time
	diskLow     bool
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
	{2, "R2_accounts", relayMigrationR2},
}

// openQueue opens the SQLite relay database at path; "" means a private
// in-memory database that does not survive the process.
func openQueue(path string, ttl time.Duration, maxCount int, maxBytes int64, now func() time.Time) (*queue, error) {
	db, err := openRelayDB(path)
	if err != nil {
		return nil, err
	}
	q := &queue{db: db, path: path, ttl: ttl, maxCount: maxCount, maxBytes: maxBytes, now: now, senders: map[string]*usage{}}
	if err := q.rebuildTotals(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return q, nil
}

// rebuildTotals is the one scan at open that fills the in-memory totals.
func (q *queue) rebuildTotals() error {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	rows, err := q.db.QueryContext(ctx, `SELECT from_key, COUNT(*), COALESCE(SUM(LENGTH(frame)), 0) FROM queue GROUP BY from_key`)
	if err != nil {
		return fmt.Errorf("read queue totals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	q.mu.Lock()
	defer q.mu.Unlock()
	q.senders, q.total = map[string]*usage{}, usage{}
	for rows.Next() {
		var from string
		var u usage
		if err := rows.Scan(&from, &u.n, &u.bytes); err != nil {
			return fmt.Errorf("read queue totals: %w", err)
		}
		q.senders[from] = &u
		q.total.n += u.n
		q.total.bytes += u.bytes
	}
	return rows.Err()
}

// adjust changes the totals of from by n envelopes and b bytes. Callers hold mu.
func (q *queue) adjust(from string, n int, b int64) {
	u := q.senders[from]
	if u == nil {
		u = &usage{}
		q.senders[from] = u
	}
	u.n += n
	u.bytes += b
	if u.n <= 0 {
		delete(q.senders, from)
	}
	q.total.n += n
	q.total.bytes += b
}

// storageLow reports whether the disk under the queue file has less free
// space than lim.minFree, asking the OS at most every diskCheckEvery. An
// in-memory queue, or a failed check, is never low. Callers hold mu.
func (q *queue) storageLow() bool {
	if q.path == "" || q.lim.minFree <= 0 || q.lim.freeDisk == nil {
		return false
	}
	now := time.Now()
	if !q.diskChecked.IsZero() && now.Sub(q.diskChecked) < diskCheckEvery {
		return q.diskLow
	}
	q.diskChecked = now
	free, err := q.lim.freeDisk(filepath.Dir(q.path))
	q.diskLow = err == nil && free < uint64(q.lim.minFree)
	return q.diskLow
}

// openRelayDB opens the SQLite relay database at path ("" is a private
// in-memory database that does not survive the process) and applies every
// pending relay migration. The relay and `relay admin` both open it this way.
func openRelayDB(path string) (*sql.DB, error) {
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
	if err := migrateDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// migrateDB applies every relay migration not yet recorded in relay_migrations.
func migrateDB(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS relay_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("create relay_migrations table: %w", err)
	}
	var current int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM relay_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read relay schema version: %w", err)
	}
	if current > len(relayMigrations) {
		return fmt.Errorf("relay database schema version %d is newer than this binary (%d)", current, len(relayMigrations))
	}
	for _, m := range relayMigrations[current:] {
		if _, err := db.ExecContext(ctx, m.sql); err != nil {
			return fmt.Errorf("relay migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := db.ExecContext(ctx,
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
// sender and id) is a no-op that still succeeds. Every cap is checked from
// the in-memory totals or through an index (queueCapQueries), never by a
// scan of the table.
func (q *queue) add(h envelope.Header, frame []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	q.mu.Lock()
	defer q.mu.Unlock()
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var one int
	err = tx.QueryRowContext(ctx, queueDedupeQuery, h.To, h.From, h.ID).Scan(&one)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	size := int64(len(frame))
	if q.storageLow() {
		return errStorageLow
	}
	if q.lim.totalBytes > 0 && q.total.bytes+size > q.lim.totalBytes {
		return &queueFullError{limitQueueTotal}
	}
	if u := q.senders[h.From]; u != nil &&
		((q.lim.senderCount > 0 && u.n >= q.lim.senderCount) || (q.lim.senderBytes > 0 && u.bytes+size > q.lim.senderBytes)) {
		return &queueFullError{limitQueueSender}
	}
	var count int
	var used int64
	if q.lim.pairCount > 0 || q.lim.pairBytes > 0 {
		if err := tx.QueryRowContext(ctx, queuePairQuery, h.To, h.From, q.cutoff()).Scan(&count, &used); err != nil {
			return err
		}
		if (q.lim.pairCount > 0 && count >= q.lim.pairCount) || (q.lim.pairBytes > 0 && used+size > q.lim.pairBytes) {
			return &queueFullError{limitQueuePair}
		}
	}
	if err := tx.QueryRowContext(ctx, queueRecipientQuery, h.To, q.cutoff()).Scan(&count, &used); err != nil {
		return err
	}
	if count >= q.maxCount || used+size > q.maxBytes {
		return &queueFullError{limitQueueRecipient}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO queue (to_key, from_key, id, enqueued, frame) VALUES (?, ?, ?, ?, ?)`,
		h.To, h.From, h.ID, q.now().UnixMilli(), frame); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	q.adjust(h.From, 1, size)
	return nil
}

// The queries behind add's cap checks. Each must be answered through an
// index (queue_dedupe, queue_by_recipient); a test checks their query plans.
const (
	queueDedupeQuery    = `SELECT 1 FROM queue WHERE to_key = ? AND from_key = ? AND id = ?`
	queuePairQuery      = `SELECT COUNT(*), COALESCE(SUM(LENGTH(frame)), 0) FROM queue WHERE to_key = ? AND from_key = ? AND enqueued >= ?`
	queueRecipientQuery = `SELECT COUNT(*), COALESCE(SUM(LENGTH(frame)), 0) FROM queue WHERE to_key = ? AND enqueued >= ?`
)

// queueCapQueries lists add's cap-check queries, for the query-plan test.
var queueCapQueries = []string{queueDedupeQuery, queuePairQuery, queueRecipientQuery}

// next returns up to limit unexpired envelopes for to with seq > after,
// oldest first, stopping early once they hold maxBytes (always at least
// one), so a drain holds a bounded batch in memory while it waits.
func (q *queue) next(to string, after int64, limit int, maxBytes int64) ([]queued, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	rows, err := q.db.QueryContext(ctx, `SELECT seq, frame FROM queue WHERE to_key = ? AND seq > ? AND enqueued >= ? ORDER BY seq LIMIT ?`,
		to, after, q.cutoff(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []queued
	var total int64
	for rows.Next() {
		var r queued
		if err := rows.Scan(&r.seq, &r.frame); err != nil {
			return nil, err
		}
		out = append(out, r)
		if total += int64(len(r.frame)); total >= maxBytes {
			break
		}
	}
	return out, rows.Err()
}

// ack deletes the envelope (from, id) addressed to to. Unknown envelopes are ignored.
func (q *queue) ack(to, from, id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	q.mu.Lock()
	defer q.mu.Unlock()
	rows, err := q.db.QueryContext(ctx, `DELETE FROM queue WHERE to_key = ? AND from_key = ? AND id = ? RETURNING LENGTH(frame)`, to, from, id)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var size int64
		if err := rows.Scan(&size); err != nil {
			return err
		}
		q.adjust(from, -1, -size)
	}
	return rows.Err()
}

// sweep deletes expired envelopes and reports how many.
func (q *queue) sweep() (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	q.mu.Lock()
	defer q.mu.Unlock()
	rows, err := q.db.QueryContext(ctx, `DELETE FROM queue WHERE enqueued < ? RETURNING from_key, LENGTH(frame)`, q.cutoff())
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var n int64
	for rows.Next() {
		var from string
		var size int64
		if err := rows.Scan(&from, &size); err != nil {
			return n, err
		}
		q.adjust(from, -1, -size)
		n++
	}
	return n, rows.Err()
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
