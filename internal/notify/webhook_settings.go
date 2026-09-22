package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// keyWebhook is the settings-table key for notify.webhook (migration 10).
const keyWebhook = "notify.webhook"

// Webhook formats (Docs/protocol/notify.md §Webhook).
const (
	FormatGeneric = "generic"
	FormatSlack   = "slack"
	FormatDiscord = "discord"
)

// ValidFormat reports whether format is one of the three known formats.
func ValidFormat(format string) bool {
	switch format {
	case FormatGeneric, FormatSlack, FormatDiscord:
		return true
	default:
		return false
	}
}

// WebhookConfig is the stored notify.webhook setting
// (Docs/protocol/notify.md §Configuration). The secret is never in it: it
// lives in the keystore.
type WebhookConfig struct {
	URL    string `json:"url"`
	Format string `json:"format"`
	Title  bool   `json:"title"`
}

// GetWebhook returns the stored webhook config, and ok=false when no webhook
// is set.
func (s *Settings) GetWebhook(ctx context.Context) (cfg WebhookConfig, ok bool, err error) {
	raw, present, err := s.get(ctx, keyWebhook)
	if err != nil {
		return WebhookConfig{}, false, err
	}
	if !present {
		return WebhookConfig{}, false, nil
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return WebhookConfig{}, false, fmt.Errorf("notify: decode webhook setting: %w", err)
	}
	if cfg.URL == "" {
		return WebhookConfig{}, false, nil
	}
	return cfg, true, nil
}

// SetWebhook stores cfg, or clears it when cfg.URL is empty.
func (s *Settings) SetWebhook(ctx context.Context, cfg WebhookConfig, now time.Time) error {
	if cfg.URL == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, keyWebhook)
		if err != nil {
			return fmt.Errorf("notify: clear webhook setting: %w", err)
		}
		return nil
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.upsert(ctx, keyWebhook, string(raw), now)
}
