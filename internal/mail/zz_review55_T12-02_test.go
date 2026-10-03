package mail

// Review 55 theme T12, finding T12-02 (R55-053), inverted by R55-F28: a
// mail_seen prune run while the receiver's wall clock is stepped forward must
// not remove rows whose mail is still inside the receive window once the clock
// is corrected, so a relay replay stays a duplicate.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

const day = 24 * time.Hour

// stepFixture is a receiver whose clock (Receiver and Opener) is *now, with
// counting team.leave and keys kinds.
func stepFixture(t *testing.T, now *time.Time) (*recvFixture, *int) {
	f := newRecvFixture(t)
	clock := func() time.Time { return *now }
	f.rcv.Now = clock
	f.rcv.Opener.Now = clock
	applied := 0
	count := Kind{Apply: func(context.Context, *sql.Tx, *Opened) error { applied++; return nil }}
	f.rcv.Kinds = map[string]Kind{"team.leave": count, "keys": count}
	return f, &applied
}

func (f *recvFixture) mailAt(id, kind string, created time.Time) envelopeAt {
	return envelopeAt{f, env(f.sender, f.recip, sealTo(f.t, f.sender, f.recip, id, kind, map[string]any{"team": "t-00000000000000000000000000000001"}, created))}
}

const testID2 = "m-00000000000000000000000000000002"
const testID3 = "m-00000000000000000000000000000003"

func TestReview55T12_02PruneDuringForwardStepAdmitsReplay(t *testing.T) {
	now := vectorNow
	f, applied := stepFixture(t, &now)
	e := f.mailAt(testID, "team.leave", vectorNow)
	e.handle(t)
	e.handle(t) // a normal replay the same day is a duplicate
	if *applied != 1 {
		t.Fatalf("applied = %d before the step, want 1", *applied)
	}

	// Five days later the wall clock steps 31 days ahead; the daily/at-start
	// prune runs once during the step.
	now = vectorNow.Add(36 * day)
	if n, err := Prune(context.Background(), f.st.DB(), now); err != nil || n != 0 {
		t.Fatalf("Prune during the step = %d, %v; want 0", n, err)
	}
	// NTP corrects the clock: true time is created + 5 d. The relay replays.
	now = vectorNow.Add(5 * day)
	e.handle(t)
	if *applied != 1 {
		t.Fatalf("applied = %d, want 1 (relay replay admitted after a prune during a forward step)", *applied)
	}
}

// A mail received during the step is stamped with the sender's time, so it does
// not move the prune cutoff forward either.
func TestReview55T12_02MailDuringForwardStep(t *testing.T) {
	now := vectorNow
	f, applied := stepFixture(t, &now)
	e1 := f.mailAt(testID, "team.leave", vectorNow)
	e1.handle(t)

	// True time is +12 d, the clock reads +40 d. A keys mail (no 14-day
	// limit) sent at the true time arrives during the step. Stamped with the
	// stepped clock it would move the cutoff past e1.
	now = vectorNow.Add(40 * day)
	sent := vectorNow.Add(12 * day)
	ann := signedAnnouncement(t, f.sender.priv, f.sender.mbox.PublicKey().Bytes(),
		sent.Format(time.RFC3339), sent.Add(30*day).Format(time.RFC3339), nil)
	keys := envelopeAt{f, env(f.sender, f.recip, sealTo(t, f.sender, f.recip, testID2, "keys", map[string]any{"announcement": ann}, sent))}
	keys.handle(t)
	if got, want := f.seenAt(t, testID2), vectorNow.Add(12*day+MaxSkew).Format(StoreTimeFmt); got != want {
		t.Fatalf("received_at of a mail received during the step = %s, want %s (created + MaxSkew)", got, want)
	}
	if n, err := Prune(context.Background(), f.st.DB(), now); err != nil || n != 0 {
		t.Fatalf("Prune during the step = %d, %v; want 0", n, err)
	}
	now = vectorNow.Add(12 * day)
	e1.handle(t)
	if *applied != 2 { // e1 once and the keys mail once
		t.Fatalf("applied = %d, want 2 (e1 replay admitted)", *applied)
	}
}

