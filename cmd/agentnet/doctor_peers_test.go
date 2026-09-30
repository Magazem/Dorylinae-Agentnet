package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 68 A12: agentnet doctor reports a peers row whose stored card no
// longer verifies (agent-card.md N1), by public key, as a warn with the fix.
func TestDoctorPeersCheck(t *testing.T) {
	ctx := context.Background()
	p := paths.Paths{DB: filepath.Join(testutil.TempDir(t), "d.db")}
	if c := checkPeers(ctx, p); c.ID != "peers" || c.State != doctorSkip {
		t.Fatalf("no database: %+v, want skip", c)
	}
	st, err := store.Open(ctx, p.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if c := checkPeers(ctx, p); c.State != doctorOK {
		t.Fatalf("no peers: %+v, want ok", c)
	}

	raw, err := os.ReadFile(filepath.Join("..", "..", "tools", "verifyvectors", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		AgentCard struct {
			Cases []struct {
				Name     string `json:"name"`
				Envelope string `json:"envelope"`
			} `json:"cases"`
		} `json:"agent_card"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	cards := map[string]string{}
	for _, c := range v.AgentCard.Cases {
		cards[c.Name] = c.Envelope
	}
	const key = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
	insert := func(card string) {
		t.Helper()
		if _, err := st.DB().Exec(`INSERT INTO peers (public_key, name, harness, skills, card, paired_at, trust, mailbox_keys)
VALUES (?, 'ada', 'custom', '[]', ?, '2026-01-02T03:04:05Z', 'code', '[]')
ON CONFLICT (public_key) DO UPDATE SET card = excluded.card`, key, card); err != nil {
			t.Fatal(err)
		}
	}
	insert(cards["P1"]) // an honest card with an extra member: still verifies
	if c := checkPeers(ctx, p); c.State != doctorOK {
		t.Fatalf("honest card: %+v, want ok", c)
	}
	insert(cards["N1"])
	c := checkPeers(ctx, p)
	if c.State != doctorWarn || !strings.Contains(c.Detail, "peer "+key+" has a card that no longer verifies") ||
		!strings.Contains(c.Fix, "re-pair") || !strings.Contains(c.Fix, "peers remove") {
		t.Fatalf("folded card: %+v", c)
	}
	if strings.Contains(c.Detail, "ada") {
		t.Fatalf("the check names the peer: %q", c.Detail)
	}
}
