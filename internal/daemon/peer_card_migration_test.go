package daemon_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// Review 68 A12 (OD-3): a peers row whose stored card no longer verifies
// (agent-card.md N1, a folded public_key) never stops the daemon from
// starting; the row is kept as it is and logged.
func TestDaemonOpensWithBadStoredCard(t *testing.T) {
	r := newHarnessRelay(t)
	a := newHarnessNode(t, "alice", r)
	a.start()
	a.stop()

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
	var n1 string
	for _, c := range v.AgentCard.Cases {
		if c.Name == "N1" {
			n1 = c.Envelope
		}
	}
	const key = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
	st, err := store.Open(context.Background(), a.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO peers (public_key, name, harness, skills, card, paired_at, trust, mailbox_keys)
VALUES (?, 'ada', 'custom', '[]', ?, '2026-01-02T03:04:05Z', 'fingerprint', '[]')`, key, n1); err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	_ = st.Close()

	a.start()
	var status daemon.StatusResult
	a.call("status", nil, &status)
	var card, trust string
	if err := a.query(`SELECT card, trust FROM peers WHERE public_key = '`+key+`'`, &card, &trust); err != nil {
		t.Fatal(err)
	}
	if card != n1 || trust != "fingerprint" {
		t.Fatalf("the row changed: trust %q, card %s", trust, card)
	}
	harnessWait(t, "the peer_card_invalid log line", func() bool { return strings.Contains(a.logs.String(), "peer_card_invalid") })
}
