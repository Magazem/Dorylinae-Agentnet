package presence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// storeTimeFmt is the internal bookkeeping timestamp format, matching
// mail.StoreTimeFmt.
const storeTimeFmt = "2006-01-02T15:04:05.000Z"

// Store persists presence_peers rows (migration 10).
type Store struct{ db *sql.DB }

// NewStore wraps db.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Accept applies the order/replay rule (Docs/protocol/presence.md §Receiving
// step 4) and, on acceptance, upserts the row (step 5). msgCreated is the
// message's verified created time; now is the receiver clock (last_rx).
//
// accepted is false, with no error, when the message is a replay or
// out-of-order and was correctly dropped. edge is true only when accepted,
// the message is online, and the peer was not effectively online just before
// it (step 6): no row, a stored goodbye, or a stored online whose last_rx is
// more than 2.5 × interval before now (a crash or lost link without goodbye).
func (s *Store) Accept(ctx context.Context, peer string, b Body, msgCreated, now time.Time) (accepted, edge bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, fmt.Errorf("presence: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var prevBoot, prevState, prevCreatedStr, prevRxStr string
	var prevSeq, prevInterval int64
	var lastAgent, lastHuman sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT boot, seq, created, state, interval, last_rx, last_agent, last_human FROM presence_peers WHERE key = ?`, peer,
	).Scan(&prevBoot, &prevSeq, &prevCreatedStr, &prevState, &prevInterval, &prevRxStr, &lastAgent, &lastHuman)
	hasRow := true
	switch {
	case errors.Is(err, sql.ErrNoRows):
		hasRow = false
	case err != nil:
		return false, false, fmt.Errorf("presence: read row: %w", err)
	}

	wasOnline := false
	if hasRow {
		prevCreated, perr := time.Parse(storeTimeFmt, prevCreatedStr)
		if perr != nil {
			return false, false, fmt.Errorf("presence: stored created: %w", perr)
		}
		ok := (b.Boot == prevBoot && b.Seq > prevSeq) ||
			(b.Boot != prevBoot && !msgCreated.Before(prevCreated))
		if !ok {
			return false, false, nil
		}
		prevRx, perr := time.Parse(storeTimeFmt, prevRxStr)
		if perr != nil {
			return false, false, fmt.Errorf("presence: stored last_rx: %w", perr)
		}
		wasOnline = EffectivelyOnline(prevState, prevRx, int(prevInterval), now)
	}

	edge = !wasOnline && b.State == "online"

	nowStr := now.UTC().Format(storeTimeFmt)
	createdStr := msgCreated.UTC().Format(storeTimeFmt)
	if b.State == "online" && b.Agent == 1 {
		lastAgent = sql.NullString{String: nowStr, Valid: true}
	}
	if b.State == "online" && b.Human == 1 {
		lastHuman = sql.NullString{String: nowStr, Valid: true}
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO presence_peers (key, boot, seq, created, state, agent, human, interval, last_rx, last_agent, last_human)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (key) DO UPDATE SET
	boot = excluded.boot, seq = excluded.seq, created = excluded.created,
	state = excluded.state, agent = excluded.agent, human = excluded.human,
	interval = excluded.interval, last_rx = excluded.last_rx,
	last_agent = excluded.last_agent, last_human = excluded.last_human`,
		peer, b.Boot, b.Seq, createdStr, b.State, b.Agent, b.Human, b.Interval, nowStr,
		nullable(lastAgent), nullable(lastHuman),
	); err != nil {
		return false, false, fmt.Errorf("presence: upsert: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, false, fmt.Errorf("presence: commit: %w", err)
	}
	return true, edge, nil
}

// EffectivelyOnline is daemon_online of Docs/protocol/presence.md §Receiving:
// the stored state is online and the last message arrived no more than
// 2.5 × interval before now (receiver clock).
func EffectivelyOnline(state string, lastRx time.Time, interval int, now time.Time) bool {
	return state == "online" && now.Sub(lastRx) <= time.Duration(interval)*5*time.Second/2
}

func nullable(n sql.NullString) any {
	if !n.Valid {
		return nil
	}
	return n.String
}
