package request

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Show runs request_show (Docs/protocol/ipc.md §Requests): out rows first,
// then in rows (from narrows the in lookup).
func (s *Store) Show(ctx context.Context, id, from string) (View, error) {
	row, err := s.findOutRow(ctx, s.DB, id)
	if err != nil && !errors.Is(err, ErrUnknownRequest) {
		return View{}, err
	}
	if err == nil {
		v, err := toView(row)
		if err != nil {
			return View{}, err
		}
		v.Delivery = s.deliveryOf(ctx, row.mailID)
		return v, nil
	}
	row, err = s.findInRow(ctx, s.DB, id, from)
	if err != nil {
		return View{}, err
	}
	return toView(row)
}

// Key identifies one request row: requests are unique per (direction, peer,
// id), not per id alone.
type Key struct {
	Direction, Peer, ID string
}

// FindKey returns the key of the first request row for which match is true,
// or ErrUnknownRequest. It reads only the key columns, so a scan does not
// decode request bodies (Docs/protocol/consult.md: request_show on a derived
// session id).
func (s *Store) FindKey(ctx context.Context, match func(Key) bool) (Key, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT direction, peer, id FROM requests`)
	if err != nil {
		return Key{}, fmt.Errorf("request: read keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.Direction, &k.Peer, &k.ID); err != nil {
			return Key{}, fmt.Errorf("request: scan key: %w", err)
		}
		if match(k) {
			return k, nil
		}
	}
	if err := rows.Err(); err != nil {
		return Key{}, fmt.Errorf("request: read keys: %w", err)
	}
	return Key{}, ErrUnknownRequest
}

// ShowKey is Show for exactly the row k, never another row with the same id.
func (s *Store) ShowKey(ctx context.Context, k Key) (View, error) {
	row, err := getRow(ctx, s.DB, k.Direction, k.Peer, k.ID)
	if err != nil {
		return View{}, err
	}
	v, err := toView(row)
	if err != nil {
		return View{}, err
	}
	if k.Direction == "out" {
		v.Delivery = s.deliveryOf(ctx, row.mailID)
	}
	return v, nil
}

func (s *Store) deliveryOf(ctx context.Context, mailID string) string {
	var st string
	err := s.DB.QueryRowContext(ctx, `SELECT state FROM outbox WHERE id = ?`, mailID).Scan(&st)
	if errors.Is(err, sql.ErrNoRows) {
		return "unknown"
	}
	if err != nil {
		return "unknown"
	}
	return st
}

// PeekTypeTitle returns the type and title of direction's row for (peer, id),
// for a work session view's request reference (Docs/protocol/work-session.md
// §IPC, session view "request": {"id", "type", "title"}) without building a
// full View (peer resolution, team, output). direction is "out" for a
// requester-role session, "in" for a worker-role one.
func (s *Store) PeekTypeTitle(ctx context.Context, direction, peer, id string) (typ, title string, err error) {
	row, err := getRow(ctx, s.DB, direction, peer, id)
	if err != nil {
		return "", "", err
	}
	if req, terr := decodeStoredBody(row.body); terr == nil {
		title = req.Title
	}
	return row.typ, title, nil
}

// ListFilter narrows request_list (Docs/protocol/ipc.md §Requests).
type ListFilter struct {
	State string // "" for any
	Team  string // team id, "" for any
	Peer  string // peer key, "" for any
}

// List runs request_list: the sender's own (out) requests, newest created
// first.
func (s *Store) List(ctx context.Context, f ListFilter) ([]View, error) {
	q := `SELECT ` + requestColumns + ` FROM requests WHERE direction = 'out'`
	var args []any
	if f.State != "" {
		q += ` AND state = ?`
		args = append(args, f.State)
	}
	if f.Team != "" {
		q += ` AND team_id = ?`
		args = append(args, f.Team)
	}
	if f.Peer != "" {
		q += ` AND peer = ?`
		args = append(args, f.Peer)
	}
	q += ` ORDER BY created DESC, id ASC`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("request: list: %w", err)
	}
	// Collect every row before running deliveryOf's own query: with one
	// pooled connection (store.Open sets SetMaxOpenConns(1)), a second query
	// started while rows is still open would block forever waiting for a
	// connection the outer cursor is holding.
	var scanned []storedRow
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("request: scan: %w", err)
		}
		scanned = append(scanned, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]View, 0, len(scanned))
	for _, r := range scanned {
		v, err := toView(r)
		if err != nil {
			return nil, err
		}
		v.Delivery = s.deliveryOf(ctx, r.mailID)
		out = append(out, v)
	}
	return out, nil
}
