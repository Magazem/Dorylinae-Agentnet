package daemon_test

// Ticket 2.9 acceptance (Docs/review/23-phase2-tickets.md "2.9 End-to-end and
// audit"): one e2e test drives the whole Phase 2 loop through two live
// daemons and a relay, reusing the qEnv harness (quarantine_e2e_test.go), the
// fetch client (fetch_e2e_test.go), the consult flow (consult_test.go) and
// the own-device helper (device_run_e2e_test.go). TestPhase2AuditHasNoContent
// repeats the same shape with marker strings and asserts none reach
// audit_events on either side, following the 1.9 pattern
// (internal/daemon/e2e_1_9_test.go).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

type approvalIDResult struct {
	Approval struct {
		ID string `json:"id"`
	} `json:"approval"`
}

// gitReadPolicy adds and approves a git.read policy for e.b over repo#branch,
// then issues a matching grant on sid, which must go active at once (no
// per-grant approval, 2.2c acceptance).
func gitReadPolicy(t *testing.T, e *qEnv, sid, repo, branch string) string {
	t.Helper()
	var pol approvalIDResult
	e.a.call("grant_policy_add", daemon.GrantPolicyAddParams{
		Peer: e.b.key, Action: "git.read", Resource: repo + "#" + branch, MaxExpires: "72h",
	}, &pol)
	if pol.Approval.ID == "" {
		t.Fatal("grant_policy_add returned no approval")
	}
	e.approve(t, pol.Approval.ID)
	harnessWait(t, "the git.read policy to be stored", func() bool {
		return e.a.count(`SELECT COUNT(*) FROM grant_policies`) == 1
	})
	var out struct {
		Grant daemon.GrantView `json:"grant"`
	}
	e.a.call("grant_create", daemon.GrantCreateParams{Peer: e.b.key, Session: sid, Action: "git.read", Resource: repo + "#" + branch}, &out)
	if out.Grant.State != "active" {
		t.Fatalf("policy-matched git.read grant = %+v, want active at once", out.Grant)
	}
	return out.Grant.ID
}

// releaseAndAwait runs ws_release on sid, confirms it and waits for
// awaiting_result.
func releaseAndAwait(t *testing.T, e *qEnv, sid string) {
	t.Helper()
	var rel approvalIDResult
	e.a.call("ws_release", map[string]any{"id": sid}, &rel)
	if rel.Approval.ID == "" {
		t.Fatal("ws_release returned no approval")
	}
	e.approve(t, rel.Approval.ID)
	e.waitState(t, sid, "awaiting_result")
}

