package daemon_test

// D48 (R55-082, R55-084): peers verify and the owner's team invite need a
// human's approval in the approval window (Docs/protocol/approval.md), and
// presence_set takes the ipc.md shape and refuses unknown fields (R55-112).

import (
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
)

func newGatePair(t *testing.T) (a, b *harnessNode) {
	t.Helper()
	r := newHarnessRelay(t)
	a, b = newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	return a, b
}

func peerTrust(t *testing.T, n *harnessNode, peer string) string {
	t.Helper()
	var trust string
	if err := n.query(`SELECT trust FROM peers WHERE public_key = '`+peer+`'`, &trust); err != nil {
		t.Fatal(err)
	}
	return trust
}

func groupedFingerprint(t *testing.T, key string) string {
	t.Helper()
	fp, err := envelope.KeyFingerprint(key)
	if err != nil {
		t.Fatal(err)
	}
	return envelope.FormatFingerprint(fp)
}

// peers verify with the right fingerprint creates an approval and changes
// nothing until a human approves; the window text names the peer and shows the
// grouped fingerprint; a rejection changes nothing; a wrong fingerprint is
// refused at once, with no approval.
func TestPeersVerifyNeedsHumanApproval(t *testing.T) {
	a, b := newGatePair(t)
	before := peerTrust(t, a, b.key)
	if before == "fingerprint" {
		t.Fatalf("trust after pairing = %q", before)
	}
	fp := groupedFingerprint(t, b.key)

	if code, _ := callCode(t, a, "peers_verify", daemon.PeerVerifyParams{Peer: b.key, Fingerprint: "AAAA BBBB CCCC DDDD EEEE"}); code != daemon.CodeFingerprintMismatch {
		t.Fatalf("wrong fingerprint: %q, want %s", code, daemon.CodeFingerprintMismatch)
	}
	var list daemon.ApprovalListResult
	a.call("approval_list", nil, &list)
	if len(list.Approvals) != 0 {
		t.Fatalf("a mismatch created %d approvals", len(list.Approvals))
	}

	var res daemon.PeerVerifyResult
	a.call("peers_verify", daemon.PeerVerifyParams{Peer: "bob", Fingerprint: strings.ReplaceAll(fp, " ", "")}, &res)
	ap := res.Approval
	if ap.Kind != approval.KindPeerVerify || ap.State != approval.StatePending {
		t.Fatalf("approval = %+v, want a pending peer_verify", ap)
	}
	if !strings.Contains(ap.Summary, fp) || !strings.Contains(ap.Summary, `"bob"`) {
		t.Fatalf("summary %q lacks the grouped fingerprint %q or the peer name", ap.Summary, fp)
	}
	if got := peerTrust(t, a, b.key); got != before {
		t.Fatalf("trust changed to %q before any approval", got)
	}
	if n := a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'peer.verify'`); n != 0 {
		t.Fatalf("peer.verify audited %d times before any approval", n)
	}

	a.call("approval_reject", map[string]string{"id": ap.ID}, nil)
	if got := peerTrust(t, a, b.key); got != before {
		t.Fatalf("trust changed to %q after a rejection", got)
	}

	a.call("peers_verify", daemon.PeerVerifyParams{Peer: b.key, Fingerprint: fp}, &res)
	a.humanApprove(res.Approval.ID)
	harnessWait(t, "trust to become fingerprint", func() bool { return peerTrust(t, a, b.key) == "fingerprint" })
	if n := a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'peer.verify'`); n != 1 {
		t.Fatalf("peer.verify audited %d times, want 1", n)
	}
}

