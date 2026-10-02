package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

var sessionsTestNow = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

// startSessions runs the identity part of daemon start-up: LoadOrCreate,
// then the session manager signing its Noise binding through the one
// identity key (review 87b N2).
func startSessions(t *testing.T, dir string, ks *keystore.Store) (*identity.Identity, error) {
	t.Helper()
	id, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, sessionsTestNow)
	if err != nil {
		return nil, err
	}
	pub, err := envelope.ParseKey(id.Card().Card.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newSessions(id, newIdentityKey(dir, ks, pub).Sign, nil, nil, Options{}, func(string) {}); err != nil {
		return nil, err
	}
	return id, nil
}

// Review 87b N2: with the card present, the keychain holding the matching key
// and a stray identity.key holding another, start-up uses the keychain key
// (the agent-card.md lifecycle row) all the way: the session manager signs
// through the same verified key, not a second plain keystore read.
func TestStrayKeyFileDoesNotBlockSessions(t *testing.T) {
	keyring.MockInit()
	defer keyring.MockInit()
	dir := testutil.TempDir(t)
	ks, err := identity.NewKeystore(dir, "auto")
	if err != nil {
		t.Fatal(err)
	}
	realID, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, sessionsTestNow)
	if err != nil || realID.KeyBackend() != "keychain" {
		t.Fatalf("setup: %v", err)
	}
	_, stray, _ := ed25519.GenerateKey(rand.Reader)
	if err := keystore.NewFile(filepath.Join(dir, identity.KeyFile)).Set(stray.Seed()); err != nil {
		t.Fatal(err)
	}
	id, err := startSessions(t, dir, ks)
	if err != nil {
		t.Fatalf("start-up with a stray key file: %v", err)
	}
	if id.Card().Card.PublicKey != realID.Card().Card.PublicKey || id.KeyBackend() != "keychain" {
		t.Fatalf("runs as %s from %s, want the keychain identity", id.Card().Card.PublicKey, id.KeyBackend())
	}
}

// Review 87b N1: a process that can write files but not the keychain replaces
// agent-card.json with a card signed by its own key and plants that key in
// identity.key. With the keychain locked the copies cannot be compared, but
// this config dir has kept its key in the keychain, so start-up refuses
// instead of running as the planted key.
func TestReplacedCardLockedKeychainRefused(t *testing.T) {
	keyring.MockInit()
	defer keyring.MockInit()
	dir := testutil.TempDir(t)
	ks, err := identity.NewKeystore(dir, "auto")
	if err != nil {
		t.Fatal(err)
	}
	realID, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, sessionsTestNow)
	if err != nil || realID.KeyBackend() != "keychain" {
		t.Fatalf("setup: %v", err)
	}
	apub, apriv, _ := ed25519.GenerateKey(rand.Reader)
	if err := keystore.NewFile(filepath.Join(dir, identity.KeyFile)).Set(apriv.Seed()); err != nil {
		t.Fatal(err)
	}
	card, err := agentcard.New(apub, "evil", "custom", nil, sessionsTestNow)
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
	keyring.MockInitWithError(errors.New("keychain locked"))
	id, err := startSessions(t, dir, ks)
	if err == nil {
		t.Fatalf("start-up ran as %s (real %s)", id.Card().Card.PublicKey, realID.Card().Card.PublicKey)
	}
	if !errors.Is(err, keystore.ErrUnavailable) {
		t.Fatalf("err = %v, want one wrapping ErrUnavailable", err)
	}
}
