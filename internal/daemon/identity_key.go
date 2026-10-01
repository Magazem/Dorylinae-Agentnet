package daemon

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
)

// identityLoadRetry is how long after a failed keystore read the identity key
// is not read again (Docs/protocol/mail.md §Mailbox keys lifecycle).
const identityLoadRetry = time.Second

// identityKey is the daemon's identity private key, read from the keystore
// once and kept for the life of the process (R55-F13, review 55 R55-051): a
// keystore read is a `security` process on macOS and a D-Bus call on Linux,
// and the mail receiver, the outbox, the presence sender and the relay
// signer each used to read it on every use. Priv hands out a copy, because
// its callers clear the key after use. A failed read is not kept: the next
// use reads again, at most once per second.
type identityKey struct {
	ks  *keystore.Store
	pub ed25519.PublicKey // the agent card's key; the stored key must match
	now func() time.Time

	mu     sync.Mutex
	priv   ed25519.PrivateKey
	failed time.Time
	err    error
}

func newIdentityKey(ks *keystore.Store, pub ed25519.PublicKey) *identityKey {
	return &identityKey{ks: ks, pub: pub, now: time.Now}
}

// loadLocked returns the cached key, reading it on first use.
func (k *identityKey) loadLocked() (ed25519.PrivateKey, error) {
	if k.priv != nil {
		return k.priv, nil
	}
	now := k.now()
	if k.err != nil && now.Sub(k.failed) < identityLoadRetry && !now.Before(k.failed) {
		return nil, k.err
	}
	priv, err := k.read()
	if err != nil {
		k.failed, k.err = now, err
		return nil, err
	}
	k.priv, k.err = priv, nil
	return priv, nil
}

func (k *identityKey) read() (ed25519.PrivateKey, error) {
	if k.ks == nil {
		return nil, errors.New("no identity keystore")
	}
	seed, _, err := k.ks.Load()
	if err != nil {
		return nil, fmt.Errorf("load identity key: %w", err)
	}
	defer clear(seed)
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("stored identity key has the wrong length")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	if len(k.pub) == ed25519.PublicKeySize && !priv.Public().(ed25519.PublicKey).Equal(k.pub) {
		clear(priv)
		return nil, errors.New("stored identity key does not match the agent card")
	}
	return priv, nil
}

// Priv returns a copy of the identity private key, for callers (the outbox,
// the mail receiver and pusher, the presence sender, debates, grants) that
// clear it after use.
func (k *identityKey) Priv() (ed25519.PrivateKey, error) {
	if k == nil {
		return nil, errors.New("no identity key")
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	priv, err := k.loadLocked()
	if err != nil {
		return nil, err
	}
	return append(ed25519.PrivateKey(nil), priv...), nil
}

// Public implements relayclient.Signer.
func (k *identityKey) Public() ed25519.PublicKey { return k.pub }

// Sign implements relayclient.Signer (the relay challenge, the mailbox
// announcements) with the cached key.
func (k *identityKey) Sign(msg []byte) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	priv, err := k.loadLocked()
	if err != nil {
		return nil, err
	}
	return ed25519.Sign(priv, msg), nil
}
