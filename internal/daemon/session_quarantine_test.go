package daemon_test

// D18 (Docs/protocol/work-session.md #inbox-copy-d18, review 27 H1, review 29
// H1): the receiver stores a message's mail_inbox.signed blank at receipt
// whenever Apply marks its content withheld or dropped. That covers (1) a
// ws.result that enters quarantined, (2) a ws.result that is ignored (wrong
// state or round), (3) a request.complete whose content is dropped as an
// early complete (covered at the package level, internal/worksession's
// TestReview27_EarlyCompleteWithoutSessionHonoursQuarantine), and (4) a
// ws.cancel while the quarantine rule holds (its reason is not stored). 2.4
// (the real quarantine rule from grants) is not implemented yet, so these
// tests drive a session into quarantined with a stub Quarantine hook
// (daemon.Options.Quarantine, added for this purpose), which also closes the
// gap flagged after the first 2.1b report: no e2e coverage of discard
// through a real two-daemon exchange.

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

func alwaysQuarantineDaemon(context.Context, *sql.Tx, string, string, int) (bool, error) {
	return true, nil
}

// noMarkerAnywhere fails the test if marker appears in any column of
// work_sessions, mail_inbox, requests or audit_events on n's database — the
// same "search every table" bar 2.1a's own review used.
func noMarkerAnywhere(t *testing.T, n *harnessNode, marker string) {
	t.Helper()
	for _, q := range []string{
		`SELECT COUNT(*) FROM work_sessions WHERE COALESCE(result,'') LIKE '%` + marker + `%' OR COALESCE(changes,'') LIKE '%` + marker + `%' OR COALESCE(last_state,'') LIKE '%` + marker + `%'`,
		`SELECT COUNT(*) FROM mail_inbox WHERE signed LIKE '%` + marker + `%'`,
		`SELECT COUNT(*) FROM requests WHERE COALESCE(note,'') LIKE '%` + marker + `%' OR COALESCE(result,'') LIKE '%` + marker + `%' OR COALESCE(body,'') LIKE '%` + marker + `%'`,
		`SELECT COUNT(*) FROM audit_events WHERE detail LIKE '%` + marker + `%'`,
	} {
		if got := n.count(q); got != 0 {
			t.Fatalf("%s = %d, want 0 (marker %q must not survive)", q, got, marker)
		}
	}
}

// TestD18_QuarantinedResultBlankedAtReceipt: a ws.result that lands
// quarantined (stub hook) must be stored blank in mail_inbox immediately at
// receipt, before any human ever acts on it — not merely blanked later by
// discard (#inbox-copy-d18 (1)). The content lives in work_sessions.result
// instead (visible again after release, TestReleaseIntegration).
func TestD18_QuarantinedResultBlankedAtReceipt(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.Quarantine = alwaysQuarantineDaemon
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	_, sid := openSession(t, a, b, teamID, "quarantine receipt test")
	const marker = "D18-RECEIPT-MARKER"

	var res daemon.SessionResult
	b.call("ws_result", map[string]any{
		"id":     sid,
		"result": map[string]any{"status": "pass", "summary": marker, "output": marker, "verification": "tests_passed"},
	}, &res)

	harnessWait(t, "A to see quarantined", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'quarantined'`) == 1
	})
	// The content really was received and stored (in work_sessions), so the
	// mail_inbox absence below proves withholding, not that nothing arrived.
	if a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND result LIKE '%`+marker+`%'`) != 1 {
		t.Fatalf("precondition: work_sessions.result does not hold the marker")
	}
	if a.count(`SELECT COUNT(*) FROM mail_inbox WHERE kind = 'ws.result'`) != 1 {
		t.Fatalf("no mail_inbox row for the ws.result mail at all")
	}
	if a.count(`SELECT COUNT(*) FROM mail_inbox WHERE kind = 'ws.result' AND signed = ''`) != 1 {
		t.Fatalf("mail_inbox row for the quarantined ws.result was not blanked at receipt")
	}
}

