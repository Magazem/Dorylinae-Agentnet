package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// Queue states (Docs/protocol/notify.md §Delivery, migration 13).
const (
	QueueStatePending = "pending"
	QueueStateSent    = "sent"
	QueueStateFailed  = "failed"
)

// queueMaxRows bounds the number of non-final (pending) rows
// (Docs/protocol/notify.md §Delivery).
const queueMaxRows = 1000

// queueRetention is how long a final row is kept after it was last updated
// (Docs/protocol/notify.md §Delivery).
const queueRetention = 7 * 24 * time.Hour

// queueMaxAge is the age (from creation) past which a pending row gives up
// regardless of attempt count (Docs/protocol/notify.md §Delivery).
const queueMaxAge = 24 * time.Hour

// retryDelays are the base delays between attempts 1->2, 2->3, ..., 6->7
// (Docs/protocol/notify.md §Delivery). Attempt 7 either succeeds or the row
// becomes failed.
var retryDelays = []time.Duration{
	10 * time.Second,
	1 * time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	6 * time.Hour,
}

// retryDelayCap is the "capped at 6h" ceiling on Retry-After
// (Docs/protocol/notify.md §Delivery).
const retryDelayCap = 6 * time.Hour

// queueRow is one webhook_queue row.
type queueRow struct {
	ID          string
	Event       string
	Body        string
	State       string
	Attempts    int
	NextAttempt sql.NullString
	Created     time.Time
	Updated     time.Time
	Status      sql.NullInt64
	Error       sql.NullString
}

// Queue wraps the webhook_queue table (migration 13,
// Docs/protocol/notify.md §Delivery).
type Queue struct {
	db *sql.DB
}

// NewQueue wraps db.
func NewQueue(db *sql.DB) *Queue { return &Queue{db: db} }

// Enqueue inserts a new pending row. When the table already holds
// queueMaxRows non-final rows, the oldest pending row is dropped first
// (state failed, error "overflow") to make room
// (Docs/protocol/notify.md §Delivery).
func (q *Queue) Enqueue(ctx context.Context, id, event string, body []byte, now time.Time) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_queue WHERE state = ?`, QueueStatePending).Scan(&n); err != nil {
		return fmt.Errorf("notify: count pending webhook rows: %w", err)
	}
	if n >= queueMaxRows {
		if err := overflowOldest(ctx, tx, now); err != nil {
			return err
		}
	}
	ts := storeTime(now)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO webhook_queue (id, event, body, state, attempts, next_attempt, created, updated)
VALUES (?, ?, ?, ?, 0, ?, ?, ?)`,
		id, event, string(body), QueueStatePending, ts, ts, ts); err != nil {
		return fmt.Errorf("notify: enqueue webhook row: %w", err)
	}
	return tx.Commit()
}

