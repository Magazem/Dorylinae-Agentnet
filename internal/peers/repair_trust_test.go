package peers

import (
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// R55-118: after a re-pair at a lower trust, the Peer reported to the CLI and
// the pair.complete audit is the stored (higher) trust, not the requested one.
func TestStoreReportsStoredTrustOnRepair(t *testing.T) {
	m, _, _, _ := newRevManager(t, nil)
	id := newRevIdent(t, "peer")
	sc, err := agentcard.Verify(id.card)
	if err != nil {
		t.Fatal(err)
	}
	first, fail := m.store(sc, id.card, TrustFingerprint, nil)
	if fail != nil {
		t.Fatal(fail.Message)
	}
	again, fail := m.store(sc, id.card, TrustCode, nil)
	if fail != nil {
		t.Fatal(fail.Message)
	}
	if again.Trust != TrustFingerprint {
		t.Fatalf("re-pair reported trust %q, want the stored %q", again.Trust, TrustFingerprint)
	}
	if again.PairedAt != first.PairedAt {
		t.Fatalf("re-pair reported paired_at %q, want the stored %q", again.PairedAt, first.PairedAt)
	}
}
