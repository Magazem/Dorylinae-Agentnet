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
	// ErrUnavailable means a backend cannot be used now (a locked keychain, a
	// timeout, or no keychain service at all; see ErrNoService).
	ErrUnavailable = errors.New("keystore: backend unavailable")
	// ErrNoService wraps ErrUnavailable: the host has no keychain service at
	// all (no D-Bus session bus or Secret Service, an unsupported platform),
	// so nothing can be stored there. Unlike a locked keychain it cannot be
	// holding a secret this host can reach (review 87 M2, L2).
	ErrNoService = fmt.Errorf("%w: no keychain service on this host", ErrUnavailable)
	// ErrConflict means backends hold different secrets and nothing told
	// Load which one is right (review 87 M1).
	ErrConflict = errors.New("keystore: the backends hold different secrets")
	// ErrMismatch means the secret is stored but no copy matches what the
	// caller expects (the agent card, the mailbox row, the stored hash).
	ErrMismatch = errors.New("keystore: no stored copy matches")
)

// Backend is one place a secret can live. Get returns ErrNotFound or an error
// wrapping ErrUnavailable for expected absences; any other error means the
// backend's copy is unusable (for example a key file others can read).
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

// Locator is implemented by backends that can say where their copy lives (a
// file path, a keychain entry), for error messages.
type Locator interface {
	Location() string
}

// Copy is the secret as one backend holds it.
type Copy struct {
	Backend string
	// Location says where the copy is (a file path, a keychain entry), or
	// repeats Backend when the backend cannot say.
	Location string
	Secret   []byte
}

// Contents is what every backend of a Store returned.
type Contents struct {
	// Copies are the secrets found, most preferred backend first.
	Copies []Copy
	// Unavailable are the backends that may hold the secret but could not be
	// read now (a locked keychain, a timeout).
	Unavailable []error
	// NoService are the keychain backends the host does not have.
	NoService []error
	// Broken are the backends whose copy is unusable (a key file others can
	// read, a corrupt entry).
	Broken []error
	// Unread names the backends counted in Unavailable, NoService or Broken:
	// those that did not say whether, or what, they hold.
	Unread []string
}

// Clear wipes every copy.
func (c Contents) Clear() {
	for _, cp := range c.Copies {
		clear(cp.Secret)
	}
}

// Distinct reports whether the copies hold more than one different secret.
func (c Contents) Distinct() bool {
	for _, cp := range c.Copies[min(1, len(c.Copies)):] {
		if string(cp.Secret) != string(c.Copies[0].Secret) {
			return true
		}
	}
	return false
}

// Read reads every backend.
func (s *Store) Read() Contents {
	var c Contents
	for _, b := range s.backends {
		v, err := b.Get()
		switch {
		case err == nil:
			c.Copies = append(c.Copies, Copy{Backend: b.Name(), Location: location(b), Secret: v})
		case errors.Is(err, ErrNotFound):
		case errors.Is(err, ErrNoService):
			c.NoService = append(c.NoService, fmt.Errorf("%s: %w", b.Name(), err))
			c.Unread = append(c.Unread, b.Name())
		case errors.Is(err, ErrUnavailable):
			c.Unavailable = append(c.Unavailable, fmt.Errorf("%s: %w", b.Name(), err))
			c.Unread = append(c.Unread, b.Name())
		default:
			c.Broken = append(c.Broken, fmt.Errorf("keystore: read %s: %w", b.Name(), err))
			c.Unread = append(c.Unread, b.Name())
		}
	}
	return c
}

func location(b Backend) string {
	if l, ok := b.(Locator); ok {
		return b.Name() + " " + l.Location()
	}
	return b.Name()
}

// Load is LoadMatching without a check: it returns the secret only when every
// copy found is the same.
func (s *Store) Load() (secret []byte, backend string, err error) { return s.LoadMatching(nil) }

// Backends names the backends of s, most preferred first.
func (s *Store) Backends() []string {
	names := make([]string, len(s.backends))
	for i, b := range s.backends {
		names[i] = b.Name()
	}
	return names
}

// LoadMatching returns the secret and the name of the backend that held it.
// It reads every backend and returns the first copy that match accepts (any
// copy when match is nil). Backends are never ranked against each other: a
// copy is chosen only because it matches data the caller already trusts (the
// agent card, the mailbox row, the stored hash of the webhook secret), so a
// process that can write files but not the keychain cannot substitute its own
// secret (review 87 M1). Then:
//
//   - With match nil, two different copies are ErrConflict.
//   - A broken backend (a key file others can read) does not hide a copy that
//     match accepts (review 87 L3); otherwise its error is returned, naming
//     the backends that do hold a copy.
//   - If no copy is accepted, the error wraps ErrUnavailable when a backend
//     could not be read now (it may hold the secret: an outage is not an
//     absence; review 55 R55-092), ErrMismatch when copies exist but none
//     matches, and ErrNotFound otherwise. A host without a keychain service
//     counts as an absence (review 87 L2).
func (s *Store) LoadMatching(match func([]byte) bool) (secret []byte, backend string, err error) {
	c := s.Read()
	defer c.Clear()
	return c.Pick(match)
}