func overflowOldest(ctx context.Context, tx *sql.Tx, now time.Time) error {
	var oldest string
	err := tx.QueryRowContext(ctx, `
SELECT id FROM webhook_queue WHERE state = ? ORDER BY created ASC LIMIT 1`, QueueStatePending).Scan(&oldest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("notify: find oldest webhook row: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE webhook_queue SET state = ?, error = 'overflow', updated = ? WHERE id = ?`,
		QueueStateFailed, storeTime(now), oldest)
	if err != nil {
		return fmt.Errorf("notify: overflow oldest webhook row: %w", err)
	}
	return nil
}

// due returns pending rows whose next_attempt is at or before now, oldest
// first, at most limit rows.
func (q *Queue) due(ctx context.Context, now time.Time, limit int) ([]queueRow, error) {
	rows, err := q.db.QueryContext(ctx, `
SELECT id, event, body, state, attempts, next_attempt, created, updated, status, error
FROM webhook_queue
WHERE state = ? AND (next_attempt IS NULL OR next_attempt <= ?)
ORDER BY next_attempt ASC
LIMIT ?`, QueueStatePending, storeTime(now), limit)
	if err != nil {
		return nil, fmt.Errorf("notify: query due webhook rows: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []queueRow
	for rows.Next() {
		var r queueRow
		var created, updated string
		if err := rows.Scan(&r.ID, &r.Event, &r.Body, &r.State, &r.Attempts, &r.NextAttempt, &created, &updated, &r.Status, &r.Error); err != nil {
			return nil, err
		}
		r.Created, err = parseStoreTime(created)
		if err != nil {
			return nil, err
		}
		r.Updated, err = parseStoreTime(updated)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkSent records a successful delivery.
func (q *Queue) MarkSent(ctx context.Context, id string, status int, now time.Time) error {
	_, err := q.db.ExecContext(ctx, `
UPDATE webhook_queue SET state = ?, attempts = attempts + 1, status = ?, error = NULL, next_attempt = NULL, updated = ?
WHERE id = ?`, QueueStateSent, status, storeTime(now), id)
	return err
}

// MarkFailed records a permanent failure (a non-retryable status, redirect,
// blocked address, exhausted retries or overage).
func (q *Queue) MarkFailed(ctx context.Context, id string, status int, errStr string, now time.Time) error {
	var statusArg any
	if status > 0 {
		statusArg = status
	}
	_, err := q.db.ExecContext(ctx, `
UPDATE webhook_queue SET state = ?, attempts = attempts + 1, status = ?, error = ?, next_attempt = NULL, updated = ?
WHERE id = ?`, QueueStateFailed, statusArg, errStr, storeTime(now), id)
	return err
}

// MarkRetry schedules another attempt at nextAttempt.
func (q *Queue) MarkRetry(ctx context.Context, id string, status int, errStr string, nextAttempt, now time.Time) error {
	var statusArg any
	if status > 0 {
		statusArg = status
	}
	_, err := q.db.ExecContext(ctx, `
UPDATE webhook_queue SET attempts = attempts + 1, status = ?, error = ?, next_attempt = ?, updated = ?
WHERE id = ?`, statusArg, errStr, storeTime(nextAttempt), storeTime(now), id)
	return err
}

// FailAllPending marks every pending row failed with errStr (used by
// "--webhook off", Docs/protocol/notify.md §Delivery).
func (q *Queue) FailAllPending(ctx context.Context, errStr string, now time.Time) error {
	_, err := q.db.ExecContext(ctx, `
UPDATE webhook_queue SET state = ?, error = ?, next_attempt = NULL, updated = ? WHERE state = ?`,
		QueueStateFailed, errStr, storeTime(now), QueueStatePending)
	return err
}

// Purge deletes final rows older than queueRetention (Docs/protocol/notify.md
// §Delivery: "Rows are deleted 7 days after sent or failed").
func (q *Queue) Purge(ctx context.Context, now time.Time) error {
	cutoff := storeTime(now.Add(-queueRetention))
	_, err := q.db.ExecContext(ctx, `
DELETE FROM webhook_queue WHERE state IN (?, ?) AND updated < ?`, QueueStateSent, QueueStateFailed, cutoff)
	return err
}

// Counts returns the pending row count and the count of rows that reached
// failed in the last 7 days (Docs/cli/notify.md, Docs/protocol/ipc.md
// §Notifications).
func (q *Queue) Counts(ctx context.Context, now time.Time) (pending, failed7d int, err error) {
	if err = q.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_queue WHERE state = ?`, QueueStatePending).Scan(&pending); err != nil {
		return 0, 0, err
	}
	cutoff := storeTime(now.Add(-queueRetention))
	if err = q.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM webhook_queue WHERE state = ? AND updated >= ?`, QueueStateFailed, cutoff).Scan(&failed7d); err != nil {
		return 0, 0, err
	}
	return pending, failed7d, nil
}

// nextRetry computes the delay before the attempt after attemptsSoFar
// (1-based: attemptsSoFar is the number of attempts already made,
// including the one that just failed). ok is false once retries are
// exhausted (Docs/protocol/notify.md §Delivery).
func nextRetry(attemptsSoFar int, retryAfter time.Duration, jitter func() float64) (delay time.Duration, ok bool) {
	if attemptsSoFar < 1 || attemptsSoFar > len(retryDelays) {
		return 0, false
	}
	base := retryDelays[attemptsSoFar-1]
	if jitter == nil {
		jitter = defaultJitter
	}
	delay = time.Duration(float64(base) * jitter())
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay > retryDelayCap {
		delay = retryDelayCap
	}
	return delay, true
}

// defaultJitter returns a uniform value in [0.9, 1.1) (Docs/protocol/notify.md
// §Delivery: "×U(0.9, 1.1)"). This is retry-delay jitter, not a security use,
// so the weaker non-cryptographic generator is fine.
func defaultJitter() float64 { return 0.9 + rand.Float64()*0.2 } //nolint:gosec

func storeTime(t time.Time) string { return t.UTC().Format(storeTimeFmt) }

func parseStoreTime(s string) (time.Time, error) {
	return time.Parse(storeTimeFmt, s)
}
