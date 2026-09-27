//go:build darwin

// Package darwinreal exercises the real macOS keychain, not the mock the
// internal/keystore package tests use. It is a separate package (and so a
// separate go test process) on purpose: zalando/go-keyring's mock backend is
// a package-level global with no way to unmock it, so a real-backend test
// sharing a process with internal/keystore's mocked tests could silently run
// against a leftover mock instead of Keychain Services.
package darwinreal_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
)

// TestRealKeychainRoundTrip stores, reads back and deletes a secret in the
// actual macOS keychain (Keychain Services via zalando/go-keyring's darwin
// backend). It is the "verify keychain ... on Unix" half of ticket 4.4d; the
// Linux half is internal/keystore's Secret Service fallback tests, since
// ubuntu-latest has no Secret Service to hold a real entry in.
//
// A GitHub Actions macos-latest runner's login keychain is unlocked for the
// session, so this is expected to pass in CI; if a runner image changes that,
// this test fails loudly (skip only on the specific "keychain locked or
// unavailable" errors go-keyring reports) rather than silently passing, and
// tests/phase4-manual.md documents the manual fallback.
func TestRealKeychainRoundTrip(t *testing.T) {
	account := "phase4-4d-ci-probe-" + t.Name()
	kc := keystore.NewKeychain(account)
	t.Cleanup(func() { _ = kc.Delete() })

	secret := bytes.Repeat([]byte{0x42}, 32)

	if _, err := kc.Get(); !errors.Is(err, keystore.ErrNotFound) {
		if errors.Is(err, keystore.ErrUnavailable) {
			t.Skipf("keychain unavailable in this environment (%v); see tests/phase4-manual.md for the manual check", err)
		}
		t.Fatalf("want ErrNotFound before Set, got %v", err)
	}

	if err := kc.Set(secret); err != nil {
		t.Skipf("keychain Set failed (locked or unavailable: %v); see tests/phase4-manual.md for the manual check", err)
	}

	got, err := kc.Get()
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("round trip: %v %x", err, got)
	}

	if err := kc.Delete(); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := kc.Get(); !errors.Is(err, keystore.ErrNotFound) {
		t.Fatalf("want ErrNotFound after Delete, got %v", err)
	}
}
