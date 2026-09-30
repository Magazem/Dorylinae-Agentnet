package team_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
)

// multiOutbox records every Submit call.
type multiOutbox struct {
	mu   sync.Mutex
	sent map[string]any // recipient -> body
}

func (o *multiOutbox) Submit(_ context.Context, to, _ string, body any) (mail.Submitted, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.sent == nil {
		o.sent = map[string]any{}
	}
	o.sent[to] = body
	return mail.Submitted{ID: mail.NewID(), State: mail.StateDelivered}, nil
}

func (o *multiOutbox) body(t *testing.T, to string) map[string]any {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	b, ok := o.sent[to].(map[string]any)
	if !ok {
		t.Fatalf("nothing sent to %s", to)
	}
	return b
}

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func storedForm(t *testing.T, raw []byte) string {
	t.Helper()
	b, err := agentcard.StoredForm(raw)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func peerCard(t *testing.T, n *testNode, key string) string {
	t.Helper()
	var card string
	if err := n.db.QueryRow(`SELECT card FROM peers WHERE public_key = ?`, key).Scan(&card); err != nil {
		t.Fatal(err)
	}
	return card
}

// entryCard returns the card member of the roster entry for key.
func entryCard(t *testing.T, body map[string]any, key string) map[string]any {
	t.Helper()
	for _, e := range teamField(t, body)["members"].([]any) {
		entry := e.(map[string]any)
		if entry["key"] == key {
			return entry["card"].(map[string]any)
		}
	}
	t.Fatalf("no roster entry for %s", key)
	return nil
}

func memberKeys(t *testing.T, body map[string]any) []string {
	t.Helper()
	var keys []string
	for _, e := range teamField(t, body)["members"].([]any) {
		keys = append(keys, e.(map[string]any)["key"].(string))
	}
	return keys
}

// ownedTeam creates a team owned by owner with each of members added (all
// paired with owner at trust code).
func ownedTeam(t *testing.T, owner *testNode, members ...*testNode) team.Team {
	t.Helper()
	ctx := context.Background()
	tm, err := owner.ts.Create(ctx, "backend", owner.now())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range members {
		pairWith(t, owner, m, peers.TrustCode)
		addPendingJoin(t, m, owner.key, "AAAA1", m.now())
		if tm, err = owner.ts.AddMember(ctx, tm.ID, m.key, owner.now()); err != nil {
			t.Fatal(err)
		}
	}
	return tm
}

// Review 68 A11: a roster entry whose card envelope has an extra top-level
// member is stored without it, and a roster built from a stored card that
// has one forwards {card, signature} only (R55-073).
func TestRosterCardStoredAndForwardedCanonical(t *testing.T) {
	ctx := context.Background()
	owner, member, other := newTestNode(t, "owner"), newTestNode(t, "member"), newTestNode(t, "other")
	tm := ownedTeam(t, owner, member, other)

	// Owner side: other's stored row carries an extra member (an old v1 row).
	withExtra := strings.Replace(string(other.card), `{"card"`, `{"relay_note":"x","card"`, 1)
	if _, err := owner.db.Exec(`UPDATE peers SET card = ? WHERE public_key = ?`, withExtra, other.key); err != nil {
		t.Fatal(err)
	}
	out := &multiOutbox{}
	owner.ts.Outbox = out
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatal(err)
	}
	body := cloneBody(t, out.body(t, member.key))
	for _, key := range []string{owner.key, member.key, other.key} {
		c := entryCard(t, body, key)
		if len(c) != 2 || c["card"] == nil || c["signature"] == nil {
			t.Fatalf("forwarded card of %s has members %v, want card and signature only", key[:8], c)
		}
	}

	// Member side: the entry for other arrives with an extra member.
	entryCard(t, body, other.key)["ok"] = true
	if err := deliver(t, owner, member, "team.roster", body); err != nil {
		t.Fatal(err)
	}
	if got, want := peerCard(t, member, other.key), storedForm(t, other.card); got != want {
		t.Fatalf("stored introduced card = %s\nwant %s", got, want)
	}
}