// TestD18_DiscardLeavesNoMarker: discarding a quarantined session (which was
// already blanked at receipt, above) leaves the marker nowhere, and closes
// both sides as cancelled.
func TestD18_DiscardLeavesNoMarker(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.Quarantine = alwaysQuarantineDaemon
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	_, sid := openSession(t, a, b, teamID, "discard quarantine test")
	const marker = "D18-DISCARD-MARKER"

	var res daemon.SessionResult
	b.call("ws_result", map[string]any{
		"id":     sid,
		"result": map[string]any{"status": "pass", "summary": marker, "verification": "tests_passed"},
	}, &res)
	harnessWait(t, "A to see quarantined", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'quarantined'`) == 1
	})

	var discardRes daemon.SessionResult
	a.call("ws_discard", map[string]any{"id": sid}, &discardRes)
	if discardRes.Session.State != "closed" || discardRes.Session.Outcome != "cancelled" {
		t.Fatalf("ws_discard = %+v", discardRes.Session)
	}
	harnessWait(t, "B's session to close", func() bool {
		return b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'closed' AND outcome = 'cancelled'`) == 1
	})

	noMarkerAnywhere(t, a, marker)
	// The mail_inbox row (proof of receipt) still exists, blanked.
	if a.count(`SELECT COUNT(*) FROM mail_inbox WHERE kind = 'ws.result' AND signed = ''`) != 1 {
		t.Fatalf("mail_inbox row for the ws.result mail is missing or non-empty")
	}
}

// TestD18_RequestChangesFromQuarantineLeavesNoMarker: the OD-P2-6 (c)
// request-changes-without-release exit from quarantined leaves the marker
// nowhere either; B is sent a new open round and the session accepts a
// fresh result normally afterwards.
func TestD18_RequestChangesFromQuarantineLeavesNoMarker(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.Quarantine = alwaysQuarantineDaemon
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	_, sid := openSession(t, a, b, teamID, "request-changes quarantine test")
	const marker = "D18-CHANGES-MARKER"

	var res daemon.SessionResult
	b.call("ws_result", map[string]any{
		"id":     sid,
		"result": map[string]any{"status": "pass", "summary": marker, "verification": "tests_passed"},
	}, &res)
	harnessWait(t, "A to see quarantined", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'quarantined'`) == 1
	})

	var rc daemon.SessionResult
	a.call("ws_request_changes", map[string]any{"id": sid, "changes": "please redo without touching secrets"}, &rc)
	if rc.Session.State != "open" || rc.Session.Round != 2 {
		t.Fatalf("ws_request_changes from quarantined = %+v, want open round 2", rc.Session)
	}

	noMarkerAnywhere(t, a, marker)
	if a.count(`SELECT COUNT(*) FROM mail_inbox WHERE kind = 'ws.result' AND signed = ''`) != 1 {
		t.Fatalf("mail_inbox row for the ws.result mail is missing or non-empty")
	}

	// B's mirror must learn round 2 (open) before it may submit again.
	harnessWait(t, "B's mirror to see open round 2", func() bool {
		return b.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'open' AND round = 2`) == 1
	})

	// The session still works for a new round: B submits again, and this
	// time (no quarantine on round 2 in this test) A can accept it.
	var res2 daemon.SessionResult
	b.call("ws_result", map[string]any{
		"id":     sid,
		"result": map[string]any{"status": "pass", "summary": "fixed", "verification": "tests_passed"},
	}, &res2)
	harnessWait(t, "A to see the round-2 result", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND round = 2 AND state != 'open'`) == 1
	})
}

// TestD18_IgnoredResultWithheld: a ws.result whose round does not match A's
// row (B still on round 1 while A's row, by whatever race, expects round 2)
// is ignored on receipt and applied nowhere, so its mail_inbox copy must be
// withheld too (#inbox-copy-d18 (2)) — not just the ones that do reach a
// session state. A's row is nudged forward directly (rather than driving a
// real round-2 exchange first) to isolate the round-mismatch branch: B's own
// mirror is untouched, so its ws_result IPC call succeeds locally and sends
// real mail with round 1, exactly as a genuinely racing B would.
func TestD18_IgnoredResultWithheld(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	_, sid := openSession(t, a, b, teamID, "ignored result test")
	const marker = "D18-IGNORED-MARKER"

	if err := a.exec(`UPDATE work_sessions SET round = 2 WHERE id = '` + sid + `'`); err != nil {
		t.Fatalf("nudge A's round forward: %v", err)
	}

	var res daemon.SessionResult
	b.call("ws_result", map[string]any{
		"id":     sid,
		"result": map[string]any{"status": "pass", "summary": marker, "verification": "tests_passed"},
	}, &res)

	harnessWait(t, "A's mail_inbox to record the ignored ws.result", func() bool {
		return a.count(`SELECT COUNT(*) FROM mail_inbox WHERE kind = 'ws.result'`) == 1
	})
	harnessWait(t, "A's ws.ignored audit for the round mismatch", func() bool {
		return a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'ws.ignored' AND detail LIKE '%"reason":"round"%'`) == 1
	})
	if a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND round = 2`) != 1 {
		t.Fatalf("A's row round changed by the ignored result: still want 2")
	}
	noMarkerAnywhere(t, a, marker)
	if a.count(`SELECT COUNT(*) FROM mail_inbox WHERE kind = 'ws.result' AND signed = ''`) != 1 {
		t.Fatalf("mail_inbox row for the ignored ws.result is missing or non-empty")
	}
}

// TestD18_QuarantinedCancelReasonWithheld: B's ws.cancel reason is never
// stored on A even in the ordinary (non-quarantined) path, but while the
// quarantine rule holds for the session its mail_inbox copy must be
// withheld too (#inbox-copy-d18 (4)), not just the reason column (which was
// never written in the first place).
func TestD18_QuarantinedCancelReasonWithheld(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.Quarantine = alwaysQuarantineDaemon
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	_, sid := openSession(t, a, b, teamID, "cancel reason quarantine test")
	const marker = "D18-CANCEL-REASON-MARKER"

	var cr daemon.SessionCancelResult
	b.call("ws_cancel", map[string]any{"id": sid, "reason": marker}, &cr)

	harnessWait(t, "A's session to close from B's cancel", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'closed'`) == 1
	})
	noMarkerAnywhere(t, a, marker)
	if a.count(`SELECT COUNT(*) FROM mail_inbox WHERE kind = 'ws.cancel' AND signed = ''`) != 1 {
		t.Fatalf("mail_inbox row for the ws.cancel mail is missing or non-empty")
	}
}

