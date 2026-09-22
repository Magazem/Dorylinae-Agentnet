package presence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Visibility modes, Docs/protocol/presence.md §Visibility.
const (
	ModeVisible   = "visible"
	ModeInvisible = "invisible"
	ModeOnlyTeam  = "only_team"
)

// Audit actions, Docs/protocol/presence.md §Audit.
const (
	ActionMode  = "presence.mode"
	ActionHuman = "presence.human"
)

// VisibilityMode is the stored presence.mode setting.
type VisibilityMode struct {
	Mode string // ModeVisible, ModeInvisible or ModeOnlyTeam
	Team string // team id, set only when Mode == ModeOnlyTeam
}

// Settings persists presence.mode and presence.human under the settings
// table (migration 10), Docs/protocol/presence.md §Visibility and §Human sharing.
type Settings struct{ db *sql.DB }

// NewSettings wraps db.
func NewSettings(db *sql.DB) *Settings { return &Settings{db: db} }

type modeJSON struct {
	Mode string `json:"mode"`
	Team string `json:"team,omitempty"`
}

// GetMode returns the stored mode, defaulting to ModeVisible when unset.
func (s *Settings) GetMode(ctx context.Context) (VisibilityMode, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'presence.mode'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return VisibilityMode{Mode: ModeVisible}, nil
	}
	if err != nil {
		return VisibilityMode{}, fmt.Errorf("presence: read mode: %w", err)
	}
	var m modeJSON
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return VisibilityMode{}, fmt.Errorf("presence: decode mode: %w", err)
	}
	return VisibilityMode(m), nil
}

// SetMode stores mode.
func (s *Settings) SetMode(ctx context.Context, mode VisibilityMode, now time.Time) error {
	raw, err := json.Marshal(modeJSON(mode))
	if err != nil {
		return err
	}
	if err := s.upsert(ctx, "presence.mode", string(raw), now); err != nil {
		return fmt.Errorf("presence: write mode: %w", err)
	}
	return nil
}

type humanJSON struct {
	Share bool `json:"share"`
}

// GetHumanShare returns the stored presence.human share flag, default true
// (Docs/protocol/presence.md §Human sharing).
func (s *Settings) GetHumanShare(ctx context.Context) (bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'presence.human'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("presence: read human: %w", err)
	}
	var h humanJSON
	if err := json.Unmarshal([]byte(raw), &h); err != nil {
		return false, fmt.Errorf("presence: decode human: %w", err)
	}
	return h.Share, nil
}

// SetHumanShare stores the presence.human share flag.
func (s *Settings) SetHumanShare(ctx context.Context, share bool, now time.Time) error {
	raw, err := json.Marshal(humanJSON{Share: share})
	if err != nil {
		return err
	}
	if err := s.upsert(ctx, "presence.human", string(raw), now); err != nil {
		return fmt.Errorf("presence: write human: %w", err)
	}
	return nil
}

func (s *Settings) upsert(ctx context.Context, key, value string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO settings (key, value, updated) VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated = excluded.updated`,
		key, value, now.UTC().Format(storeTimeFmt))
	return err
}
