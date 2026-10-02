package identity_test

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// R55-F10 A12: an own card made before the text rule (agent-card.md N18,
// seed 00..1f) still loads; the report flags it so the daemon can warn.
func TestLegacyTextOwnCardLoads(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "tools", "verifyvectors", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		AgentCard struct {
			Cases []struct{ Name, Envelope string } `json:"cases"`
		} `json:"agent_card"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	var n18 string
	for _, c := range v.AgentCard.Cases {
		if c.Name == "N18" {
			n18 = c.Envelope
		}
	}
	dir := testutil.TempDir(t)
	ks := fileStore(t, dir)
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	if _, _, err := ks.Save(seed); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, identity.CardFile), []byte(n18), 0o600); err != nil {
		t.Fatal(err)
	}
	id, rep, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now)
	if err != nil {
		t.Fatalf("legacy own card refused: %v", err)
	}
	if rep.Created || rep.LegacyText == nil || !strings.Contains(rep.LegacyText.Error(), "name contains a bidi control") {
		t.Fatalf("report = %+v", rep)
	}
	if id.Card().Card.Name != "Ada "+string(rune(0x202E))+"tset" {
		t.Fatalf("name = %+q", id.Card().Card.Name)
	}
	// An ordinary card is not flagged.
	dir2 := testutil.TempDir(t)
	if _, _, err := identity.LoadOrCreate(dir2, fileStore(t, dir2), identity.Options{Name: "alpha"}, now); err != nil {
		t.Fatal(err)
	}
	if _, rep, err := identity.LoadOrCreate(dir2, fileStore(t, dir2), identity.Options{}, now); err != nil || rep.LegacyText != nil {
		t.Fatalf("ordinary card: %+v, %v", rep, err)
	}
}
