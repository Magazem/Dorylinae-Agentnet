package daemon_test

// Ticket 1.1d acceptance: team invite and join via pairing v2
// (Docs/review/11-phase1-tickets.md, 1.1d).

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

// harnessTeamInvite runs team_invite on owner and waits for the issuer's code
// (or a terminal, non-pending state, e.g. team_full).
func harnessTeamInvite(t *testing.T, owner *harnessNode, teamRef string) daemon.TeamInviteResult {
	t.Helper()
	var res daemon.TeamInviteResult
	owner.call("team_invite", daemon.TeamInviteParams{Team: teamRef}, &res)
	harnessWait(t, "invite code", func() bool {
		owner.call("pair_status", daemon.PairStatusParams{PairingID: res.ID}, &res.PairStatus)
		return res.Code != "" || res.State != "pending"
	})
	return res
}

// harnessTeamJoin runs team_join on joiner with code and waits for the exchange to end.
func harnessTeamJoin(t *testing.T, joiner *harnessNode, code string) daemon.PairStatus {
	t.Helper()
	var res daemon.PairStatus
	joiner.call("team_join", daemon.TeamJoinParams{Code: code}, &res)
	harnessWait(t, "join to finish", func() bool {
		joiner.call("pair_status", daemon.PairStatusParams{PairingID: res.ID}, &res)
		return res.State == "complete" || res.State == "failed"
	})
	return res
}

// teamMemberKeys returns the member keys team_show reports, or an empty map
// if the team is not known yet (the roster has not arrived): callers poll
// with harnessWait rather than treating that as a hard failure.
func teamMemberKeys(t *testing.T, n *harnessNode, teamRef string) map[string]bool {
	t.Helper()
	var res daemon.TeamShowResult
	if err := ipcCallErr(n, "team_show", daemon.TeamRefParams{Team: teamRef}, &res); err != nil {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(res.Team.Members))
	for _, m := range res.Team.Members {
		out[m.PublicKey] = true
	}
	return out
}

