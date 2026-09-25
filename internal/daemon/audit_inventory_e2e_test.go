package daemon_test

// The e2e scenarios of TestAuditInventory (see audit_inventory_test.go).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

func (r *invRun) mark(method string) {
	r.mu.Lock()
	r.called[method] = true
	r.mu.Unlock()
}

// TestAuditInventory drives every state-changing method through two live daemons
// and checks the documented audit action of each (audit.md §Scope).
func TestAuditInventory(t *testing.T) {
	run := newInvRun()
	e := newQEnv(t)
	a, b := e.a, e.b
	raw := func() *json.RawMessage { return &json.RawMessage{} }

	// Presence, notifications, mail, ping, trust.
	run.call(a, "presence_set", daemon.PresenceSetParams{Invisible: true}, raw())
	run.call(a, "presence_set", daemon.PresenceSetParams{Visible: true}, raw())
	off := false
	run.call(a, "notify_set", daemon.NotifySetParams{Desktop: &off}, raw())
	a.submit(b.key, "note", "inventory")
	run.mark("mail_submit")
	run.call(a, "ping", daemon.PingParams{Peer: b.key}, raw())
	fpRaw, err := envelope.KeyFingerprint(b.key)
	if err != nil {
		t.Fatal(err)
	}
	run.call(a, "peers_verify", daemon.PeerVerifyParams{Peer: b.key, Fingerprint: envelope.FormatFingerprint(fpRaw)}, raw())

	// Teams: rename, and a second and third team for leave, remove and delete.
	run.call(a, "team_rename", daemon.TeamRenameParams{Team: e.teamID, Name: "x2"}, raw())
	newTeam := func(name string) string {
		var tr daemon.TeamResult
		a.call("team_create", daemon.TeamCreateParams{Name: name}, &tr)
		if j := harnessTeamJoin(t, b, harnessTeamInvite(t, a, tr.Team.ID).Code); j.State != "complete" {
			t.Fatalf("join %s: %+v", name, j)
		}
		harnessWait(t, name+"'s roster on both sides", func() bool {
			return len(teamMemberKeys(t, a, tr.Team.ID)) == 2 && len(teamMemberKeys(t, b, tr.Team.ID)) == 2
		})
		return tr.Team.ID
	}
	run.call(b, "team_leave", daemon.TeamRefParams{Team: newTeam("second")}, raw())
	third := newTeam("third")
	run.call(a, "team_remove", daemon.TeamRemoveParams{Team: third, Peer: b.key}, raw())
	run.call(a, "team_delete", daemon.TeamRefParams{Team: third}, raw())

	// Requests.
	submit := func(title string) string {
		var sub daemon.RequestSubmitResult
		run.call(a, "request_submit", daemon.RequestSubmitParams{To: b.key, Type: "task", Team: e.teamID, Title: title, Brief: "What: " + title + "\n"}, &sub)
		harnessWait(t, "B to see "+title, func() bool {
			return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+sub.ID+`' AND state = 'pending'`) == 1
		})
		return sub.ID
	}
	accept := func(id string) string {
		var acc daemon.RequestLifecycleResult
		run.call(b, "request_accept", map[string]any{"id": id}, &acc)
		if acc.Request.Session == nil {
			t.Fatalf("request_accept opened no session: %+v", acc)
		}
		sid := acc.Request.Session.ID
		harnessWait(t, "A's session", func() bool {
			return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'open'`) == 1
		})
		return sid
	}
	run.call(b, "request_decline", map[string]any{"id": submit("to decline"), "reason": "busy"}, &daemon.RequestLifecycleResult{})
	run.call(b, "request_defer", map[string]any{"id": submit("to defer"), "until": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, &daemon.RequestLifecycleResult{})
	run.call(a, "request_cancel", map[string]any{"id": submit("to cancel")}, &daemon.RequestCancelResult{})
	toResend := submit("to resend")
	var mailID string
	if err := a.query(`SELECT mail_id FROM requests WHERE direction = 'out' AND id = '`+toResend+`'`, &mailID); err != nil {
		t.Fatal(err)
	}
	if err := a.exec(`UPDATE outbox SET state = 'failed' WHERE id = ?`, mailID); err != nil {
		t.Fatal(err)
	}
	run.call(a, "request_resend", map[string]any{"id": toResend}, raw())

	// Session 1: grant, approvals, fetch, result, quarantine, release, changes, accept.
	sid1 := accept(submit("session one"))
	dir := qDir(t)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	type grantOut struct {
		Grant    daemon.GrantView `json:"grant"`
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
	}
	var g1 grantOut
	run.call(a, "grant_create", daemon.GrantCreateParams{Peer: b.key, Session: sid1, Action: "fs.read", Resource: dir}, &g1)
	run.call(a, "approval_open", map[string]any{"id": g1.Approval.ID}, raw())
	e.approve(t, g1.Approval.ID)
	harnessWait(t, "the grant to be active", func() bool {
		return a.count(`SELECT COUNT(*) FROM grants WHERE id = '`+g1.Grant.ID+`' AND state = 'active'`) == 1
	})
	var g1b grantOut
	a.call("grant_create", daemon.GrantCreateParams{Peer: b.key, Session: sid1, Action: "fs.read", Resource: dir}, &g1b)
	run.call(a, "approval_reject", map[string]any{"id": g1b.Approval.ID}, raw())
	if st, err := e.bFetch(daemon.FetchStartParams{Grant: g1.Grant.ID, Op: capability.OpRead, Path: "f.txt", Length: 16}); err != nil || st.State != "complete" {
		t.Fatalf("fetch = %+v, %v", st, err)
	}
	run.mark("fetch_start")
	run.call(b, "ws_result", map[string]any{"id": sid1, "result": qMarkerResult("INV1")}, &daemon.SessionResult{})
	e.waitState(t, sid1, "quarantined")
	var rel approvalIDResult
	run.call(a, "ws_release", map[string]any{"id": sid1}, &rel)
	e.approve(t, rel.Approval.ID)
	e.waitState(t, sid1, "awaiting_result")
	run.call(a, "ws_request_changes", map[string]any{"id": sid1, "changes": "again"}, &daemon.SessionResult{})
	harnessWait(t, "B's session to reopen", func() bool {
		return b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid1+`' AND state = 'open' AND round = 2`) == 1
	})

	// A grant policy, added (approved) and removed; the first grant revoked.
	var pol approvalIDResult
	run.call(a, "grant_policy_add", daemon.GrantPolicyAddParams{Peer: b.key, Action: "fs.read", Resource: dir, MaxExpires: "72h"}, &pol)
	e.approve(t, pol.Approval.ID)
	harnessWait(t, "the policy to be stored", func() bool { return a.count(`SELECT COUNT(*) FROM grant_policies`) == 1 })
	var policyID string
	if err := a.query(`SELECT id FROM grant_policies`, &policyID); err != nil {
		t.Fatal(err)
	}
	run.call(a, "grant_policy_remove", map[string]any{"id": policyID}, raw())
	run.call(a, "grant_revoke", daemon.GrantRevokeParams{ID: g1.Grant.ID}, &daemon.GrantRevokeResult{})

	run.call(b, "ws_result", map[string]any{"id": sid1, "result": map[string]any{"status": "pass", "summary": "ok", "verification": "tests_passed"}}, &daemon.SessionResult{})
	// The result is quarantined again (the session still has a fs.read grant): release it.
	e.waitState(t, sid1, "quarantined")
	releaseAndAwait(t, e, sid1)
	run.call(a, "ws_accept_result", map[string]any{"id": sid1}, &daemon.SessionResult{})

	// Session 2 is quarantined and discarded, session 3 cancelled, session 4 completed by B.
	sid2 := accept(submit("session two"))
	e.grant(t, sid2, "fs.read", dir, false)
	e.result(t, sid2, qMarkerResult("INV3"))
	e.waitState(t, sid2, "quarantined")
	run.call(a, "ws_discard", map[string]any{"id": sid2}, &daemon.SessionResult{})
	run.call(a, "ws_cancel", map[string]any{"id": accept(submit("session three"))}, &daemon.SessionResult{})
	// A worker-side cancel sends the ws.cancel mail; A applies it (ws.cancel_in).
	run.call(b, "ws_cancel", map[string]any{"id": accept(submit("session five"))}, &daemon.SessionResult{})
	harnessWait(t, "A to apply the worker's cancel", func() bool { return auditHas(a, "ws.cancel_in") })
	req4 := submit("session four")
	accept(req4)
	run.call(b, "request_complete", map[string]any{"id": req4, "result": map[string]any{"status": "pass", "summary": "ok", "output": "ok\n"}}, &daemon.RequestLifecycleResult{})
	harnessWait(t, "A to hold session four's result (awaiting or quarantined)", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE request_id = '`+req4+`' AND state IN ('awaiting_result', 'quarantined')`) == 1
	})

	// peers_remove last: it ends the pairing.
	run.call(a, "peers_remove", daemon.PeerRemoveParams{Peer: b.key}, raw())

	// The no-paths rule (review 44 L4): no row of either log carries a path separator.
	for _, n := range []*harnessNode{a, b} {
		for _, ev := range auditList(t, n, audit.ListParams{Limit: audit.MaxLimit}) {
			if strings.ContainsAny(string(ev.Detail), `/\`) {
				t.Errorf("%s: row %d (%s) detail looks like a path: %s", n.name, ev.ID, ev.Action, ev.Detail)
			}
		}
	}
	checkInventory(t, run, methodInventory, sortedKeys(methodInventory, false), true, a, b)
	checkInventory(t, run, mailKindInventory, sortedKeys(mailKindInventory, false), false, a, b)
}

// TestAuditInventoryDevices is the device half: link, scope and unlink between
// two own devices (the harness pair is a controller and a helper).
func TestAuditInventoryDevices(t *testing.T) {
	run := newInvRun()
	ctrl, help := newDevPair(t)
	link := func(d, peer *devNode, role string) {
		var res daemon.DeviceLinkResult
		run.call(d.harnessNode, "device_link", daemon.DeviceLinkParams{Peer: peer.key, As: role, Fingerprint: devFP(t, peer)}, &res)
		d.win.answer(res.Approval.ID, "approve", d.notifier.lastCode(t))
		harnessWait(t, d.name+"'s approval to be applied", func() bool { return d.linkCount("pending_approval") == 0 })
	}
	link(ctrl, help, "controller")
	link(help, ctrl, "helper")
	harnessWait(t, "both sides to be active", func() bool { return ctrl.linkCount("active") == 1 && help.linkCount("active") == 1 })

	repo := testutil.TempDir(t)
	scope := map[string]any{
		"types":    []string{"task"},
		"repos":    []map[string]string{{"label": "repo", "path": repo}},
		"commands": []map[string]any{{"name": "t", "repo": "repo", "argv": []string{"go", "version"}, "timeout_s": 5}},
		"expires":  help.clk.Now().Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339),
	}
	rawScope, _ := json.Marshal(scope)
	var set daemon.DeviceScopeSetResult
	run.call(help.harnessNode, "device_scope_set", map[string]any{"peer": ctrl.key, "scope": json.RawMessage(rawScope)}, &set)
	help.win.answer(set.Approval.ID, "approve", help.notifier.lastCode(t))
	harnessWait(t, "the scope to be stored", func() bool { return help.count(`SELECT COUNT(*) FROM device_scopes`) == 1 })
	run.call(help.harnessNode, "device_scope_clear", daemon.DevicePeerParams{Peer: ctrl.key}, &daemon.DeviceScopeClearResult{})
	run.call(ctrl.harnessNode, "device_unlink", daemon.DeviceUnlinkParams{Peer: help.key}, &daemon.DeviceUnlinkResult{})
	harnessWait(t, "both sides to be revoked", func() bool { return ctrl.linkCount("revoked") == 1 && help.linkCount("revoked") == 1 })

	// device_link and device_scope_set/clear run on the helper, device_unlink on the controller.
	checkDeviceInventory(t, run, ctrl.harnessNode, help.harnessNode)
}

func checkDeviceInventory(t *testing.T, run *invRun, ctrl, help *harnessNode) {
	t.Helper()
	checkInventory(t, run, methodInventory, sortedKeys(methodInventory, true), true, ctrl, help)
	checkInventory(t, run, mailKindInventory, sortedKeys(mailKindInventory, true), false, ctrl, help)
}
