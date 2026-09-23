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

	// 2.1b: an accept opens a work session, so request_complete on an open
	// session is a shorthand for ws_result (Docs/protocol/work-session.md,
	// "request_complete while a session exists"): the request itself stays
	// accepted, and a ws.result mail is sent instead of request.complete.
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
	// B's own mirror row does not move to awaiting_result until A's ws.state
	// confirms it (B is a mirror, not authoritative): it can still read "open"
	// here, right after the local ws.result submission.
	if comp.Request.State != "accepted" || comp.Request.Session == nil {
		t.Fatalf("request_complete = %+v", comp)
	}
	if b.count(`SELECT COUNT(*) FROM outbox WHERE kind = 'ws.result'`) != 1 {
		t.Fatalf("request_complete on an open session should send ws.result, not request.complete")
	}

	harnessWait(t, "A's session to see the result", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+comp.Request.Session.ID+`' AND state = 'awaiting_result'`) == 1
	})
	var acc2 daemon.SessionResult
	a.call("ws_accept_result", map[string]any{"id": comp.Request.Session.ID}, &acc2)
	if acc2.Session.State != "closed" || acc2.Session.Outcome != "accepted" {
		t.Fatalf("ws_accept_result = %+v", acc2)
	}

	harnessWait(t, "A's mirror to see completed", func() bool {
		return a.count(`SELECT COUNT(*) FROM requests WHERE direction = 'out' AND id = '`+sub.ID+`' AND state = 'completed'`) == 1
	})
	harnessWait(t, "B's request to see completed", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+sub.ID+`' AND state = 'completed'`) == 1
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

// TestInboxListIPC is the 1.6b acceptance test through IPC: three requests
// (low, high, normal) from one sender list as high, normal, low, each
// carrying a priority; a deferred request stays hidden until inbox_list
// --all is used.
func TestInboxListIPC(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	submit := func(urgency, reason string) string {
		var sub daemon.RequestSubmitResult
		a.call("request_submit", daemon.RequestSubmitParams{
			To: b.key, Type: "task", Team: teamID, Title: "t", Brief: "What: x\n",
			Urgency: urgency, UrgencyReason: reason,
		}, &sub)
		return sub.ID
	}
	lowID := submit("low", "")
	highID := submit("high", "time sensitive")
	normalID := submit("normal", "")

	harnessWait(t, "B to see all three pending", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND state = 'pending'`) == 3
	})

	var inbox daemon.RequestListResult
	b.call("inbox_list", map[string]any{}, &inbox)
	if len(inbox.Requests) != 3 {
		t.Fatalf("inbox_list = %d requests, want 3", len(inbox.Requests))
	}
	gotIDs := []string{inbox.Requests[0].ID, inbox.Requests[1].ID, inbox.Requests[2].ID}
	wantIDs := []string{highID, normalID, lowID}
	if gotIDs[0] != wantIDs[0] || gotIDs[1] != wantIDs[1] || gotIDs[2] != wantIDs[2] {
		t.Fatalf("inbox order = %v, want %v", gotIDs, wantIDs)
	}
	for _, v := range inbox.Requests {
		if v.Priority == nil {
			t.Errorf("request %s: priority is absent", v.ID)
		}
	}

	// A deferred request is hidden by default, and present under --all.
	var acc daemon.RequestLifecycleResult
	until := "2m"
	b.call("request_defer", map[string]any{"id": normalID, "until": until}, &acc)

	var afterDefer daemon.RequestListResult
	b.call("inbox_list", map[string]any{}, &afterDefer)
	for _, v := range afterDefer.Requests {
		if v.ID == normalID {
			t.Fatalf("deferred request %s is still in the default inbox", normalID)
		}
	}

	var withAll daemon.RequestListResult
	b.call("inbox_list", map[string]any{"all": true}, &withAll)
	found := false
	for _, v := range withAll.Requests {
		if v.ID == normalID {
			found = true
			if v.State != "deferred" {
				t.Errorf("deferred request state = %q, want deferred", v.State)
			}
		}
	}
	if !found {
		t.Fatalf("inbox_list --all does not show the deferred request %s", normalID)
	}
}
