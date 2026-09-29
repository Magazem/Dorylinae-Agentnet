package daemon_test

// R55-F5 acceptance over real daemons (Docs/review/58-r55-f5-spec.md A1, A8,
// A9, A10): every kind's approval summary names the peer by its full
// fingerprint, states what decides the approval, holds no result content,
// is refused (never cut) when too long, and is rejected at confirm when the
// peer renamed itself while the approval waited (OD-R55F5-5).

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/displaytext"
	"github.com/Magazem/Dorylinae-Agentnet/internal/envelope"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

// keyFP is the grouped fingerprint of a wire key, as the summary shows it.
func keyFP(t *testing.T, key string) string {
	t.Helper()
	fp, err := envelope.KeyFingerprint(key)
	if err != nil {
		t.Fatal(err)
	}
	return envelope.FormatFingerprint(fp)
}

func displayQuote(s string) string { return displaytext.Quote(s) }

// renamePeer changes the name n holds for the peer key, as a roster update
// or a re-pairing would while an approval waits.
func renamePeer(t *testing.T, n *harnessNode, key, name string) {
	t.Helper()
	if err := n.exec(`UPDATE peers SET name = ? WHERE public_key = ?`, name, key); err != nil {
		t.Fatal(err)
	}
}

// waitRejectedPrecondition waits for approval id to be rejected with reason
// precondition (the confirm-time comparison of R55-F5).
func waitRejectedPrecondition(t *testing.T, n *harnessNode, id string) {
	t.Helper()
	harnessWait(t, id+" to be rejected (precondition)", func() bool {
		return n.count(`SELECT COUNT(*) FROM approvals WHERE id = '`+id+`' AND state = 'rejected'`) == 1 &&
			n.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'approval.reject' AND detail LIKE '%`+id+`%' AND detail LIKE '%"precondition"%'`) == 1
	})
}

func approvalListRaw(t *testing.T, n *harnessNode) string {
	t.Helper()
	var raw json.RawMessage
	n.call("approval_list", nil, &raw)
	return string(raw)
}

// A9, A10 (release, accept_result): the summaries state the peer, its
// fingerprint, the request, the session, the round, the status, the sizes
// and (release) K; the result's content is in no summary and not in
// approval_list; a rename while the approval waits rejects it.
func TestResultApprovalSummaries(t *testing.T) {
	e := newQEnv(t)
	_, sid := openSession(t, e.a, e.b, e.teamID, "sensitive work")
	e.grant(t, sid, "fs.read", qDir(t), false)
	const marker = "QMARK-F5"
	e.result(t, sid, qMarkerResult(marker))
	e.waitState(t, sid, "quarantined")

	var rel struct {
		Approval approval.View `json:"approval"`
	}
	e.a.call("ws_release", map[string]any{"id": sid}, &rel)
	s := rel.Approval.Summary
	for _, want := range []string{
		"Release the quarantined result of peer " + keyFP(t, e.b.key) + ` named "bob" for your task request "sensitive work" (session ` + sid + ", round 1): status pass, ",
		" bytes of output, 0 artifact(s). Your agent will then be able to read it. This session had 1 sensitive grant(s). Confirm only if you mean to release this result.",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("release summary lacks %q:\n%s", want, s)
		}
	}
	if list := approvalListRaw(t, e.a); strings.Contains(s, marker) || strings.Contains(list, marker) {
		t.Fatal("the quarantined result's content is in the summary or approval_list")
	}
	if n := e.a.count(`SELECT COUNT(*) FROM approvals WHERE summary LIKE '%` + marker + `%'`); n != 0 {
		t.Fatal("approvals.summary holds the result's content")
	}

	// OD-R55F5-5: the peer renames itself while the approval waits.
	renamePeer(t, e.a, e.b.key, "bob2")
	e.approve(t, rel.Approval.ID)
	waitRejectedPrecondition(t, e.a, rel.Approval.ID)
	if e.a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'quarantined'`) != 1 {
		t.Fatal("a rejected release changed the session")
	}

	// A fresh release shows the new name and goes through.
	e.a.call("ws_release", map[string]any{"id": sid}, &rel)
	if !strings.Contains(rel.Approval.Summary, `named "bob2"`) {
		t.Fatalf("release summary: %s", rel.Approval.Summary)
	}
	e.approve(t, rel.Approval.ID)
	e.waitState(t, sid, "awaiting_result")

	var acc struct {
		Approval approval.View `json:"approval"`
	}
	e.a.call("ws_accept_result", map[string]any{"id": sid, "human": true}, &acc)
	s = acc.Approval.Summary
	if !strings.HasPrefix(s, "Accept the result of peer "+keyFP(t, e.b.key)+` named "bob2" for your task request "sensitive work" (session `+sid+", round 1): status pass, ") ||
		!strings.HasSuffix(s, "This closes the request as accepted. Confirm only if you checked this result.") {
		t.Fatalf("accept summary: %s", s)
	}
	if strings.Contains(s, marker) || strings.Contains(approvalListRaw(t, e.a), marker) {
		t.Fatal("the result's content is in the accept_result summary or approval_list")
	}
	renamePeer(t, e.a, e.b.key, "bob3")
	e.approve(t, acc.Approval.ID)
	waitRejectedPrecondition(t, e.a, acc.Approval.ID)
	if e.a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'awaiting_result'`) != 1 {
		t.Fatal("a rejected accept_result changed the session")
	}
}

// A8, A10 (device_link): the summary holds the role, the other device's
// fingerprint first and 'agentnet identity'; a rename while it waits rejects
// it and no link attempt is sent.
func TestDeviceLinkSummaryShowsFingerprint(t *testing.T) {
	ctrl, help := newDevPair(t)
	res, err := ctrl.requestLink(help, "controller", devFP(t, help))
	if err != nil {
		t.Fatal(err)
	}
	s := res.Approval.Summary
	want := "Link this device as the controller of peer " + devFP(t, help) + ` named "desktop". `
	if !strings.HasPrefix(s, want) || !strings.Contains(s, "Compare all five groups of this fingerprint with what 'agentnet identity' shows on the other device.") {
		t.Fatalf("link summary: %s", s)
	}
	renamePeer(t, ctrl.harnessNode, help.key, "desktop2")
	ctrl.win.answer(res.Approval.ID, "approve", ctrl.notifier.lastCode(t))
	waitRejectedPrecondition(t, ctrl.harnessNode, res.Approval.ID)
	if n := ctrl.count(`SELECT COUNT(*) FROM device_links WHERE state IN ('waiting', 'active')`); n != 0 {
		t.Fatalf("a rejected link approval left %d links", n)
	}
	if n := ctrl.auditCount("device.link_intent"); n != 0 {
		t.Fatal("a rejected link approval sent its link attempt")
	}
}

// A1 (review 55 C14-02, inverted), A8, A10 (device_scope): a scope whose
// summary does not fit the window is refused with bad_scope before any
// approval exists; a scope that fits names the controller's fingerprint; a
// rename while it waits rejects it and no scope is stored.
func TestDeviceScopeSummary(t *testing.T) {
	e := newHelperEnv(t, true)
	scope := func(cmds ...map[string]any) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{
			"types": []string{"task"}, "repos": []map[string]string{{"label": "repo", "path": e.repo}},
			"commands": cmds, "expires": e.help.clk.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
		})
		return raw
	}
	var res daemon.DeviceScopeSetResult
	err := ipcCallErr(e.help.harnessNode, "device_scope_set", map[string]any{"peer": e.ctrl.key, "scope": scope(
		e.cmd("test", 900, nil, "test", strings.Repeat("a", 4000)),
		e.cmd("zz", 900, nil, "-c", "HIDDEN-PAYLOAD"),
	)}, &res)
	var ie *ipc.Error
	if !errors.As(err, &ie) || ie.Code != daemon.CodeBadScope || !strings.HasPrefix(ie.Message, "scope: ") {
		t.Fatalf("a scope too long for the window: err = %v, want bad_scope on scope", err)
	}
	if n := e.help.count(`SELECT COUNT(*) FROM approvals WHERE kind = 'device_scope'`); n != 0 {
		t.Fatalf("a refused scope created %d approvals", n)
	}
	if n := e.help.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'approval.create' AND detail LIKE '%device_scope%'`); n != 0 {
		t.Fatal("a refused scope was audited as an approval")
	}

	if err := ipcCallErr(e.help.harnessNode, "device_scope_set", map[string]any{"peer": e.ctrl.key, "scope": scope(e.cmd("mk-pass", 60, nil, "exit", "0"))}, &res); err != nil {
		t.Fatal(err)
	}
	if want := "Let peer " + devFP(t, e.ctrl) + ` named "laptop" run 1 command(s) (mk-pass) on this device until `; !strings.HasPrefix(res.Approval.Summary, want) {
		t.Fatalf("scope summary: %s", res.Approval.Summary)
	}
	renamePeer(t, e.help.harnessNode, e.ctrl.key, "laptop2")
	e.help.win.answer(res.Approval.ID, "approve", e.help.notifier.lastCode(t))
	waitRejectedPrecondition(t, e.help.harnessNode, res.Approval.ID)
	if n := e.help.count(`SELECT COUNT(*) FROM device_scopes`); n != 0 {
		t.Fatal("a rejected scope approval stored the scope")
	}
}

// A10 (debate_constraint): the summary shows the fingerprint; a rename while
// it waits rejects it and the constraint is stored nowhere.
func TestDebateConstraintSummaryRename(t *testing.T) {
	a, b, teamID := newConstrainPair(t)
	var res daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{To: b.key, Type: "debate", Team: teamID, Title: "Retries", Brief: "How should the outbox retry?",
		Debate: &daemon.DebateParam{Position: e2ePosition("Capped backoff"), Rounds: 1}}, &res)
	sid := res.Session
	harnessWait(t, "B to store the debate", phaseIs(b.harnessNode, sid, debate.PhaseInvited))
	dsSubmit(t, b.ds, sid, debate.KindPosition, `{"argument":"Simple.","claim":"Fixed retry"}`)
	a.run.mark("debate_submit")
	harnessWait(t, "A to reach rounds", phaseIs(a.harnessNode, sid, debate.PhaseRounds))

	pend := a.constrain(t, sid, "No new dependency")
	if want := "Add a human constraint to the debate " + sid + " with peer " + keyFP(t, b.key) + ` named "bob": "No new dependency".`; !strings.HasPrefix(pend.Summary, want) {
		t.Fatalf("constraint summary: %s", pend.Summary)
	}
	renamePeer(t, a.harnessNode, b.key, "bob2")
	a.approve(t, pend.ID)
	waitRejectedPrecondition(t, a.harnessNode, pend.ID)
	if n := a.count(`SELECT COUNT(*) FROM debate_constraints WHERE session = '` + sid + `'`); n != 0 {
		t.Fatal("a rejected constraint was stored")
	}
}

