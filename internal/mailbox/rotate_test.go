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
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/keystore"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mailbox"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func liveCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mailbox_keys_own WHERE deleted IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func keyFileExists(dir, keyID string) bool {
	_, err := os.Stat(filepath.Join(dir, mailbox.Dir, keyID+".key"))
	return err == nil
}

func keyIDOf(t *testing.T, raw []byte, identity string, now time.Time) mail.KeyID {
	t.Helper()
	ann, _, err := mail.ParseAnnouncement(raw, identity, now)
	if err != nil {
		t.Fatal(err)
	}
	return ann.KeyID
}

func TestRotationSchedule(t *testing.T) {
	dir := testutil.TempDir(t)
	db := newDB(t)
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	now := t0
	k, pub, _ := newKeysOn(t, dir, db, func() time.Time { return now })
	identity := base64.RawURLEncoding.EncodeToString(pub)

	var pushed [][]byte
	k.OnRotate(func(a []byte) { pushed = append(pushed, a) })

	first, err := k.Announcement()
	if err != nil {
		t.Fatal(err)
	}
	firstID := keyIDOf(t, first, identity, now)
	pushed = nil

	maxLive := 0
	for now = t0; now.Before(t0.Add(30 * 24 * time.Hour)); now = now.Add(time.Hour) {
		if _, err := k.Rotate(context.Background()); err != nil {
			t.Fatalf("at %v: %v", now.Sub(t0), err)
		}
		n := liveCount(t, db)
		maxLive = max(maxLive, n)
		if n > mailbox.MaxLive {
			t.Fatalf("at %v: %d live keys", now.Sub(t0), n)
		}
		age := now.Sub(t0)
		_, live := k.MailboxKey(firstID)
		switch {
		case age < 21*24*time.Hour:
			if !live || !keyFileExists(dir, firstID.String()) {
				t.Fatalf("at %v: first key gone too early", age)
			}
		default:
			if live || keyFileExists(dir, firstID.String()) {
				t.Fatalf("at %v: first key still live or on disk (21 d)", age)
			}
		}
	}
	if maxLive != 3 {
		t.Errorf("max live keys = %d, want 3", maxLive)
	}
	// Rotations at 7, 14, 21 and 28 days.
	if len(pushed) != 4 {
		t.Errorf("rotation hook ran %d times, want 4", len(pushed))
	}

	// The first key: retired at 7 d, deleted at 21 d, row kept.
	var retired, deleted string
	if err := db.QueryRow(`SELECT retired, deleted FROM mailbox_keys_own WHERE key_id = ?`, firstID.String()).Scan(&retired, &deleted); err != nil {
		t.Fatal(err)
	}
	if retired != t0.Add(7*24*time.Hour).Format(mail.StoreTimeFmt) || deleted != t0.Add(21*24*time.Hour).Format(mail.StoreTimeFmt) {
		t.Errorf("retired %s, deleted %s", retired, deleted)
	}
	// Exactly one current key.
	var cur int
	_ = db.QueryRow(`SELECT COUNT(*) FROM mailbox_keys_own WHERE retired IS NULL AND deleted IS NULL`).Scan(&cur)
	if cur != 1 {
		t.Errorf("%d current keys", cur)
	}
}

func TestRotationAuditsAndRetiredKeyStillDecrypts(t *testing.T) {
	dir := testutil.TempDir(t)
	db := newDB(t)
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	now := t0
	k, pub, _ := newKeysOn(t, dir, db, func() time.Time { return now })
	identity := base64.RawURLEncoding.EncodeToString(pub)
	a := &recordAudit{}
	if err := k.Attach(context.Background(), db, a, nil); err != nil {
		t.Fatal(err)
	}
	first, _ := k.Announcement()
	firstID := keyIDOf(t, first, identity, now)
	now = t0.Add(7 * 24 * time.Hour)
	second, err := k.Rotate(context.Background())
	if err != nil || second == nil {
		t.Fatalf("no rotation at 7 d: %v", err)
	}
	if _, ok := k.MailboxKey(firstID); !ok {
		t.Error("retired key does not decrypt")
	}
	if _, ok := k.MailboxKey(keyIDOf(t, second, identity, now)); !ok {
		t.Error("new key not found")
	}
	if got, err := k.Announcement(); err != nil || string(got) != string(second) {
		t.Errorf("Announcement is not the new key: %v", err)
	}
	if len(a.events) != 2 || a.events[1].detail["retired"] != firstID.String() || a.events[0].detail["retired"] != "" {
		t.Errorf("audit = %+v", a.events)
	}
	if a.events[1].action != mailbox.ActionRotate {
		t.Errorf("action = %q", a.events[1].action)
	}
	// No rotation before the current key is 7 d old.
	if again, err := k.Rotate(context.Background()); err != nil || again != nil {
		t.Errorf("rotated again: %v", err)
	}
}

