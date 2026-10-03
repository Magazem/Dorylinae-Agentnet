package mailbox_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mailbox"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
	"github.com/zalando/go-keyring"
)

// TestKeychainLegacyAccountFallback is review 60 F1: a mailbox key stored
// under the account of the dir as spelled (before R55-088) is still found,
// not rotated away, and deleted from both accounts by the sweep.
func TestKeychainLegacyAccountFallback(t *testing.T) {
	keyring.MockInit()
	dir := testutil.TempDir(t)
	spelled := testutil.OtherSpelling(t, dir)
	sum := sha256.Sum256([]byte(spelled))
	legacyHex := hex.EncodeToString(sum[:8])
	canonHex := strings.TrimPrefix(keystore.AccountFor(dir), "identity-")
	if legacyHex == canonHex {
		t.Skip("spelling is already canonical")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil }
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	db := newDB(t)
	open := func() *mailbox.Keys {
		k := mailbox.New(spelled, "auto", pub, sign, clock)
		if err := k.Attach(context.Background(), db, nil, nil); err != nil {
			t.Fatal(err)
		}
		return k
	}

	first, err := open().Announcement()
	if err != nil {
		t.Fatal(err)
	}
	id := keyIDOf(t, first, base64.RawURLEncoding.EncodeToString(pub), now)
	if keyFileExists(spelled, id.String()) {
		t.Fatal("key went to the file, not the keychain")
	}
	// Move the entry to where a release before R55-088 stored it.
	canon := keystore.NewKeychain("mailbox-" + canonHex + "-" + id.String())
	legacy := keystore.NewKeychain("mailbox-" + legacyHex + "-" + id.String())
	seed, err := canon.Get()
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Set(seed); err != nil {
		t.Fatal(err)
	}
	if err := canon.Delete(); err != nil {
		t.Fatal(err)
	}

	k := open()
	if again, err := k.Announcement(); err != nil || string(again) != string(first) {
		t.Fatalf("legacy key was rotated away: %v", err)
	}
	if _, ok := k.MailboxKey(id); !ok {
		t.Fatal("legacy key not found (key_miss)")
	}
	if _, err := canon.Get(); err != nil {
		t.Fatalf("key not copied to the canonical account: %v", err)
	}

	// Its successor is made by the 7-day rotation: a key is deleted by age only
	// once a newer key has been current for 7 days (R55-F28).
	now = now.Add(mailbox.RotateAfter)
	if _, err := k.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(mailbox.DeleteGrace + 14*24*time.Hour)
	// Mail received at that time gives the age basis (review 100 M1).
	if err := mailbox.ReceivedMailAt(k, now); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, kc := range []*keystore.Keychain{canon, legacy} {
		if _, err := kc.Get(); !errors.Is(err, keystore.ErrNotFound) {
			t.Fatalf("private key survived the sweep: %v", err)
		}
	}
}
