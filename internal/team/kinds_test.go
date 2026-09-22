package team_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// captureOutbox records the last Submit call instead of delivering it.
type captureOutbox struct {
	to, kind string
	body     any
}

func (c *captureOutbox) Submit(_ context.Context, to, kind string, body any) (mail.Submitted, error) {
	c.to, c.kind, c.body = to, kind, body
	return mail.Submitted{ID: mail.NewID(), State: mail.StateDelivered}, nil
}

// pairedTeam creates a fresh owner+member pair (trust code), a team owned by
// owner with member added, and returns the exact roster body owner would
// broadcast to member.
func pairedTeam(t *testing.T) (owner, member *testNode, body map[string]any) {
	t.Helper()
	owner = newTestNode(t, "owner")
	member = newTestNode(t, "member")
	pairWith(t, owner, member, peers.TrustCode)
	ctx := context.Background()
	tm, err := owner.ts.Create(ctx, "backend", owner.now())
	if err != nil {
		t.Fatal(err)
	}
	// A real join writes the pending-join row before team.join reaches the
	// owner and triggers AddMember; simulate that ordering here too.
	addPendingJoin(t, member, owner.key, "AAAA1", member.now())
	if _, err := owner.ts.AddMember(ctx, tm.ID, member.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	co := &captureOutbox{}
	owner.ts.Outbox = co
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatal(err)
	}
	if co.to != member.key || co.kind != "team.roster" {
		t.Fatalf("capture = %+v", co)
	}
	b, ok := co.body.(map[string]any)
	if !ok {
		t.Fatalf("body type = %T", co.body)
	}
	return owner, member, b
}

func teamField(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	tf, ok := body["team"].(map[string]any)
	if !ok {
		t.Fatalf("body has no team field: %+v", body)
	}
	return tf
}

func cloneBody(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	v, err := agentcard.ParseStrict(raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatal("clone: not an object")
	}
	return m
}

func TestRosterForgedOwner(t *testing.T) {
	owner, member, body := pairedTeam(t)
	body = cloneBody(t, body)
	other := newTestNode(t, "other")
	pairWith(t, other, member, peers.TrustCode)
	err := deliver(t, other, member, "team.roster", body) // From = other, but team.owner still = owner's key
	if err == nil || !errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("forged owner: err = %v, want ErrBadBody", err)
	}
	_ = owner
}

