package daemon_test

import (
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// R55-029 (Docs/protocol/work-session.md §Run sessions), end to end with the
// real helper runner: an auto-accepted run request is a run session on the
// helper. The runner's result reaches the controller; the helper's IPC
// ws_result, request_complete and ws_cancel are bad_state; the view shows
// runner: true. A request for changes is answered with ws.cancel, and the
// controller's session closes cancelled.
func TestHelperRunSessionOwnedByRunner(t *testing.T) {
	e := newHelperEnv(t, true)
	e.setScope(t, []string{"task"}, e.cmd("mk-pass", 60, nil, "exit", "0"))
	reqID := e.submit(t, "task", "mk-pass")
	res := e.result(t, reqID)
	if res.Status != "pass" || !strings.HasPrefix(res.Summary, "mk-pass: exit 0 in ") {
		t.Fatalf("the controller's session holds %+v, want the runner's result", res)
	}

	var sid string
	if err := e.help.query(`SELECT id FROM work_sessions WHERE request_id = '`+reqID+`'`, &sid); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method string
		params map[string]any
	}{
		{"ws_result", map[string]any{"id": sid, "result": map[string]any{"status": "pass", "verification": "tests_passed"}}},
		{"request_complete", map[string]any{"id": reqID, "note": "forged"}},
		{"ws_cancel", map[string]any{"id": sid}},
	} {
		err := ipcCallErr(e.help.harnessNode, c.method, c.params, &map[string]any{})
		if errCode(err) != "bad_state" || !strings.Contains(err.Error(), "run session") {
			t.Fatalf("helper %s on a run session: err = %v, want bad_state", c.method, err)
		}
	}
	var show daemon.SessionShowResult
	e.help.call("ws_show", map[string]any{"id": sid}, &show)
	if !show.Session.Runner {
		t.Fatalf("helper view = %+v, want runner true", show.Session)
	}

	var rc daemon.SessionResult
	e.ctrl.call("ws_request_changes", map[string]any{"id": sid, "changes": "run it again"}, &rc)
	harnessWait(t, "the controller's session to close cancelled", func() bool {
		return e.ctrl.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'closed' AND outcome = 'cancelled'`) == 1
	})
}
