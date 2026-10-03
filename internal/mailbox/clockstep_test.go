package mailbox_test

// Mailbox keys under a stepped wall clock (R55-F28; review 55 R55-103, theme
// T12-01, and review 100 M1 and L1). A wall clock stepped forward for some
// rotation runs, then corrected, must neither delete a key that peers still
// seal to nor leave a current key whose announcement peers refuse. Key age is
// measured at the earlier of now and the newest received mail.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
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

func TestRotateForwardClockStepKeepsSealedKey(t *testing.T) {
	dir := testutil.TempDir(t)
	db := newDB(t)
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	now := t0
	k, pub, _ := newKeysOn(t, dir, db, func() time.Time { return now })
	identity := base64.RawURLEncoding.EncodeToString(pub)

	first, err := k.Announcement()
	if err != nil {
		t.Fatal(err)
	}
	firstID := keyIDOf(t, first, identity, now)

	// The clock jumps 30 days ahead for two hourly Rotate runs.
	for i := 0; i < 2; i++ {
		now = t0.Add(30*24*time.Hour + time.Duration(i)*time.Hour)
		if _, err := k.Rotate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// NTP corrects it (true time).
	now = t0.Add(2 * time.Hour)
	if _, err := k.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, live := k.MailboxKey(firstID); !live {
		t.Fatalf("key %s that peers seal to was deleted during the step", firstID)
	}
	cur, err := k.Announcement()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mail.ParseAnnouncement(cur, identity, now); err != nil {
		t.Fatalf("a peer at true time refuses the current announcement: %v", err)
	}
	curID := keyIDOf(t, cur, identity, now)
	if _, live := k.MailboxKey(curID); !live {
		t.Fatal("current key has no private half")
	}

	// 20 days later (true time) the announcement is still accepted. The cap
	// removes the future-dated key, not the first one, when the next key is
	// made. With mail arriving, the first key goes at not_after + 7 d: its
	// successor (made at the correction) has been current for 7 days.
	now = t0.Add(20 * 24 * time.Hour)
	if _, err := k.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	cur, _ = k.Announcement()
	if _, _, err := mail.ParseAnnouncement(cur, identity, now); err != nil {
		t.Fatalf("still refused 20 days later: %v", err)
	}
	if !keyFileExists(dir, firstID.String()) {
		t.Fatal("first key deleted before not_after + 7 d")
	}
	now = t0.Add(21*24*time.Hour + time.Minute)
	if err := mailbox.ReceivedMailAt(k, now); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if keyFileExists(dir, firstID.String()) {
		t.Fatal("first key not deleted at not_after + 7 d")
	}
	if n := liveCount(t, db); n > 3 {
		t.Fatalf("%d live keys, want at most 3", n)
	}
}

// Review 100 M1: the key before the current one is kept too. Peers that have
// not yet accepted the current key (offline, or with mail already queued)
// still seal to it; a run 30 days ahead must not age it out.
func TestRotateForwardClockStepKeepsPredecessor(t *testing.T) {
	dir := testutil.TempDir(t)
	db := newDB(t)
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	now := t0
	k, pub, _ := newKeysOn(t, dir, db, func() time.Time { return now })
	identity := base64.RawURLEncoding.EncodeToString(pub)

	// Three keys on a steady clock with mail arriving: ages 14 d, 7 d, 0 d.
	var ids []mail.KeyID
	for i := 0; i < 3; i++ {
		now = t0.Add(time.Duration(i) * 7 * 24 * time.Hour)
		if err := mailbox.ReceivedMailAt(k, now); err != nil {
			t.Fatal(err)
		}
		if _, err := k.Rotate(context.Background()); err != nil {
			t.Fatal(err)
		}
		a, _ := k.Announcement()
		ids = append(ids, keyIDOf(t, a, identity, now))
	}
	// Two days later (true time) mail sealed to ids[1] may still be queued.
	trueNow := now.Add(2 * 24 * time.Hour)
	if err := mailbox.ReceivedMailAt(k, trueNow); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		now = trueNow.Add(30*24*time.Hour + time.Duration(i)*time.Hour)
		if _, err := k.Rotate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	now = trueNow.Add(time.Hour)
	if _, err := k.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids[1:] {
		if _, live := k.MailboxKey(id); !live {
			t.Fatalf("key %s that peers may still seal to was deleted by a forward step", id)
		}
	}
	// ids[0] is the oldest of three when the stepped run adds a key, so the
	// MaxLive cap removes it (its successor has been current for 9 days).
	if keyFileExists(dir, ids[0].String()) {
		t.Fatalf("key %s kept: more than 3 keys live", ids[0])
	}
	cur, err := k.Announcement()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mail.ParseAnnouncement(cur, identity, now); err != nil {
		t.Fatalf("a peer at true time refuses the current announcement: %v", err)
	}
	if n := liveCount(t, db); n > 3 {
		t.Fatalf("%d live keys, want at most 3", n)
	}
}

// A clock stepped back makes a new current key dated in the past. The MaxLive
// cap then removes the oldest key, whose successor has been current for 7
// days; nothing else is deleted, and after the correction the current
// announcement is accepted again.
func TestRotateBackwardClockStep(t *testing.T) {
	dir := testutil.TempDir(t)
	db := newDB(t)
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	now := t0
	k, pub, _ := newKeysOn(t, dir, db, func() time.Time { return now })
	identity := base64.RawURLEncoding.EncodeToString(pub)

	// Three keys on a steady clock: ages 14 d, 7 d and 0 d.
	var ids []mail.KeyID
	for i := 0; i < 3; i++ {
		now = t0.Add(time.Duration(i) * 7 * 24 * time.Hour)
		if _, err := k.Rotate(context.Background()); err != nil {
			t.Fatal(err)
		}
		a, _ := k.Announcement()
		ids = append(ids, keyIDOf(t, a, identity, now))
	}
	trueNow := now

	// The clock steps 20 days back for a few runs.
	var stepped mail.KeyID
	for i := 0; i < 3; i++ {
		now = trueNow.Add(-20*24*time.Hour + time.Duration(i)*time.Hour)
		if _, err := k.Rotate(context.Background()); err != nil {
			t.Fatal(err)
		}
		a, _ := k.Announcement()
		id := keyIDOf(t, a, identity, now)
		if i > 0 && id != stepped {
			t.Fatalf("run %d made another key %s, want %s kept", i, id, stepped)
		}
		stepped = id
		if _, live := k.MailboxKey(id); !live {
			t.Fatalf("run %d: current key %s has no private half", i, id)
		}
	}
	now = trueNow.Add(time.Hour)
	if keyFileExists(dir, ids[0].String()) {
		t.Fatalf("key %s kept: more than 3 keys live", ids[0])
	}
	for _, id := range ids[1:] {
		if !keyFileExists(dir, id.String()) {
			t.Fatalf("key %s deleted during a backward step", id)
		}
		if _, live := k.MailboxKey(id); !live {
			t.Fatalf("key %s not live after the correction", id)
		}
	}
	if _, err := k.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	cur, err := k.Announcement()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mail.ParseAnnouncement(cur, identity, now); err != nil {
		t.Fatalf("a peer refuses the current announcement after the correction: %v", err)
	}
	if n := liveCount(t, db); n > 3 {
		t.Fatalf("%d live keys, want at most 3", n)
	}
}

// Review 100 L1: the MaxLive cap never deletes the current key, even when a
// backward step has made it the oldest by created and a failed deletion (a
// locked keychain) has left four keys live.
func TestCapNeverDeletesCurrentKey(t *testing.T) {
	keyring.MockInit()
	defer keyring.MockInit()
	dir := testutil.TempDir(t)
	db := newDB(t)
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	now := t0
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	idks := keystore.New(keystore.NewFile(filepath.Join(dir, "identity.key")))
	if _, _, err := idks.Save(priv.Seed()); err != nil {
		t.Fatal(err)
	}
	k := mailbox.New(dir, "auto", pub, relayclient.NewKeySigner(priv).Sign, func() time.Time { return now })
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
	identity := base64.RawURLEncoding.EncodeToString(pub)
	var ids []mail.KeyID
	for i := 0; i < 3; i++ {
		now = t0.Add(time.Duration(i) * 7 * 24 * time.Hour)
		if _, err := k.Rotate(context.Background()); err != nil {
			t.Fatal(err)
		}
		a, _ := k.Announcement()
		ids = append(ids, keyIDOf(t, a, identity, now))
	}
	if keyFileExists(dir, ids[0].String()) {
		t.Skip("the keys went to the file backend")
	}
	trueNow := now

	// 20 days back with the keychain locked: the new key goes to the file,
	// and the cap cannot delete ids[0], so four keys are live.
	locked = true
	now = trueNow.Add(-20 * 24 * time.Hour)
	ann, _ := k.Rotate(context.Background())
	if ann == nil {
		t.Fatal("no new key for a current key dated in the future")
	}
	stepped := keyIDOf(t, ann, identity, now)
	if n := liveCount(t, db); n != 4 {
		t.Fatalf("%d live keys, want 4 (the cap's deletion failed)", n)
	}

	// The keychain is back: the next run's cap deletes ids[0], not the current
	// key, which now sorts first by created.
	locked = false
	now = now.Add(time.Hour)
	if _, err := k.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, live := k.MailboxKey(stepped); !live {
		t.Fatalf("the cap deleted the current key %s", stepped)
	}
	if a, _ := k.Announcement(); keyIDOf(t, a, identity, now) != stepped {
		t.Fatal("the current key was replaced")
	}
	if d := deletedAt(t, db, ids[0].String()); !d.Valid {
		t.Fatalf("key %s not deleted by the cap", ids[0])
	}
	if n := liveCount(t, db); n != 3 {
		t.Fatalf("%d live keys, want 3", n)
	}
}

// After a downtime, the key before the first new one is deleted by age once
// mail shows its successor has been current for 7 days. An idle daemon (no
// mail) has no age basis: the MaxLive cap deletes the key a rotation later.
func TestRotateAgeDeletionNeedsMail(t *testing.T) {
	for _, idle := range []bool{false, true} {
		dir := testutil.TempDir(t)
		db := newDB(t)
		t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
		now := t0
		k, pub, _ := newKeysOn(t, dir, db, func() time.Time { return now })
		identity := base64.RawURLEncoding.EncodeToString(pub)
		a, err := k.Announcement()
		if err != nil {
			t.Fatal(err)
		}
		first := keyIDOf(t, a, identity, now)
		mailAt := func() {
			if idle {
				return
			}
			if err := mailbox.ReceivedMailAt(k, now); err != nil {
				t.Fatal(err)
			}
		}
		mailAt()
		for _, d := range []time.Duration{30, 37} {
			now = t0.Add(d * 24 * time.Hour)
			mailAt()
			if _, err := k.Rotate(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		if gone := !keyFileExists(dir, first.String()); gone == idle {
			t.Fatalf("idle=%v: first key deleted = %v at day 37", idle, gone)
		}
		now = t0.Add(44 * 24 * time.Hour)
		mailAt()
		if _, err := k.Rotate(context.Background()); err != nil {
			t.Fatal(err)
		}
		if keyFileExists(dir, first.String()) {
			t.Fatalf("idle=%v: first key kept at day 44", idle)
		}
		if n := liveCount(t, db); n > 3 {
			t.Fatalf("idle=%v: %d live keys, want at most 3", idle, n)
		}
	}
}

// On a steady clock a key is still deleted at not_after + 7 d (21 d), not
// before: by age when mail arrives, by the MaxLive cap on an idle daemon.
func TestRotateSteadyScheduleUnchanged(t *testing.T) {
	for _, idle := range []bool{false, true} {
		dir := testutil.TempDir(t)
		db := newDB(t)
		t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
		now := t0
		k, pub, _ := newKeysOn(t, dir, db, func() time.Time { return now })
		identity := base64.RawURLEncoding.EncodeToString(pub)
		a, err := k.Announcement()
		if err != nil {
			t.Fatal(err)
		}
		first := keyIDOf(t, a, identity, now)
		for h := 1; h <= 21*24+1; h++ {
			now = t0.Add(time.Duration(h) * time.Hour)
			if !idle {
				if err := mailbox.ReceivedMailAt(k, now); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := k.Rotate(context.Background()); err != nil {
				t.Fatal(err)
			}
			gone := !keyFileExists(dir, first.String())
			if want := !now.Before(t0.Add(21 * 24 * time.Hour)); gone != want {
				t.Fatalf("idle=%v: at +%dh first key deleted = %v, want %v", idle, h, gone, want)
			}
		}
	}
}