// foldedCard is a card for n, signed by n, that also carries "public_Key"
// with U+212A in place of k (agent-card.md N1): it passes the signature and
// fails the schema.
func foldedCard(t *testing.T, n *testNode, other string) string {
	t.Helper()
	sc, err := agentcard.Verify(n.card)
	if err != nil {
		t.Fatal(err)
	}
	c := sc.Card
	skills := []any{}
	for _, s := range c.Skills {
		skills = append(skills, map[string]any{"id": s.ID, "name": s.Name, "description": s.Description})
	}
	card := map[string]any{
		"version": json.Number("1"), "name": c.Name, "public_key": c.PublicKey, "harness": c.Harness,
		"skills": skills, "created": c.Created, "public_Key": other,
	}
	canon, err := agentcard.CanonicalValue(card)
	if err != nil {
		t.Fatal(err)
	}
	msg := append([]byte("dorylinae-agent-card-v1\n"), canon...)
	sig := ed25519.Sign(n.priv, msg)
	env, err := agentcard.CanonicalValue(map[string]any{"card": card, "signature": b64u(sig)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentcard.Verify(env); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("precondition: the folded card must fail the schema, got %v", err)
	}
	return string(env)
}

// Review 68 A13: with a member row holding a folded card, a roster update is
// refused locally with an error naming the member, while team remove of that
// member succeeds and sends the remaining members a roster without it.
func TestBadStoredCardBlocksRosterButNotRemove(t *testing.T) {
	ctx := context.Background()
	owner, good, bad := newTestNode(t, "owner"), newTestNode(t, "good"), newTestNode(t, "bad")
	tm := ownedTeam(t, owner, good, bad)
	if _, err := owner.db.Exec(`UPDATE peers SET card = ? WHERE public_key = ?`, foldedCard(t, bad, good.key), bad.key); err != nil {
		t.Fatal(err)
	}
	out := &multiOutbox{}
	owner.ts.Outbox = out

	// A roster update (rename) is refused locally, naming the member and the remedy.
	if _, err := owner.ts.Rename(ctx, tm.ID, "renamed", owner.now()); err != nil {
		t.Fatal(err)
	}
	err := owner.ts.Broadcast(ctx, tm.ID, nil)
	if err == nil || !strings.Contains(err.Error(), bad.key) || !strings.Contains(err.Error(), "team remove") {
		t.Fatalf("roster with a bad card: err = %v, want a local error naming %s", err, bad.key)
	}
	if len(out.sent) != 0 {
		t.Fatalf("a roster was sent: %v", out.sent)
	}

	// team remove of that member succeeds (as internal/daemon runs it).
	nt, err := owner.ts.RemoveMember(ctx, tm.ID, bad.key, owner.now())
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.ts.Broadcast(ctx, nt.ID, []string{bad.key}); err != nil {
		t.Fatalf("team remove broadcast: %v", err)
	}
	if keys := memberKeys(t, out.body(t, good.key)); len(keys) != 2 || contains(keys, bad.key) {
		t.Fatalf("roster to the remaining member lists %v", keys)
	}
	if keys := memberKeys(t, out.body(t, bad.key)); len(keys) != 1 || keys[0] != owner.key {
		t.Fatalf("roster to the removed member lists %v, want the owner only", keys)
	}
	// The remaining member accepts it.
	if err := deliver(t, owner, good, "team.roster", cloneBody(t, out.body(t, good.key))); err != nil {
		t.Fatalf("remaining member refused the roster: %v", err)
	}
	// The bad row is still there: nothing deletes or downgrades it.
	var trust string
	if err := owner.db.QueryRow(`SELECT trust FROM peers WHERE public_key = ?`, bad.key).Scan(&trust); err != nil || trust != peers.TrustCode {
		t.Fatalf("bad peer row: trust %q, %v", trust, err)
	}
}

// team delete is not blocked either: the final (dissolved) roster leaves the
// member with the bad card out, which team.md allows (0-32 entries).
func TestBadStoredCardDoesNotBlockDelete(t *testing.T) {
	ctx := context.Background()
	owner, good, bad := newTestNode(t, "owner"), newTestNode(t, "good"), newTestNode(t, "bad")
	tm := ownedTeam(t, owner, good, bad)
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
		t.Fatalf("team delete broadcast: %v", err)
	}
	body := out.body(t, good.key)
	if keys := memberKeys(t, body); contains(keys, bad.key) || !contains(keys, good.key) {
		t.Fatalf("dissolved roster lists %v", keys)
	}
	if teamField(t, body)["state"] != team.StateDissolved {
		t.Fatalf("state = %v", teamField(t, body)["state"])
	}
	if err := deliver(t, owner, good, "team.roster", cloneBody(t, body)); err != nil {
		t.Fatalf("member refused the dissolved roster: %v", err)
	}
}

// Review 76 L1: the member left out of the final roster is still sent it and
// applies it as dissolved, not removed (team.md Apply step 4: a dissolved
// roster is recognised by its state, not by self's presence).
func TestOmittedMemberAppliesDissolved(t *testing.T) {
	ctx := context.Background()
	owner, good, bad := newTestNode(t, "owner"), newTestNode(t, "good"), newTestNode(t, "bad")
	tm := ownedTeam(t, owner, good, bad)
	first := &multiOutbox{}
	owner.ts.Outbox = first
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, owner, bad, "team.roster", cloneBody(t, first.body(t, bad.key))); err != nil {
		t.Fatal(err)
	}
	if g, err := bad.ts.Get(ctx, tm.ID); err != nil || g.State != team.StateActive {
		t.Fatalf("setup: state %q, %v", g.State, err)
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
	body := out.body(t, bad.key) // the omitted member is still a recipient
	if keys := memberKeys(t, body); contains(keys, bad.key) {
		t.Fatalf("the final roster lists the member with the bad card: %v", keys)
	}
	if err := deliver(t, owner, bad, "team.roster", cloneBody(t, body)); err != nil {
		t.Fatalf("omitted member refused the dissolved roster: %v", err)
	}
	if g, err := bad.ts.Get(ctx, tm.ID); err != nil || g.State != team.StateDissolved {
		t.Fatalf("omitted member state %q, %v; want dissolved", g.State, err)
	}
}

// A roster with state active that leaves self out still means removed.
func TestActiveRosterWithoutSelfIsRemoved(t *testing.T) {
	ctx := context.Background()
	owner, good, gone := newTestNode(t, "owner"), newTestNode(t, "good"), newTestNode(t, "gone")
	tm := ownedTeam(t, owner, good, gone)
	first := &multiOutbox{}
	owner.ts.Outbox = first
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, owner, gone, "team.roster", cloneBody(t, first.body(t, gone.key))); err != nil {
		t.Fatal(err)
	}
	nt, err := owner.ts.RemoveMember(ctx, tm.ID, gone.key, owner.now())
	if err != nil {
		t.Fatal(err)
	}
	out := &multiOutbox{}
	owner.ts.Outbox = out
	if err := owner.ts.Broadcast(ctx, nt.ID, []string{gone.key}); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, owner, gone, "team.roster", cloneBody(t, out.body(t, gone.key))); err != nil {
		t.Fatal(err)
	}
	if g, err := gone.ts.Get(ctx, tm.ID); err != nil || g.State != team.StateRemoved {
		t.Fatalf("removed member state %q, %v; want removed", g.State, err)
	}
}
