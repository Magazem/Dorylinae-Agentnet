package team_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// Regression tests for Docs/review/16-1.1b-review.md.

// recordOutbox records every Submit call.
type recordOutbox struct {
	calls []recorded
}

type recorded struct {
	to, kind string
	body     any
}

func (r *recordOutbox) Submit(_ context.Context, to, kind string, body any) (mail.Submitted, error) {
	r.calls = append(r.calls, recorded{to, kind, body})
	return mail.Submitted{ID: mail.NewID(), State: mail.StateDelivered}, nil
}

func pendingJoins(t *testing.T, n *testNode, owner string) int {
	t.Helper()
	var c int
	if err := n.db.QueryRow(`SELECT COUNT(*) FROM team_pending_joins WHERE owner_key = ?`, owner).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

// invitedRoster returns an owner, a joiner with one pending join from it, the
// team, and the roster the owner broadcasts to the joiner.
func invitedRoster(t *testing.T) (owner, joiner *testNode, tm team.Team, body map[string]any) {
	t.Helper()
	owner = newTestNode(t, "owner")
	joiner = newTestNode(t, "joiner")
	pairWith(t, owner, joiner, peers.TrustCode)
	ctx := context.Background()
	tm, err := owner.ts.Create(ctx, "backend", owner.now())
	if err != nil {
		t.Fatal(err)
	}
	addPendingJoin(t, joiner, owner.key, "AAAA1", joiner.now())
	if _, err := owner.ts.AddMember(ctx, tm.ID, joiner.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	co := &captureOutbox{}
	owner.ts.Outbox = co
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatal(err)
	}
	return owner, joiner, tm, cloneBody(t, co.body.(map[string]any))
}

func TestRosterDissolvedDoesNotConsumePendingJoin(t *testing.T) {
	owner, joiner, tm, _ := invitedRoster(t)
	ctx := context.Background()
	if _, err := owner.ts.Delete(ctx, tm.ID, owner.now()); err != nil {
		t.Fatal(err)
	}
	co := &captureOutbox{}
	owner.ts.Outbox = co
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := teamField(t, co.body.(map[string]any))["state"]; got != team.StateDissolved {
		t.Fatalf("state = %v", got)
	}
	if err := deliver(t, owner, joiner, "team.roster", co.body); err != nil {
		t.Fatal(err)
	}
	if _, err := joiner.ts.Get(ctx, tm.ID); !errors.Is(err, team.ErrNotFound) {
		t.Fatalf("dissolved roster for an unknown team was applied: %v", err)
	}
	if n := pendingJoins(t, joiner, owner.key); n != 1 {
		t.Fatalf("pending joins = %d, want 1 (a dissolved roster must not consume it)", n)
	}
}

func TestRosterWithoutSelfDoesNotConsumePendingJoin(t *testing.T) {
	owner, joiner, tm, body := invitedRoster(t)
	tf := teamField(t, body)
	var ownerOnly []any
	for _, m := range tf["members"].([]any) {
		if m.(map[string]any)["key"] == owner.key {
			ownerOnly = append(ownerOnly, m)
		}
	}
	tf["members"] = ownerOnly
	if err := deliver(t, owner, joiner, "team.roster", body); err != nil {
		t.Fatal(err)
	}
	if _, err := joiner.ts.Get(context.Background(), tm.ID); !errors.Is(err, team.ErrNotFound) {
		t.Fatalf("roster without self was applied: %v", err)
	}
	if n := pendingJoins(t, joiner, owner.key); n != 1 {
		t.Fatalf("pending joins = %d, want 1", n)
	}
	if !contains(auditActions(t, joiner), team.ActionRosterIgnored) {
		t.Fatal("want team.roster_ignored")
	}
}

func TestRosterEpochAt2Pow53IsBadBody(t *testing.T) {
	owner, joiner, _, body := invitedRoster(t)
	teamField(t, body)["epoch"] = json.Number("9007199254740992")
	if err := deliver(t, owner, joiner, "team.roster", body); !errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("epoch 2^53: err = %v, want ErrBadBody", err)
	}
	teamField(t, body)["epoch"] = json.Number("9007199254740991")
	if err := deliver(t, owner, joiner, "team.roster", body); err != nil {
		t.Fatalf("epoch 2^53-1: %v", err)
	}
}

