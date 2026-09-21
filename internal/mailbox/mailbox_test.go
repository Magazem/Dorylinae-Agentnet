package mailbox_test

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mailbox"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// newDB opens a migrated database in a temporary directory.
func newDB(t *testing.T) *sql.DB {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "agentnet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.DB()
}

func newKeys(t *testing.T, dir string, now func() time.Time) (*mailbox.Keys, ed25519.PublicKey) {
	t.Helper()
	k, pub, _ := newKeysOn(t, dir, newDB(t), now)
	return k, pub
}

// newKeysOn returns Keys for a fresh identity attached to db.
func newKeysOn(t *testing.T, dir string, db *sql.DB, now func() time.Time) (*mailbox.Keys, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil }
	k := mailbox.New(dir, "file", pub, sign, now)
	if err := k.Attach(context.Background(), db, nil, nil); err != nil {
		t.Fatal(err)
	}
	return k, pub, priv
}

func TestFirstKeyIsCreatedOnceAndAnnounced(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	db := newDB(t)
	k, pub, _ := newKeysOn(t, dir, db, func() time.Time { return now })

	raw, err := k.Announcement()
	if err != nil {
		t.Fatal(err)
	}
	ann, canon, err := mail.ParseAnnouncement(raw, base64.RawURLEncoding.EncodeToString(pub), now)
	if err != nil || string(canon) != string(raw) {
		t.Fatalf("announcement does not verify or is not canonical: %v", err)
	}
	if !ann.NotAfter.Equal(now.Add(14 * 24 * time.Hour)) {
		t.Errorf("not_after = %v, want created + 14 d", ann.NotAfter)
	}

	// The private key is in the keystore and matches the announced public key.
	seed, err := os.ReadFile(filepath.Join(dir, mailbox.Dir, ann.KeyID.String()+".key"))
	if err != nil {
		t.Fatal(err)
	}
	priv, err := base64.RawURLEncoding.DecodeString(string(seed[:len(seed)-1]))
	if err != nil {
		t.Fatal(err)
	}
	x, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil || string(x.PublicKey().Bytes()) != string(ann.Pub) {
		t.Fatalf("stored private key does not match the announcement: %v", err)
	}

	// Asking again, also from a fresh Keys (a daemon restart), returns the same key.
	again, err := k.Announcement()
	if err != nil || string(again) != string(raw) {
		t.Fatalf("second call changed the key: %v", err)
	}
	restarted := mailbox.New(dir, "file", pub, func(m []byte) ([]byte, error) { return nil, os.ErrClosed }, func() time.Time { return now.Add(time.Hour) })
	if err := restarted.Attach(context.Background(), db, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := restarted.Announcement(); err != nil || string(got) != string(raw) {
		t.Fatalf("restart did not reuse the key: %v", err)
	}
}

func TestNewKeyWhenPrivateKeyIsGoneOrAnnouncementExpired(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cur := now
	k, _ := newKeys(t, dir, func() time.Time { return cur })
	first, err := k.Announcement()
	if err != nil {
		t.Fatal(err)
	}
	ann, _, _ := mail.ParseAnnouncement(first, mustIdentity(t, first), now)
	if err := os.Remove(filepath.Join(dir, mailbox.Dir, ann.KeyID.String()+".key")); err != nil {
		t.Fatal(err)
	}
	second, err := k.Announcement()
	if err != nil || string(second) == string(first) {
		t.Fatalf("no new key after the private key was lost: %v", err)
	}
	cur = now.Add(15 * 24 * time.Hour) // past not_after
	third, err := k.Announcement()
	if err != nil || string(third) == string(second) {
		t.Fatalf("no new key after the announcement expired: %v", err)
	}
}

func TestBadKeystoreModeIsAnError(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	k := mailbox.New(t.TempDir(), "bogus", pub, func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil }, nil)
	if err := k.Attach(context.Background(), newDB(t), nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Announcement(); err == nil {
		t.Fatal("expected an error for an unknown keystore mode")
	}
}

// mustIdentity returns the identity field of a canonical announcement.
func mustIdentity(t *testing.T, raw []byte) string {
	t.Helper()
	const marker = `"identity":"`
	s := string(raw)
	i := strings.Index(s, marker)
	if i < 0 {
		t.Fatal("no identity in announcement")
	}
	return s[i+len(marker) : i+len(marker)+43]
}
