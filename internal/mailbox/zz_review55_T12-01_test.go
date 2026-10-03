package mailbox_test

// Review 55 theme T12, finding T12-01 (R55-103), inverted by R55-F28: a wall
// clock stepped forward for one rotation run, then corrected, must neither
// delete the key peers seal to nor leave a current key whose announcement
// peers refuse.

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func TestReview55T12_01ForwardClockStep(t *testing.T) {
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

	// 20 days later (true time) the announcement is still accepted, and the
	// first key is deleted once its successor (made at the correction) has
	// been current for 7 days and its own not_after + 7 d has passed.
	now = t0.Add(20 * 24 * time.Hour)
	if _, err := k.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	cur, _ = k.Announcement()
	if _, _, err := mail.ParseAnnouncement(cur, identity, now); err != nil {
		t.Fatalf("still refused 20 days later: %v", err)
	}
	now = t0.Add(21*24*time.Hour + time.Minute)
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

// A clock stepped back deletes nothing, and after the correction the current
// announcement is accepted again.
func TestReview55T12_01BackwardClockStep(t *testing.T) {
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
	for i := 0; i < 3; i++ {
		now = trueNow.Add(-20*24*time.Hour + time.Duration(i)*time.Hour)
		if _, err := k.Rotate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	now = trueNow.Add(time.Hour)
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

// On a steady clock a key is still deleted at not_after + 7 d (21 d), not
// before.
func TestReview55T12_01SteadyScheduleUnchanged(t *testing.T) {
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
		if _, err := k.Rotate(context.Background()); err != nil {
			t.Fatal(err)
		}
		gone := !keyFileExists(dir, first.String())
		if want := !now.Before(t0.Add(21 * 24 * time.Hour)); gone != want {
			t.Fatalf("at +%dh first key deleted = %v, want %v", h, gone, want)
		}
	}
}