// TestD18_RefusedCancelReasonWithheld (review 35 H2): a ws.cancel that A
// refuses (its session is not open) is applied nowhere; while the quarantine
// rule holds its reason must not survive in the inbox copy either
// (#inbox-copy-d18 (4)). A's row is nudged to awaiting_result directly, so
// B's mirror is still open and its ws_cancel sends real mail.
func TestD18_RefusedCancelReasonWithheld(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.Quarantine = alwaysQuarantineDaemon
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	_, sid := openSession(t, a, b, teamID, "refused cancel test")
	const marker = "D18-REFUSED-CANCEL-MARKER"
	if err := a.exec(`UPDATE work_sessions SET state = 'awaiting_result' WHERE id = '` + sid + `'`); err != nil {
		t.Fatalf("nudge A's state: %v", err)
	}
	var cr daemon.SessionCancelResult
	b.call("ws_cancel", map[string]any{"id": sid, "reason": marker}, &cr)

	harnessWait(t, "A to refuse B's cancel", func() bool {
		return a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'ws.cancel_in' AND detail LIKE '%refused%'`) == 1
	})
	noMarkerAnywhere(t, a, marker)
	if a.count(`SELECT COUNT(*) FROM mail_inbox WHERE kind = 'ws.cancel' AND signed = ''`) != 1 {
		t.Fatalf("mail_inbox row for the refused ws.cancel is missing or non-empty")
	}
}

// TestD18_OrphanResultWithheld (review 35 H2): a ws.result for a request A
// has no out row for is applied nowhere, so its inbox copy is blank too.
func TestD18_OrphanResultWithheld(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	reqID, sid := openSession(t, a, b, teamID, "orphan result test")
	const marker = "D18-ORPHAN-MARKER"
	if err := a.exec(`DELETE FROM requests WHERE direction = 'out' AND id = '` + reqID + `'`); err != nil {
		t.Fatalf("drop A's out row: %v", err)
	}
	var res daemon.SessionResult
	b.call("ws_result", map[string]any{
		"id":     sid,
		"result": map[string]any{"status": "pass", "summary": marker, "verification": "tests_passed"},
	}, &res)

	harnessWait(t, "A's ws.orphan audit", func() bool {
		return a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'ws.orphan' AND detail LIKE '%ws.result%'`) == 1
	})
	noMarkerAnywhere(t, a, marker)
	if a.count(`SELECT COUNT(*) FROM mail_inbox WHERE kind = 'ws.result' AND signed = ''`) != 1 {
		t.Fatalf("mail_inbox row for the orphan ws.result is missing or non-empty")
	}
}
