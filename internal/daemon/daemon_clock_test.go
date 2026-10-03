package daemon_test

import (
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// R55-F28 (review 55 R55-151, O-106): request_defer parses a relative "until"
// on the daemon clock (Options.Now), the clock of the request store, not on
// time.Now. B's clock runs an hour ahead here.
func TestRequestDeferUsesDaemonClock(t *testing.T) {
	const skew = time.Hour
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	b.Now = func() time.Time { return time.Now().Add(skew) }
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	var sub daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "task", Team: teamID, Title: "t", Brief: "What: x\n",
	}, &sub)
	harnessWait(t, "B to see the request", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND state = 'pending'`) == 1
	})

	before := time.Now()
	var res daemon.RequestLifecycleResult
	b.call("request_defer", map[string]any{"id": sub.ID, "until": "2m"}, &res)
	after := time.Now()

	var until string
	if err := b.query(`SELECT deferred_until FROM requests WHERE direction = 'in' AND id = '`+sub.ID+`'`, &until); err != nil {
		t.Fatal(err)
	}
	got, err := time.Parse(time.RFC3339, until)
	if err != nil {
		t.Fatalf("deferred_until %q: %v", until, err)
	}
	lo := before.Add(skew + 2*time.Minute).Truncate(time.Second)
	hi := after.Add(skew + 2*time.Minute)
	if got.Before(lo) || got.After(hi) {
		t.Fatalf("deferred_until = %s, want between %s and %s (the daemon clock + 2m)", got, lo, hi)
	}
}