func TestRosterEpochNotGreaterIgnoredSilently(t *testing.T) {
	owner, member, body := pairedTeam(t)
	ctx := context.Background()
	if err := deliver(t, owner, member, "team.roster", body); err != nil {
		t.Fatal(err)
	}
	before, err := member.ts.Get(ctx, teamField(t, body)["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	// Resend the exact same (already-applied) epoch: must be a no-op, no audit.
	beforeAudits := len(auditActions(t, member))
	if err := deliver(t, owner, member, "team.roster", cloneBody(t, body)); err != nil {
		t.Fatal(err)
	}
	after, err := member.ts.Get(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("replayed roster changed team: before=%+v after=%+v", before, after)
	}
	if got := len(auditActions(t, member)); got != beforeAudits {
		t.Fatalf("replayed roster audited %d new events, want 0", got-beforeAudits)
	}
}

func TestRosterUnknownTeamWithoutPendingJoinIgnored(t *testing.T) {
	owner := newTestNode(t, "owner")
	stranger := newTestNode(t, "stranger")
	pairWith(t, owner, stranger, peers.TrustCode)
	ctx := context.Background()
	tm, err := owner.ts.Create(ctx, "backend", owner.now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ts.AddMember(ctx, tm.ID, stranger.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	co := &captureOutbox{}
	owner.ts.Outbox = co
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatal(err)
	}
	// stranger never wrote a team_pending_joins row: not invited.
	if err := deliver(t, owner, stranger, "team.roster", co.body); err != nil {
		t.Fatal(err)
	}
	if _, err := stranger.ts.Get(ctx, tm.ID); !errors.Is(err, team.ErrNotFound) {
		t.Fatalf("unknown team got applied without a pending join: %v", err)
	}
	acts := auditActions(t, stranger)
	if !contains(acts, team.ActionRosterIgnored) {
		t.Fatalf("actions = %v, want team.roster_ignored", acts)
	}
}

func TestRosterOwnerTrustRelayIgnored(t *testing.T) {
	owner := newTestNode(t, "owner")
	member := newTestNode(t, "member")
	pairWith(t, owner, member, peers.TrustRelay) // v1: not trusted to introduce
	ctx := context.Background()
	tm, err := owner.ts.Create(ctx, "backend", owner.now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ts.AddMember(ctx, tm.ID, member.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	co := &captureOutbox{}
	owner.ts.Outbox = co
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, owner, member, "team.roster", co.body); err != nil {
		t.Fatal(err)
	}
	if _, err := member.ts.Get(ctx, tm.ID); !errors.Is(err, team.ErrNotFound) {
		t.Fatal("roster from an untrusted (relay) owner was applied")
	}
	acts := auditActions(t, member)
	if !contains(acts, team.ActionRosterIgnored) {
		t.Fatalf("actions = %v, want team.roster_ignored (owner_trust)", acts)
	}
}

func TestRosterBadCardIsBadBody(t *testing.T) {
	_, member, body := pairedTeam(t)
	body = cloneBody(t, body)
	tf := teamField(t, body)
	members := tf["members"].([]any)
	entry := members[0].(map[string]any)
	card := entry["card"].(map[string]any)
	card["signature"] = "AAAA" // tampered
	entry["card"] = card
	owner2 := newTestNode(t, "owner")
	err := deliver(t, owner2, member, "team.roster", body)
	if err == nil || !errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("bad card: err = %v, want ErrBadBody", err)
	}
}

func TestRoster33MembersIsBadBody(t *testing.T) {
	owner := newTestNode(t, "owner")
	member := newTestNode(t, "member")
	pairWith(t, owner, member, peers.TrustCode)
	ctx := context.Background()
	tm, err := owner.ts.Create(ctx, "backend", owner.now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ts.AddMember(ctx, tm.ID, member.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	co := &captureOutbox{}
	owner.ts.Outbox = co
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatal(err)
	}
	body := cloneBody(t, co.body.(map[string]any))
	tf := teamField(t, body)
	members := tf["members"].([]any)
	extra := make([]any, 0, 33)
	extra = append(extra, members...)
	base := members[1].(map[string]any) // the member entry (index 0 is owner, by construction order)
	for len(extra) < 33 {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		c, err := agentcard.New(pub, "x", "h", nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		sc, err := agentcard.Sign(priv, c)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(sc)
		gen, err := agentcard.ParseStrict(raw)
		if err != nil {
			t.Fatal(err)
		}
		clone := map[string]any{}
		for k, v := range base {
			clone[k] = v
		}
		clone["key"] = envelope.KeyString(pub)
		clone["card"] = gen
		clone["mailbox"] = nil
		extra = append(extra, clone)
	}
	tf["members"] = extra
	owner2 := newTestNode(t, "owner2")
	err = deliver(t, owner2, member, "team.roster", body)
	if err == nil || !errors.Is(err, mail.ErrBadBody) {
		t.Fatalf("33 members: err = %v, want ErrBadBody", err)
	}
}

func TestRosterExpiredMailboxAcceptedAsNull(t *testing.T) {
	owner, member, body := pairedTeam(t)
	body = cloneBody(t, body)
	tf := teamField(t, body)
	members := tf["members"].([]any)
	// Find owner's own entry, which carries owner's live mailbox; make it
	// stale by re-signing an announcement whose not_after is already past.
	idx := -1
	for i, raw := range members {
		if raw.(map[string]any)["key"] == owner.key {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("owner not found in roster members")
	}
	entry := members[idx].(map[string]any)
	stale, err := mail.SignAnnouncement(owner.priv.Public().(ed25519.PublicKey),
		func(m []byte) ([]byte, error) { return ed25519.Sign(owner.priv, m), nil },
		owner.mpub, owner.now().Add(-40*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	gen, err := agentcard.ParseStrict(stale)
	if err != nil {
		t.Fatal(err)
	}
	entry["mailbox"] = gen
	members[idx] = entry
	tf["members"] = members
	// member already holds owner's real mailbox key from direct pairing;
	// clear it so the assertion below isolates what the roster itself stored.
	if _, err := member.db.Exec(`UPDATE peers SET mailbox_keys = '[]' WHERE public_key = ?`, owner.key); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, owner, member, "team.roster", body); err != nil {
		t.Fatalf("expired mailbox should be accepted as null, got %v", err)
	}
	var raw string
	if err := member.db.QueryRow(`SELECT mailbox_keys FROM peers WHERE public_key = ?`, owner.key).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "[]" {
		t.Fatalf("owner mailbox_keys = %s, want [] (stale announcement dropped, not stored)", raw)
	}
}

func TestRosterRemovingSelfSetsRemovedAndGC(t *testing.T) {
	owner := newTestNode(t, "owner")
	member := newTestNode(t, "member")
	other := newTestNode(t, "other")
	pairWith(t, owner, member, peers.TrustCode)
	pairWith(t, owner, other, peers.TrustCode)
	// member must NOT know other directly; the roster introduces them fresh.
	ctx := context.Background()
	tm, err := owner.ts.Create(ctx, "backend", owner.now())
	if err != nil {
		t.Fatal(err)
	}
	addPendingJoin(t, member, owner.key, "AAAA1", member.now())
	if _, err := owner.ts.AddMember(ctx, tm.ID, member.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ts.AddMember(ctx, tm.ID, other.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	co := &captureOutbox{}
	owner.ts.Outbox = co
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatal(err)
	}
	// member applies the full roster (introducing "other" as trust=team).
	if err := deliver(t, owner, member, "team.roster", co.body); err != nil {
		t.Fatal(err)
	}
	var trust string
	if err := member.db.QueryRow(`SELECT trust FROM peers WHERE public_key = ?`, other.key).Scan(&trust); err != nil {
		t.Fatal(err)
	}
	if trust != peers.TrustTeam {
		t.Fatalf("other trust = %q, want team", trust)
	}
	// Now owner removes member: the next roster to member lists only the
	// owner and member's local state becomes removed; other must be GC'd.
	if _, err := owner.ts.RemoveMember(ctx, tm.ID, member.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	cap2 := &captureOutbox{}
	owner.ts.Outbox = cap2
	if err := owner.ts.Broadcast(ctx, tm.ID, []string{member.key}); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, owner, member, "team.roster", cap2.body); err != nil {
		t.Fatal(err)
	}
	got, err := member.ts.Get(ctx, tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != team.StateRemoved {
		t.Fatalf("member local state = %q, want removed", got.State)
	}
	var n int
	if err := member.db.QueryRow(`SELECT COUNT(*) FROM peers WHERE public_key = ?`, other.key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("introduced peer was not GC'd after the owning team removed self")
	}
}

func TestTwoPendingJoinsFromOneOwner(t *testing.T) {
	owner := newTestNode(t, "owner")
	joiner := newTestNode(t, "joiner")
	pairWith(t, owner, joiner, peers.TrustCode)
	ctx := context.Background()
	t1, err := owner.ts.Create(ctx, "t1", owner.now())
	if err != nil {
		t.Fatal(err)
	}
	t2, err := owner.ts.Create(ctx, "t2", owner.now())
	if err != nil {
		t.Fatal(err)
	}
	addPendingJoin(t, joiner, owner.key, "AAAA1", owner.now())
	addPendingJoin(t, joiner, owner.key, "BBBB2", owner.now().Add(time.Second))
	if _, err := owner.ts.AddMember(ctx, t1.ID, joiner.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ts.AddMember(ctx, t2.ID, joiner.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	cap1 := &captureOutbox{}
	owner.ts.Outbox = cap1
	if err := owner.ts.Broadcast(ctx, t1.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, owner, joiner, "team.roster", cap1.body); err != nil {
		t.Fatal(err)
	}
	cap2 := &captureOutbox{}
	owner.ts.Outbox = cap2
	if err := owner.ts.Broadcast(ctx, t2.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, owner, joiner, "team.roster", cap2.body); err != nil {
		t.Fatal(err)
	}
	got1, err := joiner.ts.Get(ctx, t1.ID)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := joiner.ts.Get(ctx, t2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got1.State != team.StateActive || got2.State != team.StateActive {
		t.Fatalf("both joins should have succeeded: t1=%+v t2=%+v", got1, got2)
	}
	var n int
	if err := joiner.db.QueryRow(`SELECT COUNT(*) FROM team_pending_joins WHERE owner_key = ?`, owner.key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("pending joins left = %d, want 0 (both consumed)", n)
	}
}

func TestRosterToRemovedMemberListsOwnerOnly(t *testing.T) {
	owner, member, _ := pairedTeam(t)
	ctx := context.Background()
	teams, err := owner.ts.List(ctx)
	if err != nil || len(teams) != 1 {
		t.Fatal(err, teams)
	}
	tm := teams[0]
	co := &captureOutbox{}
	owner.ts.Outbox = co
	if err := owner.ts.Broadcast(ctx, tm.ID, []string{member.key}); err != nil {
		t.Fatal(err)
	}
	body, ok := co.body.(map[string]any)
	if !ok {
		t.Fatalf("body type = %T", co.body)
	}
	tf := teamField(t, body)
	members, _ := tf["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("roster to a removed member has %d members, want 1 (owner only)", len(members))
	}
	entry := members[0].(map[string]any)
	if entry["key"] != owner.key {
		t.Fatalf("owner-only roster lists %v, want owner", entry["key"])
	}
}

func TestPeersRemoveOfOwnerSetsTeamsLeftAndGCs(t *testing.T) {
	owner := newTestNode(t, "owner")
	member := newTestNode(t, "member")
	other := newTestNode(t, "other")
	pairWith(t, owner, member, peers.TrustCode)
	pairWith(t, owner, other, peers.TrustCode)
	ctx := context.Background()
	tm, err := owner.ts.Create(ctx, "backend", owner.now())
	if err != nil {
		t.Fatal(err)
	}
	addPendingJoin(t, member, owner.key, "AAAA1", member.now())
	if _, err := owner.ts.AddMember(ctx, tm.ID, member.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ts.AddMember(ctx, tm.ID, other.key, owner.now()); err != nil {
		t.Fatal(err)
	}
	co := &captureOutbox{}
	owner.ts.Outbox = co
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, owner, member, "team.roster", co.body); err != nil {
		t.Fatal(err)
	}
	// member now removes its peer entry for owner (simulating `peers remove`).
	if err := member.ps.Remove(ctx, owner.key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := member.ts.OwnerRemoved(ctx, owner.key, member.now()); err != nil {
		t.Fatal(err)
	}
	got, err := member.ts.Get(ctx, tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != team.StateLeft {
		t.Fatalf("team state after owner removed = %q, want left", got.State)
	}
	var n int
	if err := member.db.QueryRow(`SELECT COUNT(*) FROM peers WHERE public_key = ?`, other.key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("introduced peer (other) was not GC'd after the owner peer was removed")
	}
	acts := auditActions(t, member)
	if !contains(acts, team.ActionLeave) {
		t.Fatalf("actions = %v, want team.leave", acts)
	}
}

func TestRejoinAfterLeave(t *testing.T) {
	owner, member, body := pairedTeam(t)
	ctx := context.Background()
	if err := deliver(t, owner, member, "team.roster", cloneBody(t, body)); err != nil {
		t.Fatal(err)
	}
	tm := teamField(t, body)
	teamID := tm["id"].(string)
	if _, _, err := member.ts.Leave(ctx, teamID, member.now()); err != nil {
		t.Fatal(err)
	}
	got, err := member.ts.Get(ctx, teamID)
	if err != nil || got.State != team.StateLeft {
		t.Fatalf("after Leave: %+v, %v", got, err)
	}
	// Owner bumps the epoch (e.g. a rename) and re-sends without a pending
	// join: must stay ignored.
	if _, err := owner.ts.Rename(ctx, teamID, "backend2", owner.now()); err != nil {
		t.Fatal(err)
	}
	co := &captureOutbox{}
	owner.ts.Outbox = co
	if err := owner.ts.Broadcast(ctx, teamID, nil); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, owner, member, "team.roster", co.body); err != nil {
		t.Fatal(err)
	}
	got, err = member.ts.Get(ctx, teamID)
	if err != nil || got.State != team.StateLeft {
		t.Fatalf("roster without a pending join must stay ignored: %+v, %v", got, err)
	}
	// Now the member gets a fresh invite (pending join) and the SAME roster
	// re-arrives at a higher epoch: it must apply.
	addPendingJoin(t, member, owner.key, "CCCC3", member.now())
	if err := deliver(t, owner, member, "team.roster", co.body); err != nil {
		t.Fatal(err)
	}
	got, err = member.ts.Get(ctx, teamID)
	if err != nil || got.State != team.StateActive {
		t.Fatalf("rejoin with a pending join must apply: %+v, %v", got, err)
	}
}

func TestTwoTeamsSharedMember(t *testing.T) {
	a := newTestNode(t, "a") // owns team x
	b := newTestNode(t, "b") // shared member of x and y
	c := newTestNode(t, "c") // owns team y
	pairWith(t, a, b, peers.TrustCode)
	pairWith(t, c, b, peers.TrustCode)
	newNetwork(t, a, b, c) // a-c never paired directly: no route needed between them
	ctx := context.Background()

	x, err := a.ts.Create(ctx, "x", a.now())
	if err != nil {
		t.Fatal(err)
	}
	addPendingJoin(t, b, a.key, "AAAA1", b.now())
	if _, err := a.ts.AddMember(ctx, x.ID, b.key, a.now()); err != nil {
		t.Fatal(err)
	}
	if err := a.ts.Broadcast(ctx, x.ID, nil); err != nil {
		t.Fatal(err)
	}

	y, err := c.ts.Create(ctx, "y", c.now())
	if err != nil {
		t.Fatal(err)
	}
	addPendingJoin(t, b, c.key, "BBBB2", b.now())
	if _, err := c.ts.AddMember(ctx, y.ID, b.key, c.now()); err != nil {
		t.Fatal(err)
	}
	if err := c.ts.Broadcast(ctx, y.ID, nil); err != nil {
		t.Fatal(err)
	}

	mx, err := b.ts.Members(ctx, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	my, err := b.ts.Members(ctx, y.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantX := map[string]bool{a.key: true, b.key: true}
	wantY := map[string]bool{c.key: true, b.key: true}
	if len(mx) != 2 || !wantX[mx[0].Key] || !wantX[mx[1].Key] {
		t.Fatalf("team x members on b = %+v, want a and b only", mx)
	}
	if len(my) != 2 || !wantY[my[0].Key] || !wantY[my[1].Key] {
		t.Fatalf("team y members on b = %+v, want c and b only", my)
	}
	// a and c must never have learned about each other.
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM peers WHERE public_key = ?`, c.key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("a learned about c through the shared member b")
	}
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM peers WHERE public_key = ?`, a.key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("c learned about a through the shared member b")
	}
}
