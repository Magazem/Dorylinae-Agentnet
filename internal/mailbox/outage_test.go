package mailbox_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mailbox"
	"github.com/Magazem/Dorylinae-Agentnet/internal/relayclient"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// Review 55 R55-092 (C05-01): in "auto" mode a keychain that is merely
// unavailable (locked, timed out) is not "private key not found": the
// current key is kept, not replaced and pushed to every peer.
func TestKeychainOutageDoesNotRotate(t *testing.T) {
	keyring.MockInit()
	defer keyring.MockInit()
	dir := testutil.TempDir(t)
	db := newDB(t)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// Identity key in the file backend (it fell back at first run), mailbox keys in "auto".
	idks := keystore.New(keystore.NewFile(filepath.Join(dir, "identity.key")))
	if _, _, err := idks.Save(priv.Seed()); err != nil {
		t.Fatal(err)
	}
	sign := relayclient.NewKeySigner(priv).Sign
	k := mailbox.New(dir, "auto", pub, sign, func() time.Time { return now })
	if err := k.Attach(context.Background(), db, nil, nil); err != nil {
		t.Fatal(err)
	}
	pushes := 0
	k.OnRotate(func([]byte) { pushes++ })
	a, err := k.Announcement()
	if err != nil {
		t.Fatal(err)
	}
	pushes = 0
	now = now.Add(time.Hour)
	keyring.MockInitWithError(errors.New("keychain locked"))
	ann, err := k.Rotate(context.Background())
	if err != nil || ann != nil {
		t.Fatalf("Rotate during an outage: announcement=%v err=%v", ann != nil, err)
	}
	b, err := k.Announcement()
	if err != nil || string(a) != string(b) || pushes != 0 || liveCount(t, db) != 1 {
		t.Fatalf("key rotated because the keychain was unavailable: err=%v pushes=%d live=%d", err, pushes, liveCount(t, db))
	}
}

// lockable is a keystore backend that can be locked: a locked keychain keeps
// its entries but refuses every call.
type lockable struct {
	keystore.Backend
	locked *bool
}

func (l *lockable) Get() ([]byte, error) {
	if *l.locked {
		return nil, keystore.ErrUnavailable
	}
	return l.Backend.Get()
}

func (l *lockable) Set(s []byte) error {
	if *l.locked {
		return keystore.ErrUnavailable
	}
	return l.Backend.Set(s)
}

func (l *lockable) Delete() error {
	if *l.locked {
		return keystore.ErrUnavailable
	}
	return l.Backend.(keystore.Deleter).Delete()
}

func deletedAt(t *testing.T, db *sql.DB, keyID string) sql.NullString {
	t.Helper()
	var d sql.NullString
	if err := db.QueryRow(`SELECT deleted FROM mailbox_keys_own WHERE key_id = ?`, keyID).Scan(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

// Review 87 M2: a key due for deletion while the keychain is only locked is
// not marked deleted (its private key is still there); the next run, with
// the keychain back, deletes it.
func TestLockedKeychainDeletionIsRetried(t *testing.T) {
	keyring.MockInit()
	defer keyring.MockInit()
	dir := testutil.TempDir(t)
	db := newDB(t)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	idks := keystore.New(keystore.NewFile(filepath.Join(dir, "identity.key")))
	if _, _, err := idks.Save(priv.Seed()); err != nil {
		t.Fatal(err)
	}
	sign := relayclient.NewKeySigner(priv).Sign
	k := mailbox.New(dir, "auto", pub, sign, func() time.Time { return now })
	locked := false
	mailbox.WrapBackends(k, func(b keystore.Backend) keystore.Backend {
		if b.Name() == "keychain" {
			return &lockable{Backend: b, locked: &locked}
		}
		return b
	})
	if err := k.Attach(context.Background(), db, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Announcement(); err != nil {
		t.Fatal(err)
	}
	var keyID, notAfter string
	if err := db.QueryRow(`SELECT key_id, not_after FROM mailbox_keys_own`).Scan(&keyID, &notAfter); err != nil {
		t.Fatal(err)
	}
	if keyFileExists(dir, keyID) {
		t.Skip("the key went to the file backend")
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
	// Mail received at that time gives the age basis (review 100 M1), so
	// the deletion is attempted.
	if err := mailbox.ReceivedMailAt(k, now); err != nil {
		t.Fatal(err)
	}
	locked = true
	_, _ = k.Rotate(context.Background())
	if d := deletedAt(t, db, keyID); d.Valid {
		t.Fatalf("key %s marked deleted at %s while the keychain was locked", keyID, d.String)
	}
	locked = false
	now = now.Add(time.Hour)
	_, _ = k.Rotate(context.Background())
	if d := deletedAt(t, db, keyID); !d.Valid {
		t.Fatalf("key %s not deleted once the keychain was back", keyID)
	}
}

// Review 87 M1: a key file planted next to a keychain-held mailbox key, with
// another X25519 key, is ignored: the key matching the row is used.
func TestPlantedMailboxKeyFileIsIgnored(t *testing.T) {
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
	if err := k.Attach(context.Background(), db, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Announcement(); err != nil {
		t.Fatal(err)
	}
	var keyID, pubB64 string
	if err := db.QueryRow(`SELECT key_id, pub FROM mailbox_keys_own`).Scan(&keyID, &pubB64); err != nil {
		t.Fatal(err)
	}
	if keyFileExists(dir, keyID) {
		t.Skip("the key went to the file backend")
	}
	planted := make([]byte, 32)
	if _, err := rand.Read(planted); err != nil {
		t.Fatal(err)
	}
	if err := keystore.NewFile(filepath.Join(dir, mailbox.Dir, keyID+".key")).Set(planted); err != nil {
		t.Fatal(err)
	}
	var id mail.KeyID
	if raw, err := hex.DecodeString(keyID); err != nil || copy(id[:], raw) != len(id) {
		t.Fatalf("key_id %q: %v", keyID, err)
	}
	got, ok := k.MailboxKey(id)
	if !ok || base64.RawURLEncoding.EncodeToString(got.PublicKey().Bytes()) != pubB64 {
		t.Fatalf("MailboxKey = %v, want the key matching the row", ok)
	}
}