// A clock stepped back prunes nothing early, and a replay after the correction
// is still a duplicate.
func TestReview55T12_02BackwardStep(t *testing.T) {
	now := vectorNow
	f, applied := stepFixture(t, &now)
	e := f.mailAt(testID, "team.leave", vectorNow)
	e.handle(t)
	now = vectorNow.Add(-40 * day)
	if n, err := Prune(context.Background(), f.st.DB(), now); err != nil || n != 0 {
		t.Fatalf("Prune during a backward step = %d, %v; want 0", n, err)
	}
	now = vectorNow.Add(3 * day)
	e.handle(t)
	if *applied != 1 {
		t.Fatalf("applied = %d, want 1", *applied)
	}
}

// On a steady clock rows are pruned 35 days after their stamp once newer mail
// has arrived, and the replay of a pruned id is refused by step 11. Without
// newer mail the newest rows are kept.
func TestReview55T12_02SteadyPrune(t *testing.T) {
	now := vectorNow
	f, applied := stepFixture(t, &now)
	e := f.mailAt(testID, "team.leave", vectorNow)
	e.handle(t)
	// A relay held the mail for 3 days: the stamp is created + MaxSkew.
	now = vectorNow.Add(3 * day)
	f.mailAt(testID2, "team.leave", vectorNow.Add(-time.Hour)).handle(t)
	if got, want := f.seenAt(t, testID2), vectorNow.Add(-time.Hour+MaxSkew).Format(StoreTimeFmt); got != want {
		t.Fatalf("received_at = %s, want %s", got, want)
	}

	now = vectorNow.Add(40 * day)
	if n, err := Prune(context.Background(), f.st.DB(), now); err != nil || n != 0 {
		t.Fatalf("Prune with no newer mail = %d, %v; want 0", n, err)
	}
	f.mailAt(testID3, "team.leave", now).handle(t)
	if n, err := Prune(context.Background(), f.st.DB(), now); err != nil || n != 2 {
		t.Fatalf("Prune = %d, %v; want 2", n, err)
	}
	if err := f.rcv.Handle(context.Background(), e.env); err == nil {
		t.Fatal("replay of a pruned id accepted, want a step 11 reject")
	}
	if *applied != 3 {
		t.Fatalf("applied = %d, want 3", *applied)
	}
}

func TestSeenCutoffEmptyAndBadBody(t *testing.T) {
	f := newRecvFixture(t)
	cut, err := SeenCutoff(context.Background(), f.st.DB(), vectorNow)
	if err != nil || cut != "" {
		t.Fatalf("empty table: cutoff %q, %v; want \"\"", cut, err)
	}
	newest := vectorNow.Add(-day)
	if _, err := f.st.DB().Exec(`INSERT INTO mail_seen (from_key, id, received_at) VALUES ('k', 'm', ?)`,
		newest.Format(StoreTimeFmt)+badBodyMark); err != nil {
		t.Fatal(err)
	}
	cut, err = SeenCutoff(context.Background(), f.st.DB(), vectorNow.Add(100*day))
	if want := newest.Add(-SeenRetention).Format(StoreTimeFmt); err != nil || cut != want {
		t.Fatalf("cutoff %q, %v; want %q", cut, err, want)
	}
}

type envelopeAt struct {
	f   *recvFixture
	env envelope.Envelope
}

func (e envelopeAt) handle(t *testing.T) {
	t.Helper()
	if err := e.f.rcv.Handle(context.Background(), e.env); err != nil {
		t.Fatalf("Handle: %v", err)
	}
}

func (f *recvFixture) seenAt(t *testing.T, id string) string {
	t.Helper()
	var at string
	if err := f.st.DB().QueryRow(`SELECT received_at FROM mail_seen WHERE id = ?`, id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}
