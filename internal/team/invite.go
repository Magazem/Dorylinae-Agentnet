package team

import (
	"context"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
)

// Team invite and join via pairing v2 (Docs/protocol/team.md §Operations:
// Invite, Join; Docs/review/11-phase1-tickets.md 1.1d): team_invite tags an
// owner's issuer pairing with InviteTag, and team_join tags a joiner's
// redeemer pairing with JoinTag. Both are peers.Completer: their Completed
// method runs once, when the underlying pairing v2 exchange ends.

// completionBudget bounds the database and outbox work a Completed hook does;
// it runs synchronously in the pairing manager's goroutine (like audit writes
// elsewhere in this package).
const completionBudget = 5 * time.Second

// InviteTag tags an owner's team_invite pairing (issuer role). On a
// completed pairing it writes the team_invites row (peer_key = the paired
// key) and audits team.invite. A failed pairing writes nothing.
type InviteTag struct {
	Store  *Store
	TeamID string
}

// Completed implements peers.Completer.
func (tag InviteTag) Completed(info peers.CompletionInfo) {
	if info.State != peers.StateComplete || info.Peer == nil || info.Lookup == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), completionBudget)
	defer cancel()
	s := tag.Store
	if err := s.RecordInvite(ctx, info.Lookup, tag.TeamID, info.PairingID, info.Peer.PublicKey, s.now()); err != nil {
		s.log().Error("team: record invite failed", "event", "team_error", "error", err)
		return
	}
	s.audited(ctx, ActorCLI, ActionInvite, map[string]any{"team": tag.TeamID, "pairing_id": info.PairingID})
}

// JoinTag tags a joiner's team_join pairing (redeemer role). On a completed
// pairing it writes the team_pending_joins row and submits team.join{lookup}
// to the owner (info.Peer, the paired key), then audits team.join. A failed
// pairing writes nothing.
type JoinTag struct {
	Store *Store
}

// Completed implements peers.Completer.
func (tag JoinTag) Completed(info peers.CompletionInfo) {
	if info.State != peers.StateComplete || info.Peer == nil || info.Lookup == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), completionBudget)
	defer cancel()
	s := tag.Store
	owner := info.Peer.PublicKey
	if err := s.RecordPendingJoin(ctx, owner, info.Lookup, s.now()); err != nil {
		s.log().Error("team: record pending join failed", "event", "team_error", "error", err)
		return
	}
	if s.Outbox == nil {
		return
	}
	if _, err := s.Outbox.Submit(ctx, owner, "team.join", map[string]any{"lookup": info.Lookup}); err != nil {
		s.log().Error("team: submit team.join failed", "event", "team_error", "error", err)
		return
	}
	s.audited(ctx, ActorCLI, ActionJoinSent, map[string]any{"pairing_id": info.PairingID, "owner": owner})
}
