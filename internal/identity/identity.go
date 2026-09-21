// Package identity manages the Ed25519 agent identity: generating the keypair
// on first run, keeping the private key in the keystore, and keeping the signed
// Agent Card (docs: Docs/protocol/agent-card.md).
//
// The private key exists in memory only while a card is being created. Nothing
// in this package hands it to callers.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
)

// ActionCreate is the audit action recorded when an identity is created.
const ActionCreate = "identity.create"

const (
	// KeyFile is the file-fallback key location inside the config dir.
	KeyFile = "identity.key"
	// CardFile caches the signed Agent Card inside the config dir.
	CardFile = "agent-card.json"

	// KeystoreEnv selects the key storage: "auto" (default) or "file".
	KeystoreEnv = "DORYLINAE_KEYSTORE"
	// NameEnv sets the card name at creation time.
	NameEnv = "DORYLINAE_AGENT_NAME"
	// HarnessEnv sets the card harness at creation time.
	HarnessEnv = "DORYLINAE_HARNESS"

	defaultHarness = "custom"
)

// Options are the card fields chosen at creation. Zero values use defaults:
// the hostname, harness "custom" and no skills.
type Options struct {
	Name    string
	Harness string
	Skills  []agentcard.Skill
}

// OptionsFromEnv reads Options from the environment.
func OptionsFromEnv() Options {
	return Options{Name: os.Getenv(NameEnv), Harness: os.Getenv(HarnessEnv)}
}

// NewKeystore builds the key storage for the config dir dir. mode is "auto"
// (keychain, then file) or "file"; empty means "auto".
func NewKeystore(dir, mode string) (*keystore.Store, error) {
	file := keystore.NewFile(filepath.Join(dir, KeyFile))
	switch mode {
	case "", "auto":
		return keystore.New(keystore.NewKeychain(keystore.AccountFor(dir)), file), nil
	case "file":
		return keystore.New(file), nil
	default:
		return nil, fmt.Errorf("%s must be \"auto\" or \"file\", got %q", KeystoreEnv, mode)
	}
}

// NewKeystoreFromEnv is NewKeystore with the mode taken from KeystoreEnv.
func NewKeystoreFromEnv(dir string) (*keystore.Store, error) {
	return NewKeystore(dir, os.Getenv(KeystoreEnv))
}

// Identity is a loaded identity: the public, signed card and where the key lives.
type Identity struct {
	card       agentcard.Signed
	keyBackend string
}

// Card returns the signed Agent Card.
func (i *Identity) Card() agentcard.Signed { return i.card }

// KeyBackend names where the private key is stored ("keychain" or "file").
func (i *Identity) KeyBackend() string { return i.keyBackend }

// CreateDetail is the audit detail of ActionCreate. It holds no secret.
type CreateDetail struct {
	PublicKey     string `json:"public_key"`
	KeyBackend    string `json:"key_backend"`
	KeyGenerated  bool   `json:"key_generated"`
	KeychainError string `json:"keychain_error,omitempty"`
}

// Report says what LoadOrCreate did.
type Report struct {
	// Created is true when a new card was made (with or without a new key).
	Created bool
	// Detail is the audit detail to record when Created is true.
	Detail CreateDetail
}

// ErrKeyLost means an Agent Card exists but its private key cannot be found.
var ErrKeyLost = errors.New("identity: agent card exists but its private key was not found")

// LoadOrCreate returns the identity stored under dir, creating it on first run.
// See the lifecycle table in Docs/protocol/agent-card.md.
func LoadOrCreate(dir string, ks *keystore.Store, opts Options, now time.Time) (*Identity, Report, error) {
	cardPath := filepath.Join(dir, CardFile)
	existing, cardErr := readCard(cardPath)
	if cardErr != nil && !errors.Is(cardErr, os.ErrNotExist) {
		return nil, Report{}, fmt.Errorf("identity: %s is not a valid signed card (delete it to start a new identity): %w", cardPath, cardErr)
	}
	haveCard := cardErr == nil

	seed, backend, err := ks.Load()
	switch {
	case err == nil:
		priv, perr := privFromSeed(seed)
		if perr != nil {
			return nil, Report{}, perr
		}
		if haveCard {
			if existing.Card.PublicKey != pubString(priv) {
				return nil, Report{}, fmt.Errorf("identity: stored private key does not match the key in %s", cardPath)
			}
			return &Identity{card: *existing, keyBackend: backend}, Report{}, nil
		}
		// Key without a card (interrupted first run): re-create the card for the same key.
		return finish(cardPath, priv, backend, false, nil, opts, now)
	case errors.Is(err, keystore.ErrNotFound):
		if haveCard {
			return nil, Report{}, fmt.Errorf("%w (%w); restore it or delete %s to start a new identity", ErrKeyLost, err, cardPath)
		}
	default:
		return nil, Report{}, err
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, Report{}, fmt.Errorf("identity: generate key: %w", err)
	}
	backend, skipped, err := ks.Save(priv.Seed())
	if err != nil {
		return nil, Report{}, fmt.Errorf("identity: store private key: %w", err)
	}
	return finish(cardPath, priv, backend, true, skipped, opts, now)
}

func finish(cardPath string, priv ed25519.PrivateKey, backend string, generated bool, skipped []error, opts Options, now time.Time) (*Identity, Report, error) {
	name, harness := opts.Name, opts.Harness
	if name == "" {
		name = defaultName()
	}
	if harness == "" {
		harness = defaultHarness
	}
	card, err := agentcard.New(priv.Public().(ed25519.PublicKey), name, harness, opts.Skills, now)
	if err != nil {
		return nil, Report{}, err
	}
	signed, err := agentcard.Sign(priv, card)
	if err != nil {
		return nil, Report{}, err
	}
	raw, err := json.MarshalIndent(signed, "", "  ")
	if err != nil {
		return nil, Report{}, fmt.Errorf("identity: marshal card: %w", err)
	}
	if err := keystore.WriteOwnerOnly(cardPath, append(raw, '\n')); err != nil {
		return nil, Report{}, fmt.Errorf("identity: write card: %w", err)
	}
	detail := CreateDetail{PublicKey: card.PublicKey, KeyBackend: backend, KeyGenerated: generated}
	if len(skipped) > 0 {
		detail.KeychainError = errors.Join(skipped...).Error()
	}
	return &Identity{card: signed, keyBackend: backend}, Report{Created: true, Detail: detail}, nil
}

func readCard(path string) (*agentcard.Signed, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is inside the daemon config dir
	if err != nil {
		return nil, err
	}
	return agentcard.Verify(raw)
}

func privFromSeed(seed []byte) (ed25519.PrivateKey, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("identity: stored key has %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func pubString(priv ed25519.PrivateKey) string {
	return base64.RawURLEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
}

func defaultName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		if r := []rune(h); len(r) > 128 {
			h = string(r[:128])
		}
		return h
	}
	return "agent"
}
