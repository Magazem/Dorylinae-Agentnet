package daemon_test

// Ticket 2.1b acceptance (Docs/review/23-phase2-tickets.md §2.1b,
// Docs/protocol/work-session.md §IPC, §CLI): the session IPC through two real
// daemons.

import (
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// openSession drives A -> B through request_submit, accept, so both sides
// have an open work session, and returns the request id and the session id.
func openSession(t *testing.T, a, b *harnessNode, teamID, title string) (reqID, sessionID string) {
	t.Helper()
	var sub daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "task", Team: teamID, Title: title, Brief: "What: " + title + "\n",
	}, &sub)
	harnessWait(t, "B to see the pending request", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+sub.ID+`' AND state = 'pending'`) == 1
	})
	var acc daemon.RequestLifecycleResult
	b.call("request_accept", map[string]any{"id": sub.ID}, &acc)
	if acc.Request.Session == nil {
		t.Fatalf("request_accept did not open a session: %+v", acc)
	}
	sid := acc.Request.Session.ID
	harnessWait(t, "A's session to open", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'open'`) == 1
	})
	return sub.ID, sid
}

// TestAcceptResultCloses is the 2.6 acceptance test
// (Docs/review/23-phase2-tickets.md §2.1b): B submits a result through
// ws_result, A accepts it through ws_accept_result, and the session closes on
// both sides with the request completed on both sides too.
func TestAcceptResultCloses(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	_, sid := openSession(t, a, b, teamID, "review this")

	var res daemon.SessionResult
	b.call("ws_result", map[string]any{
		"id":     sid,
		"result": map[string]any{"status": "pass", "summary": "looks good", "verification": "tests_passed"},
	}, &res)
	if res.Session.State != "open" {
		t.Fatalf("ws_result on B = %+v, want B's mirror still open (A owns the transition)", res)
	}
	if b.count(`SELECT COUNT(*) FROM outbox WHERE kind = 'ws.result'`) != 1 {
		t.Fatalf("want one ws.result mail")
	}

	harnessWait(t, "A to see awaiting_result", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'awaiting_result'`) == 1
	})

	var show daemon.SessionShowResult
	a.call("ws_show", map[string]any{"id": sid}, &show)
	if show.Session.Result == nil || show.Session.Result.Status != "pass" || show.Session.Result.Summary != "looks good" {
		t.Fatalf("ws_show on A = %+v", show.Session)
	}
	if show.Session.Verification != "tests_passed" || show.Session.VerificationBy != "worker" {
		t.Fatalf("verification = %q/%q, want tests_passed/worker", show.Session.Verification, show.Session.VerificationBy)
	}

	var acc daemon.SessionResult
	a.call("ws_accept_result", map[string]any{"id": sid}, &acc)
	if acc.Session.State != "closed" || acc.Session.Outcome != "accepted" {
		t.Fatalf("ws_accept_result = %+v", acc)
	}

	harnessWait(t, "B's session to close", func() bool {
		return b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'closed'`) == 1
	})
	harnessWait(t, "B's request to complete", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND result IS NOT NULL AND state = 'completed'`) == 1
	})
	harnessWait(t, "A's request to complete", func() bool {
		return a.count(`SELECT COUNT(*) FROM requests WHERE direction = 'out' AND state = 'completed'`) == 1
	})
}

