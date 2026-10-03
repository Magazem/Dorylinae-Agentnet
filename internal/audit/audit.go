// Package audit holds the append-only audit log of every action the daemon takes.
//
// The audit_events table is created by the store migrations. Since migration
// 18 every row carries a hash that commits to the row and to the previous
// row's hash (Docs/protocol/audit.md §The chain); rows written before it are
// chained virtually and sealed by the audit.chain_start row. Every writer goes
// through Append or AppendTx: a trigger refuses unchained rows.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Well-known actors and actions.
const (
	ActorDaemon = "daemon"
	ActorCLI    = "cli"

	ActionDaemonStart         = "daemon.start"
	ActionDaemonStop          = "daemon.stop"
	ActionDaemonStopRequested = "daemon.stop_requested"
	ActionServiceInstall      = "service.install"
	ActionServiceUninstall    = "service.uninstall"
	ActionPeerVerify          = "peer.verify"
	ActionPeerRemove          = "peer.remove"
	ActionPeerVerifyFail      = "peer.verify_fail"
	ActionChainStart          = "audit.chain_start"
)

// WriteError is the error of an audit append that failed: the row could not be
// written (disk full, I/O error, a lock held past busy_timeout, or the chain
// refusing the row). Callers and tests use it to tell a failed row from a
// failed change (audit.md §When the row cannot be written).
type WriteError struct {
	Action string
	Err    error
}

func (e *WriteError) Error() string { return e.Err.Error() }
func (e *WriteError) Unwrap() error { return e.Err }

// TxLostError is returned by AppendTxSoft when SQLite ended the whole
// transaction (SQLITE_FULL, IOERR, NOMEM, RAISE(ROLLBACK)), so the savepoint
// saved nothing. The S- caller retries its change once in a new transaction
// without the row (audit.md, "What S- really guarantees").
type TxLostError struct {
	Action string
	Err    error
}

func (e *TxLostError) Error() string {
	return fmt.Sprintf("audit: append %s: transaction lost: %v", e.Action, e.Err)
}
func (e *TxLostError) Unwrap() error { return e.Err }

var errorLog atomic.Pointer[slog.Logger]

// noRows holds the transactions of RunSoft's retry, whose S- rows are skipped.
var noRows sync.Map

// SetErrorLog installs the logger every failed append reports to, once, with
// the action and the driver error, never the detail. daemon.Run installs it
// before the first append. A nil logger turns the report off.
func SetErrorLog(l *slog.Logger) { errorLog.Store(l) }

func logFailure(action string, err error) {
	if l := errorLog.Load(); l != nil {
		l.Error("audit write failed", "event", "audit_error", "action", action, "error", err.Error())
	}
}

// ReportFailure logs, like a failed append, that the row of action could not
// be built (its detail could not be read), with the error and never the
// detail. The caller still writes what it can and goes on (review 97 L3).
func ReportFailure(action string, err error) { logFailure(action, err) }

// fail wraps err as a *WriteError and reports it.
func fail(action string, err error) error {
	logFailure(action, err)
	return &WriteError{Action: action, Err: err}
}

// Event is one row of the audit log.
type Event struct {
	ID     int64
	TS     time.Time
	Actor  string
	Action string
	Detail json.RawMessage
}

// Log appends to and reads the audit log.
type Log struct {
	db *sql.DB
}

// New returns a Log over a migrated database.
func New(db *sql.DB) *Log { return &Log{db: db} }

// Append records an event. detail is marshalled to JSON; nil becomes {}.
//
// It runs its own BEGIN IMMEDIATE transaction on a dedicated connection, so
// the chain head is read under SQLite's write lock even when another process
// (agentnetd install) appends to the same file; the DSN's busy_timeout does
// the waiting (audit.md §Appending).
func (l *Log) Append(ctx context.Context, actor, action string, detail any) error {
	if err := l.append(ctx, actor, action, detail); err != nil {
		return fail(action, err)
	}
	return nil
}

func (l *Log) append(ctx context.Context, actor, action string, detail any) (err error) {
	raw, err := marshalDetail(actor, action, detail)
	if err != nil {
		return err
	}
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("audit: append %s: %w", action, err)
	}
	defer func() { _ = conn.Close() }()
	defer func() {
		if err != nil {
			// Background: a cancelled ctx must not leave the pooled
			// connection inside a transaction. This covers a failed BEGIN
			// too: the driver can report a cancellation after BEGIN already
			// took effect (INV-5); with no transaction open, ROLLBACK is a
			// harmless error.
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	if _, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("audit: append %s: begin: %w", action, err)
	}
	if err = appendChained(ctx, conn, actor, action, raw); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("audit: append %s: commit: %w", action, err)
	}
	return nil
}

