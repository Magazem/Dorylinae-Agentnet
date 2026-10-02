package keystore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
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
func KeychainFor(prefix, dir string) *Keychain { return KeychainEntriesFor(prefix, dir).Entry("") }

// KeychainEntries names the keychain entries prefix+<hash>+suffix of one home,
// for stores that keep several entries per home (the mailbox keys). The
// accounts are derived once, so a later failure to resolve the dir cannot
// switch an entry to another account mid-run (review 60b F8b-05).
type KeychainEntries struct {
	account, legacy string
}

// KeychainEntriesFor returns the entries of dir under prefix, with the same
// account and fallback as KeychainFor.
func KeychainEntriesFor(prefix, dir string) KeychainEntries {
	return KeychainEntries{account: accountFor(prefix, paths.Canonical(dir)), legacy: accountFor(prefix, dir)}
}

// Entry returns the keychain backend for the account prefix+<hash>+suffix.
func (e KeychainEntries) Entry(suffix string) *Keychain {
	k := &Keychain{account: e.account + suffix}
	if e.legacy != e.account {
		k.legacy = []string{e.legacy + suffix}
	}
	return k
}

// NewKeychain returns a keychain backend for account.
func NewKeychain(account string) *Keychain { return &Keychain{account: account} }

// Name implements Backend.
func (*Keychain) Name() string { return "keychain" }

// Location implements Locator: the service and account of the entry.
func (k *Keychain) Location() string { return "entry " + Service + "/" + k.account }

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
		return nil, unavailable(err)
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
// returns ErrNotFound when none held it. Other failures wrap ErrUnavailable,
// like Get's, and ErrNoService when the host has no keychain service
// (review 55 R55-092, review 87 M2).
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
		return unavailable(err)
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

// unavailable wraps a keychain failure in ErrNoService when it says the host
// has no keychain service at all, and in ErrUnavailable otherwise (a locked
// keychain, a dismissed unlock prompt, a timeout): those may still hold the
// secret (review 87 M2, L2). Only failures that cannot mean "locked" count as
// no service.
func unavailable(err error) error {
	if noService(err) {
		return fmt.Errorf("%w: %w", ErrNoService, err)
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

// noServiceSigns are the messages of go-keyring and godbus when the host has
// no D-Bus session bus or no Secret Service on it.
var noServiceSigns = []string{
	"couldn't determine address of session bus",
	"org.freedesktop.DBus.Error.ServiceUnknown",
	"was not provided by any .service files",
	"dbus-launch",
}

func noService(err error) bool {
	if errors.Is(err, keyring.ErrUnsupportedPlatform) {
		return true
	}
	msg := err.Error()
	for _, sign := range noServiceSigns {
		if strings.Contains(msg, sign) {
			return true
		}
	}
	return false
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
