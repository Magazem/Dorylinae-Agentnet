package daemon_test

import (
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// seedTeamID is a fixed valid team id for the presence e2e tests.
const seedTeamID = "t-0123456789abcdef0123456789abcdef"

func teamStatus(t *testing.T, n *harnessNode, teamID string) daemon.StatusResult {
	t.Helper()
	var st daemon.StatusResult
	n.call("status", daemon.StatusParams{Team: teamID}, &st)
	return st
}

func teamMember(t *testing.T, st daemon.StatusResult, key string) *daemon.StatusTeamMember {
	t.Helper()
	if st.Team == nil {
		return nil
	}
	for i := range st.Team.Members {
		if st.Team.Members[i].PublicKey == key {
			return &st.Team.Members[i]
		}
	}
	return nil
}

// setUpPresenceTeam starts A and B, pairs them, seeds a shared active team
// (standing in for the team_invite/team_join flow, ticket 1.1d, not yet in
// this worktree), and waits for A to see B online.
func setUpPresenceTeam(t *testing.T, presenceInterval time.Duration) (r *harnessRelay, a, b *harnessNode) {
	t.Helper()
	r = newHarnessRelay(t)
	a = newHarnessNode(t, "alice", r)
	b = newHarnessNode(t, "bob", r)
	a.PresenceInterval, b.PresenceInterval = presenceInterval, presenceInterval
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
	harnessWait(t, "A to see B online", func() bool {
		m := teamMember(t, teamStatus(t, a, seedTeamID), b.key)
		return m != nil && m.DaemonOnline
	})
	return r, a, b
}

// TestPresenceOfflineAfterKill: B's daemon and its relay connection go away
// together, without a goodbye (Docs/protocol/presence.md §Levels, "Daemon
// killed on B"). With PresenceInterval scaled to 1 s, A must show B offline
// within 2.5 s + 0.5 s (bound 90 s at the real 30 s interval).
func TestPresenceOfflineAfterKill(t *testing.T) {
	r, a, b := setUpPresenceTeam(t, time.Second)

	// Drop the relay (so B's shutdown goodbye, sent over a connection that is
	// already down, fails and is dropped with no retry) and stop B, together.
	r.stop()
	b.stop()
	begin := time.Now()
	r.start()
	waitRelayConnected(t, r, a.key)

	deadline := begin.Add(3 * time.Second)
	for {
		m := teamMember(t, teamStatus(t, a, seedTeamID), b.key)
		if m != nil && !m.DaemonOnline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("A did not show B offline within %s of the kill", time.Since(begin))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if d := time.Since(begin); d > 3*time.Second {
		t.Errorf("A took %s to show B offline, want under 3s (2.5s + 0.5s at a 1s interval)", d)
	}
}

// TestPresenceAgentActiveEdge: any IPC call on B, after its agent window has
// lapsed, sends an immediate heartbeat and A reflects agent_active quickly
// (Docs/protocol/presence.md, OD-P1-7; bound 30 s at the real interval).
func TestPresenceAgentActiveEdge(t *testing.T) {
	r := newHarnessRelay(t)
	a := newHarnessNode(t, "alice", r)
	b := newHarnessNode(t, "bob", r)
	a.PresenceInterval, b.PresenceInterval = time.Second, time.Second
	b.AgentWindow = 200 * time.Millisecond
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
	harnessWait(t, "A to see B online", func() bool {
		m := teamMember(t, teamStatus(t, a, seedTeamID), b.key)
		return m != nil && m.DaemonOnline
	})

	// Let B's short agent window lapse, and A's next heartbeat report it idle.
	harnessWait(t, "A to see B agent idle", func() bool {
		m := teamMember(t, teamStatus(t, a, seedTeamID), b.key)
		return m != nil && !m.AgentActive
	})

	begin := time.Now()
	var id daemon.IdentityResult
	b.call("identity", nil, &id) // any IPC call on B: the agent-active edge

	deadline := begin.Add(2 * time.Second)
	for {
		m := teamMember(t, teamStatus(t, a, seedTeamID), b.key)
		if m != nil && m.AgentActive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("A did not show B agent_active within %s of the IPC call", time.Since(begin))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestPresenceOnlineEdgeFlushesOutbox: A has mail queued for B while B is
// unreachable; when B's heartbeat resumes, the online edge
// (Docs/protocol/presence.md §Receiving step 6) flushes it at once instead of
// waiting for the outbox's own backoff (internal/mail/outbox.go OnPeerOnline).
func TestPresenceOnlineEdgeFlushesOutbox(t *testing.T) {
	r, a, b := setUpPresenceTeam(t, time.Second)

	b.stop()
	harnessWait(t, "relay to see B leave", func() bool { return !r.rs.Connected(b.key) })
	res := a.submit(b.key, "note", "presence-edge-flush")
	harnessWait(t, "A's mail to reach the relay", func() bool { s, _ := a.outboxState(res.ID); return s == "relayed" })

	begin := time.Now()
	b.start()
	waitRelayConnected(t, r, b.key)
	harnessWait(t, "B to receive the mail promptly via the presence online edge", func() bool {
		return b.count(`SELECT COUNT(*) FROM mail_inbox`) == 1
	})
	if d := time.Since(begin); d > 10*time.Second {
		t.Errorf("delivery took %s after B came back, want well under the outbox backoff (1 min+)", d)
	}
}

// TestStatusTeamJSONShape checks the "team" object of "status --team" against
// Docs/protocol/ipc.md §status: self first attributes, sort order (owner
// first, then name), and every documented member field.
func TestStatusTeamJSONShape(t *testing.T) {
	_, a, b := setUpPresenceTeam(t, time.Second)

	st := teamStatus(t, a, seedTeamID)
	if st.Team == nil {
		t.Fatal("status --team returned no team object")
	}
	tm := st.Team
	if tm.ID != seedTeamID || tm.Owner != a.key || tm.State != "active" || tm.Epoch != 1 {
		t.Fatalf("team = %+v", tm)
	}
	if len(tm.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(tm.Members))
	}
	if !tm.Members[0].Owner || tm.Members[0].PublicKey != a.key {
		t.Fatalf("owner is not listed first: %+v", tm.Members[0])
	}
	self := teamMember(t, st, a.key)
	if self == nil || !self.Self || !self.DaemonOnline || self.LastSeen == nil {
		t.Fatalf("self member = %+v", self)
	}
	peer := teamMember(t, st, b.key)
	if peer == nil || peer.Self || !peer.DaemonOnline || peer.LastSeen == nil {
		t.Fatalf("peer member = %+v", peer)
	}
	if st.Presence.Relay != "connected" {
		t.Fatalf("presence.relay = %q, want connected", st.Presence.Relay)
	}
}