// TestSessionRequestChanges: A requests changes on an awaiting_result
// session; B sees open again at round 2, submits a new result, and A can
// still accept it.
func TestSessionRequestChanges(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	_, sid := openSession(t, a, b, teamID, "please review")

	var res daemon.SessionResult
	b.call("ws_result", map[string]any{"id": sid, "result": map[string]any{"status": "fail", "verification": "none"}}, &res)
	harnessWait(t, "A to see awaiting_result", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'awaiting_result'`) == 1
	})

	var rc daemon.SessionResult
	a.call("ws_request_changes", map[string]any{"id": sid, "changes": "please add a test"}, &rc)
	if rc.Session.State != "open" || rc.Session.Round != 2 {
		t.Fatalf("ws_request_changes = %+v", rc.Session)
	}

	harnessWait(t, "B to see round 2, open", func() bool {
		return b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'open' AND round = 2`) == 1
	})
	var bShow daemon.SessionShowResult
	b.call("ws_show", map[string]any{"id": sid}, &bShow)
	if bShow.Session.Changes != "please add a test" {
		t.Fatalf("B's session changes = %q", bShow.Session.Changes)
	}

	var res2 daemon.SessionResult
	b.call("ws_result", map[string]any{"id": sid, "result": map[string]any{"status": "pass", "verification": "none"}}, &res2)
	harnessWait(t, "A to see round 2 awaiting_result", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'awaiting_result' AND round = 2`) == 1
	})
	var acc daemon.SessionResult
	a.call("ws_accept_result", map[string]any{"id": sid}, &acc)
	if acc.Session.State != "closed" {
		t.Fatalf("ws_accept_result round 2 = %+v", acc)
	}
}

// TestSessionCancelBothSides covers A's cancel (open -> closed) and B's
// ws_cancel (applied automatically by A when open, review 27 L6).
func TestSessionCancelBothSides(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	// A cancels an open session.
	_, sid1 := openSession(t, a, b, teamID, "cancel from A")
	var cancelA daemon.SessionResult
	a.call("ws_cancel", map[string]any{"id": sid1, "reason": "changed my mind"}, &cancelA)
	if cancelA.Session.State != "closed" || cancelA.Session.Outcome != "cancelled" {
		t.Fatalf("A's ws_cancel = %+v", cancelA)
	}
	harnessWait(t, "B to see the session closed", func() bool {
		return b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid1+`' AND state = 'closed'`) == 1
	})

	// B cancels an open session: A applies it automatically.
	_, sid2 := openSession(t, a, b, teamID, "cancel from B")
	var cancelB daemon.SessionCancelResult
	b.call("ws_cancel", map[string]any{"id": sid2}, &cancelB)
	if cancelB.Duplicate || cancelB.Session.Cancel != "requested" {
		t.Fatalf("B's ws_cancel = %+v", cancelB)
	}
	harnessWait(t, "A to apply B's cancel", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid2+`' AND state = 'closed' AND outcome = 'cancelled'`) == 1
	})
	harnessWait(t, "B to see the session closed and cancel cleared", func() bool {
		return b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid2+`' AND state = 'closed' AND cancel IS NULL`) == 1
	})

	// A second B-side cancel on an already-closed session is bad_state.
	err := ipcCallErr(b, "ws_cancel", map[string]any{"id": sid2}, &daemon.SessionCancelResult{})
	if errCode(err) != "bad_state" {
		t.Fatalf("cancel of a closed session: err = %v, want bad_state", err)
	}
}