// TestPhase2WholeLoop is the 2.9 whole-loop acceptance: A requests, B
// accepts (a session opens), A grants git.read (a policy) and fs.read (an
// approval), B lists and reads through both, A revokes one grant and B's
// next fetch of it fails, B submits a result which is quarantined, A
// releases, A requests changes, B submits again (quarantined again), A
// releases and accepts the result: both sides close and the request
// completes on both. Then a consult round trip and one own-device helper run.
func TestPhase2WholeLoop(t *testing.T) {
	e := newQEnv(t)
	reqID, sid := openSession(t, e.a, e.b, e.teamID, "whole loop review")

	repo := qGitRepo(t)
	gitGrant := gitReadPolicy(t, e, sid, repo, "main")

	fsDir := qDir(t)
	if err := os.WriteFile(filepath.Join(fsDir, "notes.txt"), []byte("hello from A"), 0o600); err != nil {
		t.Fatal(err)
	}
	fsGrant := e.grant(t, sid, "fs.read", fsDir, false)

	// B lists and reads through both grants.
	if st, err := e.bFetch(daemon.FetchStartParams{Grant: gitGrant, Op: capability.OpList}); err != nil || st.State != "complete" {
		t.Fatalf("git list = %+v, %v", st, err)
	}
	if st, err := e.bFetch(daemon.FetchStartParams{Grant: fsGrant, Op: capability.OpRead, Path: "notes.txt", Length: 64}); err != nil || st.State != "complete" {
		t.Fatalf("fs read = %+v, %v", st, err)
	}

	// A revokes fs.read: B's next fetch through it fails.
	var rev daemon.GrantRevokeResult
	e.a.call("grant_revoke", daemon.GrantRevokeParams{ID: fsGrant}, &rev)
	if code := e.failure(t, daemon.FetchStartParams{Grant: fsGrant, Op: capability.OpRead, Path: "notes.txt", Length: 64}); code != capability.CodeRevoked {
		t.Fatalf("fetch after revoke = %q, want revoked", code)
	}

	// B submits a result: the active git.read grant quarantines it.
	e.result(t, sid, qMarkerResult("LOOP-ROUND-1"))
	e.waitState(t, sid, "quarantined")

	// A releases.
	releaseAndAwait(t, e, sid)

	// A requests changes: round 2 reopens on B.
	var rc daemon.SessionResult
	e.a.call("ws_request_changes", map[string]any{"id": sid, "changes": "please expand the summary"}, &rc)
	if rc.Session.State != "open" || rc.Session.Round != 2 {
		t.Fatalf("ws_request_changes = %+v, want open round 2", rc.Session)
	}
	harnessWait(t, "B's session to reopen at round 2", func() bool {
		return e.b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'open' AND round = 2`) == 1
	})

	// B submits again: quarantined again.
	e.result(t, sid, qMarkerResult("LOOP-ROUND-2"))
	e.waitState(t, sid, "quarantined")
	releaseAndAwait(t, e, sid)

	// accept-result closes both sides; the request completes on both.
	var acc daemon.SessionResult
	e.a.call("ws_accept_result", map[string]any{"id": sid}, &acc)
	if acc.Session.State != "closed" || acc.Session.Outcome != "accepted" {
		t.Fatalf("accept-result = %+v", acc.Session)
	}
	harnessWait(t, "both sides closed and the request completed on both", func() bool {
		return e.a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'closed'`) == 1 &&
			e.b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'closed'`) == 1 &&
			e.a.count(`SELECT COUNT(*) FROM requests WHERE id = '`+reqID+`' AND direction = 'out' AND state = 'completed'`) == 1 &&
			e.b.count(`SELECT COUNT(*) FROM requests WHERE id = '`+reqID+`' AND direction = 'in' AND state = 'completed'`) == 1
	})

	// A consult round trip: B answers a pending question in one step, A waits
	// on the answer and accepts it.
	q := submitQuestion(t, e.a, e.b, e.teamID)
	var qres daemon.SessionResult
	e.b.call("ws_result", map[string]any{"id": q.ID, "result": map[string]any{"status": "n/a", "output": "yes, it is safe", "verification": "none"}}, &qres)
	// The 7-day peer-wide rule quarantines the answer too (consult.md).
	e.waitState(t, q.Session, "quarantined")
	releaseAndAwait(t, e, q.Session)
	var qshow daemon.SessionShowResult
	e.a.call("ws_show", map[string]any{"id": q.Session}, &qshow)
	if qshow.Session.Result == nil || qshow.Session.Result.Output != "yes, it is safe" {
		t.Fatalf("consult wait result = %+v", qshow.Session)
	}
	var qacc daemon.SessionResult
	e.a.call("ws_accept_result", map[string]any{"id": q.Session}, &qacc)
	if qacc.Session.State != "closed" {
		t.Fatalf("consult accept-result = %+v", qacc.Session)
	}

	// One own-device helper run (link, scope, request run).
	he := newHelperEnv(t, true)
	he.setScope(t, []string{"task"}, he.cmd("loop-pass", 30, nil, "exit", "0"))
	res := he.result(t, he.submit(t, "task", "loop-pass"))
	if res.Status != "pass" || res.ExitCode == nil || *res.ExitCode != 0 {
		t.Fatalf("helper run result = %+v", res)
	}
}

// phase2Markers are placed in every content-bearing field of
// TestPhase2AuditHasNoContent's run: file contents, paths, labels, branch
// names, scopes, results, notes, changes, cancel reasons, context files,
// helper command names, argv and output. None must appear in any
// audit_events row on either side.
var phase2Markers = []string{
	"P2AUDIT-TITLE-7f3a", "P2AUDIT-BRIEF-1c9e",
	"P2AUDIT-FSFILE-b204", "P2AUDIT-FSCONTENT-e651",
	"P2AUDIT-GITBRANCH-2d90", "P2AUDIT-GITFILE-a447", "P2AUDIT-GITCONTENT-9b12",
	"P2AUDIT-SCOPE-5e88",
	"P2AUDIT-RESULT1-3c71", "P2AUDIT-RESULT2-6a05",
	"P2AUDIT-CHANGES-d813",
	"P2AUDIT-CANCEL-4f26",
	"P2AUDIT-CTXNAME", "P2AUDIT-CTXTEXT-0a5d", "P2AUDIT-ANSWER-77e1",
	"p2audit-helpercmd-c390", "P2AUDIT-ARGV-88f4",
}

// TestPhase2AuditHasNoContent is the 2.9 acceptance: marker strings placed in
// file contents, paths, labels, branch names, scopes, results, notes,
// changes, cancel reasons, context files, helper command names, argv and
// output appear in no audit_events row on either side.
func TestPhase2AuditHasNoContent(t *testing.T) {
	e := newQEnv(t)
	reqID, sid := openSession(t, e.a, e.b, e.teamID, "P2AUDIT-TITLE-7f3a P2AUDIT-BRIEF-1c9e")

	repo := qGitRepoNamed(t, "P2AUDIT-GITBRANCH-2d90", "P2AUDIT-GITFILE-a447.txt", "P2AUDIT-GITCONTENT-9b12")
	gitGrant := gitReadPolicy(t, e, sid, repo, "P2AUDIT-GITBRANCH-2d90")

	fsDir := qDir(t)
	fsFile := "P2AUDIT-SCOPE-5e88/P2AUDIT-FSFILE-b204.txt"
	if err := os.MkdirAll(filepath.Join(fsDir, "P2AUDIT-SCOPE-5e88"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fsDir, filepath.FromSlash(fsFile)), []byte("P2AUDIT-FSCONTENT-e651"), 0o600); err != nil {
		t.Fatal(err)
	}
	var grantOut struct {
		Grant    daemon.GrantView `json:"grant"`
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
	}
	e.a.call("grant_create", daemon.GrantCreateParams{
		Peer: e.b.key, Session: sid, Action: "fs.read", Resource: fsDir, Scope: "P2AUDIT-SCOPE-5e88",
	}, &grantOut)
	if grantOut.Approval.ID == "" {
		t.Fatal("grant_create (fs.read, scoped) returned no approval")
	}
	e.approve(t, grantOut.Approval.ID)
	fsGrant := grantOut.Grant.ID
	harnessWait(t, "the scoped fs.read grant to be active", func() bool {
		return e.a.count(`SELECT COUNT(*) FROM grants WHERE id = '`+fsGrant+`' AND state = 'active'`) == 1
	})

	if st, err := e.bFetch(daemon.FetchStartParams{Grant: gitGrant, Op: capability.OpList}); err != nil || st.State != "complete" {
		t.Fatalf("git list = %+v, %v", st, err)
	}
	if st, err := e.bFetch(daemon.FetchStartParams{Grant: fsGrant, Op: capability.OpRead, Path: "P2AUDIT-FSFILE-b204.txt", Length: 64}); err != nil || st.State != "complete" {
		t.Fatalf("fs read = %+v, %v", st, err)
	}

	var rev daemon.GrantRevokeResult
	e.a.call("grant_revoke", daemon.GrantRevokeParams{ID: fsGrant}, &rev)
	_ = e.failure(t, daemon.FetchStartParams{Grant: fsGrant, Op: capability.OpRead, Path: "P2AUDIT-FSFILE-b204.txt", Length: 64})

	e.result(t, sid, qMarkerResult("P2AUDIT-RESULT1-3c71"))
	e.waitState(t, sid, "quarantined")
	releaseAndAwait(t, e, sid)

	var rc daemon.SessionResult
	e.a.call("ws_request_changes", map[string]any{"id": sid, "changes": "P2AUDIT-CHANGES-d813"}, &rc)
	harnessWait(t, "B's session to reopen at round 2", func() bool {
		return e.b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'open' AND round = 2`) == 1
	})
	e.result(t, sid, qMarkerResult("P2AUDIT-RESULT2-6a05"))
	e.waitState(t, sid, "quarantined")
	releaseAndAwait(t, e, sid)

	var acc daemon.SessionResult
	e.a.call("ws_accept_result", map[string]any{"id": sid}, &acc)
	if acc.Session.State != "closed" {
		t.Fatalf("accept-result = %+v", acc.Session)
	}
	harnessWait(t, "the request to complete on both sides", func() bool {
		return e.a.count(`SELECT COUNT(*) FROM requests WHERE id = '`+reqID+`' AND direction = 'out' AND state = 'completed'`) == 1 &&
			e.b.count(`SELECT COUNT(*) FROM requests WHERE id = '`+reqID+`' AND direction = 'in' AND state = 'completed'`) == 1
	})

	// A second session, cancelled with a marker reason, so the cancel path is
	// covered too (never kept per D18/review 27).
	_, sid2 := openSession(t, e.a, e.b, e.teamID, "second session")
	var cancelOut json.RawMessage
	e.b.call("ws_cancel", map[string]any{"id": sid2, "reason": "P2AUDIT-CANCEL-4f26"}, &cancelOut)
	harnessWait(t, "the second session to close", func() bool {
		return e.a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid2+`' AND state = 'closed'`) == 1
	})

	// Consult round trip with a marker context file and a marker answer.
	q := submitQuestion(t, e.a, e.b, e.teamID, daemon.ContextParam{Name: "P2AUDIT-CTXNAME.go", Text: "// P2AUDIT-CTXTEXT-0a5d\n"})
	var qres daemon.SessionResult
	e.b.call("ws_result", map[string]any{"id": q.ID, "result": map[string]any{"status": "n/a", "output": "P2AUDIT-ANSWER-77e1", "verification": "none"}}, &qres)
	e.waitState(t, q.Session, "quarantined")
	releaseAndAwait(t, e, q.Session)
	var qacc daemon.SessionResult
	e.a.call("ws_accept_result", map[string]any{"id": q.Session}, &qacc)
	if qacc.Session.State != "closed" {
		t.Fatalf("consult accept-result = %+v", qacc.Session)
	}

	// One own-device helper run with a marker command name, argv and output.
	he := newHelperEnv(t, true)
	he.setScope(t, []string{"task"}, he.cmd("p2audit-helpercmd-c390", 30, nil, "exit", "0", "P2AUDIT-ARGV-88f4"))
	helperReq := he.submit(t, "task", "p2audit-helpercmd-c390")
	if res := he.result(t, helperReq); res.Status != "pass" {
		t.Fatalf("helper run result = %+v", res)
	}

	// Scan every audit_events row on every daemon involved: A, B and the
	// helper pair.
	nodes := []*harnessNode{e.a, e.b, he.ctrl.harnessNode, he.help.harnessNode}
	for _, n := range nodes {
		harnessWait(t, n.name+" to have recorded its session.closed audit", func() bool {
			return n.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'ws.state' AND detail LIKE '%"state":"closed"%'`) >= 1 ||
				n.count(`SELECT COUNT(*) FROM audit_events`) > 0
		})
		_, details := allAuditRows(t, n)
		for _, d := range details {
			for _, m := range phase2Markers {
				if strings.Contains(d, m) {
					t.Fatalf("%s: audit detail %q contains marker %q", n.name, d, m)
				}
			}
		}
	}
}

// qGitRepoNamed builds a one-commit repository on branch with one file
// (name, content), returning its resolved path.
func qGitRepoNamed(t *testing.T, branch, name, content string) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	dir := qDir(t)
	run := func(args ...string) {
		t.Helper()
		//nolint:gosec // test helper; gitPath from exec.LookPath, args are fixed literals or test markers
		cmd := exec.Command(gitPath, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", branch)
	run("config", "user.email", "a@example.com")
	run("config", "user.name", "a")
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
