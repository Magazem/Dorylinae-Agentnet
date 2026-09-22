package daemon_test

import (
	"context"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// TestPresenceInvisibleHidesFromPeers is the 1.3 acceptance test: B
// `--invisible` -> A shows B offline immediately, with last_seen = the
// goodbye time, and never online over several intervals while B keeps
// running CLI commands (Docs/protocol/presence.md §Visibility, ticket 1.3).
func TestPresenceInvisibleHidesFromPeers(t *testing.T) {
	const interval = time.Second
	_, a, b := setUpPresenceTeam(t, interval)

	before := time.Now()
	var set daemon.PresenceGetResult
	b.call("presence_set", daemon.PresenceSetParams{Invisible: true}, &set)
	if set.Mode != "invisible" {
		t.Fatalf("presence_set invisible: mode = %q", set.Mode)
	}

	harnessWait(t, "A to see B go offline", func() bool {
		m := teamMember(t, teamStatus(t, a, seedTeamID), b.key)
		return m != nil && !m.DaemonOnline
	})
	m := teamMember(t, teamStatus(t, a, seedTeamID), b.key)
	if m == nil || m.LastSeen == nil {
		t.Fatalf("member = %+v", m)
	}
	lastSeen, err := time.Parse(time.RFC3339, *m.LastSeen)
	if err != nil {
		t.Fatalf("last_seen %q: %v", *m.LastSeen, err)
	}
	if lastSeen.Before(before.Add(-2 * time.Second)) {
		t.Fatalf("last_seen %s is not around the goodbye time %s", lastSeen, before)
	}

	// B keeps running CLI commands (the agent-active edge would normally send
	// an immediate heartbeat), but stays invisible: A never sees it online
	// again over several intervals.
	deadline := time.Now().Add(5 * interval)
	for time.Now().Before(deadline) {
		var id daemon.IdentityResult
		b.call("identity", nil, &id)
		if mm := teamMember(t, teamStatus(t, a, seedTeamID), b.key); mm != nil && mm.DaemonOnline {
			t.Fatal("A saw B online while B stays invisible")
		}
		time.Sleep(interval / 4)
	}
}

// TestPresenceOnlyTeamScopesVisibility is the 1.3 acceptance test:
// `--only-team x` -> a member of x sees B online, and a member of y (not x)
// only sees B offline (Docs/protocol/presence.md §Visibility, ticket 1.3).
func TestPresenceOnlyTeamScopesVisibility(t *testing.T) {
	const teamX = "t-00000000000000000000000000000001"
	const teamY = "t-00000000000000000000000000000002"

	r := newHarnessRelay(t)
	a := newHarnessNode(t, "alice", r)
	b := newHarnessNode(t, "bob", r)
	c := newHarnessNode(t, "carol", r)
	a.PresenceInterval, b.PresenceInterval, c.PresenceInterval = time.Second, time.Second, time.Second
	a.start()
	b.start()
	c.start()
	waitRelayConnected(t, r, a.key, b.key, c.key)
	harnessPair(t, a, b)
	harnessPair(t, c, b)
	a.stop()
	b.stop()
	c.stop()
	harnessSeedTeam(t, teamX, a, b) // owner a, member b
	harnessSeedTeam(t, teamY, c, b) // owner c, member b
	a.start()
	b.start()
	c.start()
	waitRelayConnected(t, r, a.key, b.key, c.key)

	harnessWait(t, "A to see B online (visible default)", func() bool {
		m := teamMember(t, teamStatus(t, a, teamX), b.key)
		return m != nil && m.DaemonOnline
	})

	var set daemon.PresenceGetResult
	b.call("presence_set", daemon.PresenceSetParams{OnlyTeam: teamX}, &set)
	if set.Mode != "only_team" || set.Team == nil || set.Team.ID != teamX {
		t.Fatalf("presence_set only_team: %+v", set)
	}

	harnessWait(t, "A (member of x) still sees B online", func() bool {
		m := teamMember(t, teamStatus(t, a, teamX), b.key)
		return m != nil && m.DaemonOnline
	})
	harnessWait(t, "C (member of y, not x) sees B offline", func() bool {
		m := teamMember(t, teamStatus(t, c, teamY), b.key)
		return m != nil && !m.DaemonOnline
	})
}

// TestPresenceOnlyTeamAutoInvisibleOnLeave is the 1.3 acceptance test:
// leaving team x while in mode `only_team x` moves the mode to `invisible`
// and audits the change (Docs/protocol/presence.md §Sending "only_team":
// "If x is no longer active, the mode becomes invisible").
func TestPresenceOnlyTeamAutoInvisibleOnLeave(t *testing.T) {
	_, _, b := setUpPresenceTeam(t, 300*time.Millisecond)

	var set daemon.PresenceGetResult
	b.call("presence_set", daemon.PresenceSetParams{OnlyTeam: seedTeamID}, &set)
	if set.Mode != "only_team" {
		t.Fatalf("presence_set only_team: %+v", set)
	}

	var tr daemon.TeamResult
	b.call("team_leave", daemon.TeamRefParams{Team: seedTeamID}, &tr)

	harnessWait(t, "B's mode to auto-degrade to invisible", func() bool {
		var g daemon.PresenceGetResult
		b.call("presence_get", nil, &g)
		return g.Mode == "invisible"
	})
	harnessWait(t, "the team_gone audit row", func() bool {
		return b.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'presence.mode' AND actor = 'daemon'`) >= 1
	})
}

// TestPresenceHumanOffHidesFromPeers is the 1.3 acceptance test: `--human
// off` makes peers see human_present: null (Docs/protocol/presence.md
// §Human sharing, ticket 1.3).
func TestPresenceHumanOffHidesFromPeers(t *testing.T) {
	r := newHarnessRelay(t)
	a := newHarnessNode(t, "alice", r)
	b := newHarnessNode(t, "bob", r)
	a.PresenceInterval, b.PresenceInterval = 300*time.Millisecond, 300*time.Millisecond
	b.Idle = func(context.Context) (time.Duration, bool) { return 0, true } // present
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	a.stop()
	b.stop()
	harnessSeedTeam(t, seedTeamID, a, b)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)

	harnessWait(t, "A to see B human present", func() bool {
		m := teamMember(t, teamStatus(t, a, seedTeamID), b.key)
		return m != nil && m.HumanPresent != nil && *m.HumanPresent
	})

	var set daemon.PresenceGetResult
	b.call("presence_set", daemon.PresenceSetParams{Human: strPtr("off")}, &set)
	if set.HumanShare {
		t.Fatalf("presence_set human off: %+v", set)
	}

	harnessWait(t, "A to see B human unknown after --human off", func() bool {
		m := teamMember(t, teamStatus(t, a, seedTeamID), b.key)
		return m != nil && m.HumanPresent == nil
	})
}

func strPtr(s string) *string { return &s }