// A team.join from a key that is still on the roster (re-invited before its
// team.leave arrived) must not fail Apply on the primary key forever.
func TestJoinFromCurrentMemberBumpsEpoch(t *testing.T) {
	owner, joiner, tm, _ := invitedRoster(t)
	ctx := context.Background()
	before, err := owner.ts.Get(ctx, tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := owner.now()
	if _, err := owner.db.Exec(`INSERT INTO team_invites (lookup, team_id, pairing_id, peer_key, created, expires) VALUES (?, ?, 'p2', ?, ?, ?)`,
		"CCCCC", tm.ID, joiner.key, now.UTC().Format("2006-01-02T15:04:05.000Z"), now.Add(time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")); err != nil {
		t.Fatal(err)
	}
	rec := &recordOutbox{}
	owner.ts.Outbox = rec
	if err := deliver(t, joiner, owner, "team.join", map[string]any{"lookup": "CCCCC"}); err != nil {
		t.Fatalf("join from a current member: %v", err)
	}
	after, err := owner.ts.Get(ctx, tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Epoch != before.Epoch+1 {
		t.Fatalf("epoch %d -> %d, want +1", before.Epoch, after.Epoch)
	}
	ms, err := owner.ts.Members(ctx, tm.ID)
	if err != nil || len(ms) != 2 {
		t.Fatalf("members = %v, %v", ms, err)
	}
	var used *string
	if err := owner.db.QueryRow(`SELECT used FROM team_invites WHERE lookup = 'CCCCC'`).Scan(&used); err != nil || used == nil {
		t.Fatalf("invite not marked used: %v", err)
	}
	if len(rec.calls) != 1 || rec.calls[0].to != joiner.key || rec.calls[0].kind != "team.roster" {
		t.Fatalf("broadcast = %+v", rec.calls)
	}
}

// The owner ran `peers remove` on a member of its own team: the next
// broadcast must still reach the other members, without the removed one.
func TestBroadcastSkipsMemberWithoutPeerRow(t *testing.T) {
	owner, b, tm, _ := invitedRoster(t)
	c := newTestNode(t, "c")
	pairWith(t, owner, c, peers.TrustCode)
	ctx := context.Background()
	if _, err := owner.ts.AddMember(ctx, tm.ID, c.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	if err := owner.ps.Remove(ctx, b.key); err != nil {
		t.Fatal(err)
	}
	rec := &recordOutbox{}
	owner.ts.Outbox = rec
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if len(rec.calls) != 1 || rec.calls[0].to != c.key {
		t.Fatalf("broadcast = %+v, want one roster to c", rec.calls)
	}
	var keys []any
	for _, m := range teamField(t, rec.calls[0].body.(map[string]any))["members"].([]any) {
		keys = append(keys, m.(map[string]any)["key"])
	}
	if len(keys) != 2 || !contains([]string{keys[0].(string), keys[1].(string)}, owner.key) || !contains([]string{keys[0].(string), keys[1].(string)}, c.key) {
		t.Fatalf("roster members = %v, want owner and c", keys)
	}
}

// A roster that removes self but introduces a new key: the key is inserted
// and collected in the same transaction, and must get no keys push.
func TestNoKeysPushToPeerCollectedInSameRoster(t *testing.T) {
	owner, member, body := pairedTeam(t)
	ctx := context.Background()
	if err := deliver(t, owner, member, "team.roster", cloneBody(t, body)); err != nil {
		t.Fatal(err)
	}
	x := newTestNode(t, "x")
	pairWith(t, owner, x, peers.TrustCode)
	teamID := teamField(t, body)["id"].(string)
	if _, err := owner.ts.AddMember(ctx, teamID, x.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ts.RemoveMember(ctx, teamID, member.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	co := &captureOutbox{}
	owner.ts.Outbox = co
	if err := owner.ts.Broadcast(ctx, teamID, nil); err != nil {
		t.Fatal(err)
	}
	if co.to != x.key {
		t.Fatalf("capture to = %s", co.to)
	}
	rec := &recordOutbox{}
	member.ts.Outbox = rec
	// The full roster meant for x (owner + x), delivered to member.
	if err := deliver(t, owner, member, "team.roster", co.body); err != nil {
		t.Fatal(err)
	}
	got, err := member.ts.Get(ctx, teamID)
	if err != nil || got.State != team.StateRemoved {
		t.Fatalf("member state = %+v, %v", got, err)
	}
	var n int
	if err := member.db.QueryRow(`SELECT COUNT(*) FROM peers WHERE public_key = ?`, x.key).Scan(&n); err != nil || n != 0 {
		t.Fatalf("x still a peer of member: %d, %v", n, err)
	}
	for _, c := range rec.calls {
		if c.kind == "keys" {
			t.Fatalf("keys pushed to %s, which was collected in the same transaction", c.to)
		}
	}
}
