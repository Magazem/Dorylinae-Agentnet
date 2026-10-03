package peers

import (
	"context"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// R55-118: after a re-pair at a lower trust, the Peer reported to the CLI and
// the pair.complete audit is the stored (higher) trust, not the requested one.
func TestStoreReportsStoredTrustOnRepair(t *testing.T) {
	m, _, log, _ := newRevManager(t, nil)
	id := newRevIdent(t, "peer")
	sc, err := agentcard.Verify(id.card)
	if err != nil {
		t.Fatal(err)
	}
	first, fail := m.store(sc, id.card, TrustFingerprint, nil, "p-1", RoleIssuer)
	if fail != nil {
		t.Fatal(fail.Message)
	}
	again, fail := m.store(sc, id.card, TrustCode, nil, "p-2", RoleIssuer)
	if fail != nil {
		t.Fatal(fail.Message)
	}
	if again.Trust != TrustFingerprint {
		t.Fatalf("re-pair reported trust %q, want the stored %q", again.Trust, TrustFingerprint)
	}
	if again.PairedAt != first.PairedAt {
		t.Fatalf("re-pair reported paired_at %q, want the stored %q", again.PairedAt, first.PairedAt)
	}
	// pair.complete is written in the storing transaction with the stored trust.
	evs, err := log.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, e := range evs {
		if e.Action == ActionPairComplete {
			rows = append(rows, string(e.Detail))
		}
	}
	if len(rows) != 2 || !strings.Contains(rows[1], `"id":"p-2"`) || !strings.Contains(rows[1], `"trust":"`+TrustFingerprint+`"`) {
		t.Fatalf("pair.complete rows = %v, want the re-pair row with the stored trust", rows)
	}
}
