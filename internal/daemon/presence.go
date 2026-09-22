package daemon

import (
	"context"
	"encoding/json"

	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/presence"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// PresenceGetResult is the result of "presence_get", Docs/cli/presence.md.
type PresenceGetResult struct {
	Mode       string   `json:"mode"`
	Team       *TeamRef `json:"team,omitempty"`
	HumanShare bool     `json:"human_share"`
}

// PresenceSetParams are the params of "presence_set", Docs/cli/presence.md:
// exactly one of Visible/Invisible/OnlyTeam may be set (a usage error
// otherwise), and Human may be combined with any of them, or sent alone.
type PresenceSetParams struct {
	Visible   bool    `json:"visible,omitempty"`
	Invisible bool    `json:"invisible,omitempty"`
	OnlyTeam  string  `json:"only_team,omitempty"`
	Human     *string `json:"human,omitempty"` // "on" or "off"
}

func registerPresence(srv *ipc.Server, sender *presence.Sender, ts *team.Store) {
	srv.Handle("presence_get", func(ctx context.Context, _ json.RawMessage) (any, error) {
		return presenceGetResult(ctx, sender, ts)
	})

	srv.Handle("presence_set", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p PresenceSetParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
			}
		}
		modeCount := 0
		if p.Visible {
			modeCount++
		}
		if p.Invisible {
			modeCount++
		}
		if p.OnlyTeam != "" {
			modeCount++
		}
		if modeCount > 1 {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "--visible, --invisible and --only-team are mutually exclusive"}
		}

		if modeCount == 1 {
			var vm presence.VisibilityMode
			switch {
			case p.Visible:
				vm = presence.VisibilityMode{Mode: presence.ModeVisible}
			case p.Invisible:
				vm = presence.VisibilityMode{Mode: presence.ModeInvisible}
			case p.OnlyTeam != "":
				t, err := resolveTeam(ctx, ts, p.OnlyTeam)
				if err != nil {
					return nil, err
				}
				if err := requireActive(t); err != nil {
					return nil, err
				}
				vm = presence.VisibilityMode{Mode: presence.ModeOnlyTeam, Team: t.ID}
			}
			if err := sender.SetMode(ctx, vm); err != nil {
				return nil, err
			}
		}

		if p.Human != nil {
			switch *p.Human {
			case "on":
				if err := sender.SetHumanShare(ctx, true); err != nil {
					return nil, err
				}
			case "off":
				if err := sender.SetHumanShare(ctx, false); err != nil {
					return nil, err
				}
			default:
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: `human must be "on" or "off"`}
			}
		}

		return presenceGetResult(ctx, sender, ts)
	})
}

func presenceGetResult(ctx context.Context, sender *presence.Sender, ts *team.Store) (PresenceGetResult, error) {
	vm := sender.Mode()
	res := PresenceGetResult{Mode: vm.Mode, HumanShare: sender.HumanShare()}
	if vm.Mode == presence.ModeOnlyTeam {
		if t, err := ts.Get(ctx, vm.Team); err == nil {
			res.Team = &TeamRef{ID: t.ID, Name: t.Name}
		} else {
			res.Team = &TeamRef{ID: vm.Team}
		}
	}
	return res, nil
}
