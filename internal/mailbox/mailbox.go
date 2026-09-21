// Package mailbox holds the daemon's own mailbox key (Docs/protocol/mail.md
// §Mailbox keys). It creates the first key on demand, which pairing needs, and
// keeps the private half in the keystore and the identity-signed announcement
// next to it. Rotation, retirement and deletion belong to ticket 1.0b.
package mailbox

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

const (
	// Dir is the directory, inside the config dir, of the mailbox key files.
	Dir = "mailbox"
	// currentFile holds the canonical signed announcement of the current key.
	currentFile = "current.json"
	// renewBefore is how long before not_after the current key stops being offered.
	renewBefore = time.Hour
)

// Keys is the daemon's own mailbox key store.
type Keys struct {
	dir      string
	acctHex  string
	mode     string
	identity ed25519.PublicKey
	sign     func(msg []byte) ([]byte, error)
	now      func() time.Time

	mu sync.Mutex
}

// New returns the mailbox keys stored under configDir. mode is "auto" (OS
// keychain, then file) or "file" and selects the private key storage like
// DORYLINAE_KEYSTORE does for the identity. sign signs with the identity key
// whose public half is identity. A nil now means time.Now.
func New(configDir, mode string, identity ed25519.PublicKey, sign func([]byte) ([]byte, error), now func() time.Time) *Keys {
	if now == nil {
		now = time.Now
	}
	return &Keys{
		dir:      filepath.Join(configDir, Dir),
		acctHex:  strings.TrimPrefix(keystore.AccountFor(configDir), "identity-"),
		mode:     mode,
		identity: identity,
		sign:     sign,
		now:      now,
	}
}

func (k *Keys) keystoreFor(keyID string) (*keystore.Store, error) {
	file := keystore.NewFile(filepath.Join(k.dir, keyID+".key"))
	switch k.mode {
	case "", "auto":
		return keystore.New(keystore.NewKeychain("mailbox-"+k.acctHex+"-"+keyID), file), nil
	case "file":
		return keystore.New(file), nil
	default:
		return nil, fmt.Errorf("mailbox: keystore mode must be \"auto\" or \"file\", got %q", k.mode)
	}
}

// Announcement returns the canonical signed announcement of the current
// mailbox key. If there is no usable key (none yet, the private key is gone,
// or the announcement expires within the hour) it creates a new one first.
// The private key is in the keystore before the announcement is returned.
func (k *Keys) Announcement() ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := k.now()
	if raw, err := os.ReadFile(filepath.Join(k.dir, currentFile)); err == nil { //nolint:gosec // path is inside the daemon config dir
		if canon, ok := k.usable(raw, now); ok {
			return canon, nil
		}
	}
	return k.create(now)
}

// usable reports whether raw is a valid announcement of our identity, with a
// private key that is still in the keystore and matches it.
func (k *Keys) usable(raw []byte, now time.Time) ([]byte, bool) {
	ann, canon, err := mail.ParseAnnouncement(raw, envelope.KeyString(k.identity), now)
	if err != nil || !ann.NotAfter.After(now.Add(renewBefore)) {
		return nil, false
	}
	ks, err := k.keystoreFor(ann.KeyID.String())
	if err != nil {
		return nil, false
	}
	seed, _, err := ks.Load()
	if err != nil {
		return nil, false
	}
	defer clear(seed)
	priv, err := ecdh.X25519().NewPrivateKey(seed)
	if err != nil || string(priv.PublicKey().Bytes()) != string(ann.Pub) {
		return nil, false
	}
	return canon, true
}

func (k *Keys) create(now time.Time) ([]byte, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("mailbox: generate key: %w", err)
	}
	seed := priv.Bytes()
	defer clear(seed)
	pub := priv.PublicKey().Bytes()
	signed, err := mail.SignAnnouncement(k.identity, k.sign, pub, now)
	if err != nil {
		return nil, err
	}
	keyID := mail.KeyIDOf(pub).String()
	if err := os.MkdirAll(k.dir, 0o700); err != nil {
		return nil, fmt.Errorf("mailbox: create %s: %w", k.dir, err)
	}
	ks, err := k.keystoreFor(keyID)
	if err != nil {
		return nil, err
	}
	if _, _, err := ks.Save(seed); err != nil {
		return nil, fmt.Errorf("mailbox: store private key: %w", err)
	}
	// The private key is stored first, so a crash never leaves an announced key without it.
	if err := keystore.WriteOwnerOnly(filepath.Join(k.dir, currentFile), signed); err != nil {
		return nil, fmt.Errorf("mailbox: write announcement: %w", err)
	}
	return signed, nil
}
