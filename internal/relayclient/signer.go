package relayclient

import (
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
)

// Signer signs relay challenges with the daemon's identity key.
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

type keystoreSigner struct {
	ks  *keystore.Store
	pub ed25519.PublicKey
}

// NewKeystoreSigner signs with the identity key held in ks. The key is read
// only for the duration of each signature and is never kept in memory. pub is
// the identity's public key; signing fails if the stored key does not match it.
func NewKeystoreSigner(ks *keystore.Store, pub ed25519.PublicKey) Signer {
	return keystoreSigner{ks: ks, pub: pub}
}

func (k keystoreSigner) Public() ed25519.PublicKey { return k.pub }

func (k keystoreSigner) Sign(msg []byte) ([]byte, error) {
	seed, _, err := k.ks.Load()
	if err != nil {
		return nil, fmt.Errorf("load identity key: %w", err)
	}
	defer clear(seed)
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("stored identity key has the wrong length")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	defer clear(priv)
	if !priv.Public().(ed25519.PublicKey).Equal(k.pub) {
		return nil, errors.New("stored identity key does not match the agent card")
	}
	return ed25519.Sign(priv, msg), nil
}
