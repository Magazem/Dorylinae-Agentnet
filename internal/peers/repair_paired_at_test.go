package peers

import (
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// R55-118, review 97 L2: a re-pair an hour later, at a lower trust, reports the
// stored (higher) trust and the first pairing's paired_at, not the request's.
func TestStoreReportsStoredTrustAndPairedAtOnRepair(t *testing.T) {
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	m, _, _, _ := newRevManager(t, func(c *Config) { c.Now = func() time.Time { return now } })
	id := newRevIdent(t, "peer")
	sc, err := agentcard.Verify(id.card)
	if err != nil {
		t.Fatal(err)
	}
	first, fail := m.store(sc, id.card, TrustFingerprint, nil, "p-1", RoleIssuer)
	if fail != nil {
		t.Fatal(fail.Message)
	}
	now = now.Add(time.Hour)
	again, fail := m.store(sc, id.card, TrustCode, nil, "p-2", RoleIssuer)
	if fail != nil {
		t.Fatal(fail.Message)
	}
	if again.Trust != TrustFingerprint {
		t.Fatalf("re-pair reported trust %q, want the stored %q", again.Trust, TrustFingerprint)
	}
	if first.PairedAt != "2026-03-04T05:06:07Z" || again.PairedAt != first.PairedAt {
		t.Fatalf("re-pair reported paired_at %q (first %q), want the first pairing's", again.PairedAt, first.PairedAt)
	}
}