// team invite creates no code until a human approves; the approval id opens
// the way to exactly one code, only for its own team.
func TestTeamInviteNeedsHumanApproval(t *testing.T) {
	a, _ := newGatePair(t)
	var tr daemon.TeamResult
	a.call("team_create", daemon.TeamCreateParams{Name: "x"}, &tr)
	var other daemon.TeamResult
	a.call("team_create", daemon.TeamCreateParams{Name: "y"}, &other)

	var res daemon.TeamInviteResult
	a.call("team_invite", daemon.TeamInviteParams{Team: tr.Team.ID}, &res)
	if res.Approval == nil || res.Approval.Kind != approval.KindTeamInvite || res.Approval.State != approval.StatePending {
		t.Fatalf("first call = %+v, want a pending team_invite approval", res)
	}
	if res.Code != "" || res.ID != "" {
		t.Fatalf("a code exists before the approval: %+v", res.PairStatus)
	}
	if !strings.Contains(res.Approval.Summary, "x") || !strings.Contains(res.Approval.Summary, tr.Team.ID) {
		t.Fatalf("summary %q lacks the team name or id", res.Approval.Summary)
	}
	id := res.Approval.ID

	// Still pending: no code.
	var again daemon.TeamInviteResult
	a.call("team_invite", daemon.TeamInviteParams{Team: tr.Team.ID, Approval: id}, &again)
	if again.Approval == nil || again.Code != "" {
		t.Fatalf("pending poll = %+v, want still pending without a code", again)
	}
	// An id that was never issued here, and another team's id, are refused.
	if code, _ := callCode(t, a, "team_invite", daemon.TeamInviteParams{Team: tr.Team.ID, Approval: "a-00000000000000000000000000000000"}); code != daemon.CodeBadState {
		t.Fatalf("unknown approval: %q, want %s", code, daemon.CodeBadState)
	}
	if code, _ := callCode(t, a, "team_invite", daemon.TeamInviteParams{Team: other.Team.ID, Approval: id}); code != daemon.CodeBadState {
		t.Fatalf("approval of another team: %q, want %s", code, daemon.CodeBadState)
	}

	a.humanApprove(id)
	var got daemon.TeamInviteResult
	harnessWait(t, "the approved invite", func() bool {
		got = daemon.TeamInviteResult{}
		a.call("team_invite", daemon.TeamInviteParams{Team: tr.Team.ID, Approval: id}, &got)
		return got.Approval == nil
	})
	if got.ID == "" || got.Team.ID != tr.Team.ID {
		t.Fatalf("approved invite = %+v, want a pairing for the team", got)
	}
	// One approval, one invite.
	if code, _ := callCode(t, a, "team_invite", daemon.TeamInviteParams{Team: tr.Team.ID, Approval: id}); code != daemon.CodeBadState {
		t.Fatalf("second use of the approval: %q, want %s", code, daemon.CodeBadState)
	}
}

// A rejected invite approval never releases a code.
func TestTeamInviteRejectedGivesNoCode(t *testing.T) {
	a, _ := newGatePair(t)
	var tr daemon.TeamResult
	a.call("team_create", daemon.TeamCreateParams{Name: "x"}, &tr)
	var res daemon.TeamInviteResult
	a.call("team_invite", daemon.TeamInviteParams{Team: tr.Team.ID}, &res)
	a.call("approval_reject", map[string]string{"id": res.Approval.ID}, nil)
	if code, _ := callCode(t, a, "team_invite", daemon.TeamInviteParams{Team: tr.Team.ID, Approval: res.Approval.ID}); code != daemon.CodeBadState {
		t.Fatalf("after a rejection: %q, want %s", code, daemon.CodeBadState)
	}
	if n := a.count(`SELECT COUNT(*) FROM team_invites`); n != 0 {
		t.Fatalf("a rejected invite left %d team_invites rows", n)
	}
}

// R55-112: presence_set takes {"mode", "team", "human_share"} (ipc.md) and
// refuses fields it does not know or a combination that means nothing.
func TestPresenceSetSpecShape(t *testing.T) {
	a, _ := newGatePair(t)
	var set daemon.PresenceGetResult
	a.call("presence_set", map[string]any{"mode": "invisible"}, &set)
	if set.Mode != "invisible" {
		t.Fatalf("presence_set {mode: invisible} = %+v", set)
	}
	a.call("presence_set", map[string]any{"human_share": false}, &set)
	if set.HumanShare || set.Mode != "invisible" {
		t.Fatalf("presence_set {human_share: false} = %+v", set)
	}
	for name, params := range map[string]map[string]any{
		"unknown field":          {"invisible": true},
		"old CLI shape":          {"only_team": "x"},
		"empty":                  {},
		"unknown mode":           {"mode": "hidden"},
		"team without only_team": {"mode": "visible", "team": "x"},
		"only_team without team": {"mode": "only_team"},
	} {
		if code, _ := callCode(t, a, "presence_set", params); code != "bad_request" {
			t.Errorf("%s: %q, want bad_request", name, code)
		}
	}
	a.call("presence_get", nil, &set)
	if set.Mode != "invisible" {
		t.Fatalf("a refused presence_set changed the mode to %q", set.Mode)
	}
}
