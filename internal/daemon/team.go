package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/approvaltext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
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

// TeamInviteParams are the params of "team_invite". Approval is empty on the
// first call, which creates the team_invite approval (D48, R55-084); the
// caller repeats the call with that approval id until the human has decided.
type TeamInviteParams struct {
	Team     string `json:"team"`
	Approval string `json:"approval,omitempty"`
}

// TeamInviteResult is the result of "team_invite": while Approval is set the
// human has not approved yet and there is no code (the pairing status is
// zero); after approval a pairing status (PairStatus, embedded so its fields
// sit at the top level) plus the invited team.
type TeamInviteResult struct {
	PairStatus
	Team     teamRef        `json:"team"`
	Approval *approval.View `json:"approval,omitempty"`
}

// inviteGateTTL bounds how long an approved-but-unused invite approval is
// remembered (the approval itself lives approval.TTL).
const inviteGateTTL = 2 * approval.TTL

// inviteGates remembers the team_invite approvals this daemon created, keyed
// by approval id: only an approval created here, for that team, and not yet
// used, releases an invite code. The map is in memory only: a restart makes
// the caller ask again.
type inviteGates struct {
	mu sync.Mutex
	m  map[string]inviteGate
}

type inviteGate struct {
	team    string
	created time.Time
}

func (g *inviteGates) put(id, team string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.m == nil {
		g.m = map[string]inviteGate{}
	}
	now := time.Now()
	for k, v := range g.m {
		if now.Sub(v.created) > inviteGateTTL {
			delete(g.m, k)
		}
	}
	g.m[id] = inviteGate{team: team, created: now}
}

// has reports whether id is a live gate for team.
func (g *inviteGates) has(id, team string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	v, ok := g.m[id]
	return ok && v.team == team && time.Since(v.created) <= inviteGateTTL
}

// take spends the gate for id and team: a compare-and-delete under the mutex,
// so of any number of concurrent callers exactly one gets true (review 79 H1).
func (g *inviteGates) take(id, team string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	v, ok := g.m[id]
	if !ok || v.team != team || time.Since(v.created) > inviteGateTTL {
		return false
	}
	delete(g.m, id)
	return true
}

func (g *inviteGates) drop(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.m, id)
}

// teamInviteFacts reads the facts of a team_invite approval through q (the
// confirm transaction at confirm time): the team must be active and owned by
// self.
func teamInviteFacts(ctx context.Context, q factQuerier, self, teamID string) (approvaltext.TeamInvite, error) {
	var name, owner, state string
	err := q.QueryRowContext(ctx, `SELECT name, owner, state FROM teams WHERE id = ?`, teamID).Scan(&name, &owner, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return approvaltext.TeamInvite{}, &ipc.Error{Code: CodeUnknownTeam, Message: "no such team"}
	}
	if err != nil {
		return approvaltext.TeamInvite{}, fmt.Errorf("approval summary: read team: %w", err)
	}
	if owner != self {
		return approvaltext.TeamInvite{}, &ipc.Error{Code: CodeNotOwner, Message: "only the team owner can do this"}
	}
	if state != team.StateActive {
		return approvaltext.TeamInvite{}, &ipc.Error{Code: CodeTeamInactive, Message: fmt.Sprintf("team %s is %s", teamID, state)}
	}
	return approvaltext.TeamInvite{TeamID: teamID, TeamName: name}, nil
}

// TeamJoinParams are the params of "team_join".
type TeamJoinParams struct {
	Code string `json:"code"`
}

func registerTeam(srv *ipc.Server, ts *team.Store, ps *peers.Store, pairs *peers.Manager, log *audit.Log, selfName string, apprStore *approval.Store) {
	gates := &inviteGates{}
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
		ref := teamRef{ID: t.ID, Name: t.Name}
		if p.Approval == "" {
			// A prompt-injected owner's agent could enrol a stranger every
			// teammate's daemon then trusts (R55-084), so the code exists only
			// after a human approves (D48).
			facts := approvaltext.TeamInvite{TeamID: t.ID, TeamName: t.Name}
			summary, err := approvaltext.BuildTeamInvite(facts)
			if err != nil {
				return nil, summaryField(err, "team")
			}
			var approvalID string
			var idMu sync.Mutex
			action := approval.Action{
				Precondition: func(ctx context.Context, tx *sql.Tx) error {
					_, err := teamInviteFacts(ctx, tx, ts.Self, t.ID)
					return err
				},
				Rebuild: rebuildWith(facts, func(ctx context.Context, tx *sql.Tx) (approvaltext.TeamInvite, error) {
					return teamInviteFacts(ctx, tx, ts.Self, t.ID)
				}, approvaltext.BuildTeamInvite),
				OnReject: func(context.Context) {
					idMu.Lock()
					id := approvalID
					idMu.Unlock()
					gates.drop(id)
				},
			}
			view, aerr := apprStore.Create(ctx, approval.KindTeamInvite, t.ID, summary, action)
			if aerr != nil {
				return nil, approvalError(aerr)
			}
			idMu.Lock()
			approvalID = view.ID
			idMu.Unlock()
			gates.put(view.ID, t.ID)
			return TeamInviteResult{Team: ref, Approval: &view}, nil
		}
		if !gates.has(p.Approval, t.ID) {
			return nil, &ipc.Error{Code: CodeBadState, Message: "no such pending invite approval for this team (start again with 'agentnet team invite')"}
		}
		view, err := apprStore.Show(ctx, p.Approval)
		if err != nil {
			gates.drop(p.Approval)
			return nil, approvalError(err)
		}
		switch view.State {
		case approval.StatePending:
			return TeamInviteResult{Team: ref, Approval: &view}, nil
		case approval.StateApproved:
		default:
			gates.drop(p.Approval)
			return nil, &ipc.Error{Code: CodeBadState, Message: "the invite was not approved (" + view.State + ")"}
		}
		// One approval releases at most one code: the id is spent atomically
		// before the pairing starts, so concurrent callers cannot each start
		// one. If StartTagged then fails the id stays spent (no code exists
		// to collect) and the caller asks for a new approval.
		if !gates.take(p.Approval, t.ID) {
			return nil, &ipc.Error{Code: CodeBadState, Message: "no such pending invite approval for this team (start again with 'agentnet team invite')"}
		}
		st, err := pairs.StartTagged(ctx, team.InviteTag{Store: ts, TeamID: t.ID})
		if err != nil {
			return nil, pairError(err)
		}
		// The release is audited with ids only, never the code.
		if err := log.Append(ctx, audit.ActorCLI, team.ActionInviteIssued, map[string]any{"team": t.ID, "approval": p.Approval, "pairing_id": st.ID}); err != nil {
			return nil, err
		}
		return TeamInviteResult{PairStatus: st, Team: ref}, nil
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
		// The team.leave mail is queued in the same transaction as the leave
		// (R55-113): a failure leaves the team active, so the call can be retried.
		nt, _, err := ts.LeaveNotify(ctx, t.ID, time.Now())
		if err != nil {
			return nil, teamError(err)
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

// A signature drift in mail.Outbox must not silently switch team leave to the
// non-atomic fallback (review 79 L3).
var _ team.TxOutbox = (*mail.Outbox)(nil)
