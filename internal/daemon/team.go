package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// IPC error codes for teams, documented in Docs/protocol/ipc.md.
const (
	CodeUnknownTeam      = "unknown_team"
	CodeAmbiguousTeam    = "ambiguous_team"
	CodeTeamExists       = "team_exists"
	CodeBadTeamName      = "bad_team_name"
	CodeNotOwner         = "not_owner"
	CodeOwnerCannotLeave = "owner_cannot_leave"
	CodeNotMember        = "not_member"
	CodeTeamInactive     = "team_inactive"
	CodeTeamFull         = "team_full"
)

// TeamSummary is the "team summary" common object of Docs/protocol/ipc.md
// §Phase 1 methods.
type TeamSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Owner   string `json:"owner"`
	Epoch   int64  `json:"epoch"`
	State   string `json:"state"`
	Role    string `json:"role"`
	Members int    `json:"members"`
}

// TeamMemberView is one entry of "team_show"'s member list: a peer ref plus
// "added", "owner" and "self".
type TeamMemberView struct {
	Name        string `json:"name"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	Added       string `json:"added"`
	Owner       bool   `json:"owner"`
	Self        bool   `json:"self"`
}

// TeamShowSummary is the team summary of "team_show", with "members" replaced
// by the member list.
type TeamShowSummary struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Owner   string           `json:"owner"`
	Epoch   int64            `json:"epoch"`
	State   string           `json:"state"`
	Role    string           `json:"role"`
	Members []TeamMemberView `json:"members"`
}

// TeamResult is the result of "team_create", "team_remove", "team_rename",
// "team_leave" and "team_delete".
type TeamResult struct {
	Team TeamSummary `json:"team"`
}

// TeamListResult is the result of "team_list".
type TeamListResult struct {
	Teams []TeamSummary `json:"teams"`
}

// TeamShowResult is the result of "team_show".
type TeamShowResult struct {
	Team TeamShowSummary `json:"team"`
}

// TeamCreateParams are the params of "team_create".
type TeamCreateParams struct {
	Name string `json:"name"`
}

// TeamListParams are the params of "team_list".
type TeamListParams struct {
	All bool `json:"all,omitempty"`
}

// TeamRefParams are the params of "team_show", "team_leave" and "team_delete".
type TeamRefParams struct {
	Team string `json:"team"`
}

// TeamRemoveParams are the params of "team_remove".
type TeamRemoveParams struct {
	Team string `json:"team"`
	Peer string `json:"peer"`
}

// TeamRenameParams are the params of "team_rename".
type TeamRenameParams struct {
	Team string `json:"team"`
	Name string `json:"name"`
}

// teamRef is the "team": {"id","name"} member of the "team_invite" result.
type teamRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TeamInviteParams are the params of "team_invite".
type TeamInviteParams struct {
	Team string `json:"team"`
}

// TeamInviteResult is the result of "team_invite": a pairing status
// (PairStatus, embedded so its fields sit at the top level) plus the invited team.
type TeamInviteResult struct {
	PairStatus
	Team teamRef `json:"team"`
}

// TeamJoinParams are the params of "team_join".
type TeamJoinParams struct {
	Code string `json:"code"`
}

func registerTeam(srv *ipc.Server, ts *team.Store, ps *peers.Store, pairs *peers.Manager, log *audit.Log, selfName string) {
	srv.Handle("team_invite", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p TeamInviteParams
		if err := json.Unmarshal(params, &p); err != nil || p.Team == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "team is required"}
		}
		t, err := resolveTeam(ctx, ts, p.Team)
		if err != nil {
			return nil, err
		}
		if t.Owner != ts.Self {
			return nil, &ipc.Error{Code: CodeNotOwner, Message: "only the team owner can do this"}
		}
		if err := requireActive(t); err != nil {
			return nil, err
		}
		members, err := ts.Members(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		if len(members) >= team.MaxMembers {
			return nil, &ipc.Error{Code: CodeTeamFull, Message: "the team already has 32 members"}
		}
		st, err := pairs.StartTagged(ctx, team.InviteTag{Store: ts, TeamID: t.ID})
		if err != nil {
			return nil, pairError(err)
		}
		return TeamInviteResult{PairStatus: st, Team: teamRef{ID: t.ID, Name: t.Name}}, nil
	})

	srv.Handle("team_join", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p TeamJoinParams
		if err := json.Unmarshal(params, &p); err != nil || p.Code == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "code is required"}
		}
		st, err := pairs.RedeemTagged(ctx, p.Code, false, team.JoinTag{Store: ts})
		if err != nil {
			return nil, pairError(err)
		}
		return st, nil
	})
	srv.Handle("team_create", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p TeamCreateParams
		if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "name is required"}
		}
		if !team.ValidName(p.Name) {
			return nil, &ipc.Error{Code: CodeBadTeamName, Message: "team names match ^[a-z0-9][a-z0-9-]{0,31}$"}
		}
		t, err := ts.Create(ctx, p.Name, time.Now())
		if err != nil {
			return nil, teamError(err)
		}
		return TeamResult{Team: teamSummary(ts, t, 1)}, nil
	})

	srv.Handle("team_list", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p TeamListParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "malformed params"}
			}
		}
		list, err := ts.List(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]TeamSummary, 0, len(list))
		for _, t := range list {
			if !p.All && t.State != team.StateActive {
				continue
			}
			members, err := ts.Members(ctx, t.ID)
			if err != nil {
				return nil, err
			}
			out = append(out, teamSummary(ts, t, len(members)))
		}
		return TeamListResult{Teams: out}, nil
	})

	srv.Handle("team_show", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p TeamRefParams
		if err := json.Unmarshal(params, &p); err != nil || p.Team == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "team is required"}
		}
		t, err := resolveTeam(ctx, ts, p.Team)
		if err != nil {
			return nil, err
		}
		members, err := ts.Members(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		peerList, err := ps.List(ctx)
		if err != nil {
			return nil, err
		}
		byKey := make(map[string]peers.Peer, len(peerList))
		for _, pr := range peerList {
			byKey[pr.PublicKey] = pr
		}
		views := make([]TeamMemberView, 0, len(members))
		for _, m := range members {
			v := TeamMemberView{PublicKey: m.Key, Added: m.Added, Owner: m.Key == t.Owner, Self: m.Key == ts.Self}
			if m.Key == ts.Self {
				v.Name = selfName
			} else if pr, ok := byKey[m.Key]; ok {
				v.Name = pr.Name
			}
			fp, err := envelope.KeyFingerprint(m.Key)
			if err != nil {
				return nil, err
			}
			v.Fingerprint = fp
			views = append(views, v)
		}
		return TeamShowResult{Team: TeamShowSummary{
			ID: t.ID, Name: t.Name, Owner: t.Owner, Epoch: t.Epoch, State: t.State,
			Role: teamRole(ts, t), Members: views,
		}}, nil
	})

	srv.Handle("team_remove", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p TeamRemoveParams
		if err := json.Unmarshal(params, &p); err != nil || p.Team == "" || p.Peer == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "team and peer are required"}
		}
		t, err := resolveTeam(ctx, ts, p.Team)
		if err != nil {
			return nil, err
		}
		if err := requireActive(t); err != nil {
			return nil, err
		}
		// Ownership is checked before resolving the peer: self is never a
		// paired peer (resolvePeer would report unknown_peer), and a caller
		// who isn't the owner gets not_owner regardless of who they named.
		if t.Owner != ts.Self {
			return nil, &ipc.Error{Code: CodeNotOwner, Message: "only the team owner can do this"}
		}
		if trimSelfRef(p.Peer) == ts.Self {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "the owner cannot remove itself; use 'agentnet team delete'"}
		}
		peer, err := resolvePeer(ctx, ps, p.Peer)
		if err != nil {
			return nil, err
		}
		nt, err := removeMemberFromTeam(ctx, ts, log, t.ID, peer.PublicKey, time.Now())
		if err != nil {
			return nil, teamError(err)
		}
		members, err := ts.Members(ctx, nt.ID)
		if err != nil {
			return nil, err
		}
		return TeamResult{Team: teamSummary(ts, nt, len(members))}, nil
	})

	srv.Handle("team_rename", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p TeamRenameParams
		if err := json.Unmarshal(params, &p); err != nil || p.Team == "" || p.Name == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "team and name are required"}
		}
		if !team.ValidName(p.Name) {
			return nil, &ipc.Error{Code: CodeBadTeamName, Message: "team names match ^[a-z0-9][a-z0-9-]{0,31}$"}
		}
		t, err := resolveTeam(ctx, ts, p.Team)
		if err != nil {
			return nil, err
		}
		if err := requireActive(t); err != nil {
			return nil, err
		}
		nt, err := ts.Rename(ctx, t.ID, p.Name, time.Now())
		if err != nil {
			return nil, teamError(err)
		}
		if err := ts.Broadcast(ctx, nt.ID, nil); err != nil {
			return nil, err
		}
		members, err := ts.Members(ctx, nt.ID)
		if err != nil {
			return nil, err
		}
		return TeamResult{Team: teamSummary(ts, nt, len(members))}, nil
	})

	srv.Handle("team_leave", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p TeamRefParams
		if err := json.Unmarshal(params, &p); err != nil || p.Team == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "team is required"}
		}
		t, err := resolveTeam(ctx, ts, p.Team)
		if err != nil {
			return nil, err
		}
		if err := requireActive(t); err != nil {
			return nil, err
		}
		nt, _, err := ts.Leave(ctx, t.ID, time.Now())
		if err != nil {
			return nil, teamError(err)
		}
		if ts.Outbox != nil {
			if _, err := ts.Outbox.Submit(ctx, nt.Owner, "team.leave", map[string]any{"team": nt.ID}); err != nil {
				return nil, err
			}
		}
		members, err := ts.Members(ctx, nt.ID)
		if err != nil {
			return nil, err
		}
		return TeamResult{Team: teamSummary(ts, nt, len(members))}, nil
	})

	srv.Handle("team_delete", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p TeamRefParams
		if err := json.Unmarshal(params, &p); err != nil || p.Team == "" {
			return nil, &ipc.Error{Code: ipc.CodeBadRequest, Message: "team is required"}
		}
		t, err := resolveTeam(ctx, ts, p.Team)
		if err != nil {
			return nil, err
		}
		if err := requireActive(t); err != nil {
			return nil, err
		}
		nt, err := ts.Delete(ctx, t.ID, time.Now())
		if err != nil {
			return nil, teamError(err)
		}
		if err := ts.Broadcast(ctx, nt.ID, nil); err != nil {
			return nil, err
		}
		members, err := ts.Members(ctx, nt.ID)
		if err != nil {
			return nil, err
		}
		return TeamResult{Team: teamSummary(ts, nt, len(members))}, nil
	})
}

// removeMemberFromTeam removes key from teamID (already known to be owned by
// self), audits team.member_remove, and broadcasts the updated roster to the
// remaining members and to key itself.
func removeMemberFromTeam(ctx context.Context, ts *team.Store, log *audit.Log, teamID, key string, now time.Time) (team.Team, error) {
	nt, err := ts.RemoveMember(ctx, teamID, key, now)
	if err != nil {
		return team.Team{}, err
	}
	if err := log.Append(ctx, audit.ActorCLI, team.ActionMemberRemove, map[string]any{"team": nt.ID, "peer": key, "epoch": nt.Epoch}); err != nil {
		return team.Team{}, err
	}
	if err := ts.Broadcast(ctx, nt.ID, []string{key}); err != nil {
		return team.Team{}, err
	}
	return nt, nil
}

// cascadeTeamRemoval removes memberKey from every active team self owns that
// it currently belongs to, before the peer itself is deleted: a `peers
// remove` of a team member cascades like `team remove` for each such team
// (Docs/cli/peers.md §peers remove).
func cascadeTeamRemoval(ctx context.Context, ts *team.Store, log *audit.Log, memberKey string, now time.Time) error {
	teams, err := ts.OwnedTeamsWithMember(ctx, memberKey)
	if err != nil {
		return err
	}
	for _, t := range teams {
		if _, err := removeMemberFromTeam(ctx, ts, log, t.ID, memberKey, now); err != nil {
			return err
		}
	}
	return nil
}

// requireActive rejects a mutation of a team that is left, removed or dissolved.
func requireActive(t team.Team) error {
	if t.State != team.StateActive {
		return &ipc.Error{Code: CodeTeamInactive, Message: fmt.Sprintf("team %s is %s", t.ID, t.State)}
	}
	return nil
}

// trimSelfRef strips the optional leading "@" from a peer reference, as
// resolvePeer does, so it can be compared with a raw public key.
func trimSelfRef(ref string) string { return strings.TrimPrefix(ref, "@") }

func teamRole(ts *team.Store, t team.Team) string {
	if t.Owner == ts.Self {
		return "owner"
	}
	return "member"
}

func teamSummary(ts *team.Store, t team.Team, members int) TeamSummary {
	return TeamSummary{ID: t.ID, Name: t.Name, Owner: t.Owner, Epoch: t.Epoch, State: t.State, Role: teamRole(ts, t), Members: members}
}

// resolveTeam finds a team by id, or by a name that is unique among this
// daemon's active local teams (Docs/protocol/team.md §Local names).
func resolveTeam(ctx context.Context, ts *team.Store, ref string) (team.Team, error) {
	if team.ValidID(ref) {
		t, err := ts.Get(ctx, ref)
		if err != nil {
			return team.Team{}, teamError(err)
		}
		return t, nil
	}
	list, err := ts.List(ctx)
	if err != nil {
		return team.Team{}, err
	}
	var matches []team.Team
	for _, t := range list {
		if t.State == team.StateActive && t.Name == ref {
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return team.Team{}, &ipc.Error{Code: CodeUnknownTeam, Message: fmt.Sprintf("no such team %q (see 'agentnet team list')", ref)}
	default:
		ids := make([]string, len(matches))
		for i, t := range matches {
			ids[i] = t.ID
		}
		return team.Team{}, &ipc.Error{Code: CodeAmbiguousTeam, Message: fmt.Sprintf("several teams are named %q; use an id: %v", ref, ids)}
	}
}

func teamError(err error) error {
	switch {
	case errors.Is(err, team.ErrNotFound):
		return &ipc.Error{Code: CodeUnknownTeam, Message: "no such team (see 'agentnet team list')"}
	case errors.Is(err, team.ErrExists):
		return &ipc.Error{Code: CodeTeamExists, Message: "an active team already has this name"}
	case errors.Is(err, team.ErrNotOwner):
		return &ipc.Error{Code: CodeNotOwner, Message: "only the team owner can do this"}
	case errors.Is(err, team.ErrOwnerCannotLeave):
		return &ipc.Error{Code: CodeOwnerCannotLeave, Message: "the owner cannot leave; use 'agentnet team delete'"}
	case errors.Is(err, team.ErrNoSuchMember):
		return &ipc.Error{Code: CodeNotMember, Message: "that peer is not a member of the team"}
	case errors.Is(err, team.ErrFull):
		return &ipc.Error{Code: CodeTeamFull, Message: "the team already has 32 members"}
	default:
		return err
	}
}
