package debate

import (
	"context"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// Ticket 3.7 (Docs/protocol/experience.md): a record exists per closed debate
// session and side (agreed, escalated, cancelled), and marker content never
// leaks into it.

func debateRecordFor(t *testing.T, n *dnode, sid, role string) (string, bool) {
	t.Helper()
	var record string
	err := n.db.QueryRow(`SELECT record FROM experience_records WHERE session = ? AND role = ?`, sid, role).Scan(&record)
	if err != nil {
		return "", false
	}
	return record, true
}

func TestExperienceRecord_DebateAgreedBothSides(t *testing.T) {
	a, b, _, sid := openPositions(t, 2)
	submit(t, a, sid, KindMove, pass0())
	pass(t, a, b, MailEntry)
	submit(t, b, sid, KindMove, pass0())
	pass(t, b, a, MailEntry)
	submit(t, a, sid, KindProposal, proposal())
	pass(t, a, b, MailEntry)
	submit(t, b, sid, KindAnswer, answer(true))
	pass(t, b, a, MailEntry)

	if st, out := sessionState(t, a, sid); st != worksession.StateClosed || out != worksession.OutcomeAccepted {
		t.Fatalf("A session %s/%s", st, out)
	}
	record, ok := debateRecordFor(t, a, sid, RoleInitiator)
	if !ok {
		t.Fatal("no experience record for A after an agreed close")
	}
	if !strings.Contains(record, `"agreed"`) {
		t.Fatalf("record missing agreed outcome: %s", record)
	}
	if !strings.Contains(record, `"decision"`) {
		t.Fatalf("record missing the Decision reference: %s", record)
	}

	cl := a.ob.take(t, MailClose)
	mustDeliver(t, b, a.self, cl)
	if _, ok := debateRecordFor(t, b, sid, RoleRespondent); !ok {
		t.Fatal("no experience record for B after an agreed close")
	}
}

func TestExperienceRecord_DebateEscalatedBothSides(t *testing.T) {
	a, b, _, sid := openPositions(t, 2)
	submit(t, a, sid, KindMove, pass0())
	pass(t, a, b, MailEntry)
	submit(t, b, sid, KindMove, pass0())
	pass(t, b, a, MailEntry)
	submit(t, a, sid, KindProposal, proposal())
	pass(t, a, b, MailEntry)
	submit(t, b, sid, KindAnswer, answer(false))
	pass(t, b, a, MailEntry)

	if st, out := sessionState(t, a, sid); st != worksession.StateClosed || out != worksession.OutcomeAccepted {
		t.Fatalf("A session %s/%s", st, out)
	}
	record, ok := debateRecordFor(t, a, sid, RoleInitiator)
	if !ok {
		t.Fatal("no experience record for A after an escalated close")
	}
	if !strings.Contains(record, `"escalated"`) {
		t.Fatalf("record missing escalated outcome: %s", record)
	}
	if strings.Contains(record, `"worked"`) {
		t.Fatalf("record has worked on an escalated close: %s", record)
	}

	cl := a.ob.take(t, MailClose)
	mustDeliver(t, b, a.self, cl)
	if _, ok := debateRecordFor(t, b, sid, RoleRespondent); !ok {
		t.Fatal("no experience record for B after an escalated close")
	}
}

func TestExperienceRecord_DebateCancelledByInitiator(t *testing.T) {
	ctx := context.Background()
	a, b, _, sid := openPositions(t, 2)
	if _, err := a.ws.Cancel(ctx, sid, "changed our minds"); err != nil {
		t.Fatal(err)
	}
	record, ok := debateRecordFor(t, a, sid, RoleInitiator)
	if !ok {
		t.Fatal("no experience record for A after cancel")
	}
	if strings.Contains(record, "changed our minds") {
		t.Fatalf("withheld cancel reason leaked into the record: %s", record)
	}
	if !strings.Contains(record, `"cancelled_by":"initiator"`) {
		t.Fatalf("record missing cancelled_by initiator: %s", record)
	}
	cl := a.ob.take(t, MailClose)
	mustDeliver(t, b, a.self, cl)
	if _, ok := debateRecordFor(t, b, sid, RoleRespondent); !ok {
		t.Fatal("no experience record for B after A's cancel")
	}
}

func TestExperienceRecord_DebateAbandonedByRespondent(t *testing.T) {
	ctx := context.Background()
	_, b, _, sid := openPositions(t, 2)
	if _, _, _, err := b.ws.SubmitCancel(ctx, sid, "no time"); err != nil {
		t.Fatal(err)
	}
	record, ok := debateRecordFor(t, b, sid, RoleRespondent)
	if !ok {
		t.Fatal("no experience record for B after abandoning")
	}
	if strings.Contains(record, "no time") {
		t.Fatalf("withheld cancel reason leaked into the record: %s", record)
	}
}
