package daemon_test

// Integration coverage for ws_accept_result --human and ws_release, from a
// confirmed approval through to the real session state transition
// (Docs/protocol/work-session.md §Accept-result, §Quarantine (2.4);
// Docs/protocol/approval.md). The approval is confirmed through the
// approval.Store/IPC directly (approval_confirm with the code captured from
// a fake notifier), not through the CLI: CLI entry to approve changes in
// 2.2d, so a CLI-level test would need rewriting there for no reason.

import (
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// approvalCodeFrom extracts the 6-digit code from the fake notifier's last
// body, exactly as TestApprovalIPCConfirmRunsAction (approval_test.go) does:
// the code is the last 6 characters of the notification body
// (Docs/protocol/approval.md §Delivering the code).
func approvalCodeFrom(body string) string {
	if len(body) < 6 {
		return ""
	}
	return body[len(body)-6:]
}

// TestAcceptResultHumanIntegration: ws_accept_result{human:true} creates a
// pending approval; confirming it (through approval_confirm, with the code
// read from the notification, as a human would) runs AcceptResultInTx and
// closes the session on both sides with verification forced to
// human_accepted (Docs/protocol/work-session.md §Accept-result).
func TestAcceptResultHumanIntegration(t *testing.T) {
	r := newHarnessRelay(t)
	notifier := &fakeApprovalNotifier{}
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.ApprovalNotify = notifier
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	_, sid := openSession(t, a, b, teamID, "human accept")

	var res daemon.SessionResult
	b.call("ws_result", map[string]any{
		"id":     sid,
		"result": map[string]any{"status": "pass", "summary": "looks good", "verification": "tests_passed"},
	}, &res)
	harnessWait(t, "A to see awaiting_result", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'awaiting_result'`) == 1
	})

	var apprResp struct {
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
	}
	a.call("ws_accept_result", map[string]any{"id": sid, "human": true}, &apprResp)
	if apprResp.Approval.ID == "" {
		t.Fatalf("ws_accept_result --human did not return an approval")
	}
	if notifier.lastBody == "" {
		t.Fatalf("no approval notification was shown")
	}
	code := approvalCodeFrom(notifier.lastBody)

	var confirmed map[string]any
	a.call("approval_confirm", map[string]any{"id": apprResp.Approval.ID, "code": code}, &confirmed)

	harnessWait(t, "A's session to close human_accepted", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'closed' AND outcome = 'accepted' AND verification = 'human_accepted'`) == 1
	})
	harnessWait(t, "B's session to close", func() bool {
		return b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'closed'`) == 1
	})
	harnessWait(t, "A's ws.close audit to record the human accept", func() bool {
		return a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'ws.accept_result' AND detail LIKE '%human_accepted%'`) == 1
	})
	harnessWait(t, "A's ws.close audit row", func() bool {
		return a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'ws.close' AND detail LIKE '%'||'`+sid+`'||'%'`) >= 1
	})
}

// TestReleaseIntegration: ws_release on a quarantined session creates a
// pending approval; confirming it runs ReleaseInTx, moving the session back
// to awaiting_result with the result now visible, and audits ws.release
// (Docs/protocol/work-session.md §Quarantine (2.4), "a session with a
// sensitive grant cannot deliver a result until released, and the audit log
// records the release").
func TestReleaseIntegration(t *testing.T) {
	r := newHarnessRelay(t)
	notifier := &fakeApprovalNotifier{}
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.Quarantine = alwaysQuarantineDaemon
	a.ApprovalNotify = notifier
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	_, sid := openSession(t, a, b, teamID, "release test")
	const marker = "RELEASE-MARKER"

	var res daemon.SessionResult
	b.call("ws_result", map[string]any{
		"id":     sid,
		"result": map[string]any{"status": "pass", "summary": marker, "verification": "tests_passed"},
	}, &res)
	harnessWait(t, "A to see quarantined", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'quarantined'`) == 1
	})

	var show daemon.SessionShowResult
	a.call("ws_show", map[string]any{"id": sid}, &show)
	if show.Session.Result != nil {
		t.Fatalf("quarantined result visible before release: %+v", show.Session)
	}
	if show.Session.Quarantine == nil {
		t.Fatalf("ws_show does not report quarantine status: %+v", show.Session)
	}

	var apprResp struct {
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
	}
	a.call("ws_release", map[string]any{"id": sid}, &apprResp)
	if apprResp.Approval.ID == "" {
		t.Fatalf("ws_release did not return an approval")
	}
	code := approvalCodeFrom(notifier.lastBody)

	var confirmed map[string]any
	a.call("approval_confirm", map[string]any{"id": apprResp.Approval.ID, "code": code}, &confirmed)

	harnessWait(t, "A's session to leave quarantined", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'awaiting_result'`) == 1
	})
	harnessWait(t, "ws.release audit", func() bool {
		return a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'ws.release' AND detail LIKE '%'||'`+sid+`'||'%'`) == 1
	})
	// No release audit ever carries the result content, only ids/enums/counts.
	if a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'ws.release' AND detail LIKE '%`+marker+`%'`) != 0 {
		t.Fatalf("ws.release audit leaked the result content")
	}

	var show2 daemon.SessionShowResult
	a.call("ws_show", map[string]any{"id": sid}, &show2)
	if show2.Session.Result == nil || show2.Session.Result.Summary != marker {
		t.Fatalf("result still withheld after release: %+v", show2.Session)
	}
}
