package team_test

// Review 76 probe (reviewer-created): the member left out of the final
// dissolved roster is still a recipient, and applies it as "removed", not
// "dissolved" (kinds.go localState: !selfIn wins over the wire state).

import (
	"context"
	"testing"
)

func TestSec76OmittedMemberSeesRemoved(t *testing.T) {
	ctx := context.Background()
	owner, good, bad := newTestNode(t, "owner"), newTestNode(t, "good"), newTestNode(t, "bad")
	tm := ownedTeam(t, owner, good, bad)
	act := &multiOutbox{}
	owner.ts.Outbox = act
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, owner, bad, "team.roster", cloneBody(t, act.body(t, bad.key))); err != nil {
		t.Fatal(err)
	}
	if g, _ := bad.ts.Get(ctx, tm.ID); g.State != "active" {
		t.Fatalf("setup: bad state %q", g.State)
	}
	if _, err := owner.db.Exec(`UPDATE peers SET card = ? WHERE public_key = ?`, foldedCard(t, bad, good.key), bad.key); err != nil {
		t.Fatal(err)
	}
	out := &multiOutbox{}
	owner.ts.Outbox = out
	nt, err := owner.ts.Delete(ctx, tm.ID, owner.now())
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.ts.Broadcast(ctx, nt.ID, nil); err != nil {
		t.Fatal(err)
	}
	body := out.body(t, bad.key) // the omitted member is still sent the roster
	if err := deliver(t, owner, bad, "team.roster", cloneBody(t, body)); err != nil {
		t.Fatalf("omitted member refused the dissolved roster: %v", err)
	}
	got, err := bad.ts.Get(ctx, tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("omitted member local state after the final roster: %q", got.State)
}
