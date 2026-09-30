package daemon_test

// R55-F18 (Docs/review/69-r55-f18-spec.md §5): work-session integrity through
// two real daemons.

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// newSessionPair starts two paired daemons in a shared team.
func newSessionPair(t *testing.T, setup func(a, b *harnessNode)) (a, b *harnessNode, teamID string) {
	t.Helper()
	r := newHarnessRelay(t)
	a, b = newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	if setup != nil {
		setup(a, b)
	}
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	return a, b, harnessSharedTeam(t, a, b, "x")
}

// R55-061: a ws.result row that turned failed/unsupported_kind while no
// trigger ran (a crash) is found by the start-up rescan: B's session closes
// and its request completes through the Phase 1 path.
func TestPhase1FallbackRescanAtStart(t *testing.T) {
	a, b, teamID := newSessionPair(t, nil)
	reqID, sid := openSession(t, a, b, teamID, "phase 1 rescan")
	a.stop() // A never receives the result
	var res daemon.SessionResult
	b.call("ws_result", map[string]any{"id": sid, "result": map[string]any{"status": "pass", "summary": "worked", "verification": "none"}}, &res)
	b.stop()
	// As if A had acked it unsupported and B crashed before the trigger ran.
	if err := b.exec(`UPDATE outbox SET state = 'failed', error = 'unsupported_kind' WHERE kind = 'ws.result'`); err != nil {
		t.Fatal(err)
	}
	b.start()
	harnessWait(t, "B's session to close through the rescan", func() bool {
		return b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'closed' AND outcome = 'cancelled'`) == 1
	})
	harnessWait(t, "B's request to complete with the result", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+reqID+`' AND state = 'completed' AND result IS NOT NULL`) == 1
	})
}

// R55-029 over IPC: on a run session B's ws_result, request_complete and
// ws_cancel are bad_state, and the view shows runner: true. (The runner's
// own result path is covered by TestHelperRunSessionOwnedByRunner and the
// worksession store tests.)
func TestRunSessionRefusedOverIPC(t *testing.T) {
	a, b, teamID := newSessionPair(t, nil)
	reqID, sid := openSession(t, a, b, teamID, "run session")
	if err := b.exec(`UPDATE work_sessions SET runner = 1 WHERE id = ?`, sid); err != nil {
		t.Fatal(err)
	}
	wantRunBadState := func(method string, params any) {
		t.Helper()
		err := ipcCallErr(b, method, params, &map[string]any{})
		if errCode(err) != "bad_state" || !strings.Contains(err.Error(), "run session") {
			t.Fatalf("%s on a run session: err = %v, want bad_state (run session)", method, err)
		}
	}
	wantRunBadState("ws_result", map[string]any{"id": sid, "result": map[string]any{"status": "pass", "verification": "tests_passed"}})
	wantRunBadState("request_complete", map[string]any{"id": reqID, "note": "done"})
	wantRunBadState("ws_cancel", map[string]any{"id": sid})
	if n := b.count(`SELECT COUNT(*) FROM outbox WHERE kind IN ('ws.result', 'ws.cancel', 'request.complete')`); n != 0 {
		t.Fatalf("a refused call sent %d mails", n)
	}
	var show daemon.SessionShowResult
	b.call("ws_show", map[string]any{"id": sid}, &show)
	if !show.Session.Runner {
		t.Fatalf("B's view = %+v, want runner true", show.Session)
	}
	var showA daemon.SessionShowResult
	a.call("ws_show", map[string]any{"id": sid}, &showA)
	if showA.Session.Runner {
		t.Fatal("A's view shows runner")
	}
}

// §Run sessions round 2, daemon level: A requests changes on a run session,
// B's daemon answers with ws.cancel, and A's session closes cancelled.
func TestRunSessionNewRoundCancelled(t *testing.T) {
	a, b, teamID := newSessionPair(t, nil)
	_, sid := openSession(t, a, b, teamID, "run round 2")
	var res daemon.SessionResult
	b.call("ws_result", map[string]any{"id": sid, "result": map[string]any{"status": "pass", "verification": "none"}}, &res)
	harnessWait(t, "A to see awaiting_result", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'awaiting_result'`) == 1
	})
	if err := b.exec(`UPDATE work_sessions SET runner = 1 WHERE id = ?`, sid); err != nil {
		t.Fatal(err)
	}
	var rc daemon.SessionResult
	a.call("ws_request_changes", map[string]any{"id": sid, "changes": "run it again"}, &rc)
	harnessWait(t, "A's session to close cancelled", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'closed' AND outcome = 'cancelled'`) == 1
	})
	if n := b.count(`SELECT COUNT(*) FROM outbox WHERE kind = 'ws.cancel'`); n != 1 {
		t.Fatalf("B sent %d ws.cancel, want 1", n)
	}
}

