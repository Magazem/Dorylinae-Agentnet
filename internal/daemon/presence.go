package daemon

import (
	"bytes"
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

// PresenceSetParams are the params of "presence_set", Docs/protocol/ipc.md
// (Presence): Mode is visible, invisible or only_team and may be omitted when
// only HumanShare is set; Team is required with only_team and forbidden
// otherwise. Unknown fields are refused.
type PresenceSetParams struct {
	Mode       string `json:"mode,omitempty"`
	Team       string `json:"team,omitempty"`
	HumanShare *bool  `json:"human_share,omitempty"`
}

func registerPresence(srv *ipc.Server, sender *presence.Sender, ts *team.Store) {
	srv.Handle("presence_get", func(ctx context.Context, _ json.RawMessage) (any, error) {
		return presenceGetResult(ctx, sender, ts)
	})

	srv.Handle("presence_set", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p PresenceSetParams
		if len(params) > 0 {
			dec := json.NewDecoder(bytes.NewReader(params))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&p); err != nil {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params: " + err.Error()}
			}
		}
		if p.Mode == "" && p.HumanShare == nil {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "presence_set needs mode or human_share"}
		}
		var vm presence.VisibilityMode
		switch p.Mode {
		case "":
			if p.Team != "" {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "team is only allowed with mode only_team"}
			}
		case presence.ModeVisible, presence.ModeInvisible:
			if p.Team != "" {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "team is only allowed with mode only_team"}
			}
			vm = presence.VisibilityMode{Mode: p.Mode}
		case presence.ModeOnlyTeam:
			if p.Team == "" {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "mode only_team needs team"}
			}
			t, err := resolveTeam(ctx, ts, p.Team)
			if err != nil {
				return nil, err
			}
			if err := requireActive(t); err != nil {
				return nil, err
			}
			vm = presence.VisibilityMode{Mode: presence.ModeOnlyTeam, Team: t.ID}
		default:
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: `mode must be "visible", "invisible" or "only_team"`}
		}

		if vm.Mode != "" {
			if err := sender.SetMode(ctx, vm); err != nil {
				return nil, err
			}
		}
		if p.HumanShare != nil {
			if err := sender.SetHumanShare(ctx, *p.HumanShare); err != nil {
				return nil, err
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