// AppendTx is Append through a caller's transaction, for code that runs
// inside one and must touch nothing else (for example an approval Action's
// Perform, Docs/review/27-2.1a-review.md C1). The row commits or rolls back
// with tx, and so does the chain head.
func AppendTx(ctx context.Context, tx *sql.Tx, actor, action string, detail any) error {
	raw, err := marshalDetail(actor, action, detail)
	if err != nil {
		return fail(action, err)
	}
	if err := appendChained(ctx, tx, actor, action, raw); err != nil {
		return fail(action, err)
	}
	return nil
}

// AppendTxSoft is AppendTx for S- actions (removals): the row is written
// inside tx through a savepoint. If the row fails, only the row is rolled
// back, the failure is logged and nil is returned, so the change commits. If
// SQLite ended the whole transaction, it returns *TxLostError and the caller
// retries its change without the row. A failure of the savepoint statements
// themselves is returned (the transaction is unusable).
func AppendTxSoft(ctx context.Context, tx *sql.Tx, actor, action string, detail any) error {
	if _, skip := noRows.Load(tx); skip {
		return nil // RunSoft's retry after a lost transaction: the failure is already logged
	}
	raw, err := marshalDetail(actor, action, detail)
	if err != nil {
		logFailure(action, err)
		return nil
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT audit_row`); err != nil {
		return fail(action, err)
	}
	if err := appendChained(ctx, tx, actor, action, raw); err != nil {
		logFailure(action, err)
		if _, rerr := tx.ExecContext(ctx, `ROLLBACK TO audit_row`); rerr != nil {
			return &TxLostError{Action: action, Err: err}
		}
		if _, rerr := tx.ExecContext(ctx, `RELEASE audit_row`); rerr != nil {
			return &TxLostError{Action: action, Err: err}
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, `RELEASE audit_row`); err != nil {
		return fail(action, err)
	}
	return nil
}

// RunSoft runs an S- change in a transaction of its own. fn does the change
// and writes its rows through AppendTxSoft only when withRows is true. If fn
// fails with *TxLostError (SQLite ended the whole transaction), RunSoft runs fn
// once more in a new transaction with withRows false, so the removal still
// happens; the row's failure is already logged. Any other error rolls back and
// is returned (audit.md, "What S- really guarantees").
//
// On the retry every AppendTxSoft on that transaction is a no-op, including
// those of nested hooks (a peer removal's OnRemovedTx hooks), so the whole
// removal commits without its rows.
func RunSoft(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx, withRows bool) error) error {
	for withRows := true; ; withRows = false {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if !withRows {
			noRows.Store(tx, struct{}{})
		}
		err = fn(tx, withRows)
		noRows.Delete(tx)
		if err == nil {
			err = tx.Commit()
			if err == nil {
				return nil
			}
		} else {
			_ = tx.Rollback()
		}
		var lost *TxLostError
		if withRows && errors.As(err, &lost) {
			continue
		}
		return err
	}
}

// AppendTx is the package function AppendTx as a method, so an interface over
// *Log (an approval store's sink, a test fake) can reach it.
func (l *Log) AppendTx(ctx context.Context, tx *sql.Tx, actor, action string, detail any) error {
	return AppendTx(ctx, tx, actor, action, detail)
}

// AppendTxSoft is the package function AppendTxSoft as a method.
func (l *Log) AppendTxSoft(ctx context.Context, tx *sql.Tx, actor, action string, detail any) error {
	return AppendTxSoft(ctx, tx, actor, action, detail)
}

// queryExecer is what both *sql.Conn and *sql.Tx provide.
type queryExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func marshalDetail(actor, action string, detail any) (string, error) {
	if actor == "" || action == "" {
		return "", fmt.Errorf("audit: actor and action are required")
	}
	if detail == nil {
		return "{}", nil
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return "", fmt.Errorf("audit: marshal detail: %w", err)
	}
	return string(raw), nil
}

// chainStartDetail is the detail of audit.chain_start; the field order is the
// canonical (sorted) order, so json.Marshal already yields the stored form.
type chainStartDetail struct {
	LegacyLastID int64 `json:"legacy_last_id"`
	LegacyRows   int64 `json:"legacy_rows"`
}

// appendChained implements audit.md §Appending inside an open write
// transaction: read the head, write audit.chain_start first if the chain has
// not started, then insert the row with id = head.id + 1 and its hash.
func appendChained(ctx context.Context, q queryExecer, actor, action, detail string) error {
	var (
		headID   int64
		headHash sql.NullString
	)
	err := q.QueryRowContext(ctx, `SELECT id, hash FROM audit_events ORDER BY id DESC LIMIT 1`).Scan(&headID, &headHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("audit: append %s: read head: %w", action, err)
	}
	var prev []byte
	if headHash.Valid {
		if prev, err = decodeHash(headHash.String); err != nil {
			return fmt.Errorf("audit: append %s: head %d: %w", action, headID, err)
		}
	} else {
		// No row, or the first append after migration 18.
		if headID, prev, err = startChain(ctx, q, headID); err != nil {
			return fmt.Errorf("audit: append %s: %w", action, err)
		}
	}
	if _, err := insertRow(ctx, q, headID+1, prev, actor, action, detail); err != nil {
		return fmt.Errorf("audit: append %s: %w", action, err)
	}
	return nil
}

// startChain chains the legacy rows virtually and writes audit.chain_start
// after them. It returns the new head id and hash.
func startChain(ctx context.Context, q queryExecer, headID int64) (int64, []byte, error) {
	var stored int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE hash IS NOT NULL`).Scan(&stored); err != nil {
		return 0, nil, fmt.Errorf("count chained rows: %w", err)
	}
	if stored > 0 {
		// Only code that bypasses this package (with the trigger dropped)
		// can leave an unchained head after the chain started.
		return 0, nil, fmt.Errorf("chain broken: unchained row %d after the chain start (run 'agentnet log --verify', which works without the daemon)", headID)
	}
	prev := genesis()
	var d chainStartDetail
	rows, err := q.QueryContext(ctx, `SELECT id, ts, actor, action, detail FROM audit_events ORDER BY id`)
	if err != nil {
		return 0, nil, fmt.Errorf("read legacy rows: %w", err)
	}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.ts, &r.actor, &r.action, &r.detail); err != nil {
			_ = rows.Close()
			return 0, nil, fmt.Errorf("read legacy rows: %w", err)
		}
		if prev, err = chainHash(prev, r); err != nil {
			_ = rows.Close()
			return 0, nil, fmt.Errorf("legacy row %d: %w", r.id, err)
		}
		d.LegacyRows++
		d.LegacyLastID = r.id
	}
	if err := rows.Close(); err != nil {
		return 0, nil, fmt.Errorf("read legacy rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, nil, fmt.Errorf("read legacy rows: %w", err)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return 0, nil, err
	}
	h, err := insertRow(ctx, q, headID+1, prev, ActorDaemon, ActionChainStart, string(raw))
	if err != nil {
		return 0, nil, err
	}
	return headID + 1, h, nil
}

