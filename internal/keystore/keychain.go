package keystore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

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
}

// AccountFor derives the keychain account for a config directory so separate
// homes keep separate keys.
func AccountFor(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return "identity-" + hex.EncodeToString(sum[:8])
}

// NewKeychain returns a keychain backend for account.
func NewKeychain(account string) *Keychain { return &Keychain{account: account} }

// Name implements Backend.
func (*Keychain) Name() string { return "keychain" }

// Get implements Backend.
func (k *Keychain) Get() ([]byte, error) {
	var v string
	err := withTimeout(func() (err error) { v, err = keyring.Get(Service, k.account); return })
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

// Delete removes the entry (used by tests and manual cleanup).
func (k *Keychain) Delete() error {
	err := withTimeout(func() error { return keyring.Delete(Service, k.account) })
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
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
