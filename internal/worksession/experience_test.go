package worksession

import (
	"context"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
)

// Ticket 3.7 (Docs/protocol/experience.md): a record exists per closed
// session and side, the never-in-the-record content never appears, and a
// rolled-back close leaves no record.

func recordFor(t *testing.T, n *node, sid, role string) (string, bool) {
	t.Helper()
	var record string
	err := n.db.QueryRow(`SELECT record FROM experience_records WHERE session = ? AND role = ?`, sid, role).Scan(&record)
	if err != nil {
		return "", false
	}
	return record, true
}

func TestExperienceRecord_AcceptedSessionBothSides(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	deliverState(t, a, b) // awaiting_result
	if _, err := a.ws.AcceptResult(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	if _, ok := recordFor(t, a, sid, RoleRequester); !ok {
		t.Fatal("no experience record for A after accept")
	}
	deliverState(t, a, b) // closed
	if _, ok := recordFor(t, b, sid, RoleWorker); !ok {
		t.Fatal("no experience record for B after its mirror closed")
	}
}

func TestExperienceRecord_CancelledSessionBothSides(t *testing.T) {
	a, b, _, sid := setupAcceptedSession(t)
	if _, err := a.ws.Cancel(context.Background(), sid, "no longer needed"); err != nil {
		t.Fatal(err)
	}
	record, ok := recordFor(t, a, sid, RoleRequester)
	if !ok {
		t.Fatal("no experience record for A after cancel")
	}
	if strings.Contains(record, "no longer needed") {
		t.Fatalf("withheld cancel reason leaked into the record: %s", record)
	}
	deliverState(t, a, b)
	if _, ok := recordFor(t, b, sid, RoleWorker); !ok {
		t.Fatal("no experience record for B after its mirror closed")
	}
}

// A quarantined-then-discarded result's marker must appear in no record
// (Docs/protocol/experience.md "Never in the record").
func TestExperienceRecord_DiscardedQuarantinedResultNeverIncluded(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	a.ws.Quarantine = alwaysQuarantine
	r := validResult()
	r.Summary = "MARKER-QUARANTINED-SECRET"
	submitAndDeliverResult(t, a, b, reqID, r)
	if _, err := a.ws.Discard(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	record, ok := recordFor(t, a, sid, RoleRequester)
	if !ok {
		t.Fatal("no experience record after discard")
	}
	if strings.Contains(record, "MARKER-QUARANTINED-SECRET") {
		t.Fatalf("discarded quarantined content leaked into the record: %s", record)
	}
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM experience_records WHERE record LIKE '%MARKER-QUARANTINED-SECRET%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("marker found in %d experience_records rows", n)
	}
}

// A result replaced by request-changes without release never contributes to
// the eventual accepted record (Docs/protocol/experience.md "Never in the
// record").
func TestExperienceRecord_ReplacedByRequestChangesNeverIncluded(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	a.ws.Quarantine = alwaysQuarantine
	r1 := validResult()
	r1.Summary = "MARKER-REPLACED-ROUND-1"
	submitAndDeliverResult(t, a, b, reqID, r1)
	if _, err := a.ws.RequestChanges(context.Background(), sid, "please redo"); err != nil {
		t.Fatal(err)
	}
	deliverState(t, a, b)
	a.ws.Quarantine = nil
	r2 := validResult()
	r2.Summary = "final good result"
	submitAndDeliverResult(t, a, b, reqID, r2)
	deliverState(t, a, b)
	if _, err := a.ws.AcceptResult(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	record, ok := recordFor(t, a, sid, RoleRequester)
	if !ok {
		t.Fatal("no experience record after accept")
	}
	if strings.Contains(record, "MARKER-REPLACED-ROUND-1") {
		t.Fatalf("round-1 replaced result leaked into the record: %s", record)
	}
	if !strings.Contains(record, "final good result") {
		t.Fatalf("accepted summary missing from the record: %s", record)
	}
}

// A dropped early-complete result never appears in any record.
func TestExperienceRecord_DroppedEarlyCompleteNeverIncluded(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	r := validResult()
	r.Summary = "MARKER-EARLY-COMPLETE"
	if ok, _, err := b.ws.SubmitResult(context.Background(), testA, reqID, r); !ok || err != nil {
		t.Fatalf("SubmitResult: %v %v", ok, err)
	}
	// A ws.result has not been delivered: A's session is still open. An early
	// request.complete now closes it cancelled and drops the result
	// (EarlyComplete is the unit under test the daemon calls from
	// request.applyComplete; call it directly, as other tests in this
	// package do).
	b.ob.last(t, KindResult) // consume it: not delivered to A
	tx, err := a.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.ws.EarlyComplete(context.Background(), tx, testB, reqID, true); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	record, ok := recordFor(t, a, sid, RoleRequester)
	if !ok {
		t.Fatal("no experience record after early complete")
	}
	if strings.Contains(record, "MARKER-EARLY-COMPLETE") {
		t.Fatalf("dropped early-complete result leaked into the record: %s", record)
	}
}

// TestExperienceWriteAuditHasNoContent: experience.write carries only ids and
// sizes (Docs/protocol/experience.md §Audit).
func TestExperienceWriteAuditHasNoContent(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	a.ws.Audit = audit.New(a.db)
	r := validResult()
	r.Summary = "MARKER-AUDIT-CONTENT"
	submitAndDeliverResult(t, a, b, reqID, r)
	deliverState(t, a, b)
	if _, err := a.ws.AcceptResult(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	var detail string
	if err := a.db.QueryRow(`SELECT detail FROM audit_events WHERE action = 'experience.write'`).Scan(&detail); err != nil {
		t.Fatalf("no experience.write audit row: %v", err)
	}
	if strings.Contains(detail, "MARKER-AUDIT-CONTENT") {
		t.Fatalf("experience.write audit carries content: %s", detail)
	}
}

// TestExperienceRecord_RolledBackCloseLeavesNoRecord: an aborted closing
// transaction leaves no experience_records row.
func TestExperienceRecord_RolledBackCloseLeavesNoRecord(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	deliverState(t, a, b)
	r, err := findByID(context.Background(), a.db, sid)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.ws.closeSessionTx(context.Background(), tx, r, OutcomeAccepted, "", "", "", a.clock); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, ok := recordFor(t, a, sid, RoleRequester); ok {
		t.Fatal("experience record survived a rolled-back close")
	}
}
