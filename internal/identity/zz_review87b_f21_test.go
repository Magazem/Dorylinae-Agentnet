package identity_test

// Review 87b proof (R55-F21re-review): a file-only writer that REPLACES the
// agent card (instead of deleting it) with a card self-signed by its own key,
// and plants the matching identity.key, is adopted even though the keychain
// answered and holds the real, different key. Fails while the finding stands.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func TestReview87bReplacedCardSubstitutesIdentity(t *testing.T) {
	keyring.MockInit()
	defer keyring.MockInit()
	dir := testutil.TempDir(t)
	ks, err := identity.NewKeystore(dir, "auto")
	if err != nil {
		t.Fatal(err)
	}
	id, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if id.KeyBackend() != "keychain" {
		t.Skipf("key went to %s", id.KeyBackend())
	}
	real := id.Card().Card.PublicKey

	// Attacker: file writes only. Plant its key file and a card it signs itself.
	apub, apriv, _ := ed25519.GenerateKey(rand.Reader)
	if err := keystore.NewFile(filepath.Join(dir, identity.KeyFile)).Set(apriv.Seed()); err != nil {
		t.Fatal(err)
	}
	card, err := agentcard.New(apub, "evil", "custom", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := agentcard.Sign(apriv, card)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(signed)
	if err := keystore.WriteOwnerOnly(filepath.Join(dir, identity.CardFile), raw); err != nil {
		t.Fatal(err)
	}

	id2, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now)
	if err == nil && id2.Card().Card.PublicKey != real {
		t.Fatalf("identity substituted: keychain (answered) holds %s, daemon runs as %s from %s",
			real, id2.Card().Card.PublicKey, id2.KeyBackend())
	}
}
