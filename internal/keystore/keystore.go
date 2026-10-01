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

// Load returns the secret and the name of the backend that held it. It reads
// every backend: when two hold different secrets, the less preferred one wins,
// because it is the newer (Save removes the copies in less preferred backends,
// but cannot remove one from a backend that was unavailable when it fell back;
// review 55 R55-092). Backends that are unavailable are skipped. If no backend
// holds the secret, the error wraps ErrUnavailable when one was skipped (it
// may hold the secret: an outage is not an absence) and ErrNotFound otherwise.
func (s *Store) Load() (secret []byte, backend string, err error) {
	var skipped []string
	for _, b := range s.backends {
		v, err := b.Get()
		switch {
		case err == nil:
			if secret != nil && string(v) == string(secret) {
				clear(v)
				continue
			}
			clear(secret)
			secret, backend = v, b.Name()
		case errors.Is(err, ErrNotFound):
		case errors.Is(err, ErrUnavailable):
			skipped = append(skipped, fmt.Sprintf("%s: %v", b.Name(), err))
		default:
			clear(secret)
			return nil, "", fmt.Errorf("keystore: read %s: %w", b.Name(), err)
		}
	}
	switch {
	case secret != nil:
		return secret, backend, nil
	case len(skipped) > 0:
		return nil, "", fmt.Errorf("%w: secret not found in the other backends (skipped %s)", ErrUnavailable, strings.Join(skipped, "; "))
	}
	return nil, "", ErrNotFound
}

// Save writes the secret to the first backend that accepts it and reads back
// identically, then removes the copies in the other backends (R55-092): a
// less preferred copy must go, or Load would return it; a more preferred one
// is removed where it can be, and otherwise Load ranks it below this one. It
// returns that backend's name and the failures of the more preferred backends
// it skipped. If a less preferred copy cannot be removed, the secret is saved
// but Save returns an error.
func (s *Store) Save(secret []byte) (backend string, skipped []error, err error) {
	for i, b := range s.backends {
		if err := saveVerified(b, secret); err != nil {
			skipped = append(skipped, fmt.Errorf("%s: %w", b.Name(), err))
			continue
		}
		for j, o := range s.backends {
			d, ok := o.(Deleter)
			if j == i || !ok {
				continue
			}
			if err := d.Delete(); err != nil && !errors.Is(err, ErrNotFound) && j > i {
				return b.Name(), skipped, fmt.Errorf("keystore: saved to %s, but the older copy in %s remains: %w", b.Name(), o.Name(), err)
			}
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

// Deleter is implemented by backends that can remove their secret.
type Deleter interface {
	Delete() error
}

// Delete removes the secret from every backend that can delete it. A secret
// that is already absent is not an error. Unavailable backends (no keychain
// service, a locked keychain) are skipped: on a host without a keychain
// nothing was stored there. A copy in a keychain that was only locked stays
// there until the next Save overwrites or outranks it. Any other failure is
// returned after every backend was tried.
func (s *Store) Delete() error {
	var errs []error
	for _, b := range s.backends {
		d, ok := b.(Deleter)
		if !ok {
			continue
		}
		if err := d.Delete(); err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrUnavailable) {
			errs = append(errs, fmt.Errorf("keystore: delete from %s: %w", b.Name(), err))
		}
	}
	return errors.Join(errs...)
}
