package daemon_test

// Ticket 2.D2 acceptance (Docs/review/23-phase2-tickets.md "2.D2 Helper scope
// and runner", Docs/protocol/device.md §Scope, §Running, §Limits): two real
// daemons and a real relay, the scope approved through the fake approval
// window, and a test program (built with go build) as the helper's commands.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

var (
	runHelperOnce sync.Once
	runHelperPath string
	runHelperErr  error
)

// buildRunHelper builds internal/device/testdata/runnerhelper once per test
// binary (go build, no shell scripts).
func buildRunHelper(t *testing.T) string {
	t.Helper()
	runHelperOnce.Do(func() {
		dir, err := os.MkdirTemp("", "dn-runhelper-")
		if err != nil {
			runHelperErr = err
			return
		}
		runHelperPath = filepath.Join(dir, "runnerhelper")
		if runtime.GOOS == "windows" {
			runHelperPath += ".exe"
		}
		if out, err := exec.Command("go", "build", "-o", runHelperPath, "../device/testdata/runnerhelper").CombinedOutput(); err != nil { //nolint:gosec // fixed test package
			runHelperErr = fmt.Errorf("%w\n%s", err, out)
		}
	})
	if runHelperErr != nil {
		t.Fatalf("build runnerhelper: %v", runHelperErr)
	}
	return runHelperPath
}

// helperEnv is a linked controller/helper pair that shares a team.
type helperEnv struct {
	ctrl, help *devNode
	team       string
	repo       string
	prog       string
}

func newHelperEnv(t *testing.T, link bool) *helperEnv {
	t.Helper()
	prog := buildRunHelper(t)
	ctrl, help := newDevPair(t)
	e := &helperEnv{ctrl: ctrl, help: help, prog: prog, repo: testutil.TempDir(t)}
	e.team = harnessSharedTeam(t, ctrl.harnessNode, help.harnessNode, "own-devices")
	if link {
		activatePair(t, ctrl, help)
	}
	return e
}

func (e *helperEnv) cmd(name string, timeout int, env []string, args ...string) map[string]any {
	c := map[string]any{"name": name, "repo": "repo", "argv": append([]string{e.prog}, args...), "timeout_s": timeout}
	if len(env) > 0 {
		c["env"] = env
	}
	return c
}

// setScope runs device_scope_set on the helper and approves it in the fake
// window, as the helper's human would.
func (e *helperEnv) setScope(t *testing.T, types []string, cmds ...map[string]any) daemon.DeviceScopeSetResult {
	t.Helper()
	scope := map[string]any{
		"types":    types,
		"repos":    []map[string]string{{"label": "repo", "path": e.repo}},
		"commands": cmds,
		"expires":  e.help.clk.Now().Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339),
	}
	raw, _ := json.Marshal(scope)
	var res daemon.DeviceScopeSetResult
	if err := ipcCallErr(e.help.harnessNode, "device_scope_set", map[string]any{"peer": e.ctrl.key, "scope": json.RawMessage(raw)}, &res); err != nil {
		t.Fatalf("device_scope_set: %v", err)
	}
	e.help.win.answer(res.Approval.ID, "approve", e.help.notifier.lastCode(t))
	harnessWait(t, "the scope to be stored", func() bool { return e.help.count(`SELECT COUNT(*) FROM device_scopes`) == 1 })
	return res
}

// submit sends a request from the controller; run "" sends none.
func (e *helperEnv) submit(t *testing.T, typ, run string) string {
	t.Helper()
	p := daemon.RequestSubmitParams{To: e.help.key, Type: typ, Team: e.team, Title: "helper run", Brief: "Run it.\n"}
	if run != "" {
		p.Run = &daemon.RunParam{Command: run}
	}
	var sub daemon.RequestSubmitResult
	e.ctrl.call("request_submit", p, &sub)
	harnessWait(t, "the helper to receive "+sub.ID, func() bool {
		return e.help.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+sub.ID+`'`) == 1
	})
	return sub.ID
}