// Pick is LoadMatching over contents already read. The secret returned is a
// copy the caller owns.
func (c Contents) Pick(match func([]byte) bool) (secret []byte, backend string, err error) {
	if match == nil && c.Distinct() {
		// Nothing says which copy is right, so each is named with where it
		// lives (review 87b N2).
		return nil, "", fmt.Errorf("%w (%s), and nothing says which one is right; keep the right copy and delete the other", ErrConflict, strings.Join(c.Locations(), "; "))
	}
	for _, cp := range c.Copies {
		// Without a check, a broken backend may hold the right secret.
		if match == nil && len(c.Broken) == 0 || match != nil && match(cp.Secret) {
			return append([]byte(nil), cp.Secret...), cp.Backend, nil
		}
	}
	switch {
	case len(c.Broken) > 0:
		err := errors.Join(c.Broken...)
		if names := c.backendNames(); len(names) > 0 {
			return nil, "", fmt.Errorf("%w; a copy is also held by %s: if that is the right one, delete the unusable copy", err, strings.Join(names, ", "))
		}
		return nil, "", err
	case len(c.Unavailable) > 0:
		what := "secret not found in the other backends"
		if len(c.Copies) > 0 {
			what = "no copy in the other backends matches"
		}
		return nil, "", fmt.Errorf("%w: %s (skipped %s)", ErrUnavailable, what, joinErrs(c.Unavailable))
	case len(c.Copies) > 0:
		return nil, "", fmt.Errorf("%w (held by %s)", ErrMismatch, strings.Join(c.backendNames(), ", "))
	case len(c.NoService) > 0:
		return nil, "", fmt.Errorf("%w (%s)", ErrNotFound, joinErrs(c.NoService))
	}
	return nil, "", ErrNotFound
}

func (c Contents) backendNames() []string {
	names := make([]string, len(c.Copies))
	for i, cp := range c.Copies {
		names[i] = cp.Backend
	}
	return names
}

// Locations says where each copy lives, most preferred backend first.
func (c Contents) Locations() []string {
	locs := make([]string, len(c.Copies))
	for i, cp := range c.Copies {
		locs[i] = cp.Location
	}
	return locs
}

func joinErrs(errs []error) string {
	s := make([]string, len(errs))
	for i, e := range errs {
		s[i] = e.Error()
	}
	return strings.Join(s, "; ")
}

// Save writes the secret to the first backend that accepts it and reads back
// identically, then removes the copies in the other backends (R55-092), so
// Load does not meet a stale one. It returns that backend's name and the
// failures of the more preferred backends it skipped. If a copy in a less
// preferred backend cannot be removed, the secret is saved but Save returns
// an error. A more preferred copy that cannot be removed now (the keychain
// is locked) stays: Load then tells the two apart with the caller's check.
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
// that is already absent is not an error, nor is a host without a keychain
// service (nothing can be stored there). A keychain that is only locked or
// timed out may still hold the secret, so that is an error and the caller
// retries later (review 87 M2: the mailbox marks a key deleted only once
// Delete succeeds). Every backend is tried before the failures are returned.
func (s *Store) Delete() error { return s.DeleteSaved("") }

// DeleteSaved is Delete for a secret that Save put in the backend named
// savedIn. "No keychain service" is judged by this process (a daemon started
// without the desktop session's D-Bus bus sees none), so it is skipped only
// for the other backends: when savedIn itself reports no service, the secret
// may still be there and the delete fails, to be retried (review 87b N3).
// An empty savedIn (not recorded) is Delete.
func (s *Store) DeleteSaved(savedIn string) error {
	var errs []error
	for _, b := range s.backends {
		d, ok := b.(Deleter)
		if !ok {
			continue
		}
		err := d.Delete()
		if errors.Is(err, ErrNoService) && savedIn != "" && b.Name() == savedIn {
			errs = append(errs, fmt.Errorf("keystore: delete from %s, where the secret was saved: %w", b.Name(), err))
			continue
		}
		if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrNoService) {
			errs = append(errs, fmt.Errorf("keystore: delete from %s: %w", b.Name(), err))
		}
	}
	return errors.Join(errs...)
}
