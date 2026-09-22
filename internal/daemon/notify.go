package daemon

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/notify"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// NotifyGetResult is the result of "notify_get" and the trailing part of
// "notify_set" (Docs/protocol/ipc.md §Notifications). Webhook is always null:
// the webhook channel is ticket 1.8b, not implemented here.
type NotifyGetResult struct {
	Desktop bool            `json:"desktop"`
	Events  map[string]bool `json:"events"`
	Webhook any             `json:"webhook"`
}

// NotifySetParams are the params of "notify_set" this ticket implements:
// desktop and events. Webhook params are ticket 1.8b's.
type NotifySetParams struct {
	Desktop *bool           `json:"desktop,omitempty"`
	Events  map[string]bool `json:"events,omitempty"`
}

// NotifyTestResult is the result of "notify_test".
type NotifyTestResult struct {
	Desktop string `json:"desktop"` // "shown", "failed" or "disabled"
	Webhook string `json:"webhook"` // always "none" until 1.8b
}

// notifyAdapter turns a request.NotifyFunc call into a notify.Trigger.Fire
// call, resolving the peer's local name (Docs/protocol/notify.md §Text and
// sanitising, "(from) is the local peer name").
func notifyAdapter(t *notify.Trigger, ps *peers.Store) request.NotifyFunc {
	if t == nil {
		return nil
	}
	return func(ctx context.Context, event string, info request.NotifyInfo) {
		name := info.Peer
		if ps != nil {
			if p, err := ps.List(ctx); err == nil {
				for _, peer := range p {
					if peer.PublicKey == info.Peer {
						name = peer.Name
						break
					}
				}
			}
		}
		t.Fire(ctx, notify.Event{
			Kind: event, PeerName: name, Type: info.Type, Urgency: info.Urgency, Title: info.Title,
			Until: info.Until, ResultStatus: info.ResultStatus, HasResult: info.HasResult,
		})
	}
}

// registerNotify wires "notify_get", "notify_set" and "notify_test"
// (Docs/protocol/ipc.md §Notifications, Docs/cli/notify.md).
func registerNotify(srv *ipc.Server, settings *notify.Settings, desktop notify.Desktop) {
	srv.Handle("notify_get", func(ctx context.Context, _ json.RawMessage) (any, error) {
		return notifyGetResult(ctx, settings)
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
		return notifyGetResult(ctx, settings)
	})

	srv.Handle("notify_test", func(ctx context.Context, _ json.RawMessage) (any, error) {
		enabled, err := settings.GetDesktopEnabled(ctx)
		if err != nil {
			return nil, err
		}
		res := NotifyTestResult{Webhook: "none"}
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

func notifyGetResult(ctx context.Context, settings *notify.Settings) (NotifyGetResult, error) {
	enabled, err := settings.GetDesktopEnabled(ctx)
	if err != nil {
		return NotifyGetResult{}, err
	}
	events, err := settings.GetEvents(ctx)
	if err != nil {
		return NotifyGetResult{}, err
	}
	return NotifyGetResult{Desktop: enabled, Events: events, Webhook: nil}, nil
}
