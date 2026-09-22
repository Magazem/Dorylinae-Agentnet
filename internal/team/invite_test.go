package team_test

// Ticket 1.1d: team invite and join via pairing v2
// (Docs/review/11-phase1-tickets.md, 1.1d; Docs/protocol/team.md §Operations).
//
// The pairing v2 exchange itself is internal/peers' concern (tested there);
// here the pairing manager's completion hook is simulated by calling
// team.InviteTag/team.JoinTag.Completed directly with a peers.CompletionInfo,
// exactly as internal/daemon's team_invite/team_join handlers would receive
// it from the tagged pairing session.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

func TestInviteTagWritesInviteRowAndAudits(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := newTestNode(t, "owner")
	tm, err := owner.ts.Create(context.Background(), "x", now)
	if err != nil {
		t.Fatal(err)
	}

	tag := team.InviteTag{Store: owner.ts, TeamID: tm.ID}
	tag.Completed(peers.CompletionInfo{
		PairingID: "pair-0123456789abcdef",
		Role:      peers.RoleIssuer,
		Lookup:    "ABCDE",
		State:     peers.StateComplete,
		Peer:      &peers.Peer{PublicKey: "peer-key-1"},
	})

	var teamID, pairingID, peerKey string
	var used sql.NullString
	if err := owner.db.QueryRow(`SELECT team_id, pairing_id, peer_key, used FROM team_invites WHERE lookup = 'ABCDE'`).
		Scan(&teamID, &pairingID, &peerKey, &used); err != nil {
		t.Fatal(err)
	}
	if teamID != tm.ID || pairingID != "pair-0123456789abcdef" || peerKey != "peer-key-1" || used.Valid {
		t.Fatalf("team_invites row = %q %q %q used=%v, want %q %q %q unused", teamID, pairingID, peerKey, used, tm.ID, "pair-0123456789abcdef", "peer-key-1")
	}
	if !contains(auditActions(t, owner), team.ActionInvite) {
		t.Errorf("audit actions = %v, want %s", auditActions(t, owner), team.ActionInvite)
	}
}

func TestInviteTagIgnoresFailedPairing(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := newTestNode(t, "owner")
	tm, err := owner.ts.Create(context.Background(), "x", now)
	if err != nil {
		t.Fatal(err)
	}
	team.InviteTag{Store: owner.ts, TeamID: tm.ID}.Completed(peers.CompletionInfo{
		PairingID: "pair-fail", Role: peers.RoleIssuer, Lookup: "ABCDE", State: peers.StateFailed,
	})
	var n int
	if err := owner.db.QueryRow(`SELECT COUNT(*) FROM team_invites`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("team_invites rows = %d, want 0 after a failed pairing", n)
	}
}

func TestJoinTagWritesPendingJoinSendsJoinAndCompletesMembership(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := newTestNode(t, "owner")
	joiner := newTestNode(t, "joiner")
	pairWith(t, owner, joiner, "code") // the pairing v2 exchange itself: trust=code both ways
	newNetwork(t, owner, joiner)

	tm, err := owner.ts.Create(context.Background(), "x", now)
	if err != nil {
		t.Fatal(err)
	}
	// The invite side: the owner's team_invite completed first, so the lookup
	// is already recorded against this team (Docs/protocol/team.md §`team.join`).
	team.InviteTag{Store: owner.ts, TeamID: tm.ID}.Completed(peers.CompletionInfo{
		PairingID: "pair-owner", Role: peers.RoleIssuer, Lookup: "ABCDE", State: peers.StateComplete,
		Peer: &peers.Peer{PublicKey: joiner.key},
	})

	team.JoinTag{Store: joiner.ts}.Completed(peers.CompletionInfo{
		PairingID: "pair-joiner", Role: peers.RoleRedeemer, Lookup: "ABCDE", State: peers.StateComplete,
		Peer: &peers.Peer{PublicKey: owner.key},
	})

	if !contains(auditActions(t, joiner), team.ActionJoinSent) {
		t.Errorf("joiner audit actions = %v, want %s", auditActions(t, joiner), team.ActionJoinSent)
	}
	// newNetwork's routedOutbox delivers everything synchronously: by the time
	// Completed returns, the owner has added the joiner and broadcast the
	// roster back, which the joiner applied, consuming its pending join
	// (Docs/protocol/team.md §`team.roster` rule 4: unknown team, live pending join).
	var n int
	if err := joiner.db.QueryRow(`SELECT COUNT(*) FROM team_pending_joins WHERE owner_key = ? AND lookup = 'ABCDE'`, owner.key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("team_pending_joins rows on joiner = %d, want 0 (consumed by the roster)", n)
	}
	members, err := joiner.ts.Members(context.Background(), tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("joiner's view of team x has %d members, want 2 (owner, joiner)", len(members))
	}
}

func TestJoinTagIgnoresFailedPairing(t *testing.T) {
	joiner := newTestNode(t, "joiner")
	team.JoinTag{Store: joiner.ts}.Completed(peers.CompletionInfo{
		PairingID: "pair-fail", Role: peers.RoleRedeemer, Lookup: "ABCDE", State: peers.StateFailed,
	})
	var n int
	if err := joiner.db.QueryRow(`SELECT COUNT(*) FROM team_pending_joins`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("team_pending_joins rows = %d, want 0 after a failed pairing", n)
	}
}

