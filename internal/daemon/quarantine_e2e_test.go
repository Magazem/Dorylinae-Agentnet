package daemon_test

// Ticket 2.4 acceptance (Docs/protocol/work-session.md §Quarantine (2.4),
// Docs/review/23-phase2-tickets.md "2.4"): the real quarantine rule from the
// grants table, driven through two live daemons and a relay. Approvals (grants
// and the release) are confirmed through the fake approval window; nothing
// here writes to the databases except through IPC.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// qNotifier records the code shown for each approval id (only titles that
// carry one: outcome notices are ignored) and every desktop notification text
// of the session.quarantined channel is recorded by qShown below.
type qNotifier struct {
	mu    sync.Mutex
	codes map[string]string
}

func (n *qNotifier) Show(_ context.Context, id string, _ time.Time, title, _ string) error {
	i := strings.Index(strings.ToLower(title), "code ")
	if i < 0 || len(title) < i+11 {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.codes == nil {
		n.codes = map[string]string{}
	}
	n.codes[id] = title[i+5 : i+11]
	return nil
}

func (n *qNotifier) Remove(context.Context, string) {}

func (n *qNotifier) code(t *testing.T, id string) string {
	t.Helper()
	var c string
	harnessWait(t, "the approval code for "+id, func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		c = n.codes[id]
		return c != ""
	})
	return c
}

// qShown collects desktop notifications (guarded: written by the notifier's
// goroutine, read by the test).
type qShown struct {
	mu    sync.Mutex
	texts []string
}

func (s *qShown) show(_ context.Context, title, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.texts = append(s.texts, title+"|"+body)
	return nil
}

func (s *qShown) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.texts...)
}

type qEnv struct {
	a, b   *harnessNode
	appr   *qNotifier
	win    *fakeWindowRunner
	shown  *qShown
	teamID string
}

func newQEnv(t *testing.T) *qEnv {
	t.Helper()
	r := newHarnessRelay(t)
	e := &qEnv{appr: &qNotifier{}, win: newFakeWindowRunner(), shown: &qShown{}}
	e.a, e.b = newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	e.a.ApprovalNotify = e.appr
	e.a.ApprovalWindow = e.win
	e.a.NotifyShow = e.shown.show
	e.a.start()
	e.b.start()
	waitRelayConnected(t, r, e.a.key, e.b.key)
	harnessPair(t, e.a, e.b)
	e.teamID = harnessSharedTeam(t, e.a, e.b, "x")
	return e
}

// approve answers approval id through the window with its own code.
func (e *qEnv) approve(t *testing.T, id string) {
	t.Helper()
	e.win.answer(id, "approve", e.appr.code(t, id))
}

// grant issues an approved grant on session sid to B and returns its id. res
// is the resource ("<path>" or "<path>#<branch>").
func (e *qEnv) grant(t *testing.T, sid, action, res string, public bool) string {
	t.Helper()
	id, apprID := e.grantPending(t, sid, action, res, public)
	e.approve(t, apprID)
	harnessWait(t, "the grant to be active", func() bool {
		return e.a.count(`SELECT COUNT(*) FROM grants WHERE id = '`+id+`' AND state = 'active' AND direction = 'issued'`) == 1
	})
	return id
}

// grantPending creates a grant that waits for its approval.
func (e *qEnv) grantPending(t *testing.T, sid, action, res string, public bool) (grantID, approvalID string) {
	t.Helper()
	var out struct {
		Grant    daemon.GrantView `json:"grant"`
		Approval struct {
			ID string `json:"id"`
		} `json:"approval"`
	}
	e.a.call("grant_create", daemon.GrantCreateParams{Peer: e.b.key, Session: sid, Action: action, Resource: res, Public: public}, &out)
	if out.Approval.ID == "" {
		t.Fatalf("grant_create returned no approval: %+v", out)
	}
	return out.Grant.ID, out.Approval.ID
}

func (e *qEnv) result(t *testing.T, sid string, res map[string]any) {
	t.Helper()
	var out daemon.SessionResult
	e.b.call("ws_result", map[string]any{"id": sid, "result": res}, &out)
}

