package daemon_test

// Ticket 1.6a e2e acceptance (Docs/review/11-phase1-tickets.md §1.6a,
// Docs/protocol/request.md §Lifecycle, §Cancel (OD-P1-11), §Result payload
// (D14)): the lifecycle IPC and mail kinds through two real daemons.

import (
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// TestLifecycleIPCRoundTrip: B accepts and then completes A's request with a
// full result through IPC; A's mirror (request_show) sees the accepted then
// completed state and the result, in full on show and without output on
// list.
func TestLifecycleIPCRoundTrip(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	var sub daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "task", Team: teamID, Title: "run the tests", Brief: "What: run them\n",
	}, &sub)

	harnessWait(t, "B to see the pending request", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND state = 'pending'`) == 1
	})

	var acc daemon.RequestLifecycleResult
	b.call("request_accept", map[string]any{"id": sub.ID}, &acc)
	if acc.Request.State != "accepted" {
		t.Fatalf("request_accept = %+v", acc)
	}

	// mail_id is the lifecycle mail just sent, not the request's mail.
	if acc.MailID == "" || b.count(`SELECT COUNT(*) FROM outbox WHERE kind = 'request.accept' AND id = '`+acc.MailID+`'`) != 1 {
		t.Fatalf("request_accept mail_id = %q, want the request.accept mail", acc.MailID)
	}

	harnessWait(t, "A's mirror to see accepted", func() bool {
		return a.count(`SELECT COUNT(*) FROM requests WHERE direction = 'out' AND id = '`+sub.ID+`' AND state = 'accepted'`) == 1
	})

	// A result the wire would reject is bad_request at IPC, not silently
	// dropped: nothing is sent and the row stays accepted (review 20).
	for _, bad := range []map[string]any{
		{"status": "pass", "artifacts": []any{}},
		{"status": "pass", "summary": ""},
		{"status": "pass", "output": nil},
		{"status": "pass", "bogus": "x"},
		{"status": "pass", "exit_code": 1.5},
	} {
		err := ipcCallErr(b, "request_complete", map[string]any{"id": sub.ID, "result": bad}, &daemon.RequestLifecycleResult{})
		if errCode(err) != "bad_request" {
			t.Errorf("request_complete result %v: err = %v, want bad_request", bad, err)
		}
	}
	if err := ipcCallErr(b, "request_complete", map[string]any{"id": sub.ID, "result": nil}, &daemon.RequestLifecycleResult{}); errCode(err) != "bad_request" {
		t.Errorf("request_complete result null: err = %v, want bad_request", err)
	}
	if n := b.count(`SELECT COUNT(*) FROM outbox WHERE kind = 'request.complete'`); n != 0 {
		t.Fatalf("request.complete mails after rejected results = %d, want 0", n)
	}

	exitCode := int64(1)
	var comp daemon.RequestLifecycleResult
	b.call("request_complete", map[string]any{
		"id": sub.ID, "note": "Ran on Windows 11.",
		"result": map[string]any{
			"status": "fail", "summary": "3 of 212 tests failed", "exit_code": exitCode,
			"output":    "--- FAIL: TestRetry (0.01s)\n",
			"artifacts": []map[string]any{{"branch": "fix/retry", "commit": "1a2b3c4"}},
		},
	}, &comp)
	if comp.Request.State != "completed" || comp.Request.Result == nil || comp.Request.Result.Status != "fail" {
		t.Fatalf("request_complete = %+v", comp)
	}
	if b.count(`SELECT COUNT(*) FROM outbox WHERE kind = 'request.complete' AND id = '`+comp.MailID+`'`) != 1 {
		t.Fatalf("request_complete mail_id = %q, want the request.complete mail", comp.MailID)
	}

	harnessWait(t, "A's mirror to see completed", func() bool {
		return a.count(`SELECT COUNT(*) FROM requests WHERE direction = 'out' AND id = '`+sub.ID+`' AND state = 'completed'`) == 1
	})

	var shown daemon.RequestShowResult
	a.call("request_show", map[string]any{"id": sub.ID}, &shown)
	if shown.Request.Result == nil || shown.Request.Result.Output == "" || shown.Request.Result.OutputBytes != len("--- FAIL: TestRetry (0.01s)\n") {
		t.Fatalf("request_show result = %+v", shown.Request.Result)
	}

	var listed daemon.RequestListResult
	a.call("request_list", map[string]any{}, &listed)
	if len(listed.Requests) != 1 || listed.Requests[0].Result == nil || listed.Requests[0].Result.Output != "" {
		t.Fatalf("request_list result should omit output: %+v", listed.Requests)
	}
	if listed.Requests[0].Result.OutputBytes != len("--- FAIL: TestRetry (0.01s)\n") {
		t.Fatalf("request_list output_bytes = %d", listed.Requests[0].Result.OutputBytes)
	}

	// B's own inbox view (request_show on the recipient) shows the result too.
	var bShown daemon.RequestShowResult
	b.call("request_show", map[string]any{"id": sub.ID, "from": a.key}, &bShown)
	if bShown.Request.Result == nil || bShown.Request.Result.Summary != "3 of 212 tests failed" {
		t.Fatalf("B's request_show result = %+v", bShown.Request.Result)
	}
}

// TestLifecycleBadStateIPC: accepting an already-accepted request is
// bad_state at IPC.
func TestLifecycleBadStateIPC(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	var sub daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "task", Team: teamID, Title: "t", Brief: "What: x\n",
	}, &sub)
	harnessWait(t, "B to see the pending request", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND state = 'pending'`) == 1
	})

	var acc daemon.RequestLifecycleResult
	b.call("request_accept", map[string]any{"id": sub.ID}, &acc)

	err := ipcCallErr(b, "request_accept", map[string]any{"id": sub.ID}, &daemon.RequestLifecycleResult{})
	if errCode(err) != "bad_state" {
		t.Fatalf("re-accept: err = %v, want bad_state", err)
	}
}

// TestCancelIPCPendingConfirmed is the 1.6a cancel acceptance test: A cancels
// a pending request through IPC; B's daemon confirms, the request leaves B's
// pending set, and A's mirror shows cancelled.
func TestCancelIPCPendingConfirmed(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	var sub daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "task", Team: teamID, Title: "t", Brief: "What: x\n",
	}, &sub)
	harnessWait(t, "B to see the pending request", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND state = 'pending'`) == 1
	})

	var cancel daemon.RequestCancelResult
	a.call("request_cancel", map[string]any{"id": sub.ID, "reason": "no longer needed"}, &cancel)
	if cancel.Duplicate || cancel.MailID == nil {
		t.Fatalf("request_cancel = %+v", cancel)
	}

	harnessWait(t, "B to confirm cancellation", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND state = 'cancelled'`) == 1
	})
	harnessWait(t, "A's mirror to see cancelled", func() bool {
		return a.count(`SELECT COUNT(*) FROM requests WHERE direction = 'out' AND id = '`+sub.ID+`' AND state = 'cancelled'`) == 1
	})

	// A second cancel is idempotent.
	var second daemon.RequestCancelResult
	a.call("request_cancel", map[string]any{"id": sub.ID}, &second)
	if !second.Duplicate {
		t.Fatalf("second cancel = %+v, want duplicate", second)
	}

	// The cancelled request is no longer in B's default inbox set.
	if n := b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND state = 'pending'`); n != 0 {
		t.Errorf("B's pending rows = %d, want 0", n)
	}
}
