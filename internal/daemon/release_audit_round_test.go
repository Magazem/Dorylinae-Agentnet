package daemon_test

// Review 97 M1: a session released twice has two approved release approvals
// with the same subject; each ws.release row names its own round's approval.

import (
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

func TestReleaseRowNamesEachRoundsApproval(t *testing.T) {
	e := newQEnv(t)
	_, sid := openSession(t, e.a, e.b, e.teamID, "two rounds")
	e.grant(t, sid, "fs.read", qDir(t), false)

	var rel struct {
		Approval approval.View `json:"approval"`
	}
	e.result(t, sid, qMarkerResult("M1R1"))
	e.waitState(t, sid, "quarantined")
	e.a.call("ws_release", map[string]any{"id": sid}, &rel)
	first := rel.Approval.ID
	e.approve(t, first)
	e.waitState(t, sid, "awaiting_result")
	waitAuditDetail(t, e.a, audit.ActorCLI, "ws.release", `"approval":"`+first+`"`, `"round":1`)

	e.a.call("ws_request_changes", map[string]any{"id": sid, "changes": "again"}, &daemon.SessionResult{})
	harnessWait(t, "B's session to reopen in round 2", func() bool {
		return e.b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'open' AND round = 2`) == 1
	})
	e.result(t, sid, qMarkerResult("M1R2"))
	e.waitState(t, sid, "quarantined")
	e.a.call("ws_release", map[string]any{"id": sid}, &rel)
	second := rel.Approval.ID
	if second == first {
		t.Fatal("round 2 reused round 1's approval")
	}
	e.approve(t, second)
	e.waitState(t, sid, "awaiting_result")
	waitAuditDetail(t, e.a, audit.ActorCLI, "ws.release", `"approval":"`+second+`"`, `"round":2`)
	if n := e.a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'ws.release' AND detail LIKE '%"approval":"` + first + `"%'`); n != 1 {
		t.Fatalf("%d ws.release rows name round 1's approval, want 1", n)
	}
}
