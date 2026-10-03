package daemon_test

// R55-F20 acceptance tests 7, 8, 9 (the IPC mapping) and 10 through real
// daemons (Docs/review/83-r55-f20-spec.md §5). Old colliding rows cannot be
// produced any more, so they are made by copying a row in the database.

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

const peerC = "PEER-C"

// cloneRow copies the rows of table matching where, applies set to the copy
// and stores it, on n's database while the daemon runs.
func cloneRow(t *testing.T, n *harnessNode, table, where, set string, args ...any) {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+n.p.DB+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	for _, q := range []string{
		`CREATE TEMP TABLE dup AS SELECT * FROM ` + table + ` WHERE ` + where,
		`UPDATE dup SET ` + set,
		`INSERT INTO ` + table + ` SELECT * FROM dup`,
		`DROP TABLE dup`,
	} {
		if _, err := conn.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		args = nil
	}
}

// Acceptance test 7, and test 9's mapping: an r- id of a requester and a
// worker session is ambiguous for ws_show and audit_list; the s- ids work.
func TestIDCollisionSessions(t *testing.T) {
	a, b, teamID := consultPair(t)
	var res daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{To: b.key, Type: "task", Team: teamID, Title: "t", Brief: "What: x"}, &res)
	harnessWait(t, "B to store the task", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+res.ID+`'`) == 1
	})
	b.call("request_accept", map[string]any{"id": res.ID, "from": a.key}, &daemon.RequestShowResult{})
	harnessWait(t, "A's requester session", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+res.Session+`'`) == 1
	})
	var one daemon.SessionShowResult
	a.call("ws_show", map[string]any{"id": res.ID}, &one)
	if one.Session.ID != res.Session {
		t.Fatalf("one session: ws_show r- = %s, want %s", one.Session.ID, res.Session)
	}
	if code, _ := callCode(t, a, "audit_list", map[string]any{"session": res.ID}); code != "" {
		t.Fatalf("one session: audit_list = %q", code)
	}

	other := worksession.DeriveID(peerC, a.key, res.ID)
	cloneRow(t, a, "work_sessions", `id = ?`, `id = '`+other+`', role = 'worker', peer = '`+peerC+`'`, res.Session)
	if code, _ := callCode(t, a, "ws_show", map[string]any{"id": res.ID}); code != daemon.CodeAmbiguousRequest {
		t.Errorf("two sessions: ws_show r- = %q, want ambiguous_request", code)
	}
	if code, _ := callCode(t, a, "ws_cancel", map[string]any{"id": res.ID}); code != daemon.CodeAmbiguousRequest {
		t.Errorf("two sessions: ws_cancel r- = %q, want ambiguous_request", code)
	}
	for _, sid := range []string{res.Session, other} {
		var v daemon.SessionShowResult
		a.call("ws_show", map[string]any{"id": sid}, &v)
		if v.Session.ID != sid {
			t.Errorf("ws_show %s = %s", sid, v.Session.ID)
		}
	}
	if code, _ := callCode(t, a, "audit_list", map[string]any{"session": res.ID}); code != daemon.CodeAmbiguousRequest {
		t.Errorf("two sessions: audit_list = %q, want ambiguous_request", code)
	}
	if code, _ := callCode(t, a, "audit_list", map[string]any{"session": res.Session}); code != "" {
		t.Errorf("audit_list s- = %q", code)
	}
}

// Review 91 S3: an old session-less request row with the same id makes the r-
// shorthand ambiguous, though only one session carries it.
func TestIDCollisionSessionlessRequest(t *testing.T) {
	a, b, teamID := consultPair(t)
	var res daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{To: b.key, Type: "task", Team: teamID, Title: "t", Brief: "What: x"}, &res)
	harnessWait(t, "B to store the task", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+res.ID+`'`) == 1
	})
	b.call("request_accept", map[string]any{"id": res.ID, "from": a.key}, &daemon.RequestShowResult{})
	harnessWait(t, "A's requester session", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+res.Session+`'`) == 1
	})
	cloneRow(t, a, "requests", `direction = 'out' AND id = ?`, `direction = 'in', peer = '`+peerC+`'`, res.ID)
	if code, _ := callCode(t, a, "ws_show", map[string]any{"id": res.ID}); code != daemon.CodeAmbiguousRequest {
		t.Errorf("session plus a session-less in row: ws_show r- = %q, want ambiguous_request", code)
	}
	var v daemon.SessionShowResult
	a.call("ws_show", map[string]any{"id": res.Session}, &v)
	if v.Session.ID != res.Session {
		t.Errorf("ws_show s- = %s, want %s", v.Session.ID, res.Session)
	}
}

