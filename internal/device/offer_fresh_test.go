package device

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// D22 (review 36 L4): an offer counts only while now < offer.at + 10 min, by
// the sender's own clock, even if the local intent is still alive.
func TestActivateNeedsFreshOffer(t *testing.T) {
	s, clk := newTestStore(t)
	waitingIntent(t, s, "p", RoleHelper)
	stale := offerFrom(s, "p", RoleController)
	stale.At = clk.Now().Add(-IntentTTL)
	if _, ok := tryActivate(t, s, "p", stale); ok {
		t.Fatal("an offer made 10 min ago activated")
	}
	fresh := offerFrom(s, "p", RoleController)
	fresh.At = clk.Now().Add(-IntentTTL + time.Second)
	if _, ok := tryActivate(t, s, "p", fresh); !ok {
		t.Fatal("an offer made 9m59s ago did not activate")
	}
}

// D22 (review 36 L4): an offer older than the last device.unlink received
// from that peer is ignored; the latest unlink wins.
func TestOfferBeforeUnlink(t *testing.T) {
	s, clk := newTestStore(t)
	ctx := context.Background()
	o := offerFrom(s, "p", RoleController)
	check := func(peer string, o Offer) bool {
		t.Helper()
		var before bool
		inTx(t, s, func(tx *sql.Tx) {
			var err error
			if before, err = s.OfferBeforeUnlinkTx(ctx, tx, peer, o); err != nil {
				t.Fatal(err)
			}
		})
		return before
	}
	if check("p", o) {
		t.Fatal("no unlink yet, but the offer counts as older")
	}
	unlinkAt := clk.Now().Add(time.Minute)
	inTx(t, s, func(tx *sql.Tx) {
		if err := s.NoteUnlinkTx(ctx, tx, "p", unlinkAt, clk.Now()); err != nil {
			t.Fatal(err)
		}
		// An older unlink arriving later does not move the mark back.
		if err := s.NoteUnlinkTx(ctx, tx, "p", unlinkAt.Add(-time.Hour), clk.Now()); err != nil {
			t.Fatal(err)
		}
	})
	if !check("p", o) {
		t.Fatal("an offer made before the unlink was not recognised")
	}
	if check("other", o) {
		t.Fatal("another peer's unlink affected this offer")
	}
	later := o
	later.At = unlinkAt.Add(time.Second)
	if check("p", later) {
		t.Fatal("an offer made after the unlink was refused")
	}
}
