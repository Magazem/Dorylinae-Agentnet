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
	"strings"
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
	// KeychainMarkerFile records that this config dir's identity key has
	// been kept in the OS keychain (review 87b N1). While the keychain cannot
	// be read, a key found only in the file is then not used on its own.
	KeychainMarkerFile = "identity.keychain"

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
		return keystore.New(keystore.KeychainFor("identity-", dir), file), nil
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
	// LegacyText is set when the stored card verifies only under the legacy
	// text rule (agentcard.TextRuleError; agent-card.md §Cards stored before
	// R55-F10): the card is kept, and the daemon logs a warning.
	LegacyText error
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

	if haveCard {
		seed, backend, noService, err := loadKey(dir, ks, existing.Card.PublicKey)
		switch {
		case err == nil:
			defer clear(seed)
			if backend == "keychain" {
				if err := markKeychain(dir); err != nil {
					return nil, Report{}, err
				}
			}
			return &Identity{card: *existing, keyBackend: backend}, Report{LegacyText: agentcard.TextRuleError(existing.Card)}, nil
		case errors.Is(err, keystore.ErrConflict), errors.Is(err, errKeychainUnread):
			return nil, Report{}, err
		case errors.Is(err, keystore.ErrMismatch):
			return nil, Report{}, fmt.Errorf("identity: no stored private key matches the key in %s, nothing was changed (%w); restore the key or delete %s to start a new identity", cardPath, err, cardPath)
		case errors.Is(err, keystore.ErrNotFound) && noService:
			// This process sees no keychain service, which does not mean the
			// user has none (review 87b N3): a new identity is not the first fix.
			return nil, Report{}, fmt.Errorf("%w (%w); the key may be in a keychain this session cannot reach (start the daemon from the desktop session), otherwise restore it or delete %s to start a new identity", ErrKeyLost, err, cardPath)
		case errors.Is(err, keystore.ErrNotFound):
			return nil, Report{}, fmt.Errorf("%w (%w); restore it or delete %s to start a new identity", ErrKeyLost, err, cardPath)
		case errors.Is(err, keystore.ErrUnavailable):
			// The keychain may hold the key but cannot be read now (locked,
			// timed out): that is not a lost key (review 55 R55-092).
			return nil, Report{}, fmt.Errorf("identity: the private key could not be read, nothing was changed (%w); unlock the keychain and start again", err)
		default:
			return nil, Report{}, err
		}
	}

	// No card. A stored key is adopted (an interrupted first run) only when
	// every backend answered and agrees: with two different keys, or one key
	// while another backend could not be read, nothing says which is ours, and
	// adopting a key a file-only writer planted would hand it the identity
	// (review 87 M1).
	// A keychain this process sees no service for counts as not read when
	// this dir has kept its key there before (review 87b N1, N3).
	c := ks.Read()
	defer c.Clear()
	unread := append(append([]error(nil), c.Unavailable...), c.Broken...)
	if keychainUsed(dir) {
		unread = append(unread, c.NoService...)
	}
	switch {
	case c.Distinct():
		return nil, Report{}, fmt.Errorf("identity: %s is missing and %s hold different private keys, nothing was changed; restore the card, or remove the key that is not this agent's", cardPath, strings.Join(c.Locations(), " and "))
	case len(c.Copies) > 0 && len(unread) > 0:
		return nil, Report{}, fmt.Errorf("identity: %s is missing and a private key is stored in %s, but other key storage could not be read (%w), nothing was changed; unlock the keychain (or fix the key file) and start again, or restore the card",
			cardPath, strings.Join(c.Locations(), " and "), errors.Join(unread...))
	case len(c.Copies) > 0:
		// Key without a card (interrupted first run): re-create the card for the same key.
		priv, err := privFromSeed(c.Copies[0].Secret)
		if err != nil {
			return nil, Report{}, err
		}
		defer clear(priv)
		if c.Copies[0].Backend == "keychain" {
			if err := markKeychain(dir); err != nil {
				return nil, Report{}, err
			}
		}
		return finish(cardPath, priv, c.Copies[0].Backend, false, nil, opts, now)
	case len(c.Broken) > 0:
		return nil, Report{}, errors.Join(c.Broken...)
	}
	// No key anywhere that could be read. A keychain that is unavailable now
	// may hold an older key, but no card refers to it, so a new identity is
	// created as on a machine without a keychain.

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, Report{}, fmt.Errorf("identity: generate key: %w", err)
	}
	backend, skipped, err := ks.Save(priv.Seed())
	if err != nil {
		return nil, Report{}, fmt.Errorf("identity: store private key: %w", err)
	}
	if backend == "keychain" {
		if err := markKeychain(dir); err != nil {
			return nil, Report{}, err
		}
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

// ReadCard returns the verified Agent Card stored under dir.
func ReadCard(dir string) (*agentcard.Signed, error) { return readCard(filepath.Join(dir, CardFile)) }

// readCard reads and verifies the own card. A card made before R55-F10 can
// only be re-signed by this daemon's key, so it is verified with the legacy
// text rule (agentcard.VerifyStored, review 76 I3); without that the daemon
// would not start.
func readCard(path string) (*agentcard.Signed, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is inside the daemon config dir
	if err != nil {
		return nil, err
	}
	return agentcard.VerifyStored(raw)
}

func privFromSeed(seed []byte) (ed25519.PrivateKey, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("identity: stored key has %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// SeedMatches returns a keystore check that accepts a stored seed only when
// its public key is pub (base64url, as in the agent card).
func SeedMatches(pub string) func(seed []byte) bool {
	return func(seed []byte) bool {
		if len(seed) != ed25519.SeedSize {
			return false
		}
		priv := ed25519.NewKeyFromSeed(seed)
		defer clear(priv)
		return pubString(priv) == pub
	}
}

// errKeychainUnread is the error of LoadKey when the matching key is only in
// the file while the keychain, which this dir has used, cannot be read.
var errKeychainUnread = fmt.Errorf("%w: the keychain could not be read", keystore.ErrUnavailable)

// LoadKey returns the identity seed whose public key is pub (the agent card's,
// base64url) and the backend that held it. It is the only way the daemon reads
// its identity key (start-up, signing, doctor; review 87b N2). The card is a
// file, self-signed, as writable as identity.key, so matching it is not
// enough (review 87b N1):
//
//   - When a more preferred backend (the keychain) answered with a different
//     key, the error wraps keystore.ErrConflict and names both copies: a
//     process that can write files but not the keychain may have replaced
//     the card and the key file. A different key in a less preferred backend
//     (a stray identity.key next to the keychain key) is ignored.
//   - When the key is found only in the file while the keychain cannot be
//     read (locked, timed out, or no service in this process) and this dir
//     has kept its key in the keychain before (KeychainMarkerFile), the
//     error wraps keystore.ErrUnavailable: the file copy cannot be compared
//     with the keychain's. The marker is a file too, so this narrows the
//     window but cannot close it (Docs/protocol/agent-card.md §Key storage).
//
// The caller clears the seed.
func LoadKey(dir string, ks *keystore.Store, pub string) (seed []byte, backend string, err error) {
	seed, backend, _, err = loadKey(dir, ks, pub)
	return seed, backend, err
}

// loadKey is LoadKey; noService also reports whether a backend said this
// process has no keychain service.
func loadKey(dir string, ks *keystore.Store, pub string) (seed []byte, backend string, noService bool, err error) {
	c := ks.Read()
	defer c.Clear()
	noService = len(c.NoService) > 0
	match := SeedMatches(pub)
	seed, backend, err = c.Pick(match)
	if err != nil {
		return nil, "", noService, err
	}
	var chosen keystore.Copy
	for _, cp := range c.Copies {
		if match(cp.Secret) {
			chosen = cp
			break
		}
	}
	// Copies are in backend order: a first copy that is not the chosen one
	// is in a more preferred backend and holds a different key.
	if first := c.Copies[0]; first.Backend != chosen.Backend {
		clear(seed)
		card := filepath.Join(dir, CardFile)
		return nil, "", noService, fmt.Errorf("identity: the agent card's key is in the %s, but the %s holds a different key, nothing was changed (%w). "+
			"A process that can write files but not the keychain may have replaced %s and the key file: unless you created this identity while the keychain was unavailable, "+
			"delete %s and the key file so that the keychain key is used again. If this identity is yours, delete the keychain entry instead",
			chosen.Location, first.Location, keystore.ErrConflict, card, card)
	}
	if order := ks.Backends(); len(order) > 1 && order[0] != chosen.Backend && contains(c.Unread, order[0]) && keychainUsed(dir) {
		clear(seed)
		return nil, "", noService, fmt.Errorf("identity: the agent card's key is only in the %s and the %s could not be read, nothing was changed (%w: %s). "+
			"This config dir has kept its key in the keychain before (%s), so the file copy is not used on its own: unlock the keychain and start again. "+
			"If the key now lives only in the file for good, set %s=file",
			chosen.Location, order[0], errKeychainUnread, unreadErrs(c), filepath.Join(dir, KeychainMarkerFile), KeystoreEnv)
	}
	return seed, backend, noService, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func unreadErrs(c keystore.Contents) string {
	var msgs []string
	for _, errs := range [][]error{c.Unavailable, c.NoService, c.Broken} {
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
	}
	return strings.Join(msgs, "; ")
}

// keychainUsed reports whether KeychainMarkerFile exists in dir. A marker
// that cannot be checked counts as present (the stricter answer).
func keychainUsed(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, KeychainMarkerFile))
	return !errors.Is(err, os.ErrNotExist)
}

// markKeychain writes KeychainMarkerFile, once.
func markKeychain(dir string) error {
	path := filepath.Join(dir, KeychainMarkerFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := keystore.WriteOwnerOnly(path, []byte("This config dir's identity key is kept in the OS keychain.\n")); err != nil {
		return fmt.Errorf("identity: write %s: %w", path, err)
	}
	return nil
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
