package approval

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// keyWrongCodes is the settings-table key holding the rolling wrong-code
// window (Docs/protocol/approval.md §Object, "The rolling wrong-code count
// lives in settings").
const keyWrongCodes = "approval.wrong_codes"

// Settings persists the rolling 24 h wrong-code window in the shared
// settings table (migration 10), so it survives a daemon restart
// (Docs/protocol/approval.md §Object).
type Settings struct{ db *sql.DB }

// NewSettings wraps db.
func NewSettings(db *sql.DB) *Settings { return &Settings{db: db} }

// wrongCodes returns the stored timestamps, pruned to the rolling window as
// of now, without writing anything back.
func (s *Settings) wrongCodes(ctx context.Context, now time.Time) ([]time.Time, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, keyWrongCodes).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("approval: read wrong codes: %w", err)
	}
	var stored []string
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, fmt.Errorf("approval: decode wrong codes: %w", err)
	}
	cutoff := now.Add(-WrongCodeWindow)
	out := make([]time.Time, 0, len(stored))
	for _, ts := range stored {
		t, err := time.Parse(storeTimeFmt, ts)
		if err != nil {
			continue
		}
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *Settings) save(ctx context.Context, times []time.Time, now time.Time) error {
	strs := make([]string, len(times))
	for i, t := range times {
		strs[i] = t.UTC().Format(storeTimeFmt)
	}
	raw, err := json.Marshal(strs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO settings (key, value, updated) VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated = excluded.updated`,
		keyWrongCodes, string(raw), now.UTC().Format(storeTimeFmt))
	if err != nil {
		return fmt.Errorf("approval: write wrong codes: %w", err)
	}
	return nil
}

// Locked reports whether the rolling window already holds MaxWrongPerDay
// wrong codes as of now.
func (s *Settings) Locked(ctx context.Context, now time.Time) (bool, error) {
	times, err := s.wrongCodes(ctx, now)
	if err != nil {
		return false, err
	}
	return len(times) >= MaxWrongPerDay, nil
}

// recordWrongCode appends now to the rolling window and reports whether this
// wrong code is the one that reaches MaxWrongPerDay (Docs/protocol/approval.md
// §Object).
func (s *Settings) recordWrongCode(ctx context.Context, now time.Time) (locked bool, err error) {
	times, err := s.wrongCodes(ctx, now)
	if err != nil {
		return false, err
	}
	times = append(times, now)
	if err := s.save(ctx, times, now); err != nil {
		return false, err
	}
	return len(times) >= MaxWrongPerDay, nil
}
