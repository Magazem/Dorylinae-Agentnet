package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
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

// Expiry sweep and WAL bound (R55-F2, relay-hosted.md §2 "Expiry sweep in
// bounded batches"): rows deleted per transaction, work per sweep tick, the
// WAL size SQLite truncates back to (and past which a tick's deletions end
// with a TRUNCATE checkpoint), marks pruned per statement, and the busy
// timeout of the DSN.
const (
	sweepBatchRows  = 32
	sweepTickBudget = 30 * time.Second
	// sweepPause lets add, ack and drains take the queue lock between two
	// batches: a woken waiter would otherwise lose the lock to the next
	// batch at least once (sync.Mutex hands over only after 1 ms of waiting).
	sweepPause     = 5 * time.Millisecond
	walSizeLimit   = 64 << 20
	pruneBatchRows = 256
	busyTimeoutMS  = 5000
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

	// sweepHook, if set, runs under mu before each sweep batch with the
	// batch's index in the tick and fails the batch if it returns an error;
	// sweptHook runs under mu after each committed batch (tests only).
	sweepHook func(i int) error
	sweptHook func(i int, n int64, took time.Duration)
	// readHook, if set, sees the rows whose frames a drain read (tests only).
	readHook func(to string, rows []queued)
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
	// R3 (R55-F2): the delivered high-water mark per recipient, H. A row of
	// to_key with seq <= H has been handed to one of its connections before,
	// so sending it again is a budgeted redelivery (relay-hosted.md §2
	// "First delivery and redelivery").
	{3, "R3_queue_delivered", `CREATE TABLE IF NOT EXISTS queue_delivered (to_key TEXT PRIMARY KEY, seq INTEGER NOT NULL) WITHOUT ROWID;`},
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
		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=secure_delete(1)" +
			"&_pragma=journal_size_limit(" + strconv.Itoa(walSizeLimit) + ")"
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
	q.mu.Lock()
	defer q.mu.Unlock()
	// The deadline starts once the lock is held, so an add that waited
	// behind a sweep batch still has its full time (R55-011).
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
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

// claim reads the delivered high-water mark H of to. If after is below H it
// returns H and no rows: the rows in (after, H] were delivered before, and
// the caller decides on their redelivery (probe, nextRange). Otherwise it
// returns up to limit unexpired rows with seq > after, oldest first,
// stopping early once they hold maxBytes (always at least one), so a drain
// holds a bounded batch in memory while it waits, and raises H to the last
// of them before returning. They are claimed as first deliveries before a
// frame is sent, so no later connection gets them as first deliveries again
// (review 66b H1). One transaction under mu; H is never lowered.
func (q *queue) claim(to string, after int64, limit int, maxBytes int64) (rows []queued, high int64, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	err = tx.QueryRowContext(ctx, `SELECT seq FROM queue_delivered WHERE to_key = ?`, to).Scan(&high)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, 0, err
	}
	if after < high {
		return nil, high, nil
	}
	rows, err = readRows(ctx, tx, `SELECT seq, frame FROM queue WHERE to_key = ? AND seq > ? AND enqueued >= ? ORDER BY seq LIMIT ?`,
		maxBytes, to, after, q.cutoff(), limit)
	if err != nil || len(rows) == 0 {
		return nil, high, err
	}
	last := rows[len(rows)-1].seq
	if _, err := tx.ExecContext(ctx, `INSERT INTO queue_delivered (to_key, seq) VALUES (?, ?)
		ON CONFLICT(to_key) DO UPDATE SET seq = MAX(seq, excluded.seq)`, to, last); err != nil {
		return nil, high, err
	}
	if err := tx.Commit(); err != nil {
		return nil, high, err
	}
	if q.readHook != nil {
		q.readHook(to, rows)
	}
	return rows, max(high, last), nil
}

// probe returns the seq and frame length of the first unexpired row of to in
// (after, upto] without reading a frame: LENGTH of a blob comes from the
// record header (review 66b M1). found is false when there is none.
func (q *queue) probe(to string, after, upto int64) (seq, size int64, found bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	err = q.db.QueryRowContext(ctx, queueProbeQuery, to, after, upto, q.cutoff()).Scan(&seq, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	return seq, size, err == nil, err
}

// queueProbeQuery is probe's query. It is answered through queue_by_recipient
// (a test checks its plan).
const queueProbeQuery = `SELECT seq, LENGTH(frame) FROM queue WHERE to_key = ? AND seq > ? AND seq <= ? AND enqueued >= ? ORDER BY seq LIMIT 1`

// nextRange returns up to limit unexpired rows of to in (after, upto], oldest
// first, stopping early once they hold maxBytes (always at least one): rows
// delivered before, for a redelivery. H is not changed.
func (q *queue) nextRange(to string, after, upto int64, limit int, maxBytes int64) ([]queued, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	rows, err := readRows(ctx, q.db, `SELECT seq, frame FROM queue WHERE to_key = ? AND seq > ? AND seq <= ? AND enqueued >= ? ORDER BY seq LIMIT ?`,
		maxBytes, to, after, upto, q.cutoff(), limit)
	if err == nil && q.readHook != nil {
		q.readHook(to, rows)
	}
	return rows, err
}

// delivered reports the delivered high-water mark of to (0 for none).
func (q *queue) delivered(to string) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	var h int64
	err := q.db.QueryRowContext(ctx, `SELECT seq FROM queue_delivered WHERE to_key = ?`, to).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return h, err
}

