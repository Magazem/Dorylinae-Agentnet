package keystore_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

var secret = bytes.Repeat([]byte{0xAB}, 32)

func TestFileRoundTripAndPermissions(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "identity.key")
	f := keystore.NewFile(path)
	if _, err := f.Get(); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := f.Set(secret); err != nil {
		t.Fatal(err)
	}
	got, err := f.Get()
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("round trip: %v %x", err, got)
	}
	if err := keystore.OwnerOnly(path); err != nil {
		t.Fatalf("key file is not owner-only: %v", err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
		}
	}
	// Overwrite keeps the restriction and leaves no temp files.
	if err := f.Set(bytes.Repeat([]byte{1}, 32)); err != nil {
		t.Fatal(err)
	}
	if err := keystore.OwnerOnly(path); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestFileRefusesBroadPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits do not apply on Windows")
	}
	// 0640 is group-readable only, 0604 is world-readable only, 0644 is both:
	// the check must reject any group or other access bit, not just the
	// common "wide open" case.
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(testutil.TempDir(t), "identity.key")
			f := keystore.NewFile(path)
			if err := f.Set(secret); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil { //nolint:gosec // deliberately too permissive, that is what this test checks
				t.Fatal(err)
			}
			if _, err := f.Get(); err == nil || errors.Is(err, keystore.ErrNotFound) {
				t.Fatalf("mode %v: expected a permissions error, got %v", mode, err)
			}
		})
	}
}

func TestFileRejectsGarbage(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "identity.key")
	if err := keystore.WriteOwnerOnly(path, []byte("!!! not base64 !!!")); err != nil {
		t.Fatal(err)
	}
	if _, err := keystore.NewFile(path).Get(); err == nil {
		t.Fatal("expected error")
	}
}

func TestKeychainPreferredWhenAvailable(t *testing.T) {
	keyring.MockInit()
	dir := testutil.TempDir(t)
	kc := keystore.NewKeychain(keystore.AccountFor(dir))
	fpath := filepath.Join(dir, "identity.key")
	s := keystore.New(kc, keystore.NewFile(fpath))

	if _, _, err := s.Load(); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	backend, skipped, err := s.Save(secret)
	if err != nil || backend != "keychain" || len(skipped) != 0 {
		t.Fatalf("Save: %q %v %v", backend, skipped, err)
	}
	if _, err := os.Stat(fpath); !os.IsNotExist(err) {
		t.Fatal("no key file should be written when the keychain works")
	}
	got, backend, err := s.Load()
	if err != nil || backend != "keychain" || !bytes.Equal(got, secret) {
		t.Fatalf("Load: %q %v", backend, err)
	}
}

// TestFileFallbackWhenKeychainUnavailable simulates the Secret Service (or
// any keychain backend) being absent - the case that only Windows exercised
// for real, since a keychain is always present there - and checks the file
// fallback's actual permission bits, not just that OwnerOnly's looser check
// (no group/other bits at all) accepted them.
func TestFileFallbackWhenKeychainUnavailable(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	dir := testutil.TempDir(t)
	fpath := filepath.Join(dir, "identity.key")
	s := keystore.New(keystore.NewKeychain(keystore.AccountFor(dir)), keystore.NewFile(fpath))

	backend, skipped, err := s.Save(secret)
	if err != nil || backend != "file" || len(skipped) != 1 {
		t.Fatalf("Save: %q %v %v", backend, skipped, err)
	}
	if err := keystore.OwnerOnly(fpath); err != nil {
		t.Fatalf("fallback file not restricted: %v", err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(fpath)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("fallback file mode = %v, want exactly 0600", fi.Mode().Perm())
		}
	}
	got, backend, err := s.Load()
	if err != nil || backend != "file" || !bytes.Equal(got, secret) {
		t.Fatalf("Load: %q %v", backend, err)
	}
}

// Review 55 R55-092 (C05-01): when the only other backend is empty, an
// unavailable keychain is reported as unavailable, not as "not found": it
// may hold the secret.
func TestLoadUnavailableKeychainIsNotNotFound(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	defer keyring.MockInit()
	dir := testutil.TempDir(t)
	s := keystore.New(keystore.NewKeychain(keystore.AccountFor(dir)), keystore.NewFile(filepath.Join(dir, "k")))
	_, _, err := s.Load()
	if !errors.Is(err, keystore.ErrUnavailable) || errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("want ErrUnavailable only, got %v", err)
	}
	if !strings.Contains(err.Error(), "keychain") {
		t.Fatalf("error should say the keychain was skipped: %v", err)
	}
}

func TestAccountsDifferPerDirectory(t *testing.T) {
	if keystore.AccountFor("/a") == keystore.AccountFor("/b") {
		t.Fatal("accounts must differ per config dir")
	}
}

// Review 55 R55-088: two spellings of one config dir share one keychain
// account.
func TestAccountSameForTwoSpellings(t *testing.T) {
	dir := testutil.TempDir(t)
	if a, b := keystore.AccountFor(dir), keystore.AccountFor(testutil.OtherSpelling(t, dir)); a != b {
		t.Fatalf("two accounts for one dir: %s, %s", a, b)
	}
}

// A key stored before R55-088 under the account of the dir as spelled is
// still found, copied to the canonical account, and deleted from both.
func TestKeychainLegacyAccountFallback(t *testing.T) {
	keyring.MockInit()
	dir := testutil.TempDir(t)
	spelled := testutil.OtherSpelling(t, dir)
	sum := sha256.Sum256([]byte(spelled))
	legacy := "identity-" + hex.EncodeToString(sum[:8])
	if legacy == keystore.AccountFor(spelled) {
		t.Skip("spelling is already canonical")
	}
	if err := keystore.NewKeychain(legacy).Set(secret); err != nil {
		t.Fatal(err)
	}
	kc := keystore.KeychainFor("identity-", spelled)
	got, err := kc.Get()
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("legacy key not found: %v", err)
	}
	if got, err := keystore.NewKeychain(keystore.AccountFor(dir)).Get(); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("key not copied to the canonical account: %v", err)
	}
	if err := kc.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.Get(); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("deleted key came back: %v", err)
	}
	if _, err := keystore.NewKeychain(legacy).Get(); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("legacy entry survived Delete: %v", err)
	}
}

// Review 60b F8b-05: KeychainEntries derives the accounts once and gives each
// entry the account and legacy fallback KeychainFor would, plus the suffix.
func TestKeychainEntriesSuffix(t *testing.T) {
	keyring.MockInit()
	dir := testutil.TempDir(t)
	spelled := testutil.OtherSpelling(t, dir)
	e := keystore.KeychainEntriesFor("mailbox-", spelled)
	base := keystore.KeychainFor("mailbox-", spelled).Location()
	if got := e.Entry("-k1").Location(); got != base+"-k1" {
		t.Fatalf("Entry location = %s, want %s-k1", got, base)
	}
	sum := sha256.Sum256([]byte(spelled))
	legacy := "mailbox-" + hex.EncodeToString(sum[:8]) + "-k1"
	if "entry "+keystore.Service+"/"+legacy == base+"-k1" {
		t.Skip("spelling is already canonical")
	}
	if err := keystore.NewKeychain(legacy).Set(secret); err != nil {
		t.Fatal(err)
	}
	if got, err := e.Entry("-k1").Get(); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("legacy entry not found: %v", err)
	}
	if _, err := e.Entry("-k2").Get(); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("other suffix: %v, want ErrNotFound", err)
	}
}
