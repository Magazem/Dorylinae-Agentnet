package mailbox_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mailbox"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// unreachable is a keychain backend this process sees no service for (a
// daemon started without the desktop session's D-Bus bus): every call fails
// with ErrNoService, though the user's keychain still holds the entries.
type unreachable struct {
	keystore.Backend
	off *bool
}

func (u *unreachable) Get() ([]byte, error) {
	if *u.off {
		return nil, keystore.ErrNoService
	}
	return u.Backend.Get()
}

func (u *unreachable) Set(s []byte) error {
	if *u.off {
		return keystore.ErrNoService
	}
	return u.Backend.Set(s)
}

func (u *unreachable) Delete() error {
	if *u.off {
		return keystore.ErrNoService
	}
	return u.Backend.(keystore.Deleter).Delete()
}

// Review 87b N3: the row records the backend the key was saved to. A key
// saved to the keychain is not marked deleted while this process sees no
// keychain service (the key may still be in the user's keychain); once the
// keychain is reachable again the delete goes through.
func TestKeychainKeyNotMarkedDeletedWithoutService(t *testing.T) {
	keyring.MockInit()
	defer keyring.MockInit()
	dir := testutil.TempDir(t)
	db := newDB(t)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil }
	k := mailbox.New(dir, "auto", pub, sign, func() time.Time { return now })
	off := false
	mailbox.WrapBackends(k, func(b keystore.Backend) keystore.Backend {
		if b.Name() == "keychain" {
			return &unreachable{Backend: b, off: &off}
		}
		return b
	})
	if err := k.Attach(context.Background(), db, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Announcement(); err != nil {
		t.Fatal(err)
	}
	var keyID, notAfter, backend string
	if err := db.QueryRow(`SELECT key_id, not_after, key_backend FROM mailbox_keys_own`).Scan(&keyID, &notAfter, &backend); err != nil {
		t.Fatal(err)
	}
	if backend != "keychain" {
		t.Fatalf("key_backend = %q, want keychain", backend)
	}
	na, err := time.Parse(time.RFC3339, notAfter)
	if err != nil {
		t.Fatal(err)
	}
	// Its successor is made by the 7-day rotation: a key is deleted by age only
	// once a newer key has been current for 7 days (R55-F28).
	now = na.Add(-mailbox.RotateAfter)
	_, _ = k.Rotate(context.Background())
	now = na.Add(mailbox.DeleteGrace + time.Hour)
	off = true
	_, _ = k.Rotate(context.Background())
	if d := deletedAt(t, db, keyID); d.Valid {
		t.Fatalf("keychain key %s marked deleted at %s while this process saw no keychain service", keyID, d.String)
	}
	// The replacement made meanwhile went to the file, and is recorded so.
	var fileRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mailbox_keys_own WHERE key_backend = 'file'`).Scan(&fileRows); err != nil || fileRows != 1 {
		t.Fatalf("file rows = %d (%v), want 1", fileRows, err)
	}
	off = false
	now = now.Add(time.Hour)
	_, _ = k.Rotate(context.Background())
	if d := deletedAt(t, db, keyID); !d.Valid {
		t.Fatalf("key %s not deleted once the keychain was reachable", keyID)
	}
}