// Acceptance test 8: the one-step answer resolves only `in` rows.
func TestIDCollisionOneStepAnswer(t *testing.T) {
	a, b, teamID := consultPair(t)
	q := submitQuestion(t, a, b, teamID)
	cloneRow(t, b, "requests", `direction = 'in' AND id = ?`, `direction = 'out', peer = '`+peerC+`'`, q.ID)
	var res daemon.SessionResult
	b.call("ws_result", map[string]any{"id": q.ID, "result": map[string]any{"output": "yes\n"}}, &res)
	if res.Session.ID != q.Session || res.Session.Result == nil || res.Session.Result.Status != "n/a" {
		t.Fatalf("ws_result r- with an out row of the same id = %+v", res)
	}

	q2 := submitQuestion(t, a, b, teamID)
	cloneRow(t, b, "requests", `direction = 'in' AND id = ?`, `peer = '`+peerC+`'`, q2.ID)
	if code, _ := callCode(t, b, "ws_result", map[string]any{"id": q2.ID, "result": map[string]any{"output": "yes\n"}}); code != daemon.CodeAmbiguousRequest {
		t.Fatalf("two in questions: ws_result = %q, want ambiguous_request", code)
	}
	if n := b.count(`SELECT COUNT(*) FROM requests WHERE id = '` + q2.ID + `' AND state = 'pending'`); n != 2 {
		t.Errorf("pending rows after the refused answer = %d, want 2", n)
	}
}

// Acceptance test 10: decision_show and debate_submit map their errors.
func TestIDCollisionDecisionErrors(t *testing.T) {
	a, b, teamID, _, _ := debatePair(t)
	var res daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{To: b.key, Type: "debate", Team: teamID, Title: "Retries", Brief: "How should the outbox retry?",
		Debate: &daemon.DebateParam{Position: e2ePosition("Capped backoff"), Rounds: 1}}, &res)
	sid := res.Session
	harnessWait(t, "B to store the debate", phaseIs(b, sid, debate.PhaseInvited))

	for _, id := range []string{"s-00000000000000000000000000000001", res.ID, sid} {
		if code, msg := callCode(t, b, "decision_show", map[string]any{"id": id}); code != daemon.CodeUnknownDecision {
			t.Errorf("decision_show %s = %q (%s), want unknown_decision", id, code, msg)
		}
	}
	otherSID := worksession.DeriveID(peerC, b.key, res.ID)
	cloneRow(t, b, "debates", `session = ?`, `session = '`+otherSID+`', peer = '`+peerC+`'`, sid)
	if code, msg := callCode(t, b, "decision_show", map[string]any{"id": res.ID}); code != daemon.CodeAmbiguousRequest {
		t.Errorf("decision_show of an r- id of two debates = %q (%s), want ambiguous_request", code, msg)
	}

	// The request is no longer pending, but the debate is still invited:
	// the one-step accept fails in the request layer.
	if err := b.exec(`UPDATE requests SET state = 'declined', state_seq = 1 WHERE direction = 'in' AND id = ?`, res.ID); err != nil {
		t.Fatal(err)
	}
	if code, msg := callCode(t, b, "debate_submit", map[string]any{"id": sid, "kind": "position", "entry": e2ePosition("Fixed retry")}); code != daemon.CodeBadState {
		t.Errorf("debate_submit on a declined request = %q (%s), want bad_state", code, msg)
	}
}
