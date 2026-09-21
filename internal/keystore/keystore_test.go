package keystore_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	path := filepath.Join(testutil.TempDir(t), "identity.key")
	f := keystore.NewFile(path)
	if err := f.Set(secret); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // deliberately too permissive
		t.Fatal(err)
	}
	if _, err := f.Get(); err == nil || errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("expected a permissions error, got %v", err)
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
	got, backend, err := s.Load()
	if err != nil || backend != "file" || !bytes.Equal(got, secret) {
		t.Fatalf("Load: %q %v", backend, err)
	}
}

func TestLoadNotFoundMentionsUnavailableKeychain(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	dir := testutil.TempDir(t)
	s := keystore.New(keystore.NewKeychain(keystore.AccountFor(dir)), keystore.NewFile(filepath.Join(dir, "k")))
	_, _, err := s.Load()
	if !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err.Error() == keystore.ErrNotFound.Error() {
		t.Fatal("error should say the keychain was skipped")
	}
}

func TestAccountsDifferPerDirectory(t *testing.T) {
	if keystore.AccountFor("/a") == keystore.AccountFor("/b") {
		t.Fatal("accounts must differ per config dir")
	}
}