// exec runs a write query against the node's database while the daemon is
// running (a separate short-lived connection; SQLite is opened WAL with a
// busy timeout, Docs/protocol/ipc.md §Endpoint).
func (n *harnessNode) exec(q string, args ...any) error {
	n.t.Helper()
	st, err := sql.Open("sqlite", "file:"+n.p.DB+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	_, err = st.Exec(q, args...)
	return err
}

// ipcCallErr makes one IPC call and returns its error instead of failing the test.
func ipcCallErr(n *harnessNode, method string, params, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ipc.Call(ctx, n.p.Endpoint, method, params, out)
}

func errCode(err error) string {
	var ie *ipc.Error
	if errors.As(err, &ie) {
		return ie.Code
	}
	return ""
}

// TestTeamInviteJoinTwoTeams is the 1.1 acceptance test (Docs/protocol/team.md
// §Scoping rules): A creates x and invites B; C creates y and invites B; B
// joins both; team show x on B lists A and B, team show y lists B and C, and
// A and C are not each other's peers. It also covers the ticket's third
// member: D joins x and gets A and B as trust=team peers, and B gets D.
func TestTeamInviteJoinTwoTeams(t *testing.T) {
	r := newHarnessRelay(t)
	a := newHarnessNode(t, "alice", r)
	b := newHarnessNode(t, "bob", r)
	c := newHarnessNode(t, "carol", r)
	d := newHarnessNode(t, "dave", r)
	a.start()
	b.start()
	c.start()
	d.start()
	waitRelayConnected(t, r, a.key, b.key, c.key, d.key)

	var teamX, teamY daemon.TeamResult
	a.call("team_create", daemon.TeamCreateParams{Name: "x"}, &teamX)
	c.call("team_create", daemon.TeamCreateParams{Name: "y"}, &teamY)

	invX := harnessTeamInvite(t, a, teamX.Team.ID)
	if invX.Code == "" {
		t.Fatalf("invite x = %+v, want a code", invX)
	}
	joinX := harnessTeamJoin(t, b, invX.Code)
	if joinX.State != "complete" {
		t.Fatalf("B join x = %+v", joinX)
	}

	invY := harnessTeamInvite(t, c, teamY.Team.ID)
	joinY := harnessTeamJoin(t, b, invY.Code)
	if joinY.State != "complete" {
		t.Fatalf("B join y = %+v", joinY)
	}

	harnessWait(t, "B to see team x's roster", func() bool { return len(teamMemberKeys(t, b, teamX.Team.ID)) == 2 })
	harnessWait(t, "B to see team y's roster", func() bool { return len(teamMemberKeys(t, b, teamY.Team.ID)) == 2 })

	membersX := teamMemberKeys(t, b, teamX.Team.ID)
	if !membersX[a.key] || !membersX[b.key] || len(membersX) != 2 {
		t.Fatalf("team x members on B = %v, want {A, B}", membersX)
	}
	membersY := teamMemberKeys(t, b, teamY.Team.ID)
	if !membersY[b.key] || !membersY[c.key] || len(membersY) != 2 {
		t.Fatalf("team y members on B = %v, want {B, C}", membersY)
	}

	// A and C are not each other's peers, and neither sees the other anywhere.
	var aPeers, cPeers daemon.PeersResult
	a.call("peers", nil, &aPeers)
	c.call("peers", nil, &cPeers)
	for _, p := range aPeers.Peers {
		if p.PublicKey == c.key {
			t.Fatal("A has C as a peer")
		}
	}
	for _, p := range cPeers.Peers {
		if p.PublicKey == a.key {
			t.Fatal("C has A as a peer")
		}
	}

	// A third member D joins x: D pairs directly with the owner A (trust=code,
	// pairing v2) and is introduced to the other member B by A's roster
	// (trust=team, Docs/protocol/team.md §Introduced peers). B, in turn, gets D.
	invX2 := harnessTeamInvite(t, a, teamX.Team.ID)
	joinX2 := harnessTeamJoin(t, d, invX2.Code)
	if joinX2.State != "complete" {
		t.Fatalf("D join x = %+v", joinX2)
	}
	harnessWait(t, "D to see A and B", func() bool { return len(teamMemberKeys(t, d, teamX.Team.ID)) == 3 })
	harnessWait(t, "B to see D", func() bool { return len(teamMemberKeys(t, b, teamX.Team.ID)) == 3 })

	var dPeers daemon.PeersResult
	d.call("peers", nil, &dPeers)
	trustByKey := make(map[string]string, len(dPeers.Peers))
	for _, p := range dPeers.Peers {
		trustByKey[p.PublicKey] = p.Trust
	}
	if trustByKey[a.key] != "code" {
		t.Fatalf("D's trust of A (direct v2 pair) = %q, want code", trustByKey[a.key])
	}
	if trustByKey[b.key] != "team" {
		t.Fatalf("D's trust of B (introduced) = %q, want team", trustByKey[b.key])
	}
	var bPeers daemon.PeersResult
	b.call("peers", nil, &bPeers)
	found := false
	for _, p := range bPeers.Peers {
		if p.PublicKey == d.key {
			found = true
			if p.Trust != "team" {
				t.Errorf("B's view of D: trust = %q, want team", p.Trust)
			}
		}
	}
	if !found {
		t.Fatal("B does not have D as a peer")
	}
}

// TestTeamInvitePlainPairDoesNotJoin: redeeming an invite code with plain
// `pair_redeem` (not team_join) pairs the two daemons without joining the
// team (Docs/cli/team.md §Invite and join).
func TestTeamInvitePlainPairDoesNotJoin(t *testing.T) {
	r := newHarnessRelay(t)
	a := newHarnessNode(t, "alice", r)
	b := newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)

	var teamX daemon.TeamResult
	a.call("team_create", daemon.TeamCreateParams{Name: "x"}, &teamX)
	inv := harnessTeamInvite(t, a, teamX.Team.ID)

	var red daemon.PairStatus
	b.call("pair_redeem", daemon.PairRedeemParams{Code: inv.Code}, &red)
	harnessWait(t, "plain pair to complete", func() bool {
		b.call("pair_status", daemon.PairStatusParams{PairingID: red.ID}, &red)
		return red.State == "complete"
	})
	if red.State != "complete" || red.Peer == nil {
		t.Fatalf("plain pair = %+v, want complete", red)
	}

	time.Sleep(300 * time.Millisecond) // let a wrongly-sent join arrive, if any
	if n := b.count(`SELECT COUNT(*) FROM team_pending_joins`); n != 0 {
		t.Fatalf("team_pending_joins rows on B = %d, want 0", n)
	}
	members := teamMemberKeys(t, a, teamX.Team.ID)
	if len(members) != 1 {
		t.Fatalf("team x members on A = %v, want just the owner", members)
	}
}

// TestTeamInviteFullTeam: an invite for a full team gives team_full without
// starting a pairing (Docs/protocol/team.md §`team.join`; ipc.md `team_invite`).
func TestTeamInviteFullTeam(t *testing.T) {
	r := newHarnessRelay(t)
	a := newHarnessNode(t, "alice", r)
	a.start()
	waitRelayConnected(t, r, a.key)

	var teamX daemon.TeamResult
	a.call("team_create", daemon.TeamCreateParams{Name: "x"}, &teamX)

	// Fill the roster to 32 members directly (cheaper than 31 real pairings).
	now := "2026-01-01T00:00:00Z"
	for i := 0; i < 31; i++ {
		key := "team-full-test-key-" + string(rune('a'+i))
		if err := a.exec(`INSERT INTO team_members (team_id, key, added) VALUES (?, ?, ?)`, teamX.Team.ID, key, now); err != nil {
			t.Fatal(err)
		}
	}

	var res daemon.TeamInviteResult
	err := ipcCallErr(a, "team_invite", daemon.TeamInviteParams{Team: teamX.Team.ID}, &res)
	if err == nil || errCode(err) != "team_full" {
		t.Fatalf("team_invite on a full team = %v, %+v, want team_full", err, res)
	}
}
