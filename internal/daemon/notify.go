package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/notify"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// NotifyWebhook is the "webhook" member of NotifyGetResult
// (Docs/protocol/ipc.md §Notifications), present only when a webhook is set.
type NotifyWebhook struct {
	URL      string `json:"url"`
	Format   string `json:"format"`
	Title    bool   `json:"title"`
	Pending  int    `json:"pending"`
	Failed7d int    `json:"failed_7d"`
}

// NotifyGetResult is the result of "notify_get" and the trailing part of
// "notify_set" (Docs/protocol/ipc.md §Notifications).
type NotifyGetResult struct {
	Desktop bool            `json:"desktop"`
	Events  map[string]bool `json:"events"`
	Webhook *NotifyWebhook  `json:"webhook"`
}

// NotifySetParams are the params of "notify_set" (Docs/protocol/ipc.md
// §Notifications). WebhookURL == "" (with WebhookURL present) removes the
// webhook.
type NotifySetParams struct {
	Desktop      *bool           `json:"desktop,omitempty"`
	Events       map[string]bool `json:"events,omitempty"`
	WebhookURL   *string         `json:"webhook_url,omitempty"`
	Format       *string         `json:"format,omitempty"`
	Title        *bool           `json:"title,omitempty"`
	RotateSecret bool            `json:"rotate_secret,omitempty"`
}

// NotifySetResult is NotifyGetResult plus the printed secret, present only
// when a secret was just created or rotated (Docs/protocol/ipc.md
// §Notifications).
type NotifySetResult struct {
	NotifyGetResult
	Secret string `json:"secret,omitempty"`
}

// NotifyTestResult is the result of "notify_test".
type NotifyTestResult struct {
	Desktop string `json:"desktop"` // "shown", "failed" or "disabled"
	Webhook string `json:"webhook"` // "queued" or "none"
}

// teamNamer is the part of team.Store the webhook payload needs.
type teamNamer interface {
	Get(ctx context.Context, id string) (team.Team, error)
}

// notifyAdapter turns a request.NotifyFunc call into a notify.Trigger.Fire
// call, resolving the peer's local name and fingerprint
// (Docs/protocol/notify.md §Text and sanitising, "(from) is the local peer
// name"; §Payload, peer.fingerprint) and the team name (§Payload, team.name).
func notifyAdapter(t *notify.Trigger, ps *peers.Store, ts teamNamer) request.NotifyFunc {
	if t == nil {
		return nil
	}
	return func(ctx context.Context, event string, info request.NotifyInfo) {
		name := info.Peer
		fp := ""
		if ps != nil {
			if p, err := ps.List(ctx); err == nil {
				for _, peer := range p {
					if peer.PublicKey == info.Peer {
						name = peer.Name
						fp = peer.Fingerprint
						break
					}
				}
			}
		}
		teamName := ""
		if info.TeamID != "" && ts != nil {
			if tm, err := ts.Get(ctx, info.TeamID); err == nil {
				teamName = tm.Name
			}
		}
		t.Fire(ctx, notify.Event{
			Kind: event, PeerName: name, PeerFP: fp, Type: info.Type, Urgency: info.Urgency, Title: info.Title,
			Until: info.Until, ResultStatus: info.ResultStatus, HasResult: info.HasResult,
			RequestID: info.RequestID, State: info.State, TeamID: info.TeamID, TeamName: teamName,
			CreatedAt: time.Now(),
		})
	}
}

// registerNotify wires "notify_get", "notify_set" and "notify_test"
// (Docs/protocol/ipc.md §Notifications, Docs/cli/notify.md).
func registerNotify(srv *ipc.Server, settings *notify.Settings, desktop notify.Desktop, wh *notify.Webhook, log notify.AuditSink) {
	srv.Handle("notify_get", func(ctx context.Context, _ json.RawMessage) (any, error) {
		return notifyGetResult(ctx, settings, wh)
	})

	srv.Handle("notify_set", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p NotifySetParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
			}
		}
		now := time.Now()
		if p.Desktop != nil {
			if err := settings.SetDesktopEnabled(ctx, *p.Desktop, now); err != nil {
				return nil, err
			}
		}
		for event, on := range p.Events {
			if !notify.ValidEvent(event) {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "unknown event " + event}
			}
			if err := settings.SetEvent(ctx, event, on, now); err != nil {
				return nil, err
			}
		}
		secret, err := applyWebhookSet(ctx, settings, wh, p, now)
		if err != nil {
			return nil, err
		}
		if detail := notifyConfigDetail(p); detail != nil && log != nil {
			_ = log.Append(ctx, audit.ActorCLI, "notify.config", detail)
		}
		res, err := notifyGetResult(ctx, settings, wh)
		if err != nil {
			return nil, err
		}
		return NotifySetResult{NotifyGetResult: res, Secret: secret}, nil
	})

	srv.Handle("notify_test", func(ctx context.Context, _ json.RawMessage) (any, error) {
		enabled, err := settings.GetDesktopEnabled(ctx)
		if err != nil {
			return nil, err
		}
		res := NotifyTestResult{Webhook: "none"}
		if wh != nil {
			queued, err := wh.EnqueueTest(ctx)
			if err != nil {
				return nil, err
			}
			if queued {
				res.Webhook = "queued"
			}
		}
		if !enabled {
			res.Desktop = "disabled"
			return res, nil
		}
		if err := desktop.Show(ctx, "AgentNet", "agentnet test notification"); err != nil {
			res.Desktop = "failed"
			return res, nil
		}
		res.Desktop = "shown"
		return res, nil
	})
}