func (e *qEnv) waitState(t *testing.T, sid, state string) {
	t.Helper()
	harnessWait(t, "A's session to be "+state, func() bool {
		return e.a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = '`+state+`'`) == 1
	})
}

// aViews returns the raw JSON of every requester-side view that could show a
// result: ws_show, ws_list and request_show.
func (e *qEnv) aViews(t *testing.T, sid, reqID string) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range []struct {
		method string
		params any
	}{
		{"ws_show", map[string]any{"id": sid}},
		{"ws_list", map[string]any{}},
		{"request_show", map[string]any{"id": reqID}},
	} {
		var raw json.RawMessage
		e.a.call(c.method, c.params, &raw)
		sb.Write(raw)
	}
	return sb.String()
}

// ipcCall is n.call without the fatal: for calls the test expects to fail.
func ipcCall(n *harnessNode, method string, params, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ipc.Call(ctx, n.p.Endpoint, method, params, out)
}

func qMarkerResult(marker string) map[string]any {
	return map[string]any{
		"status": "pass", "summary": marker + "-summary", "output": marker + "-output",
		"notes": marker + "-notes", "verification": "tests_passed",
	}
}

func qDir(t *testing.T) string {
	t.Helper()
	return testutil.TempDir(t)
}

func qGitRepo(t *testing.T) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	dir := testutil.TempDir(t)
	run := func(args ...string) {
		t.Helper()
		//nolint:gosec // test helper; gitPath from exec.LookPath, args are fixed literals
		cmd := exec.Command(gitPath, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "a@example.com")
	run("config", "user.name", "a")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi"), 0o600); err != nil {
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

// TestSensitiveQuarantine is the 2.4 acceptance: a session with a sensitive
// grant cannot deliver a result until released, and the audit log records the
// release.
func TestSensitiveQuarantine(t *testing.T) {
	t.Run("quarantine_views_release_and_next_round", func(t *testing.T) {
		e := newQEnv(t)
		reqID, sid := openSession(t, e.a, e.b, e.teamID, "sensitive work")
		e.grant(t, sid, "fs.read", qDir(t), false)

		const m1 = "QMARK-ONE"
		e.result(t, sid, qMarkerResult(m1))
		e.waitState(t, sid, "quarantined")

		// Views show sizes and status only; the marker is on no A-side IPC result
		// and in no table (the inbox copy is blank, D18).
		views := e.aViews(t, sid, reqID)
		if strings.Contains(views, m1) {
			t.Fatalf("a quarantined result leaked into an A-side view: %s", views)
		}
		var show daemon.SessionShowResult
		e.a.call("ws_show", map[string]any{"id": sid}, &show)
		if show.Session.Result != nil || show.Session.Quarantine == nil {
			t.Fatalf("ws_show while quarantined = %+v, want quarantine sizes and no result", show.Session)
		}
		if q := show.Session.Quarantine; q.Status != "pass" || q.ResultBytes == 0 || q.OutputBytes == 0 {
			t.Fatalf("quarantine view = %+v, want status and sizes", q)
		}
		// The notification is content-free: it fires, and shows no marker.
		harnessWait(t, "the session.quarantined notification", func() bool {
			for _, s := range e.shown.all() {
				if strings.Contains(s, "quarantined") {
					return true
				}
			}
			return false
		})
		for _, s := range e.shown.all() {
			if strings.Contains(s, m1) {
				t.Fatalf("notification leaked content: %q", s)
			}
		}
		noMarkerInInbox := e.a.count(`SELECT COUNT(*) FROM mail_inbox WHERE signed LIKE '%` + m1 + `%'`)
		auditHits := e.a.count(`SELECT COUNT(*) FROM audit_events WHERE detail LIKE '%` + m1 + `%'`)
		if noMarkerInInbox != 0 || auditHits != 0 {
			t.Fatalf("marker in mail_inbox (%d) or audit (%d)", noMarkerInInbox, auditHits)
		}
		// accept-result is refused while quarantined.
		var refused json.RawMessage
		if err := ipcCall(e.a, "ws_accept_result", map[string]any{"id": sid}, &refused); err == nil {
			t.Fatal("accept-result on a quarantined session succeeded")
		}

		// release needs an approval: nothing changes until the human answers.
		var rel struct {
			Approval struct {
				ID string `json:"id"`
			} `json:"approval"`
		}
		e.a.call("ws_release", map[string]any{"id": sid}, &rel)
		if rel.Approval.ID == "" {
			t.Fatal("ws_release returned no approval")
		}
		if e.a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'quarantined'`) != 1 {
			t.Fatal("session left quarantined without an approval")
		}
		if e.a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'ws.release'`) != 0 {
			t.Fatal("ws.release audited before the approval")
		}
		e.approve(t, rel.Approval.ID)
		e.waitState(t, sid, "awaiting_result")
		harnessWait(t, "ws.release in the audit log", func() bool {
			return e.a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'ws.release' AND detail LIKE '%`+sid+`%' AND detail LIKE '%`+rel.Approval.ID+`%'`) == 1
		})
		var after daemon.SessionShowResult
		e.a.call("ws_show", map[string]any{"id": sid}, &after)
		if after.Session.Result == nil || after.Session.Result.Summary != m1+"-summary" {
			t.Fatalf("result not visible after release: %+v", after.Session)
		}

		// request-changes: the next round is quarantined again.
		var rc daemon.SessionResult
		e.a.call("ws_request_changes", map[string]any{"id": sid, "changes": "please redo"}, &rc)
		harnessWait(t, "B's session to reopen at round 2", func() bool {
			return e.b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'open' AND round = 2`) == 1
		})
		const m2 = "QMARK-TWO"
		e.result(t, sid, qMarkerResult(m2))
		e.waitState(t, sid, "quarantined")
		if v := e.aViews(t, sid, reqID); strings.Contains(v, m2) {
			t.Fatalf("round 2 leaked into a view: %s", v)
		}
	})

	// review 35 H1: a release approval asked for round 1 must not release
	// round 2, even though the session is back in quarantined when the human
	// answers it.
	t.Run("stale_release_approval_cannot_release_the_next_round", func(t *testing.T) {
		e := newQEnv(t)
		reqID, sid := openSession(t, e.a, e.b, e.teamID, "stale release")
		e.grant(t, sid, "fs.read", qDir(t), false)
		e.result(t, sid, qMarkerResult("QMARK-STALE-1"))
		e.waitState(t, sid, "quarantined")
		var rel struct {
			Approval struct {
				ID string `json:"id"`
			} `json:"approval"`
		}
		e.a.call("ws_release", map[string]any{"id": sid}, &rel)
		var rc daemon.SessionResult
		e.a.call("ws_request_changes", map[string]any{"id": sid, "changes": "redo"}, &rc)
		harnessWait(t, "B's session to reopen at round 2", func() bool {
			return e.b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'open' AND round = 2`) == 1
		})
		const m2 = "QMARK-STALE-2"
		e.result(t, sid, qMarkerResult(m2))
		harnessWait(t, "A's round-2 result to be quarantined", func() bool {
			return e.a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'quarantined' AND round = 2`) == 1
		})
		e.approve(t, rel.Approval.ID)
		harnessWait(t, "the stale release approval to be rejected", func() bool {
			return e.a.count(`SELECT COUNT(*) FROM approvals WHERE id = '`+rel.Approval.ID+`' AND state = 'rejected'`) == 1
		})
		if e.a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'quarantined'`) != 1 {
			t.Fatal("a round-1 release approval released round 2")
		}
		if e.a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'ws.release'`) != 0 {
			t.Fatal("ws.release audited for a stale approval")
		}
		if v := e.aViews(t, sid, reqID); strings.Contains(v, m2) {
			t.Fatalf("leak: %s", v)
		}
	})

	t.Run("revoked_before_result_still_quarantines", func(t *testing.T) {
		e := newQEnv(t)
		reqID, sid := openSession(t, e.a, e.b, e.teamID, "revoked grant")
		g := e.grant(t, sid, "fs.read", qDir(t), false)
		var rv daemon.GrantRevokeResult
		e.a.call("grant_revoke", daemon.GrantRevokeParams{ID: g}, &rv)
		e.result(t, sid, qMarkerResult("QMARK-REVOKED"))
		e.waitState(t, sid, "quarantined")
		if v := e.aViews(t, sid, reqID); strings.Contains(v, "QMARK-REVOKED") {
			t.Fatalf("leak: %s", v)
		}
	})

	t.Run("public_git_grant_does_not_quarantine", func(t *testing.T) {
		e := newQEnv(t)
		_, sid := openSession(t, e.a, e.b, e.teamID, "public repo")
		e.grant(t, sid, "git.read", qGitRepo(t)+"#main", true)
		e.result(t, sid, qMarkerResult("QMARK-PUBLIC"))
		e.waitState(t, sid, "awaiting_result")
		var show daemon.SessionShowResult
		e.a.call("ws_show", map[string]any{"id": sid}, &show)
		if show.Session.Result == nil {
			t.Fatalf("a --public git grant withheld the result: %+v", show.Session)
		}
	})

	t.Run("never_approved_sensitive_grant_does_not_quarantine", func(t *testing.T) {
		e := newQEnv(t)
		_, sid := openSession(t, e.a, e.b, e.teamID, "unapproved grant")
		e.grantPending(t, sid, "fs.read", qDir(t), false)
		e.result(t, sid, qMarkerResult("QMARK-UNAPPROVED"))
		e.waitState(t, sid, "awaiting_result")
	})

	t.Run("second_grantless_session_with_same_peer_is_quarantined", func(t *testing.T) {
		e := newQEnv(t)
		_, sid1 := openSession(t, e.a, e.b, e.teamID, "first")
		e.grant(t, sid1, "fs.read", qDir(t), false)
		reqID2, sid2 := openSession(t, e.a, e.b, e.teamID, "second, no grant")
		e.result(t, sid2, qMarkerResult("QMARK-SECOND"))
		e.waitState(t, sid2, "quarantined")
		if v := e.aViews(t, sid2, reqID2); strings.Contains(v, "QMARK-SECOND") {
			t.Fatalf("leak: %s", v)
		}
	})

	t.Run("cancel_reason_is_not_kept_while_the_rule_holds", func(t *testing.T) {
		e := newQEnv(t)
		reqID, sid := openSession(t, e.a, e.b, e.teamID, "cancel with reason")
		e.grant(t, sid, "fs.read", qDir(t), false)
		var out json.RawMessage
		e.b.call("ws_cancel", map[string]any{"id": sid, "reason": "QMARK-CANCEL-REASON"}, &out)
		harnessWait(t, "A's session to close", func() bool {
			return e.a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'closed'`) == 1
		})
		noMarkerAnywhere(t, e.a, "QMARK-CANCEL-REASON")
		if v := e.aViews(t, sid, reqID); strings.Contains(v, "QMARK-CANCEL-REASON") {
			t.Fatalf("leak: %s", v)
		}
	})
}

