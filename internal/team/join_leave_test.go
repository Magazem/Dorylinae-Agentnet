package team_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

func TestJoinNoInvite(t *testing.T) {
	owner := newTestNode(t, "owner")
	joiner := newTestNode(t, "joiner")
	pairWith(t, owner, joiner, peers.TrustCode)
	if err := deliver(t, joiner, owner, "team.join", map[string]any{"lookup": "AAAAA"}); err != nil {
		t.Fatal(err)
	}
	acts := auditActions(t, owner)
	if !contains(acts, team.ActionJoinIgnored) {
		t.Fatalf("actions = %v, want team.join_ignored", acts)
	}
}

func TestJoinBadLookupIsBadBody(t *testing.T) {
	owner := newTestNode(t, "owner")
	joiner := newTestNode(t, "joiner")
	pairWith(t, owner, joiner, peers.TrustCode)
	err := deliver(t, joiner, owner, "team.join", map[string]any{"lookup": "not-valid!"})
	if err == nil || !errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("bad lookup: err = %v, want ErrBadBody", err)
	}
}

func TestJoinTeamFull(t *testing.T) {
	owner := newTestNode(t, "owner")
	joiner := newTestNode(t, "joiner")
	pairWith(t, owner, joiner, peers.TrustCode)
	ctx := context.Background()
	tm, err := owner.ts.Create(ctx, "backend", owner.now())
	if err != nil {
		t.Fatal(err)
	}
	// Fill the team to 32 with placeholder members directly (no card needed
	// for this table-only check).
	for i := 0; i < 31; i++ {
		if _, err := owner.db.Exec(`INSERT INTO team_members (team_id, key, added) VALUES (?, ?, ?)`,
			tm.ID, "filler-"+string(rune('a'+i)), owner.now().Format("2006-01-02T15:04:05Z")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := owner.db.Exec(`INSERT INTO team_invites (lookup, team_id, pairing_id, peer_key, created, expires) VALUES
		(?, ?, 'p1', ?, ?, ?)`, "BBBBB", tm.ID, joiner.key,
		owner.now().Format("2006-01-02T15:04:05.000Z"), owner.now().Add(time.Hour).Format("2006-01-02T15:04:05.000Z")); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, joiner, owner, "team.join", map[string]any{"lookup": "bbbbb"}); err != nil {
		t.Fatal(err)
	}
	acts := auditActions(t, owner)
	if !contains(acts, team.ActionJoinIgnored) {
		t.Fatalf("actions = %v, want team.join_ignored (team_full)", acts)
	}
}

func TestLeaveUnknownTeamIgnored(t *testing.T) {
	owner := newTestNode(t, "owner")
	member := newTestNode(t, "member")
	pairWith(t, owner, member, peers.TrustCode)
	if err := deliver(t, member, owner, "team.leave", map[string]any{"team": "t-" + "0123456789abcdef0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	acts := auditActions(t, owner)
	if !contains(acts, team.ActionLeaveIgnored) {
		t.Fatalf("actions = %v, want team.leave_ignored", acts)
	}
}

func TestLeaveOwnerCannotBeRemoved(t *testing.T) {
	owner := newTestNode(t, "owner")
	ctx := context.Background()
	tm, err := owner.ts.Create(ctx, "backend", owner.now())
	if err != nil {
		t.Fatal(err)
	}
	// The owner "leaving" itself must be ignored, not applied.
	if err := deliver(t, owner, owner, "team.leave", map[string]any{"team": tm.ID}); err != nil {
		t.Fatal(err)
	}
	got, err := owner.ts.Get(ctx, tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Epoch != 1 {
		t.Fatalf("epoch changed to %d after an owner-leave attempt", got.Epoch)
	}
}

func TestLeaveAppliesAndBroadcasts(t *testing.T) {
	owner := newTestNode(t, "owner")
	member := newTestNode(t, "member")
	pairWith(t, owner, member, peers.TrustCode)
	newNetwork(t, owner, member)
	ctx := context.Background()
	tm, err := owner.ts.Create(ctx, "backend", owner.now())
	if err != nil {
		t.Fatal(err)
	}
	addPendingJoin(t, member, owner.key, "AAAA1", member.now())
	if _, err := owner.ts.AddMember(ctx, tm.ID, member.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, member, owner, "team.leave", map[string]any{"team": tm.ID}); err != nil {
		t.Fatal(err)
	}
	got, err := owner.ts.Get(ctx, tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Epoch != 3 { // 1 create, 2 add member, 3 remove on leave
		t.Fatalf("epoch = %d, want 3", got.Epoch)
	}
	members, err := owner.ts.Members(ctx, tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].Key != owner.key {
		t.Fatalf("members after leave = %+v, want owner only", members)
	}
}