// A5 (review 55 C11-01): the grant summary states the resolved path, the
// label, the branch, the scope, PUBLIC and NOT quarantined, the duration,
// the UTC expiry, the session, the quoted request title and the peer's
// fingerprint first; fs.read without a scope says the whole folder and
// Sensitive.
func TestGrantApprovalSummary(t *testing.T) {
	e := newQEnv(t)
	_, sid := openSession(t, e.a, e.b, e.teamID, "Check the parser")
	repo := qGitRepo(t)
	var out struct {
		Grant    daemon.GrantView `json:"grant"`
		Approval approval.View    `json:"approval"`
	}
	e.a.call("grant_create", daemon.GrantCreateParams{Peer: e.b.key, Session: sid, Action: "git.read", Resource: repo + "#main",
		Scope: "docs", Public: true, Expires: "2h"}, &out)
	var label string
	if err := e.a.query(`SELECT label FROM grants WHERE id = '`+out.Grant.ID+`'`, &label); err != nil {
		t.Fatal(err)
	}
	s := out.Approval.Summary
	for _, want := range []string{
		"Grant git.read to peer " + keyFP(t, e.b.key) + ` named "bob" on ` + displayQuote(repo) + " (label " + displayQuote(label) + `), branch "main", only "docs" inside it, for 2 h until `,
		" UTC, in session " + sid + ` (your task request "Check the parser"). PUBLIC: you state this repository is public; results of this session are NOT quarantined. Confirm only if you asked for exactly this.`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("grant summary lacks %q:\n%s", want, s)
		}
	}
	e.a.call("grant_create", daemon.GrantCreateParams{Peer: e.b.key, Session: sid, Action: "fs.read", Resource: qDir(t)}, &out)
	if s := out.Approval.Summary; !strings.Contains(s, ", the whole folder, for 2 h until ") || !strings.Contains(s, "Sensitive: results of this session stay quarantined until you release them.") {
		t.Fatalf("fs.read summary: %s", s)
	}
}
