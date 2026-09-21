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

	ActionDaemonStart = "daemon.start"
	ActionDaemonStop  = "daemon.stop"
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
	_, err := l.db.ExecContext(ctx,
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
