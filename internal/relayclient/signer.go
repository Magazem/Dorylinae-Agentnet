package relayclient

import (
	"crypto/ed25519"
)

// Signer signs relay challenges with the daemon's identity key. The daemon's
// signer is its one verified identity key (internal/daemon identityKey):
// there is no signer that reads the keystore on its own (review 87b N2).
type Signer interface {
	// Public is the identity public key.
	Public() ed25519.PublicKey
	// Sign returns an Ed25519 signature of msg by that key.
	Sign(msg []byte) ([]byte, error)
}

type keySigner struct{ priv ed25519.PrivateKey }

// NewKeySigner signs with an in-memory private key. Intended for tests.
func NewKeySigner(priv ed25519.PrivateKey) Signer { return keySigner{priv} }

func (k keySigner) Public() ed25519.PublicKey { return k.priv.Public().(ed25519.PublicKey) }

func (k keySigner) Sign(msg []byte) ([]byte, error) { return ed25519.Sign(k.priv, msg), nil }