// querier is what readRows needs of a *sql.DB or *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// readRows runs a query returning seq and frame and collects its rows,
// stopping once they hold maxBytes (always at least one).
func readRows(ctx context.Context, db querier, query string, maxBytes int64, args ...any) ([]queued, error) {
	rows, err := db.QueryContext(ctx, query, args...)
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

// sweep deletes every expired envelope (sweepExpired without a time limit)
// and reports how many.
func (q *queue) sweep() (int64, error) {
	n, _, err := q.sweepExpired(0, nil)
	return n, err
}

// sweepExpired deletes expired rows oldest first, sweepBatchRows per
// transaction with mu released between batches, until none is left, budget
// has passed (0: no limit) or stop is closed; it reports the rows and frame
// bytes deleted. A failed batch rolls back only itself: the batches before
// it stay deleted and the next tick carries on (R55-011).
func (q *queue) sweepExpired(budget time.Duration, stop <-chan struct{}) (n, bytes int64, err error) {
	start := time.Now()
	for i := 0; ; i++ {
		bn, bb, err := q.sweepBatch(i)
		n, bytes = n+bn, bytes+bb
		if err != nil || bn == 0 {
			return n, bytes, err
		}
		if budget > 0 && time.Since(start) >= budget {
			return n, bytes, nil
		}
		select {
		case <-stop:
			return n, bytes, nil
		case <-time.After(sweepPause):
		}
	}
}

// sweepBatch deletes at most sweepBatchRows expired rows, oldest first, in
// one transaction, and adjusts the totals only once it has committed, so an
// error part way leaves them exact.
func (q *queue) sweepBatch(i int) (n, bytes int64, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	start := time.Now()
	if q.sweepHook != nil {
		if err := q.sweepHook(i); err != nil {
			return 0, 0, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	gone, err := deleteExpired(ctx, tx, q.cutoff())
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	for _, g := range gone {
		q.adjust(g.from, -1, -g.size)
		bytes += g.size
	}
	n = int64(len(gone))
	if q.sweptHook != nil {
		q.sweptHook(i, n, time.Since(start))
	}
	return n, bytes, nil
}

// expiredRow is a row sweepBatch deleted: its sender and frame length.
type expiredRow struct {
	from string
	size int64
}

// deleteExpired runs one batch's DELETE in tx and collects what it deleted.
func deleteExpired(ctx context.Context, tx *sql.Tx, cutoff int64) ([]expiredRow, error) {
	rows, err := tx.QueryContext(ctx, `DELETE FROM queue WHERE seq IN (SELECT seq FROM queue WHERE enqueued < ? ORDER BY enqueued LIMIT ?)
		RETURNING from_key, LENGTH(frame)`, cutoff, sweepBatchRows)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var gone []expiredRow
	for rows.Next() {
		var g expiredRow
		if err := rows.Scan(&g.from, &g.size); err != nil {
			return nil, err
		}
		gone = append(gone, g)
	}
	return gone, rows.Err()
}

// pruneDelivered deletes the delivered marks of recipients with no queued row
// left, pruneBatchRows per statement with mu released between, until none is
// left, budget has passed (0: no limit) or stop is closed. A later row for
// such a key gets a larger seq than any before it, so it is a first
// delivery, as it should be.
func (q *queue) pruneDelivered(budget time.Duration, stop <-chan struct{}) (int64, error) {
	start := time.Now()
	var total int64
	for {
		n, err := q.pruneBatch()
		total += n
		if err != nil || n == 0 {
			return total, err
		}
		if budget > 0 && time.Since(start) >= budget {
			return total, nil
		}
		select {
		case <-stop:
			return total, nil
		default:
		}
	}
}

func (q *queue) pruneBatch() (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	res, err := q.db.ExecContext(ctx, `DELETE FROM queue_delivered WHERE to_key IN (SELECT d.to_key FROM queue_delivered d
		WHERE NOT EXISTS (SELECT 1 FROM queue q WHERE q.to_key = d.to_key) LIMIT ?)`, pruneBatchRows)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// truncateWAL runs a TRUNCATE checkpoint that never waits: while another
// connection reads an old snapshot (a relay backup) the busy handler would
// otherwise hold the relay's only connection for the busy timeout (review
// 66b M3). A busy result is not an error: the checkpoint is skipped. It
// reports whether the WAL was truncated.
func (q *queue) truncateWAL() (bool, error) {
	if q.path == "" {
		return false, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), queueOpTimeout)
	defer cancel()
	conn, err := q.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout = 0`); err != nil {
		return false, err
	}
	defer func() { _, _ = conn.ExecContext(ctx, `PRAGMA busy_timeout = `+strconv.Itoa(busyTimeoutMS)) }()
	var busy, logFrames, checkpointed int64
	err = conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed)
	switch {
	case err != nil && isBusy(err):
		return false, nil
	case err != nil:
		return false, err
	}
	return busy == 0, nil
}

// isBusy reports whether err is SQLite's SQLITE_BUSY or SQLITE_LOCKED.
func isBusy(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "SQLITE_LOCKED") || strings.Contains(msg, "database is locked")
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
