package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// storeTimeFmt matches internal/mail.StoreTimeFmt without importing mail.
const storeTimeFmt = "2006-01-02T15:04:05.000Z"

// Settings keys under the shared settings table (migration 10).
const (
	keyEvents  = "notify.events"
	keyDesktop = "notify.desktop"
)

// Events, Docs/protocol/notify.md §Triggers.
const (
	EventReceived  = "request.received"
	EventAccepted  = "request.accepted"
	EventDeclined  = "request.declined"
	EventDeferred  = "request.deferred"
	EventCompleted = "request.completed"
	EventCancelled = "request.cancelled"
)

// DefaultEvents is the default on/off state of each event
// (Docs/protocol/notify.md §Triggers).
var DefaultEvents = map[string]bool{
	EventReceived:  true,
	EventAccepted:  true,
	EventDeclined:  true,
	EventDeferred:  false,
	EventCompleted: false,
	EventCancelled: true,
}

// ValidEvent reports whether event is one of the six known events.
func ValidEvent(event string) bool {
	_, ok := DefaultEvents[event]
	return ok
}

// Settings persists notify.events and notify.desktop under the settings
// table (migration 10), Docs/protocol/notify.md §Triggers.
type Settings struct{ db *sql.DB }

// NewSettings wraps db.
func NewSettings(db *sql.DB) *Settings { return &Settings{db: db} }

// GetDesktopEnabled returns the stored notify.desktop.enabled flag, default true.
func (s *Settings) GetDesktopEnabled(ctx context.Context) (bool, error) {
	raw, ok, err := s.get(ctx, keyDesktop)
	if err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	var v struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return false, fmt.Errorf("notify: decode desktop setting: %w", err)
	}
	return v.Enabled, nil
}

// SetDesktopEnabled stores the notify.desktop.enabled flag.
func (s *Settings) SetDesktopEnabled(ctx context.Context, enabled bool, now time.Time) error {
	raw, err := json.Marshal(struct {
		Enabled bool `json:"enabled"`
	}{enabled})
	if err != nil {
		return err
	}
	return s.upsert(ctx, keyDesktop, string(raw), now)
}

// GetEvents returns the effective on/off state of every known event: the
// stored value where set, DefaultEvents otherwise.
func (s *Settings) GetEvents(ctx context.Context) (map[string]bool, error) {
	out := make(map[string]bool, len(DefaultEvents))
	for k, v := range DefaultEvents {
		out[k] = v
	}
	raw, ok, err := s.get(ctx, keyEvents)
	if err != nil {
		return nil, err
	}
	if !ok {
		return out, nil
	}
	var stored map[string]bool
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, fmt.Errorf("notify: decode events setting: %w", err)
	}
	for k, v := range stored {
		if ValidEvent(k) {
			out[k] = v
		}
	}
	return out, nil
}

// EventEnabled reports whether event is currently on.
func (s *Settings) EventEnabled(ctx context.Context, event string) (bool, error) {
	events, err := s.GetEvents(ctx)
	if err != nil {
		return false, err
	}
	return events[event], nil
}

// SetEvent stores the on/off state of one event, leaving the others untouched.
func (s *Settings) SetEvent(ctx context.Context, event string, on bool, now time.Time) error {
	if !ValidEvent(event) {
		return fmt.Errorf("notify: unknown event %q", event)
	}
	events, err := s.GetEvents(ctx)
	if err != nil {
		return err
	}
	events[event] = on
	raw, err := json.Marshal(events)
	if err != nil {
		return err
	}
	return s.upsert(ctx, keyEvents, string(raw), now)
}

func (s *Settings) get(ctx context.Context, key string) (value string, ok bool, err error) {
	var raw string
	err = s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("notify: read %s: %w", key, err)
	}
	return raw, true, nil
}

func (s *Settings) upsert(ctx context.Context, key, value string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO settings (key, value, updated) VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated = excluded.updated`,
		key, value, now.UTC().Format(storeTimeFmt))
	if err != nil {
		return fmt.Errorf("notify: write %s: %w", key, err)
	}
	return nil
}