// R55-115 over IPC: ws_accept_result, ws_request_changes, ws_discard and A's
// ws_cancel return the mail_id of the ws.state outbox row they submitted.
func TestSessionMailIDs(t *testing.T) {
	var quarantine atomic.Bool // read by A's daemon goroutines
	a, b, teamID := newSessionPair(t, func(a, _ *harnessNode) {
		a.Quarantine = func(context.Context, *sql.Tx, string, string, int) (bool, error) { return quarantine.Load(), nil }
	})
	wantRow := func(what, id string) {
		t.Helper()
		if id == "" || a.count(`SELECT COUNT(*) FROM outbox WHERE kind = 'ws.state' AND id = '`+id+`'`) != 1 {
			t.Fatalf("%s: mail_id %q is not A's ws.state outbox row", what, id)
		}
	}
	submit := func(sid string) {
		t.Helper()
		var res daemon.SessionResult
		b.call("ws_result", map[string]any{"id": sid, "result": map[string]any{"status": "pass", "verification": "none"}}, &res)
	}
	waitA := func(sid, state string, round int) {
		t.Helper()
		harnessWait(t, "A's session "+state, func() bool {
			return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = '`+state+`' AND round = `+strconv.Itoa(round)) == 1
		})
	}
	waitB := func(sid, state string, round int) {
		t.Helper()
		harnessWait(t, "B's session "+state, func() bool {
			return b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = '`+state+`' AND round = `+strconv.Itoa(round)) == 1
		})
	}

	_, sid := openSession(t, a, b, teamID, "mail ids")
	submit(sid)
	waitA(sid, "awaiting_result", 1)
	var out daemon.SessionResult
	a.call("ws_request_changes", map[string]any{"id": sid, "changes": "again"}, &out)
	wantRow("ws_request_changes", out.MailID)
	waitB(sid, "open", 2)
	submit(sid)
	waitA(sid, "awaiting_result", 2)
	a.call("ws_accept_result", map[string]any{"id": sid}, &out)
	wantRow("ws_accept_result", out.MailID)

	_, sid2 := openSession(t, a, b, teamID, "mail ids cancel")
	a.call("ws_cancel", map[string]any{"id": sid2}, &out)
	wantRow("ws_cancel", out.MailID)

	quarantine.Store(true)
	_, sid3 := openSession(t, a, b, teamID, "mail ids discard")
	submit(sid3)
	waitA(sid3, "quarantined", 1)
	a.call("ws_discard", map[string]any{"id": sid3}, &out)
	wantRow("ws_discard", out.MailID)
}

// Review 78 S2, daemon level: a run session whose ws.result expired (never
// delivered) while no trigger ran is cancelled by the runner at start-up,
// and A closes it. The expired result is seeded in B's database: a real
// submission would still reach A through the relay's queue.
func TestRunSessionExpiredResultCancelledAtStart(t *testing.T) {
	a, b, teamID := newSessionPair(t, nil)
	_, sid := openSession(t, a, b, teamID, "run expired")
	a.stop()
	b.stop()
	const mailID = "m-0000000000000000000000000000beef"
	if err := b.exec(`INSERT INTO outbox (id, to_key, kind, created, state, updated) VALUES (?, ?, 'ws.result', '2026-01-01T00:00:00.000Z', 'expired', '2026-01-01T00:00:00.000Z')`, mailID, a.key); err != nil {
		t.Fatal(err)
	}
	if err := b.exec(`UPDATE work_sessions SET runner = 1, result = '{"status":"pass","verification":"none"}', result_round = 1, result_mail = ? WHERE id = ?`, mailID, sid); err != nil {
		t.Fatal(err)
	}
	a.start()
	b.start()
	waitRelayConnected(t, a.relay, a.key, b.key)
	harnessWait(t, "B to send ws.cancel for the stuck run", func() bool {
		return b.count(`SELECT COUNT(*) FROM outbox WHERE kind = 'ws.cancel'`) == 1
	})
	harnessWaitFor(t, "A to close the session cancelled", 30*time.Second, func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'closed' AND outcome = 'cancelled'`) == 1
	}, func() {
		var st, ost, oerr string
		_ = a.query(`SELECT state FROM work_sessions WHERE id = '`+sid+`'`, &st)
		_ = b.query(`SELECT state, COALESCE(error, '') FROM outbox WHERE kind = 'ws.cancel'`, &ost, &oerr)
		t.Logf("A session %s; B ws.cancel row %s %q; A log: %s B log: %s", st, ost, oerr, a.logs.String(), b.logs.String())
	})
}
