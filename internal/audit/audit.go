// Package audit holds the append-only audit log of every action the daemon takes.
//
// The audit_events table is created by the store migrations. It is not yet
// hash-chained; ticket 3.6 adds a chain column with a later migration, which
// is why rows are addressed by their monotonically increasing id.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Well-known actors and actions.
const (
	ActorDaemon = "daemon"
	ActorCLI    = "cli"

	ActionDaemonStart      = "daemon.start"
	ActionDaemonStop       = "daemon.stop"
	ActionServiceInstall   = "service.install"
	ActionServiceUninstall = "service.uninstall"
	ActionPeerVerify       = "peer.verify"
	ActionPeerRemove       = "peer.remove"
	ActionPeerVerifyFail   = "peer.verify_fail"
)

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
func (l *Log) Append(ctx context.Context, actor, action string, detail any) error {
	return appendTo(ctx, l.db, actor, action, detail)
}

// AppendTx is Append through a caller's transaction, for code that runs
// inside one and must touch nothing else (for example an approval Action's
// Perform, Docs/review/27-2.1a-review.md C1). The row commits or rolls back
// with tx.
func AppendTx(ctx context.Context, tx *sql.Tx, actor, action string, detail any) error {
	return appendTo(ctx, tx, actor, action, detail)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func appendTo(ctx context.Context, db execer, actor, action string, detail any) error {
	if actor == "" || action == "" {
		return fmt.Errorf("audit: actor and action are required")
	}
	raw := []byte("{}")
	if detail != nil {
		var err error
		if raw, err = json.Marshal(detail); err != nil {
			return fmt.Errorf("audit: marshal detail: %w", err)
		}
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO audit_events (ts, actor, action, detail) VALUES (?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano), actor, action, string(raw))
	if err != nil {
		return fmt.Errorf("audit: append %s: %w", action, err)
	}
	return nil
}

// List returns all events in insertion order.
func (l *Log) List(ctx context.Context) ([]Event, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT id, ts, actor, action, detail FROM audit_events ORDER BY id`)
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
