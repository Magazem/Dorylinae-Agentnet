package team_test

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// legacyTextCard is a card for n, signed by n, whose name holds U+202E: it
// passes VerifyStored (the legacy text rule) and fails Verify (R55-F10,
// agent-card.md N18).
func legacyTextCard(t *testing.T, n *testNode) string {
	t.Helper()
	sc, err := agentcard.Verify(n.card)
	if err != nil {
		t.Fatal(err)
	}
	c := sc.Card
	card := map[string]any{
		"version": json.Number("1"), "name": "Ada RLOtset", "public_key": c.PublicKey, "harness": c.Harness,
		"skills": []any{}, "created": c.Created,
	}
	card["name"] = strings.Replace(card["name"].(string), "RLO", string(rune(0x202E)), 1)
	canon, err := agentcard.CanonicalValue(card)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(n.priv, append([]byte("dorylinae-agent-card-v1\n"), canon...))
	env, err := agentcard.CanonicalValue(map[string]any{"card": card, "signature": b64u(sig)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentcard.Verify(env); err == nil {
		t.Fatal("precondition: Verify must refuse the legacy card")
	}
	if _, err := agentcard.VerifyStored(env); err != nil {
		t.Fatalf("precondition: VerifyStored must accept the legacy card: %v", err)
	}
	return string(env)
}

// R55-F10 A13 (team) and A14: a member whose stored card predates the text
// rule does not block a roster: forwardCard forwards it, the roster is
// built, and a member accepts it and stores the introduced card. A roster
// entry whose card fails the schema (N1) is still refused.
func TestLegacyTextCardDoesNotBlockRoster(t *testing.T) {
	ctx := context.Background()
	owner, member, legacy := newTestNode(t, "owner"), newTestNode(t, "member"), newTestNode(t, "legacy")
	tm := ownedTeam(t, owner, member, legacy)
	legacyCard := legacyTextCard(t, legacy)
	if _, err := owner.db.Exec(`UPDATE peers SET card = ? WHERE public_key = ?`, legacyCard, legacy.key); err != nil {
		t.Fatal(err)
	}
	out := &multiOutbox{}
	owner.ts.Outbox = out
	if err := owner.ts.Broadcast(ctx, tm.ID, nil); err != nil {
		t.Fatalf("roster with a legacy card: %v", err)
	}
	body := out.body(t, member.key)
	got, err := json.Marshal(entryCard(t, body, legacy.key))
	if err != nil {
		t.Fatal(err)
	}
	if canon, err := agentcard.StoredForm(got); err != nil || string(canon) != legacyCard {
		t.Fatalf("forwarded card = %s, %v; want %s", got, err, legacyCard)
	}

	// A14: the member accepts the roster and stores the introduced card.
	if err := deliver(t, owner, member, "team.roster", cloneBody(t, body)); err != nil {
		t.Fatalf("member refused a roster carrying a legacy card: %v", err)
	}
	if got := peerCard(t, member, legacy.key); got != legacyCard {
		t.Fatalf("introduced card = %s\nwant %s", got, legacyCard)
	}

	// A roster entry that fails the schema is still refused.
	bad := cloneBody(t, out.body(t, legacy.key))
	var folded map[string]any
	if err := json.Unmarshal([]byte(foldedCard(t, member, legacy.key)), &folded); err != nil {
		t.Fatal(err)
	}
	for _, e := range teamField(t, bad)["members"].([]any) {
		if entry := e.(map[string]any); entry["key"] == member.key {
			entry["card"] = folded
		}
	}
	other := newTestNode(t, "other")
	pairWith(t, owner, other, "code")
	if err := deliver(t, owner, other, "team.roster", bad); err == nil || !strings.Contains(err.Error(), "roster member.card") {
		t.Fatalf("roster with a folded member card: %v, want a refusal", err)
	}
}
