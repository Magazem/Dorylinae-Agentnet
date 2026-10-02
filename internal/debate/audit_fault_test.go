package debate

// R55-F31 T4.7: decision.create is an S row written in the transaction that
// stores the Decision. With its insert failing, A's close fails and no
// decisions row exists; without the fault the same step closes the debate.

import (
	"errors"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
)

func TestDecisionCreateAuditFailureAbortsTheClose(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	toConverge(t, a, b, sid)
	submit(t, b, sid, KindAnswer, answer(true))
	answerMail := b.ob.take(t, MailEntry)

	// A writes through a real audit log, with a TEMP trigger refusing the row.
	real := audit.New(a.db)
	a.ds.Audit = real
	if _, err := a.db.Exec(`CREATE TEMP TRIGGER audit_fail BEFORE INSERT ON main.audit_events
WHEN NEW.action = 'decision.create' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	assertTrigger := func() {
		t.Helper()
		var n int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM sqlite_temp_master WHERE name = 'audit_fail'`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("the trigger is gone (%d, %v)", n, err)
		}
	}
	assertTrigger()
	err := deliver(t, a, b.self, answerMail)
	var we *audit.WriteError
	if !errors.As(err, &we) || we.Action != "decision.create" {
		t.Fatalf("A's close = %v, want an *audit.WriteError for decision.create", err)
	}
	assertTrigger()
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM decisions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d decisions rows after the failed close (%v)", n, err)
	}
	if p := phaseOf(t, a, sid); p == PhaseClosing || p == PhaseClosed {
		t.Fatalf("A's phase %s after the failed close", p)
	}
	// The mail was not applied, so it is redelivered; with the fault gone it closes.
	if _, err := a.db.Exec(`DROP TRIGGER audit_fail`); err != nil {
		t.Fatal(err)
	}
	mustDeliver(t, a, b.self, answerMail)
	if p := phaseOf(t, a, sid); p != PhaseClosing {
		t.Fatalf("A's phase %s after the redelivery, want closing", p)
	}
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM decisions`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d decisions rows after the redelivery (%v)", n, err)
	}
}
