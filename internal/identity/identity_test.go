package identity_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/identity"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
)

var now = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func fileStore(t *testing.T, dir string) *keystore.Store {
	t.Helper()
	ks, err := identity.NewKeystore(dir, "file")
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

func TestFirstRunCreatesThenReuses(t *testing.T) {
	dir := t.TempDir()
	ks := fileStore(t, dir)

	id1, rep, err := identity.LoadOrCreate(dir, ks, identity.Options{Name: "alpha", Harness: "codex"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Created || !rep.Detail.KeyGenerated || rep.Detail.KeyBackend != "file" {
		t.Fatalf("unexpected report: %+v", rep)
	}
	c := id1.Card().Card
	if c.Name != "alpha" || c.Harness != "codex" || c.Created != "2026-03-04T05:06:07Z" || len(c.Skills) != 0 {
		t.Fatalf("unexpected card: %+v", c)
	}

	// Second start: same key, same card, no creation event.
	id2, rep2, err := identity.LoadOrCreate(dir, ks, identity.Options{Name: "ignored"}, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Created {
		t.Fatal("second start must not create")
	}
	if id2.Card().Signature != id1.Card().Signature || id2.Card().Card.PublicKey != id1.Card().Card.PublicKey {
		t.Fatal("second start changed the card")
	}
	if id2.Card().Card.Name != "alpha" {
		t.Fatal("card must be immutable after creation")
	}
}

func TestKeyMaterialOnlyInKeystore(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := identity.LoadOrCreate(dir, fileStore(t, dir), identity.Options{}, now); err != nil {
		t.Fatal(err)
	}
	seed, _, err := fileStore(t, dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	card, err := os.ReadFile(filepath.Join(dir, identity.CardFile)) //nolint:gosec // test reads its own temp dir
	if err != nil {
		t.Fatal(err)
	}
	for _, enc := range []string{base64.RawURLEncoding.EncodeToString(seed), base64.StdEncoding.EncodeToString(seed)} {
		if strings.Contains(string(card), enc) {
			t.Fatal("card file contains the seed")
		}
	}
	if err := keystore.OwnerOnly(filepath.Join(dir, identity.KeyFile)); err != nil {
		t.Fatal(err)
	}
}

func TestKeychainUsedWhenAvailable(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	ks, err := identity.NewKeystore(dir, "auto")
	if err != nil {
		t.Fatal(err)
	}
	id, rep, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if id.KeyBackend() != "keychain" || rep.Detail.KeychainError != "" {
		t.Fatalf("expected keychain, got %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(dir, identity.KeyFile)); !os.IsNotExist(err) {
		t.Fatal("key file must not exist when the keychain works")
	}
	id2, rep2, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now)
	if err != nil || rep2.Created || id2.Card().Card.PublicKey != id.Card().Card.PublicKey {
		t.Fatalf("reuse failed: %v %+v", err, rep2)
	}
}

func TestFallsBackToFileAndRecordsWhy(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	dir := t.TempDir()
	ks, _ := identity.NewKeystore(dir, "auto")
	id, rep, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if id.KeyBackend() != "file" || rep.Detail.KeychainError == "" {
		t.Fatalf("expected file fallback with reason, got %+v", rep)
	}
	if err := keystore.OwnerOnly(filepath.Join(dir, identity.KeyFile)); err != nil {
		t.Fatal(err)
	}
	// Restart with the keychain still down finds the file key.
	if _, rep2, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now); err != nil || rep2.Created {
		t.Fatalf("restart: %v %+v", err, rep2)
	}
}

// The two tests below build the "card without key" and "key without card"
// states directly instead of deleting freshly written files: on Windows a
// scanner can hold a just-deleted file open, leaving it delete-pending, and
// t.TempDir cleanup then fails with "directory is not empty".

func TestKeyLostIsAnErrorNotARotation(t *testing.T) {
	src := t.TempDir()
	if _, _, err := identity.LoadOrCreate(src, fileStore(t, src), identity.Options{}, now); err != nil {
		t.Fatal(err)
	}
	card, err := os.ReadFile(filepath.Join(src, identity.CardFile)) //nolint:gosec // test reads its own temp dir
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir() // a card, but no key
	if err := os.WriteFile(filepath.Join(dir, identity.CardFile), card, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = identity.LoadOrCreate(dir, fileStore(t, dir), identity.Options{}, now)
	if !errors.Is(err, identity.ErrKeyLost) {
		t.Fatalf("want ErrKeyLost, got %v", err)
	}
	// Nothing was overwritten and no key was created.
	if got, err := os.ReadFile(filepath.Join(dir, identity.CardFile)); err != nil || string(got) != string(card) { //nolint:gosec // test reads its own temp dir
		t.Fatalf("card changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, identity.KeyFile)); !os.IsNotExist(err) {
		t.Fatal("a key must not be created when the card exists")
	}
}

func TestMissingCardIsRecreatedForSameKey(t *testing.T) {
	dir := t.TempDir()
	ks := fileStore(t, dir)
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	if _, _, err := ks.Save(seed); err != nil { // a key, but no card
		t.Fatal(err)
	}
	id, rep, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now)
	if err != nil {
		t.Fatal(err)
	}
	wantPub := base64.RawURLEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey))
	if !rep.Created || rep.Detail.KeyGenerated || id.Card().Card.PublicKey != wantPub {
		t.Fatalf("unexpected: %+v", rep)
	}
}

func TestTamperedCardFileRefusesToStart(t *testing.T) {
	dir := t.TempDir()
	ks := fileStore(t, dir)
	if _, _, err := identity.LoadOrCreate(dir, ks, identity.Options{Name: "alpha"}, now); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, identity.CardFile)
	raw, _ := os.ReadFile(path) //nolint:gosec // test reads its own temp dir
	tampered := []byte(strings.Replace(string(raw), `"alpha"`, `"mallory"`, 1))
	if err := os.WriteFile(path, tampered, 0o600); err != nil { //nolint:gosec // test writes its own temp dir
		t.Fatal(err)
	}
	if _, _, err := identity.LoadOrCreate(dir, ks, identity.Options{}, now); err == nil {
		t.Fatal("tampered card must be rejected")
	}
	if got, _ := os.ReadFile(path); string(got) != string(tampered) { //nolint:gosec // test reads its own temp dir
		t.Fatal("tampered card must not be overwritten")
	}
}

func TestCardSignatureVerifies(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := identity.LoadOrCreate(dir, fileStore(t, dir), identity.Options{}, now); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, identity.CardFile)) //nolint:gosec // test reads its own temp dir
	if _, err := agentcard.Verify(raw); err != nil {
		t.Fatal(err)
	}
}

func TestNewKeystoreRejectsUnknownMode(t *testing.T) {
	if _, err := identity.NewKeystore(t.TempDir(), "cloud"); err == nil {
		t.Fatal("expected error")
	}
}
