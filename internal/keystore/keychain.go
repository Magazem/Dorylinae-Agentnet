package keystore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/paths"
	"github.com/zalando/go-keyring"
)

// Service is the keychain service name.
const Service = "dorylinae"

// keychainTimeout bounds a keychain call; a locked or absent Secret Service can hang.
const keychainTimeout = 5 * time.Second

// Keychain keeps the secret in the OS keychain (Windows Credential Manager,
// macOS Keychain, Linux Secret Service).
type Keychain struct {
	account string
	// legacy are older account names for the same entry, read when account
	// has none.
	legacy []string
}

// AccountFor derives the keychain account for a config directory so separate
// homes keep separate keys. It hashes paths.Canonical(dir), so two spellings
// of one directory share one account.
func AccountFor(dir string) string { return accountFor("identity-", paths.Canonical(dir)) }

func accountFor(prefix, dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return prefix + hex.EncodeToString(sum[:8])
}

// KeychainFor returns the keychain backend for the entry prefix+<hash of the
// canonical dir>. Before review 55 (R55-088) the hash was of dir as spelled;
// when that differs, the old account stays as a fallback: Get finds a secret
// stored there and copies it to the new account, and Delete removes both, so
// an existing key is neither lost nor resurrected after a delete.
func KeychainFor(prefix, dir string) *Keychain {
	k := &Keychain{account: accountFor(prefix, paths.Canonical(dir))}
	if old := accountFor(prefix, dir); old != k.account {
		k.legacy = []string{old}
	}
	return k
}

// NewKeychain returns a keychain backend for account.
func NewKeychain(account string) *Keychain { return &Keychain{account: account} }

// Name implements Backend.
func (*Keychain) Name() string { return "keychain" }

// Get implements Backend.
func (k *Keychain) Get() ([]byte, error) {
	b, err := get(k.account)
	if !errors.Is(err, ErrNotFound) {
		return b, err
	}
	for _, old := range k.legacy {
		b, err := get(old)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// Best effort: the legacy entry still holds the secret if this fails.
		_ = k.Set(b)
		return b, nil
	}
	return nil, ErrNotFound
}

func get(account string) ([]byte, error) {
	var v string
	err := withTimeout(func() (err error) { v, err = keyring.Get(Service, account); return })
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	b, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return nil, errors.New("keychain entry is not valid base64url")
	}
	return b, nil
}

// Set implements Backend.
func (k *Keychain) Set(secret []byte) error {
	enc := base64.RawURLEncoding.EncodeToString(secret)
	return withTimeout(func() error { return keyring.Set(Service, k.account, enc) })
}

// Delete removes the entry, under its current and legacy accounts. It
// returns ErrNotFound when none held it.
func (k *Keychain) Delete() error {
	found := false
	var errs []error
	for _, account := range append([]string{k.account}, k.legacy...) {
		err := withTimeout(func() error { return keyring.Delete(Service, account) })
		switch {
		case err == nil:
			found = true
		case !errors.Is(err, keyring.ErrNotFound):
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

func withTimeout(f func() error) error {
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(keychainTimeout):
		return errors.New("keychain call timed out")
	}
}
