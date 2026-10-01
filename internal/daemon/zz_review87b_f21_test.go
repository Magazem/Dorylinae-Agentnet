package daemon

// Review 87b proof: with the card present, the keychain holding the matching
// key and a stray identity.key holding another, identity.LoadOrCreate accepts
// the keychain copy (Docs/protocol/agent-card.md lifecycle table), but the
// daemon's session manager signs its Noise binding through
// relayclient.NewKeystoreSigner, whose plain Store.Load returns ErrConflict:
// start-up fails. Fails while the finding stands.

import (
	"encoding/json"
	"errors"
	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"

	"time"

	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func TestReview87bStrayKeyFileBlocksSessions(t *testing.T) {
	keyring.MockInit()
	defer keyring.MockInit()
	dir := testutil.TempDir(t)
	ks, err := identity.NewKeystore(dir, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if id, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, testNow87b); err != nil || id.KeyBackend() != "keychain" {
		t.Fatalf("setup: %v", err)
	}
	_, stray, _ := ed25519.GenerateKey(rand.Reader)
	if err := keystore.NewFile(filepath.Join(dir, identity.KeyFile)).Set(stray.Seed()); err != nil {
		t.Fatal(err)
	}
	id, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, testNow87b)
	if err != nil {
		t.Fatalf("identity with stray file: %v", err)
	}
	if _, err := newSessions(id, ks, nil, nil, Options{}, func(string) {}); err != nil {
		t.Fatalf("identity accepted, but sessions fail: %v", err)
	}
}

var testNow87b = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

// With the keychain locked, the plain Load in the Noise signer returns the
// only readable copy, so a replaced card + planted key file passes the whole
// identity start path (LoadOrCreate, then newSessions): end-to-end
// substitution. Fails while the finding stands.
func TestReview87bReplacedCardLockedKeychainEndToEnd(t *testing.T) {
	keyring.MockInit()
	defer keyring.MockInit()
	dir := testutil.TempDir(t)
	ks, err := identity.NewKeystore(dir, "auto")
	if err != nil {
		t.Fatal(err)
	}
	real, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, testNow87b)
	if err != nil || real.KeyBackend() != "keychain" {
		t.Fatalf("setup: %v", err)
	}
	apub, apriv, _ := ed25519.GenerateKey(rand.Reader)
	if err := keystore.NewFile(filepath.Join(dir, identity.KeyFile)).Set(apriv.Seed()); err != nil {
		t.Fatal(err)
	}
	card, _ := agentcard.New(apub, "evil", "custom", nil, testNow87b)
	signed, _ := agentcard.Sign(apriv, card)
	raw, _ := json.Marshal(signed)
	if err := keystore.WriteOwnerOnly(filepath.Join(dir, identity.CardFile), raw); err != nil {
		t.Fatal(err)
	}
	keyring.MockInitWithError(errors.New("keychain locked"))
	id, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, testNow87b)
	if err != nil {
		return // refused: fixed
	}
	if _, err := newSessions(id, ks, nil, nil, Options{}, func(string) {}); err == nil && id.Card().Card.PublicKey != real.Card().Card.PublicKey {
		t.Fatalf("daemon identity path runs as the planted key %s (real %s)", id.Card().Card.PublicKey, real.Card().Card.PublicKey)
	}
}
