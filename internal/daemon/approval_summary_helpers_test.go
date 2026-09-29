package daemon

import (
	"crypto/ed25519"
	"crypto/sha256"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approvaltext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/device"
	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

// summaryTestPeer is a paired peer named name, with a key derived from seed.
func summaryTestPeer(seed, name string) approvaltext.Peer {
	s := sha256.Sum256([]byte(seed))
	pub := ed25519.NewKeyFromSeed(s[:]).Public().(ed25519.PublicKey)
	return approvaltext.Peer{Key: envelope.KeyString(pub), Name: name, Paired: true}
}

// scopeSummaryFor builds a scope's approval summary as device_scope_set does.
func scopeSummaryFor(t *testing.T, name string, sc device.Scope) (string, error) {
	t.Helper()
	f, err := scopeFacts(summaryTestPeer("controller", name), sc)
	if err != nil {
		t.Fatal(err)
	}
	return approvaltext.BuildScope(f)
}

// constraintSummaryFor builds a debate constraint's approval summary as
// debate_constrain does.
func constraintSummaryFor(name, sid, text string) (string, error) {
	return approvaltext.BuildConstraint(approvaltext.Constraint{Session: sid, Peer: summaryTestPeer("debater", name), ID: "c-1", Text: text})
}

func displayQuoteForTest(s string) string { return displaytext.Quote(s) }
