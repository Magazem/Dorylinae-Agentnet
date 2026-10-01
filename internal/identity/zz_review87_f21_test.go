package identity_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 87 finding 87-01 (evidence; fails while the finding stands): with the identity key
// in the keychain, a process that can only write files in the config dir
// (no keychain access) plants identity.key and removes the card; because the
// file now outranks the keychain on Load, the daemon adopts the planted key
// as its identity. Before R55-F21 the keychain copy won.
func TestReview87FilePlantOutranksKeychainIdentity(t *testing.T) {
	keyring.MockInit()
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
	_, planted, _ := ed25519.GenerateKey(rand.Reader)
	if err := keystore.NewFile(filepath.Join(dir, identity.KeyFile)).Set(planted.Seed()); err != nil {
		t.Fatal(err)
	}
	// With the card in place the mismatch stops start-up (denial of service).
	if _, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now); err == nil {
		t.Fatal("planted key accepted with the old card present")
	} else {
		t.Logf("card present: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, identity.CardFile)); err != nil {
		t.Fatal(err)
	}
	id2, rep, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now)
	if err != nil {
		t.Fatalf("fixed? %v", err)
	}
	plantedPub := base64.RawURLEncoding.EncodeToString(planted.Public().(ed25519.PublicKey))
	if id2.Card().Card.PublicKey == plantedPub {
		t.Fatalf("identity substituted: the card now carries the planted key (backend %s, generated=%v); the original key is still in the keychain",
			id2.KeyBackend(), rep.Detail.KeyGenerated)
	}
}