// TestTeamJoinWrongLookupIsIgnored: a team.join naming a lookup the owner
// never issued (or issued to a different key) is ignored, not applied
// (Docs/protocol/team.md §`team.join`; ticket 1.1d acceptance).
func TestTeamJoinWrongLookupIsIgnored(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := newTestNode(t, "owner")
	joiner := newTestNode(t, "joiner")
	pairWith(t, owner, joiner, "code")
	newNetwork(t, owner, joiner)

	tm, err := owner.ts.Create(context.Background(), "x", now)
	if err != nil {
		t.Fatal(err)
	}
	team.InviteTag{Store: owner.ts, TeamID: tm.ID}.Completed(peers.CompletionInfo{
		PairingID: "pair-owner", Role: peers.RoleIssuer, Lookup: "REAL1", State: peers.StateComplete,
		Peer: &peers.Peer{PublicKey: joiner.key},
	})

	// The joiner sends team.join with a lookup that was never issued to it.
	if err := deliver(t, joiner, owner, "team.join", map[string]any{"lookup": "WRONG"}); err != nil {
		t.Fatal(err)
	}
	members, err := owner.ts.Members(context.Background(), tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 {
		t.Fatalf("team x members on owner = %d, want 1 (owner only)", len(members))
	}
	if !contains(auditActions(t, owner), team.ActionJoinIgnored) {
		t.Errorf("owner audit actions = %v, want %s", auditActions(t, owner), team.ActionJoinIgnored)
	}
}

// TestTeamInviteScopedToInvitedTeam: an owner with two teams issues an
// invite for team x. A joiner that redeems it becomes a member of x only,
// never of the owner's other team z (a code for team x can't join team z).
func TestTeamInviteScopedToInvitedTeam(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := newTestNode(t, "owner")
	joiner := newTestNode(t, "joiner")
	pairWith(t, owner, joiner, "code")
	newNetwork(t, owner, joiner)

	teamX, err := owner.ts.Create(context.Background(), "x", now)
	if err != nil {
		t.Fatal(err)
	}
	teamZ, err := owner.ts.Create(context.Background(), "z", now)
	if err != nil {
		t.Fatal(err)
	}

	team.InviteTag{Store: owner.ts, TeamID: teamX.ID}.Completed(peers.CompletionInfo{
		PairingID: "pair-owner", Role: peers.RoleIssuer, Lookup: "XTEAM", State: peers.StateComplete,
		Peer: &peers.Peer{PublicKey: joiner.key},
	})
	team.JoinTag{Store: joiner.ts}.Completed(peers.CompletionInfo{
		PairingID: "pair-joiner", Role: peers.RoleRedeemer, Lookup: "XTEAM", State: peers.StateComplete,
		Peer: &peers.Peer{PublicKey: owner.key},
	})

	membersX, err := owner.ts.Members(context.Background(), teamX.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(membersX) != 2 {
		t.Fatalf("team x members = %d, want 2", len(membersX))
	}
	membersZ, err := owner.ts.Members(context.Background(), teamZ.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(membersZ) != 1 {
		t.Fatalf("team z members = %d, want 1 (invite for x must not join z)", len(membersZ))
	}
}

func TestRecordInviteReplacesReusedLookup(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := newTestNode(t, "owner")
	tm, err := owner.ts.Create(context.Background(), "x", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.ts.RecordInvite(context.Background(), "ABCDE", tm.ID, "pair-1", "key-1", now); err != nil {
		t.Fatal(err)
	}
	if err := owner.ts.RecordInvite(context.Background(), "ABCDE", tm.ID, "pair-2", "key-2", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var pairingID, peerKey string
	if err := owner.db.QueryRow(`SELECT pairing_id, peer_key FROM team_invites WHERE lookup = 'ABCDE'`).Scan(&pairingID, &peerKey); err != nil {
		t.Fatal(err)
	}
	if pairingID != "pair-2" || peerKey != "key-2" {
		t.Fatalf("team_invites row = %q %q, want the replaced (second) invite", pairingID, peerKey)
	}
	var n int
	if err := owner.db.QueryRow(`SELECT COUNT(*) FROM team_invites`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("team_invites rows = %d, want 1 (replaced, not duplicated)", n)
	}
}

func TestRecordPendingJoinAllowsTwoInvitesFromOneOwner(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	joiner := newTestNode(t, "joiner")
	if err := joiner.ts.RecordPendingJoin(context.Background(), "owner-key", "AAAAA", now); err != nil {
		t.Fatal(err)
	}
	if err := joiner.ts.RecordPendingJoin(context.Background(), "owner-key", "BBBBB", now); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := joiner.db.QueryRow(`SELECT COUNT(*) FROM team_pending_joins WHERE owner_key = 'owner-key'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("team_pending_joins rows = %d, want 2 (two outstanding invites from one owner)", n)
	}
}

func TestRecordInviteAndPendingJoinPruneExpiredRows(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	owner := newTestNode(t, "owner")
	tm, err := owner.ts.Create(context.Background(), "x", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.ts.RecordInvite(context.Background(), "OLD01", tm.ID, "pair-old", "key-old", now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := owner.ts.RecordPendingJoin(context.Background(), "owner-key", "OLD02", now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A later call (any lookup) prunes rows whose expiry has already passed.
	if err := owner.ts.RecordInvite(context.Background(), "NEW01", tm.ID, "pair-new", "key-new", now); err != nil {
		t.Fatal(err)
	}
	if err := owner.ts.RecordPendingJoin(context.Background(), "owner-key", "NEW02", now); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := owner.db.QueryRow(`SELECT COUNT(*) FROM team_invites WHERE lookup = 'OLD01'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expired team_invites row still present")
	}
	if err := owner.db.QueryRow(`SELECT COUNT(*) FROM team_pending_joins WHERE lookup = 'OLD02'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expired team_pending_joins row still present")
	}
}