func TestMoreThanThreeLiveDeletesOldestEarly(t *testing.T) {
	dir := testutil.TempDir(t)
	db := newDB(t)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	k, pub, _ := newKeysOn(t, dir, db, func() time.Time { return now })
	identity := base64.RawURLEncoding.EncodeToString(pub)
	var ids []mail.KeyID
	for i := 0; i < 5; i++ {
		raw, err := k.Announcement()
		if err != nil {
			t.Fatal(err)
		}
		id := keyIDOf(t, raw, identity, now)
		ids = append(ids, id)
		// Losing the secret makes the next Announcement create a new key.
		if err := os.Remove(filepath.Join(dir, mailbox.Dir, id.String()+".key")); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
	}
	if n := liveCount(t, db); n > mailbox.MaxLive {
		t.Errorf("%d live keys, want at most %d", n, mailbox.MaxLive)
	}
}

func TestLegacyCurrentJSONImportedOnce(t *testing.T) {
	dir := testutil.TempDir(t)
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	db := newDB(t)

	// A key as 0.8c left it: secret in the file keystore and current.json.
	_, pub, priv := newKeysOn(t, testutil.TempDir(t), newDB(t), clock) // just an identity
	x, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := mail.SignAnnouncement(pub, func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil }, x.PublicKey().Bytes(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	keyID := mail.KeyIDOf(x.PublicKey().Bytes()).String()
	if err := os.MkdirAll(filepath.Join(dir, mailbox.Dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := keystore.NewFile(filepath.Join(dir, mailbox.Dir, keyID+".key")).Set(x.Bytes()); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, mailbox.Dir, "current.json")
	if err := os.WriteFile(legacy, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	attach := func() *mailbox.Keys {
		k := mailbox.New(dir, "file", pub, func(m []byte) ([]byte, error) { return ed25519.Sign(priv, m), nil }, clock)
		if err := k.Attach(context.Background(), db, nil, nil); err != nil {
			t.Fatal(err)
		}
		return k
	}
	k := attach()
	if liveCount(t, db) != 1 {
		t.Fatalf("legacy key not imported")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Error("current.json was not removed after the import")
	}
	got, err := k.Announcement()
	if err != nil || string(got) != string(raw) {
		t.Fatalf("imported key is not the current one: %v", err)
	}
	if _, ok := k.MailboxKey(mail.KeyIDOf(x.PublicKey().Bytes())); !ok {
		t.Error("imported key does not decrypt")
	}
	// A second start does not import again, nor duplicate.
	attach()
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM mailbox_keys_own`).Scan(&n)
	if n != 1 {
		t.Errorf("%d rows after the second start", n)
	}

	// Rotation later retires the imported key like any other.
	now = now.Add(7 * 24 * time.Hour)
	if a, err := k.Rotate(context.Background()); err != nil || a == nil {
		t.Fatalf("no rotation: %v", err)
	}
	if _, ok := k.MailboxKey(mail.KeyIDOf(x.PublicKey().Bytes())); !ok {
		t.Error("retired imported key does not decrypt")
	}
}

type auditEvent struct {
	action string
	detail map[string]string
}

type recordAudit struct{ events []auditEvent }

func (r *recordAudit) Append(_ context.Context, _, action string, detail any) error {
	d, _ := detail.(map[string]string)
	r.events = append(r.events, auditEvent{action, d})
	return nil
}
