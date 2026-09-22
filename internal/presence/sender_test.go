package presence

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/peers"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
	"github.com/Magazem/Dorylinae-Agentnet/internal/team"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// TestIntervalFormula checks Docs/protocol/presence.md §Sending:
// interval = max(30, ceil(|visible|/3)), 30 s at 90 peers and 31 s at 91, and
// the PresenceInterval test override.
func TestIntervalFormula(t *testing.T) {
	s := &Sender{}
	cases := []struct {
		n    int
		want int
	}{{0, 30}, {1, 30}, {90, 30}, {91, 31}, {900, 300}}
	for _, c := range cases {
		if got := s.intervalSeconds(c.n); got != c.want {
			t.Errorf("intervalSeconds(%d) = %d, want %d", c.n, got, c.want)
		}
	}
	s.PresenceInterval = 1500 * time.Millisecond
	if got := s.intervalSeconds(90); got != 1 {
		t.Errorf("PresenceInterval override: intervalSeconds(90) = %d, want 1", got)
	}
}

// TestEffectivelyOnlineBoundary is the fake-clock unit test of ticket 1.2c:
// the last heartbeat at t, daemon_online true at t+75s, false at t+75.001s
// (under the 90 s acceptance bound), for a 30 s interval.
func TestEffectivelyOnlineBoundary(t *testing.T) {
	last := testNow
	if !EffectivelyOnline("online", last, 30, last.Add(75*time.Second)) {
		t.Fatal("t+75s: want daemon_online true")
	}
	if EffectivelyOnline("online", last, 30, last.Add(75*time.Second+time.Millisecond)) {
		t.Fatal("t+75.001s: want daemon_online false")
	}
}

// fakeOutbox records team.roster sends for TestMaybeResyncCooldown.
type fakeOutbox struct{ sent []string }

func (f *fakeOutbox) Submit(_ context.Context, to, _ string, _ any) (mail.Submitted, error) {
	f.sent = append(f.sent, to)
	return mail.Submitted{ID: "m-0000000000000000000000000000000000000000000000000000000000000000", State: "queued"}, nil
}

// newResyncFixture builds a real team.Store owning one team, so
// Sender.MaybeResync can exercise Store.ResyncMember end to end.
func newResyncFixture(t *testing.T, now func() time.Time) (*team.Store, *fakeOutbox, team.Team) {
	t.Helper()
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c, err := agentcard.New(pub, "owner", "h", []agentcard.Skill{{ID: "s", Name: "Skill", Description: "d"}}, now())
	if err != nil {
		t.Fatal(err)
	}
	sc, err := agentcard.Sign(priv, c)
	if err != nil {
		t.Fatal(err)
	}
	card, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(testutil.TempDir(t), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ps := peers.NewStore(st.DB())
	self := envelope.KeyString(pub)
	ts := team.NewStore(st.DB(), ps, self)
	ts.OwnCard = func() ([]byte, error) { return card, nil }
	ts.Now = now
	ob := &fakeOutbox{}
	ts.Outbox = ob
	tm, err := ts.Create(ctx, "x", now())
	if err != nil {
		t.Fatal(err)
	}
	return ts, ob, tm
}

// TestMaybeResyncCooldown checks Docs/protocol/presence.md §Receiving step 7:
// a lower epoch resends the roster to the reporting peer, at most once per
// 10 minutes, and not at all once the epoch is caught up.
func TestMaybeResyncCooldown(t *testing.T) {
	now := testNow
	clock := func() time.Time { return now }
	ts, ob, tm := newResyncFixture(t, clock)
	s := &Sender{Team: ts, Self: ts.Self, Now: clock}

	behind := map[string]int64{tm.ID: 0} // team is at epoch 1
	s.MaybeResync("peer-1", behind)
	if len(ob.sent) != 1 || ob.sent[0] != "peer-1" {
		t.Fatalf("first report: sent = %v, want [peer-1]", ob.sent)
	}
	s.MaybeResync("peer-1", behind) // within the 10-minute cooldown
	if len(ob.sent) != 1 {
		t.Fatalf("within cooldown: sent = %v, want no new send", ob.sent)
	}
	now = now.Add(11 * time.Minute)
	s.MaybeResync("peer-1", behind)
	if len(ob.sent) != 2 {
		t.Fatalf("after cooldown: sent = %v, want a second send", ob.sent)
	}
	upToDate := map[string]int64{tm.ID: tm.Epoch}
	s.MaybeResync("peer-1", upToDate)
	if len(ob.sent) != 2 {
		t.Fatalf("caught-up epoch: sent = %v, want no new send", ob.sent)
	}
	// A team this daemon does not own is ignored.
	s.MaybeResync("peer-1", map[string]int64{"t-11111111111111111111111111111111": 0})
	if len(ob.sent) != 2 {
		t.Fatalf("unowned team: sent = %v, want no new send", ob.sent)
	}
}
