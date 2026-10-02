package identity_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// keychainIdentity creates an identity whose key is in the (mock) keychain.
func keychainIdentity(t *testing.T) (string, *keystore.Store, string) {
	t.Helper()
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
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
		t.Fatalf("key went to %s", id.KeyBackend())
	}
	if _, err := os.Stat(filepath.Join(dir, identity.KeychainMarkerFile)); err != nil {
		t.Fatalf("no keychain marker after creating the key there: %v", err)
	}
	return dir, ks, id.Card().Card.PublicKey
}

// plant writes what a process that can write files but not the keychain can:
// its own identity.key and, when card is set, an agent card it signs itself.
func plant(t *testing.T, dir string, card bool) string {
	t.Helper()
	apub, apriv, _ := ed25519.GenerateKey(rand.Reader)
	if err := keystore.NewFile(filepath.Join(dir, identity.KeyFile)).Set(apriv.Seed()); err != nil {
		t.Fatal(err)
	}
	c, err := agentcard.New(apub, "evil", "custom", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if !card {
		return c.PublicKey
	}
	signed, err := agentcard.Sign(apriv, c)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(signed)
	if err := keystore.WriteOwnerOnly(filepath.Join(dir, identity.CardFile), raw); err != nil {
		t.Fatal(err)
	}
	return c.PublicKey
}

// Review 87b N1: the card is a self-signed file, as writable as identity.key.
// A replaced card plus a planted key file it matches does not win over a
// different key the keychain answered with: start-up refuses, and the error
// names both copies.
func TestReplacedCardDoesNotSubstituteIdentity(t *testing.T) {
	dir, ks, realKey := keychainIdentity(t)
	planted := plant(t, dir, true)
	id, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now)
	if err == nil {
		t.Fatalf("identity substituted: keychain holds %s, daemon runs as %s from %s", realKey, id.Card().Card.PublicKey, id.KeyBackend())
	}
	if !errors.Is(err, keystore.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	for _, want := range []string{filepath.Join(dir, identity.KeyFile), "keychain entry " + keystore.Service + "/" + keystore.AccountFor(dir), "delete the keychain entry"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
	if _, _, err := identity.LoadKey(dir, ks, planted); !errors.Is(err, keystore.ErrConflict) {
		t.Fatalf("LoadKey for the planted card = %v, want ErrConflict", err)
	}
}

// Without a marker the dir has never kept its key in the keychain (it fell
// back to the file at creation): a locked keychain does not stop the file key.
func TestFileFallbackKeyUsedWhileKeychainLocked(t *testing.T) {
	keyring.MockInitWithError(errors.New("keychain locked"))
	defer keyring.MockInit()
	dir := testutil.TempDir(t)
	ks, err := identity.NewKeystore(dir, "auto")
	if err != nil {
		t.Fatal(err)
	}
	id, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now)
	if err != nil || id.KeyBackend() != "file" {
		t.Fatalf("setup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, identity.KeychainMarkerFile)); !os.IsNotExist(err) {
		t.Fatalf("keychain marker written for a file key: %v", err)
	}
	id2, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now)
	if err != nil || id2.Card().Card.PublicKey != id.Card().Card.PublicKey || id2.KeyBackend() != "file" {
		t.Fatalf("file key with the keychain locked: %v", err)
	}
}

// With the marker, a key only in the file is not used while the keychain
// cannot be read, so it cannot be compared with the keychain's copy; with
// DORYLINAE_KEYSTORE=file the keychain is not consulted and it is.
func TestFileKeyRefusedWhileUsedKeychainUnreadable(t *testing.T) {
	dir, _, _ := keychainIdentity(t)
	planted := plant(t, dir, true)
	auto, err := identity.NewKeystore(dir, "auto")
	if err != nil {
		t.Fatal(err)
	}
	for name, kerr := range map[string]error{"locked": errors.New("keychain locked"), "no service": keyring.ErrUnsupportedPlatform} {
		keyring.MockInitWithError(kerr)
		_, _, err := identity.LoadOrCreate(dir, auto, identity.Options{}, now)
		if !errors.Is(err, keystore.ErrUnavailable) || !strings.Contains(err.Error(), identity.KeychainMarkerFile) {
			t.Fatalf("%s keychain: err = %v, want ErrUnavailable naming the marker", name, err)
		}
	}
	id, _, err := identity.LoadOrCreate(dir, fileStore(t, dir), identity.Options{}, now)
	if err != nil || id.Card().Card.PublicKey != planted {
		t.Fatalf("file mode: %v", err)
	}
}

// Review 87b N1, N3: with no card, a keychain this process sees no service for
// counts as not read once the dir has kept its key there: a planted file key
// is not adopted (a daemon started from ssh, cron or a unit without the
// desktop's D-Bus bus).
func TestNoCardNoServiceDoesNotAdoptFileKey(t *testing.T) {
	dir, ks, _ := keychainIdentity(t)
	if err := os.Remove(filepath.Join(dir, identity.CardFile)); err != nil {
		t.Fatal(err)
	}
	plant(t, dir, false)
	keyring.MockInitWithError(keyring.ErrUnsupportedPlatform)
	if _, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now); err == nil {
		t.Fatal("no card, no keychain service: the file key was adopted")
	}
	if _, err := os.Stat(filepath.Join(dir, identity.CardFile)); !os.IsNotExist(err) {
		t.Fatalf("a card was written: %v", err)
	}
}

// Review 87b N3: with the card present and the key missing while this process
// sees no keychain service, the error says the key may be in a keychain this
// session cannot reach before suggesting a new identity.
func TestKeyLostWithoutServiceMentionsUnreachableKeychain(t *testing.T) {
	dir, ks, _ := keychainIdentity(t)
	if err := os.Remove(filepath.Join(dir, identity.KeychainMarkerFile)); err != nil {
		t.Fatal(err)
	}
	keyring.MockInitWithError(keyring.ErrUnsupportedPlatform)
	_, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now)
	if !errors.Is(err, identity.ErrKeyLost) || !strings.Contains(err.Error(), "cannot reach") {
		t.Fatalf("err = %v, want ErrKeyLost mentioning an unreachable keychain", err)
	}
}

// Loading the key from the keychain writes the marker for a dir that has
// none yet (a dir from before the marker).
func TestKeychainLoadWritesMarker(t *testing.T) {
	dir, ks, _ := keychainIdentity(t)
	marker := filepath.Join(dir, identity.KeychainMarkerFile)
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if _, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker not written back: %v", err)
	}
}
