// Package keystore stores one small secret (the identity seed) in the OS
// keychain when available, falling back to an owner-only file.
package keystore

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNotFound means the secret is not stored (in any backend, for Store.Load).
	ErrNotFound = errors.New("keystore: secret not found")
	// ErrUnavailable means a backend cannot be used at all (no keychain service, timeout).
	ErrUnavailable = errors.New("keystore: backend unavailable")
)

// Backend is one place a secret can live. Get returns ErrNotFound or an error
// wrapping ErrUnavailable for expected absences; any other error is fatal.
type Backend interface {
	Name() string
	Get() ([]byte, error)
	Set(secret []byte) error
}

// Store tries backends in order.
type Store struct {
	backends []Backend
}

// New returns a Store over backends, most preferred first.
func New(backends ...Backend) *Store { return &Store{backends: backends} }

// Load returns the secret and the name of the backend that held it. Backends
// that are unavailable are skipped; if nothing holds the secret the error
// wraps ErrNotFound and mentions any skipped backend.
func (s *Store) Load() (secret []byte, backend string, err error) {
	var skipped []string
	for _, b := range s.backends {
		v, err := b.Get()
		switch {
		case err == nil:
			return v, b.Name(), nil
		case errors.Is(err, ErrNotFound):
		case errors.Is(err, ErrUnavailable):
			skipped = append(skipped, fmt.Sprintf("%s: %v", b.Name(), err))
		default:
			return nil, "", fmt.Errorf("keystore: read %s: %w", b.Name(), err)
		}
	}
	if len(skipped) > 0 {
		return nil, "", fmt.Errorf("%w (skipped %s)", ErrNotFound, strings.Join(skipped, "; "))
	}
	return nil, "", ErrNotFound
}

// Save writes the secret to the first backend that accepts it and reads back
// identically. It returns that backend's name and the failures of the more
// preferred backends it skipped.
func (s *Store) Save(secret []byte) (backend string, skipped []error, err error) {
	for _, b := range s.backends {
		if err := saveVerified(b, secret); err != nil {
			skipped = append(skipped, fmt.Errorf("%s: %w", b.Name(), err))
			continue
		}
		return b.Name(), skipped, nil
	}
	return "", skipped, fmt.Errorf("keystore: no backend accepted the secret: %w", errors.Join(skipped...))
}

func saveVerified(b Backend, secret []byte) error {
	if err := b.Set(secret); err != nil {
		return err
	}
	got, err := b.Get()
	if err != nil {
		return fmt.Errorf("read back: %w", err)
	}
	if string(got) != string(secret) {
		return errors.New("read back a different value")
	}
	return nil
}