// TestSessionCloseRejectsPendingApprovals is review 28 L8: closing a session
// rejects its pending approvals (its grants' and its own release) at close,
// not lazily at the next confirm.
func TestSessionCloseRejectsPendingApprovals(t *testing.T) {
	t.Run("grant_approval_on_cancel", func(t *testing.T) {
		e := newQEnv(t)
		_, sid := openSession(t, e.a, e.b, e.teamID, "close with pending grant")
		_, apprID := e.grantPending(t, sid, "fs.read", qDir(t), false)
		var out daemon.SessionResult
		e.a.call("ws_cancel", map[string]any{"id": sid}, &out)
		harnessWait(t, "the pending grant approval to be rejected at close", func() bool {
			return e.a.count(`SELECT COUNT(*) FROM approvals WHERE id = '`+apprID+`' AND state = 'rejected'`) == 1
		})
		harnessWait(t, "approval.reject with reason precondition", func() bool {
			return e.a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'approval.reject' AND detail LIKE '%`+apprID+`%' AND detail LIKE '%precondition%'`) == 1
		})
	})

	t.Run("release_approval_on_discard", func(t *testing.T) {
		e := newQEnv(t)
		_, sid := openSession(t, e.a, e.b, e.teamID, "discard with pending release")
		e.grant(t, sid, "fs.read", qDir(t), false)
		e.result(t, sid, qMarkerResult("QMARK-DISCARD"))
		e.waitState(t, sid, "quarantined")
		var rel struct {
			Approval struct {
				ID string `json:"id"`
			} `json:"approval"`
		}
		e.a.call("ws_release", map[string]any{"id": sid}, &rel)
		var out daemon.SessionResult
		e.a.call("ws_discard", map[string]any{"id": sid}, &out)
		harnessWait(t, "the pending release approval to be rejected at close", func() bool {
			return e.a.count(`SELECT COUNT(*) FROM approvals WHERE id = '`+rel.Approval.ID+`' AND state = 'rejected'`) == 1
		})
		noMarkerAnywhere(t, e.a, "QMARK-DISCARD")
	})
}
