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

// PeerView is one peer's effective presence at a point in time
// (Docs/protocol/presence.md §Receiving, "Effective state of a peer"), used by
// status --team.
type PeerView struct {
	// Known is false when this daemon has never accepted a presence message
	// from the peer: every other field is then zero.
	Known            bool
	DaemonOnline     bool
	AgentActive      bool
	HumanPresent     *bool // nil = unknown or daemon offline
	LastSeen         *time.Time
	AgentLastActive  *time.Time
	HumanLastPresent *time.Time
}

// View returns peer's effective presence at now.
func (s *Store) View(ctx context.Context, peer string, now time.Time) (PeerView, error) {
	var state string
	var interval, agent, human int
	var lastRxStr string
	var lastAgent, lastHuman sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT state, agent, human, interval, last_rx, last_agent, last_human FROM presence_peers WHERE key = ?`, peer,
	).Scan(&state, &agent, &human, &interval, &lastRxStr, &lastAgent, &lastHuman)
	if errors.Is(err, sql.ErrNoRows) {
		return PeerView{}, nil
	}
	if err != nil {
		return PeerView{}, fmt.Errorf("presence: view: %w", err)
	}
	lastRx, perr := time.Parse(storeTimeFmt, lastRxStr)
	if perr != nil {
		return PeerView{}, fmt.Errorf("presence: view last_rx: %w", perr)
	}
	v := PeerView{Known: true, LastSeen: &lastRx}
	v.DaemonOnline = EffectivelyOnline(state, lastRx, interval, now)
	if v.DaemonOnline {
		v.AgentActive = agent == 1
		if human != 2 {
			b := human == 1
			v.HumanPresent = &b
		}
	}
	if lastAgent.Valid {
		if t, perr := time.Parse(storeTimeFmt, lastAgent.String); perr == nil {
			v.AgentLastActive = &t
		}
	}
	if lastHuman.Valid {
		if t, perr := time.Parse(storeTimeFmt, lastHuman.String); perr == nil {
			v.HumanLastPresent = &t
		}
	}
	return v, nil
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