// applyWebhookSet applies the webhook-related fields of a notify_set call,
// returning the printed secret when one was just created or rotated
// (Docs/protocol/notify.md §Configuration).
func applyWebhookSet(ctx context.Context, settings *notify.Settings, wh *notify.Webhook, p NotifySetParams, now time.Time) (secret string, err error) {
	if p.WebhookURL == nil && p.Format == nil && p.Title == nil && !p.RotateSecret {
		return "", nil
	}
	if wh == nil {
		return "", &ipc.Error{Code: ipc.CodeBadRequest, Message: "webhook channel unavailable"}
	}
	cfg, existed, err := settings.GetWebhook(ctx)
	if err != nil {
		return "", err
	}

	if p.WebhookURL != nil && *p.WebhookURL == "" {
		if err := settings.SetWebhook(ctx, notify.WebhookConfig{}, now); err != nil {
			return "", err
		}
		if err := wh.DeleteSecret(); err != nil {
			return "", err
		}
		if err := wh.Queue.FailAllPending(ctx, "removed", now); err != nil {
			return "", err
		}
		return "", nil
	}

	if p.WebhookURL != nil {
		if err := notify.ValidateWebhookURL(*p.WebhookURL); err != nil {
			return "", &ipc.Error{Code: "bad_webhook", Message: err.Error()}
		}
		cfg.URL = *p.WebhookURL
	}
	if p.Format != nil {
		if !notify.ValidFormat(*p.Format) {
			return "", &ipc.Error{Code: ipc.CodeBadRequest, Message: "unknown webhook format " + *p.Format}
		}
		cfg.Format = *p.Format
	}
	if cfg.Format == "" {
		cfg.Format = notify.FormatGeneric
	}
	if p.Title != nil {
		cfg.Title = *p.Title
	}
	if cfg.URL == "" {
		return "", &ipc.Error{Code: "bad_webhook", Message: "no webhook URL configured"}
	}

	firstSet := !existed && p.WebhookURL != nil
	if firstSet || p.RotateSecret {
		secret, err = wh.RotateSecret()
		if err != nil {
			return "", err
		}
	}
	if err := settings.SetWebhook(ctx, cfg, now); err != nil {
		return "", err
	}
	return secret, nil
}

// notifyConfigDetail is the notify.config audit detail for a successful
// notify_set, or nil when it changed nothing: {desktop?, events?, webhook:
// "set"|"removed"|"rotated"?, format?, title?}. The URL and the secret are
// never in it (Docs/protocol/notify.md §Configuration, §Audit).
func notifyConfigDetail(p NotifySetParams) map[string]any {
	d := map[string]any{}
	if p.Desktop != nil {
		d["desktop"] = *p.Desktop
	}
	if len(p.Events) > 0 {
		d["events"] = p.Events
	}
	switch {
	case p.WebhookURL != nil && *p.WebhookURL == "":
		d["webhook"] = "removed"
	case p.WebhookURL != nil:
		d["webhook"] = "set"
	case p.RotateSecret:
		d["webhook"] = "rotated"
	}
	if d["webhook"] != "removed" {
		if p.Format != nil {
			d["format"] = *p.Format
		}
		if p.Title != nil {
			d["title"] = *p.Title
		}
	}
	if len(d) == 0 {
		return nil
	}
	return d
}

func notifyGetResult(ctx context.Context, settings *notify.Settings, wh *notify.Webhook) (NotifyGetResult, error) {
	enabled, err := settings.GetDesktopEnabled(ctx)
	if err != nil {
		return NotifyGetResult{}, err
	}
	events, err := settings.GetEvents(ctx)
	if err != nil {
		return NotifyGetResult{}, err
	}
	res := NotifyGetResult{Desktop: enabled, Events: events}
	cfg, ok, err := settings.GetWebhook(ctx)
	if err != nil {
		return NotifyGetResult{}, err
	}
	if ok {
		w := &NotifyWebhook{URL: cfg.URL, Format: cfg.Format, Title: cfg.Title}
		if wh != nil {
			if pending, failed7d, err := wh.Queue.Counts(ctx, time.Now()); err == nil {
				w.Pending, w.Failed7d = pending, failed7d
			}
		}
		res.Webhook = w
	}
	return res, nil
}