func (e *helperEnv) helpState(t *testing.T, reqID string) string {
	t.Helper()
	var st string
	if err := e.help.query(fmt.Sprintf(`SELECT state FROM requests WHERE direction = 'in' AND id = '%s'`, reqID), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// result waits for the controller's session of reqID to hold a result and
// returns it.
func (e *helperEnv) result(t *testing.T, reqID string) daemon.SessionResultView {
	t.Helper()
	var show daemon.SessionShowResult
	deadline := time.Now().Add(60 * time.Second)
	for {
		err := ipcCallErr(e.ctrl.harnessNode, "ws_show", map[string]any{"id": reqID}, &show)
		if err == nil && show.Session.State == "awaiting_result" && show.Session.Result != nil {
			return *show.Session.Result
		}
		if time.Now().After(deadline) {
			var queue string
			_ = e.help.query(`SELECT value FROM settings WHERE key = 'device.runs'`, &queue)
			t.Fatalf("no result for %s: ws_show err %v, session %+v; helper queue %s; helper device.run rows %d; helper log:\n%s",
				reqID, err, show.Session, queue, e.help.auditCount("device.run"), e.help.logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (e *helperEnv) outOfScope(t *testing.T, reqID, check string) {
	t.Helper()
	harnessWait(t, "device.out_of_scope "+check+" for "+reqID, func() bool {
		return e.help.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'device.out_of_scope' AND detail LIKE '%"`+reqID+`"%' AND detail LIKE '%"check":"`+check+`"%'`) == 1
	})
	if st := e.helpState(t, reqID); st != "pending" {
		t.Fatalf("an out-of-scope request (%s) is %s on the helper, want pending in the normal inbox", check, st)
	}
	if n := e.help.count(`SELECT COUNT(*) FROM work_sessions WHERE request_id = '` + reqID + `'`); n != 0 {
		t.Fatalf("an out-of-scope request (%s) opened a session", check)
	}
}

// In scope: auto-accept, the command runs in the repo with only the listed
// environment, exit 0 is pass and exit 3 fail, the output tail is at most
// 32768 bytes without ANSI sequences, the timeout kills the process tree,
// and no audit row holds a command name, argv, path or output.
func TestHelperRunsInScopeRequests(t *testing.T) {
	const secret = "s3cr3t-token-marker"
	t.Setenv("SECRET_TOKEN", secret)
	t.Setenv("GOFLAGS", "-mod=mod")
	e := newHelperEnv(t, true)
	beat := filepath.Join(e.repo, "beat.txt")
	set := e.setScope(t, []string{"task"},
		e.cmd("mk-env", 60, []string{"GOFLAGS"}, "env"),
		e.cmd("mk-pass", 60, nil, "exit", "0"),
		e.cmd("mk-three", 60, nil, "exit", "3"),
		e.cmd("mk-spam", 60, nil, "spam", "5000"),
		e.cmd("mk-pwd", 60, nil, "pwd"),
		e.cmd("mk-tree", 2, nil, "tree", beat),
	)
	// The approval summary shows the resolved absolute program and full argv.
	e.help.win.mu.Lock()
	summary := e.help.win.starts[len(e.help.win.starts)-1].summary
	e.help.win.mu.Unlock()
	progJSON, _ := json.Marshal(e.prog)
	if !strings.Contains(summary, string(progJSON)) || !strings.Contains(summary, "mk-tree") || !strings.Contains(summary, `"spam","5000"`) {
		t.Fatalf("approval summary does not show the resolved argv: %q", summary)
	}
	if a0 := set.Scope.Commands[0].Argv[0]; !filepath.IsAbs(a0) {
		t.Fatalf("argv[0] not resolved: %q", a0)
	}
	// The link view shows the scope's names on the helper.
	var list daemon.DeviceListResult
	e.help.call("device_list", nil, &list)
	if len(list.Links) != 1 || list.Links[0].Scope == nil || len(list.Links[0].Scope.Commands) != 6 {
		t.Fatalf("device_list on the helper: %+v", list.Links)
	}

	envReq := e.submit(t, "task", "mk-env")
	res := e.result(t, envReq)
	if res.Status != "pass" || res.ExitCode == nil || *res.ExitCode != 0 || !strings.HasPrefix(res.Summary, "mk-env: exit 0 in ") {
		t.Fatalf("env result: %+v", res)
	}
	if strings.Contains(res.Output, secret) || strings.Contains(res.Output, "SECRET_TOKEN") || strings.Contains(strings.ToUpper(res.Output), "DORYLINAE") {
		t.Fatalf("the daemon's secrets reached the command:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "GOFLAGS=-mod=mod") || !strings.Contains(strings.ToUpper(res.Output), "PATH=") {
		t.Fatalf("the listed environment is missing:\n%s", res.Output)
	}
	// Auto-accepted by the daemon, with its session open on the helper.
	if st := e.helpState(t, envReq); st != "accepted" {
		t.Fatalf("helper request state = %s, want accepted", st)
	}
	if n := e.help.count(`SELECT COUNT(*) FROM requests WHERE id = '` + envReq + `' AND first_response = 'accept'`); n != 1 {
		t.Fatal("first_response is not accept")
	}
	if n := e.help.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'request.accept' AND actor = 'daemon' AND detail LIKE '%` + envReq + `%'`); n != 1 {
		t.Fatal("no request.accept audit by the daemon")
	}

	if r := e.result(t, e.submit(t, "task", "mk-pass")); r.Status != "pass" || *r.ExitCode != 0 {
		t.Fatalf("exit 0: %+v", r)
	}
	if r := e.result(t, e.submit(t, "task", "mk-three")); r.Status != "fail" || r.ExitCode == nil || *r.ExitCode != 3 || !strings.HasPrefix(r.Summary, "mk-three: exit 3 in ") {
		t.Fatalf("exit 3: %+v", r)
	}
	spam := e.result(t, e.submit(t, "task", "mk-spam"))
	if len(spam.Output) > 32768 || strings.ContainsAny(spam.Output, "\x1b\r") || !strings.HasSuffix(spam.Output, "last line on stderr\n") {
		t.Fatalf("spam output: %d bytes, escapes %v", len(spam.Output), strings.ContainsAny(spam.Output, "\x1b\r"))
	}
	pwd := e.result(t, e.submit(t, "task", "mk-pwd"))
	want, _ := filepath.EvalSymlinks(e.repo)
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(pwd.Output))
	if !strings.EqualFold(got, want) {
		t.Fatalf("the command ran in %q, want the repo %q", got, want)
	}
	tree := e.result(t, e.submit(t, "task", "mk-tree"))
	if tree.Status != "fail" || tree.Summary != "mk-tree: timed out after 2 s" {
		t.Fatalf("timeout result: %+v", tree)
	}
	fi1, err := os.Stat(beat)
	if err != nil {
		t.Fatalf("the grandchild never ran: %v", err)
	}
	time.Sleep(time.Second)
	if fi2, _ := os.Stat(beat); fi2.Size() != fi1.Size() {
		t.Fatal("the grandchild still runs after the timeout")
	}
	harnessWait(t, "six device.run rows", func() bool { return e.help.auditCount("device.run") == 6 })

	// The command name, argv, paths and output appear in no audit row, on
	// either side.
	markers := []string{"mk-env", "mk-pass", "mk-three", "mk-spam", "mk-pwd", "mk-tree", "runnerhelper", "spam\",", "line 00", "last line", "GOFLAGS", secret, filepath.Base(e.repo)}
	for _, n := range []*devNode{e.ctrl, e.help} {
		_, details := allAuditRows(t, n.harnessNode)
		for _, d := range details {
			for _, m := range markers {
				if strings.Contains(d, m) {
					t.Errorf("%s: audit row holds %q: %s", n.name, m, d)
				}
			}
		}
	}
}

// Every failed check sends the request to the normal inbox with the right
// device.out_of_scope check, and nothing runs.
func TestHelperOutOfScopeGoesToInbox(t *testing.T) {
	e := newHelperEnv(t, false)
	// No link yet.
	e.outOfScope(t, e.submit(t, "task", "mk-ok"), "link")
	// A plain request from a peer that is not a controller is just a
	// request: no device audit at all.
	plain := e.submit(t, "task", "")
	time.Sleep(200 * time.Millisecond)
	if n := e.help.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'device.out_of_scope' AND detail LIKE '%` + plain + `%'`); n != 0 {
		t.Fatal("a normal request was audited as out of scope")
	}
	activatePair(t, e.ctrl, e.help)
	e.outOfScope(t, e.submit(t, "task", "mk-ok"), "scope")
	e.setScope(t, []string{"task"}, e.cmd("mk-ok", 60, nil, "exit", "0"))
	e.outOfScope(t, e.submit(t, "review", "mk-ok"), "type")
	e.outOfScope(t, e.submit(t, "task", "mk-other"), "command")
	e.outOfScope(t, e.submit(t, "task", ""), "command") // from the controller, without run
	// A request created before the link became active never runs: move the
	// activation an hour into the future.
	future := time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	if err := e.help.exec(`UPDATE device_links SET activated_at = ? WHERE state = 'active'`, future); err != nil {
		t.Fatal(err)
	}
	e.outOfScope(t, e.submit(t, "task", "mk-ok"), "created")
	past := time.Now().Add(-time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	if err := e.help.exec(`UPDATE device_links SET activated_at = ? WHERE state = 'active'`, past); err != nil {
		t.Fatal(err)
	}
	// Still in scope now.
	if r := e.result(t, e.submit(t, "task", "mk-ok")); r.Status != "pass" {
		t.Fatalf("in scope: %+v", r)
	}
	// Scope expiry needs no message.
	e.help.clk.Advance(8 * 24 * time.Hour)
	e.outOfScope(t, e.submit(t, "task", "mk-ok"), "expired")
	if n := e.help.auditCount("device.run"); n != 1 {
		t.Fatalf("device.run rows = %d, want only the one in-scope run", n)
	}
}

// Unlink on the helper: the next request goes to the inbox at once.
func TestHelperUnlinkStopsRuns(t *testing.T) {
	e := newHelperEnv(t, true)
	e.setScope(t, []string{"task"}, e.cmd("mk-ok", 60, nil, "exit", "0"))
	if r := e.result(t, e.submit(t, "task", "mk-ok")); r.Status != "pass" {
		t.Fatalf("before unlink: %+v", r)
	}
	var un daemon.DeviceUnlinkResult
	e.help.call("device_unlink", daemon.DeviceUnlinkParams{Peer: e.ctrl.key}, &un)
	e.outOfScope(t, e.submit(t, "task", "mk-ok"), "link")
	if n := e.help.count(`SELECT COUNT(*) FROM device_scopes`); n != 0 {
		t.Fatal("the scope survived the unlink")
	}
}

// Limits: one run at a time and at most 8 queued (the 9th is out of scope,
// "queue"); clearing the scope drops the queued runs with ws.cancel while the
// running one finishes; a restart reports an executing run as interrupted and
// drops the queued ones.
func TestHelperQueueClearAndRestart(t *testing.T) {
	e := newHelperEnv(t, true)
	e.setScope(t, []string{"task"}, e.cmd("mk-sleep", 60, nil, "sleep", "3"))
	first := e.submit(t, "task", "mk-sleep")
	var queued []string
	for i := 0; i < 2; i++ {
		queued = append(queued, e.submit(t, "task", "mk-sleep"))
	}
	var cleared daemon.DeviceScopeClearResult
	e.help.call("device_scope_clear", daemon.DevicePeerParams{Peer: e.ctrl.key}, &cleared)
	for _, id := range queued {
		harnessWait(t, "the controller to close the dropped run "+id, func() bool {
			return e.ctrl.count(`SELECT COUNT(*) FROM work_sessions WHERE request_id = '`+id+`' AND state = 'closed' AND outcome = 'cancelled'`) == 1
		})
	}
	if r := e.result(t, first); r.Status != "pass" {
		t.Fatalf("the running command did not finish: %+v", r)
	}

	// Fill the queue behind a long run: 1 running + 8 queued, the 10th is refused.
	e.setScope(t, []string{"task"}, e.cmd("mk-long", 120, nil, "sleep", "60"))
	running := e.submit(t, "task", "mk-long")
	harnessWait(t, "the long run to start", func() bool {
		return e.help.count(`SELECT COUNT(*) FROM settings WHERE key = 'device.runs' AND value LIKE '%"running":%'`) == 1
	})
	queued = nil
	for i := 0; i < 8; i++ {
		queued = append(queued, e.submit(t, "task", "mk-long"))
	}
	e.outOfScope(t, e.submit(t, "task", "mk-long"), "queue")

	// Restart the helper: the executing run is reported interrupted and the
	// queued ones are dropped.
	e.help.stop()
	e.help.start()
	r := e.result(t, running)
	if r.Status != "fail" || r.Summary != "mk-long: interrupted" {
		t.Fatalf("after restart: %+v", r)
	}
	for _, id := range queued {
		harnessWait(t, "the controller to close the dropped run "+id, func() bool {
			return e.ctrl.count(`SELECT COUNT(*) FROM work_sessions WHERE request_id = '`+id+`' AND state = 'closed' AND outcome = 'cancelled'`) == 1
		})
	}
}

// A rejected link attempt frees its hierarchy slot at once (review 36 L8): a
// helper whose attempt with one controller was rejected can start one with
// another controller without waiting for the attempt to lapse.
func TestDeviceLinkRejectFreesSlot(t *testing.T) {
	ctrl, help := newDevPair(t)
	other := newDevNode(t, "tablet", help.relay)
	other.start()
	waitRelayConnected(t, help.relay, other.key)
	harnessPair(t, help.harnessNode, other.harnessNode)
	res, err := help.requestLink(ctrl, "helper", devFP(t, ctrl))
	if err != nil {
		t.Fatal(err)
	}
	help.win.answer(res.Approval.ID, "reject", "")
	harnessWait(t, "the attempt to be revoked", func() bool { return help.linkCount("pending_approval") == 0 })
	if _, err := help.requestLink(other, "helper", devFP(t, other)); err != nil {
		t.Fatalf("a second helper attempt right after a rejected one: %v", err)
	}
}
