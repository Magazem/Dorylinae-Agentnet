package daemon_test

// D18 (Docs/review/27-2.1a-review.md H1): when a quarantined result is
// discarded or changes are requested without release, the signed plaintext
// of the ws.result mail it was decoded from must be blanked in mail_inbox in
// the same transaction, leaving no copy of the dropped result anywhere in
// the database. 2.4 (the real quarantine rule from grants) is not
// implemented yet, so these tests drive a session into quarantined with a
// stub Quarantine hook (daemon.Options.Quarantine, added for this purpose),
// which also closes the gap flagged after the first 2.1b report: no e2e
// coverage of discard through a real two-daemon exchange.

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

// TestD18_DiscardBlanksMailInbox: B's result reaches A quarantined (stub
// hook); A discards it. The result content must be gone not just from
// work_sessions (2.1a already covers that) but from the mail_inbox row the
// ws.result mail was verified into (D18): mail_inbox keeps its row (from_key,
// id, kind, created, received_at unchanged, so dedupe is unaffected) but its
// signed plaintext is blanked in the same transaction as the discard.
func TestD18_DiscardBlanksMailInbox(t *testing.T) {
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
		"result": map[string]any{"status": "pass", "summary": marker, "output": marker, "verification": "tests_passed"},
	}, &res)

	harnessWait(t, "A to see quarantined", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND state = 'quarantined'`) == 1
	})
	// Precondition: before discard, the marker really is present (mail_inbox
	// holds the verified ws.result plaintext), so the assertion below proves
	// something was actually removed, not that nothing was ever stored.
	harnessWait(t, "A's mail_inbox to hold the ws.result plaintext", func() bool {
		return a.count(`SELECT COUNT(*) FROM mail_inbox WHERE kind = 'ws.result' AND signed LIKE '%`+marker+`%'`) == 1
	})
	if a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+sid+`' AND result LIKE '%`+marker+`%'`) != 1 {
		t.Fatalf("precondition: work_sessions.result does not hold the marker before discard")
	}

	var discardRes daemon.SessionResult
	a.call("ws_discard", map[string]any{"id": sid}, &discardRes)
	if discardRes.Session.State != "closed" || discardRes.Session.Outcome != "cancelled" {
		t.Fatalf("ws_discard = %+v", discardRes.Session)
	}

	noMarkerAnywhere(t, a, marker)
	// The row itself (proof of receipt) must survive, only blanked, so
	// dedupe and any future audit of "a ws.result arrived" still work.
	if a.count(`SELECT COUNT(*) FROM mail_inbox WHERE kind = 'ws.result' AND signed = ''`) != 1 {
		t.Fatalf("mail_inbox row for the ws.result mail was deleted or left non-empty, want one blanked row")
	}
}

// TestD18_RequestChangesFromQuarantineBlanksMailInbox: the OD-P2-6 (c)
// request-changes-without-release exit from quarantined must blank the same
// mail_inbox row as discard (D18); B is sent a new open round and the
// session accepts a fresh result normally afterwards.
func TestD18_RequestChangesFromQuarantineBlanksMailInbox(t *testing.T) {
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
	harnessWait(t, "A's mail_inbox to hold the ws.result plaintext", func() bool {
		return a.count(`SELECT COUNT(*) FROM mail_inbox WHERE kind = 'ws.result' AND signed LIKE '%`+marker+`%'`) == 1
	})

	var rc daemon.SessionResult
	a.call("ws_request_changes", map[string]any{"id": sid, "changes": "please redo without touching secrets"}, &rc)
	if rc.Session.State != "open" || rc.Session.Round != 2 {
		t.Fatalf("ws_request_changes from quarantined = %+v, want open round 2", rc.Session)
	}

	noMarkerAnywhere(t, a, marker)
	if a.count(`SELECT COUNT(*) FROM mail_inbox WHERE kind = 'ws.result' AND signed = ''`) != 1 {
		t.Fatalf("mail_inbox row for the ws.result mail was deleted or left non-empty, want one blanked row")
	}

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