func insertRow(ctx context.Context, q queryExecer, id int64, prev []byte, actor, action, detail string) ([]byte, error) {
	r := row{id: id, ts: time.Now().UTC().Format(time.RFC3339Nano), actor: actor, action: action, detail: detail}
	h, err := chainHash(prev, r)
	if err != nil {
		return nil, err
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO audit_events (id, ts, actor, action, detail, hash) VALUES (?, ?, ?, ?, ?, ?)`,
		r.id, r.ts, r.actor, r.action, r.detail, encodeHash(h)); err != nil {
		return nil, err
	}
	return h, nil
}

// List returns the logged events in insertion order. The chain's own
// audit.chain_start row is bookkeeping, not an event, and is omitted; Verify
// covers it.
func (l *Log) List(ctx context.Context) ([]Event, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT id, ts, actor, action, detail FROM audit_events
		WHERE NOT (action = ? AND hash IS NOT NULL) ORDER BY id`, ActionChainStart)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		var (
			e      Event
			ts     string
			detail string
		)
		if err := rows.Scan(&e.ID, &ts, &e.Actor, &e.Action, &detail); err != nil {
			return nil, fmt.Errorf("audit: scan: %w", err)
		}
		if e.TS, err = time.Parse(time.RFC3339Nano, ts); err != nil {
			return nil, fmt.Errorf("audit: parse ts: %w", err)
		}
		e.Detail = json.RawMessage(detail)
		out = append(out, e)
	}
	return out, rows.Err()
}