// TestSessionListOmitsOutputAndChanges: ws_list's view omits result.output,
// result.notes and changes, keeping their sizes
// (Docs/protocol/work-session.md §IPC, "Session view").
func TestSessionListOmitsOutputAndChanges(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	_, sid := openSession(t, a, b, teamID, "list test")
	var res daemon.SessionResult
	b.call("ws_result", map[string]any{
		"id": sid,
		"result": map[string]any{
			"status": "pass", "output": "some output text\n", "verification": "none",
		},
		"notes": "worker notes here",
	}, &res)
	harnessWait(t, "A to see awaiting_result", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'awaiting_result'`) == 1
	})

	var show daemon.SessionShowResult
	a.call("ws_show", map[string]any{"id": sid}, &show)
	if show.Session.Result == nil || show.Session.Result.Output == "" || show.Session.Result.Notes == "" {
		t.Fatalf("ws_show should keep output/notes in full: %+v", show.Session.Result)
	}

	var list daemon.SessionListResult
	a.call("ws_list", map[string]any{}, &list)
	if len(list.Sessions) != 1 {
		t.Fatalf("ws_list = %d sessions, want 1", len(list.Sessions))
	}
	lv := list.Sessions[0]
	if lv.Result == nil {
		t.Fatalf("ws_list result missing")
	}
	if lv.Result.Output != "" || lv.Result.Notes != "" {
		t.Fatalf("ws_list should omit output/notes: %+v", lv.Result)
	}
	if lv.Result.OutputBytes == 0 || lv.Result.ResultBytes == 0 {
		t.Fatalf("ws_list should keep sizes: %+v", lv.Result)
	}
}

// TestWSResultNotWorker: ws_result from the requester side (not the worker)
// is not_worker.
func TestWSResultNotWorker(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	_, sid := openSession(t, a, b, teamID, "not worker")
	err := ipcCallErr(a, "ws_result", map[string]any{"id": sid, "result": map[string]any{"status": "pass", "verification": "none"}}, &daemon.SessionResult{})
	if errCode(err) != "not_worker" {
		t.Fatalf("A submitting a result: err = %v, want not_worker", err)
	}
}

// TestWSDiscardWithoutQuarantineIsBadState: ws_discard outside quarantined
// is bad_state (Docs/protocol/work-session.md §Transitions).
func TestWSDiscardWithoutQuarantineIsBadState(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	_, sid := openSession(t, a, b, teamID, "discard test")
	err := ipcCallErr(a, "ws_discard", map[string]any{"id": sid}, &daemon.SessionResult{})
	if errCode(err) != "bad_state" {
		t.Fatalf("ws_discard on an open session: err = %v, want bad_state", err)
	}
}

// TestWSShowResolvesRequestID: ws_show accepts an r-... id, resolved through
// its session (Docs/protocol/work-session.md §IPC, "ws_show").
func TestWSShowResolvesRequestID(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	reqID, sid := openSession(t, a, b, teamID, "resolve by request id")
	var show daemon.SessionShowResult
	a.call("ws_show", map[string]any{"id": reqID}, &show)
	if show.Session.ID != sid {
		t.Fatalf("ws_show by request id = %+v, want session %s", show.Session, sid)
	}

	// Unknown ids of either shape are unknown_session.
	err := ipcCallErr(a, "ws_show", map[string]any{"id": "s-00000000000000000000000000000000"}, &daemon.SessionShowResult{})
	if errCode(err) != "unknown_session" {
		t.Fatalf("unknown session id: err = %v, want unknown_session", err)
	}
	err = ipcCallErr(a, "ws_show", map[string]any{"id": "r-00000000000000000000000000000000"}, &daemon.SessionShowResult{})
	if errCode(err) != "unknown_session" {
		t.Fatalf("unknown request id: err = %v, want unknown_session", err)
	}
}

// TestRequestViewHasSession: the request view gains a "session" member once
// a session exists (Docs/protocol/work-session.md §IPC).
func TestRequestViewHasSession(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	var sub daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "task", Team: teamID, Title: "session ref", Brief: "What: x\n",
	}, &sub)
	var shownBefore daemon.RequestShowResult
	a.call("request_show", map[string]any{"id": sub.ID}, &shownBefore)
	if shownBefore.Request.Session != nil {
		t.Fatalf("request_show before accept has a session: %+v", shownBefore.Request.Session)
	}

	harnessWait(t, "B to see the pending request", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+sub.ID+`' AND state = 'pending'`) == 1
	})
	var acc daemon.RequestLifecycleResult
	b.call("request_accept", map[string]any{"id": sub.ID}, &acc)
	if acc.Request.Session == nil || acc.Request.Session.State != "open" || acc.Request.Session.Round != 1 {
		t.Fatalf("request_accept session ref = %+v", acc.Request.Session)
	}
}
