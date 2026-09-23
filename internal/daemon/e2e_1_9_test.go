package daemon_test

// Ticket 1.9 acceptance (Docs/review/11-phase1-tickets.md §1.9,
// Docs/protocol/request.md §Audit and metrics, Docs/orchestration/HANDOFF.md
// §3 D11/D12/D14): the offline request lifecycle through two real daemons and
// a real relay, and the audit log's content and completeness on both sides.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// auditDetails returns the "detail" column of every audit_events row on n
// whose action matches, in insertion order.
func auditDetails(t *testing.T, n *harnessNode, action string) []string {
	t.Helper()
	st, err := store.Open(context.Background(), n.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rows, err := st.DB().Query(`SELECT detail FROM audit_events WHERE action = ? ORDER BY id`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

// allAuditRows returns every action and detail on n, in insertion order, and
// asserts every row has a non-empty ts.
func allAuditRows(t *testing.T, n *harnessNode) (actions, details []string) {
	t.Helper()
	st, err := store.Open(context.Background(), n.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rows, err := st.DB().Query(`SELECT ts, action, detail FROM audit_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ts, action, detail string
		if err := rows.Scan(&ts, &action, &detail); err != nil {
			t.Fatal(err)
		}
		if ts == "" {
			t.Errorf("%s: audit row %s has no timestamp", n.name, action)
		}
		actions = append(actions, action)
		details = append(details, detail)
	}
	return actions, details
}

// TestOfflineLifecycleEndToEnd is the 1.9 acceptance test: B is stopped, A
// submits a request (queued in under 2 s) and cancels a second one while B
// stays offline. B restarts: the first request is in B's inbox and A's
// mirror shows it delivered; the second shows cancelled. B accepts the
// first; A's mirror shows accepted and a notification fires. B completes it
// with a D14 result and A's mirror shows completed with the result.
func TestOfflineLifecycleEndToEnd(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	notified := make(chan string, 8)
	a.NotifyShow = func(_ context.Context, title, _ string) error {
		notified <- title
		return nil
	}
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	b.stop()
	harnessWait(t, "relay to see B leave", func() bool { return !r.rs.Connected(b.key) })

	const title1, brief1 = "e2e first request", "What: e2e first request\n"
	begin := time.Now()
	var sub1 daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "task", Team: teamID, Title: title1, Brief: brief1,
	}, &sub1)
	if sub1.Status != "queued" {
		t.Fatalf("submit 1 status = %q, want queued", sub1.Status)
	}
	if d := time.Since(begin); d >= 2*time.Second {
		t.Errorf("submit 1 took %v, want < 2s", d)
	}

	const title2, brief2 = "e2e second request", "What: e2e second request\n"
	var sub2 daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "task", Team: teamID, Title: title2, Brief: brief2,
	}, &sub2)
	var cancel daemon.RequestCancelResult
	a.call("request_cancel", map[string]any{"id": sub2.ID, "reason": "not needed anymore"}, &cancel)
	if cancel.Duplicate || cancel.MailID == nil {
		t.Fatalf("cancel of request 2 = %+v", cancel)
	}

	// B comes back: both mails deliver from the relay's queue.
	b.start()
	harnessWait(t, "B's inbox to have request 1", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+sub1.ID+`' AND state = 'pending'`) == 1
	})
	harnessWait(t, "A's request 1 show to be delivered", func() bool {
		var shown daemon.RequestShowResult
		a.call("request_show", map[string]any{"id": sub1.ID}, &shown)
		return shown.Request.Delivery == "delivered"
	})
	harnessWait(t, "B to show request 2 cancelled", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+sub2.ID+`' AND state = 'cancelled'`) == 1
	})

	// B accepts request 1: A's mirror shows accepted, and a notification fires.
	var acc daemon.RequestLifecycleResult
	b.call("request_accept", map[string]any{"id": sub1.ID}, &acc)
	if acc.Request.State != "accepted" {
		t.Fatalf("request_accept = %+v", acc)
	}
	harnessWait(t, "A's mirror to see accepted", func() bool {
		return a.count(`SELECT COUNT(*) FROM requests WHERE direction = 'out' AND id = '`+sub1.ID+`' AND state = 'accepted'`) == 1
	})
	select {
	case title := <-notified:
		if !strings.Contains(title, "accepted") {
			t.Errorf("notification title = %q, want it to mention acceptance", title)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no notification fired for B's accept")
	}

	// B completes with a D14 result: 2.1b's accept opened a work session, so
	// this is a shorthand for ws_result (Docs/protocol/work-session.md,
	// "request_complete while a session exists"); A must accept-result to
	// actually complete the request.
	var comp daemon.RequestLifecycleResult
	b.call("request_complete", map[string]any{
		"id": sub1.ID, "note": "ran on the harness",
		"result": map[string]any{"status": "pass", "summary": "all good", "output": "ok\n"},
	}, &comp)
	if comp.Request.State != "accepted" || comp.Request.Session == nil {
		t.Fatalf("request_complete = %+v", comp)
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
		return a.count(`SELECT COUNT(*) FROM requests WHERE direction = 'out' AND id = '`+sub1.ID+`' AND state = 'completed'`) == 1
	})
	var shown daemon.RequestShowResult
	a.call("request_show", map[string]any{"id": sub1.ID}, &shown)
	if shown.Request.Result == nil || shown.Request.Result.Summary != "all good" {
		t.Fatalf("A's request_show result = %+v", shown.Request.Result)
	}
}

// TestAuditHasNoContent is the 1.9 acceptance test's audit check: unique
// marker strings in the request titles/briefs, the cancel reason, the
// completion note and result must appear in no audit_events row on either
// side, and every Docs/protocol/request.md §Audit event this flow produces
// is present, with a timestamp, on the correct side.
func TestAuditHasNoContent(t *testing.T) {
	const (
		mTitle1  = "MARKTITLE1"
		mBrief1  = "What: MARKBRIEF1\n"
		mTitle2  = "MARKTITLE2"
		mBrief2  = "What: MARKBRIEF2\n"
		mCancel  = "MARKCANCELREASON"
		mNote    = "MARKCOMPLETENOTE"
		mSummary = "MARKRESULTSUMMARY"
		mOutput  = "MARKRESULTOUTPUT"
	)
	markers := []string{mTitle1, mBrief1, mTitle2, mBrief2, mCancel, mNote, mSummary, mOutput}

	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	var sub1 daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "task", Team: teamID, Title: mTitle1, Brief: mBrief1,
	}, &sub1)
	var sub2 daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "task", Team: teamID, Title: mTitle2, Brief: mBrief2,
	}, &sub2)
	harnessWait(t, "B to see both pending", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND state = 'pending'`) == 2
	})

	var cancel daemon.RequestCancelResult
	a.call("request_cancel", map[string]any{"id": sub2.ID, "reason": mCancel}, &cancel)
	if cancel.Duplicate {
		t.Fatalf("cancel = %+v, want a real cancel", cancel)
	}
	harnessWait(t, "B to confirm cancellation", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+sub2.ID+`' AND state = 'cancelled'`) == 1
	})

	var acc daemon.RequestLifecycleResult
	b.call("request_accept", map[string]any{"id": sub1.ID}, &acc)
	harnessWait(t, "A's mirror to see accepted", func() bool {
		return a.count(`SELECT COUNT(*) FROM requests WHERE direction = 'out' AND id = '`+sub1.ID+`' AND state = 'accepted'`) == 1
	})

	var comp daemon.RequestLifecycleResult
	b.call("request_complete", map[string]any{
		"id": sub1.ID, "note": mNote,
		"result": map[string]any{"status": "pass", "summary": mSummary, "output": mOutput + "\n"},
	}, &comp)
	if comp.Request.State != "accepted" || comp.Request.Session == nil {
		t.Fatalf("request_complete = %+v", comp)
	}
	harnessWait(t, "A's session to see the result", func() bool {
		return a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '`+comp.Request.Session.ID+`' AND state = 'awaiting_result'`) == 1
	})
	var acc2 daemon.SessionResult
	a.call("ws_accept_result", map[string]any{"id": comp.Request.Session.ID}, &acc2)
	if acc2.Session.State != "closed" {
		t.Fatalf("ws_accept_result = %+v", acc2)
	}
	harnessWait(t, "A's mirror to see completed", func() bool {
		return a.count(`SELECT COUNT(*) FROM requests WHERE direction = 'out' AND id = '`+sub1.ID+`' AND state = 'completed'`) == 1
	})

	// The last request.state row is written after the mirror's commit: poll
	// for it rather than reading the audit log once.
	harnessWait(t, "A's three request.state audit rows", func() bool {
		return len(auditDetails(t, a, "request.state")) >= 3
	})

	// No marker string appears in any audit_events row, on either side.
	for _, n := range []*harnessNode{a, b} {
		_, details := allAuditRows(t, n)
		for _, d := range details {
			for _, m := range markers {
				if strings.Contains(d, m) {
					t.Errorf("%s: audit detail %s contains marker %q", n.name, d, m)
				}
			}
		}
	}

	// Every Docs/protocol/request.md §Audit event this flow produces is
	// present, on the side that logs it, each with at least one row.
	wantOnA := []string{"request.submit", "request.submit", "request.cancel", "request.state", "request.state"}
	wantOnB := []string{"request.in", "request.in", "request.cancel_in", "request.accept", "request.complete"}
	for _, action := range dedupe(wantOnA) {
		if got := auditDetails(t, a, action); len(got) == 0 {
			t.Errorf("A: no %s audit rows", action)
		}
	}
	for _, action := range dedupe(wantOnB) {
		if got := auditDetails(t, b, action); len(got) == 0 {
			t.Errorf("B: no %s audit rows", action)
		}
	}
	if n := len(auditDetails(t, a, "request.submit")); n != 2 {
		t.Errorf("A: request.submit rows = %d, want 2", n)
	}
	if n := len(auditDetails(t, a, "request.state")); n != 3 {
		t.Errorf("A: request.state rows = %d, want 3 (the cancel, accept and complete mirrors)", n)
	}
	if n := len(auditDetails(t, b, "request.in")); n != 2 {
		t.Errorf("B: request.in rows = %d, want 2", n)
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
