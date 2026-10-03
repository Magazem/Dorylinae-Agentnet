package debate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/decision"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// R55-F29 (Docs/review/92-r55-f29-spec.md §5): B's abandon after its answer,
// the timeout gate, the covered entries and the record fields.

// outboxRows counts n's stored outbox rows of kind (the spy also records
// mail whose transaction rolled back).
func outboxRows(t *testing.T, n *dnode, kind string) int {
	t.Helper()
	var c int
	if err := n.db.QueryRow(`SELECT COUNT(*) FROM outbox WHERE kind = ?`, kind).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func expRecord(t *testing.T, n *dnode, sid, role string) map[string]any {
	t.Helper()
	raw, ok := debateRecordFor(t, n, sid, role)
	if !ok {
		t.Fatalf("%s has no experience record for %s", n.name(), sid)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func abandon(n *dnode, sid string) error {
	_, _, _, err := n.ws.SubmitCancel(context.Background(), sid, "", worksession.ByAgent)
	return err
}

// answered plays to B's accepting answer, which A has not applied yet.
func answered(t *testing.T) (a, b *dnode, reqID, sid string) {
	t.Helper()
	a, b, reqID, sid = openPositions(t, 1)
	toConverge(t, a, b, sid)
	submit(t, b, sid, KindAnswer, answer(true))
	return a, b, reqID, sid
}

// Test 9 (R55-221, OD-F29-1 (b)).
func TestF29AbandonAfterAnswer(t *testing.T) {
	refused := func(t *testing.T, b *dnode, sid string) {
		t.Helper()
		before := outboxRows(t, b, worksession.KindCancel)
		err := abandon(b, sid)
		var bse *BadStateError
		if !errors.As(err, &bse) || !strings.Contains(bse.Msg, "you answered") {
			t.Fatalf("abandon after the answer = %v, want BadStateError", err)
		}
		if outboxRows(t, b, worksession.KindCancel) != before || b.audit.has("debate.abandon") {
			t.Fatal("a refused abandon queued a ws.cancel or audited debate.abandon")
		}
		if v := view(t, b, sid); v.Phase != PhaseConverge {
			t.Fatalf("B %s after a refused abandon", v.Phase)
		}
	}
	abandoned := func(t *testing.T, b *dnode, sid string) {
		t.Helper()
		before := outboxRows(t, b, worksession.KindCancel)
		if err := abandon(b, sid); err != nil {
			t.Fatalf("abandon: %v", err)
		}
		if v := view(t, b, sid); v.Phase != PhaseClosed || v.Reason != ReasonAbandoned {
			t.Fatalf("B %s/%s, want closed/abandoned", v.Phase, v.Reason)
		}
		if !b.audit.has("debate.abandon") || outboxRows(t, b, worksession.KindCancel) != before+1 {
			t.Fatal("abandon not audited or no ws.cancel queued")
		}
	}

	t.Run("refused until the close arrives", func(t *testing.T) {
		a, b, _, sid := answered(t)
		at := parseWireTime(b.clock.UTC().Format(timeFmt))
		if v := view(t, b, sid); !v.Deadline.Equal(at.Add(DefaultTurnTimeoutS * time.Second)) {
			t.Fatalf("B deadline %v, want the answer's at + timeout", v.Deadline)
		}
		b.advance(time.Hour - time.Second)
		refused(t, b, sid)
		pass(t, b, a, MailEntry)
		mustDeliver(t, b, a.self, a.ob.take(t, MailClose))
		mustDeliver(t, a, b.self, b.ob.take(t, MailSign))
		if da, db := record(t, a, sid), record(t, b, sid); da.State != DecisionSigned || db.State != DecisionSigned {
			t.Fatalf("Decisions %s/%s, want signed", da.State, db.State)
		}
	})
	t.Run("overdue", func(t *testing.T) {
		_, b, _, sid := answered(t)
		b.advance(time.Hour)
		abandoned(t, b, sid)
	})
	t.Run("held close", func(t *testing.T) {
		a, b, _, sid := openPositions(t, 1)
		toConverge(t, a, b, sid)
		constrain(t, a, sid, "Keep the wire format")
		mustDeliver(t, b, a.self, closeTo(t, a, b, sid)) // held: the constraint is missing
		abandoned(t, b, sid)
	})
	t.Run("before the answer", func(t *testing.T) {
		_, b, _, sid := openPositions(t, 1)
		abandoned(t, b, sid)
	})
	t.Run("answered before the upgrade", func(t *testing.T) {
		_, b, _, sid := answered(t)
		if _, err := b.db.Exec(`UPDATE debates SET turn_deadline = NULL WHERE session = ?`, sid); err != nil {
			t.Fatal(err)
		}
		b.advance(30 * time.Minute)
		refused(t, b, sid)
		b.advance(30 * time.Minute)
		abandoned(t, b, sid)
	})
}

// Test 6, the store side (R55-071): while the gate is not ready a passed
// deadline closes nothing and B's late entry is applied; once ready, a
// missing slot closes the debate.
func TestF29TimeoutsWaitForTheGate(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	ready := false
	a.ds.TimeoutsReady = func() bool { return ready }
	submit(t, a, sid, KindMove, pass0())
	pass(t, a, b, MailEntry)
	submit(t, b, sid, KindMove, pass0()) // queued "at the relay"
	a.advance(2 * time.Hour)
	if closed, err := a.ds.SweepOne(context.Background(), sid); err != nil || closed {
		t.Fatalf("SweepOne = %v, %v; want no close while the gate is not ready", closed, err)
	}
	pass(t, b, a, MailEntry)
	if v := view(t, a, sid); v.Phase != PhaseConverge {
		t.Fatalf("A %s after B's late entry, want converge", v.Phase)
	}
	a.advance(2 * time.Hour)
	if closed, _ := a.ds.SweepOne(context.Background(), sid); closed {
		t.Fatal("closed while the gate is not ready")
	}
	ready = true
	if closed, err := a.ds.SweepOne(context.Background(), sid); err != nil || !closed {
		t.Fatalf("SweepOne = %v, %v; want a close once ready", closed, err)
	}
	if v := view(t, a, sid); v.Reason != ReasonTimeout {
		t.Fatalf("A reason %q, want timeout", v.Reason)
	}
}

// Test 8 (R55-126): a bad reveal is debate.broken on B only, never
// debate.refused. Test 11: B's bad-reveal record.
func TestF29BadRevealEventAndRecord(t *testing.T) {
	a, b := newDNode(t, keyA), newDNode(t, keyB)
	_, sid := startDebate(t, a, b, 2)
	submit(t, b, sid, KindPosition, testPosition("B: fixed retry"))
	pass(t, b, a, request.KindAccept)
	pass(t, b, a, MailEntry)
	rv := a.ob.take(t, MailReveal)
	rv.body["nonce"] = strings.Repeat("ab", 32)
	mustDeliver(t, b, keyA, rv)
	if b.events.count(EventBroken) != 1 || b.events.count(EventRefused) != 0 || a.events.count(EventBroken) != 0 {
		t.Fatalf("events B broken %d refused %d, A broken %d", b.events.count(EventBroken), b.events.count(EventRefused), a.events.count(EventBroken))
	}
	m := expRecord(t, b, sid, RoleRespondent)
	f, _ := m["failed"].(map[string]any)
	if f["cancelled_by"] != RoleRespondent || f["cause"] != "bad_reveal" {
		t.Fatalf("failed = %v", m["failed"])
	}
	if m["verification"] != "none" || m["team"] != testTeam {
		t.Fatalf("verification %v, team %v", m["verification"], m["team"])
	}
	if _, ok := m["verification_by"]; ok {
		t.Fatal("verification_by on a debate record")
	}
}

// Test 11: B's refused close records cause decision_refused and B's own
// Decision as acceptance.decision; with no derivable record, none.
func TestF29RefusedCloseRecord(t *testing.T) {
	t.Run("derivable", func(t *testing.T) {
		a, b, _, sid := answered(t)
		pass(t, b, a, MailEntry)
		cl := a.ob.take(t, MailClose)
		cl.body["decision"] = strings.Repeat("0", 64)
		mustDeliver(t, b, a.self, cl)
		own := record(t, b, sid)
		m := expRecord(t, b, sid, RoleRespondent)
		f, _ := m["failed"].(map[string]any)
		if len(f) != 2 || f["cancelled_by"] != RoleRespondent || f["cause"] != "decision_refused" {
			t.Fatalf("failed = %v", m["failed"])
		}
		acc, _ := m["acceptance"].(map[string]any)
		d, _ := acc["decision"].(map[string]any)
		if acc["outcome"] != OutcomeCancelled || d["id"] != own.ID || d["hash"] != own.Hash {
			t.Fatalf("acceptance = %v, want B's own %s %s", acc, own.ID, own.Hash)
		}
		if _, ok := m["worked"]; ok {
			t.Fatal("worked on a refused close")
		}
	})
	t.Run("not derivable", func(t *testing.T) {
		a, b, _, sid := openPositions(t, 1)
		submit(t, a, sid, KindMove, pass0())
		pass(t, a, b, MailEntry)
		a.advance(2 * time.Hour)
		if _, err := a.ds.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		cl := a.ob.take(t, MailClose)
		cl.body["entries"] = json.Number("1")
		mustDeliver(t, b, a.self, cl)
		noRecord(t, b, sid)
		m := expRecord(t, b, sid, RoleRespondent)
		if acc, _ := m["acceptance"].(map[string]any); acc["decision"] != nil {
			t.Fatalf("acceptance.decision = %v, want absent", acc["decision"])
		}
		if f, _ := m["failed"].(map[string]any); f["cause"] != "decision_refused" {
			t.Fatalf("failed = %v", m["failed"])
		}
	})
}

func disagreement(points ...string) []any {
	out := make([]any, len(points))
	for i, p := range points {
		out[i] = map[string]any{"point": p, "initiator": "A view " + p, "respondent": "B view " + p}
	}
	return out
}

// Test 10 (R55-170, OD-F29-6): A's timeout close cuts B's answer, which had
// 2 remaining-disagreement points while the proposal had 1. B's record
// counts the Decision's 1, and its rounds and claims come from covered
// entries only.
func TestF29RecordCoversTheCut(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	submit(t, a, sid, KindMove, pass0())
	pass(t, a, b, MailEntry)
	submit(t, b, sid, KindMove, pass0())
	pass(t, b, a, MailEntry)
	prop := proposal()
	prop["remaining_disagreement"] = disagreement("p1")
	submit(t, a, sid, KindProposal, prop)
	pass(t, a, b, MailEntry)
	ans := answer(false)
	ans["remaining_disagreement"] = disagreement("p2", "p3")
	submit(t, b, sid, KindAnswer, ans) // never reaches A
	a.advance(2 * time.Hour)
	if _, err := a.ds.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	cl := a.ob.take(t, MailClose)
	if cl.body["entries"] != json.Number("5") || cl.body["reason"] != ReasonTimeout {
		t.Fatalf("close %v", cl.body)
	}
	mustDeliver(t, b, a.self, cl)
	mustDeliver(t, a, b.self, b.ob.take(t, MailSign))
	_, want := decisionFacts(record(t, b, sid).Decision)
	if want != 1 {
		t.Fatalf("the Decision has %d remaining points, want 1", want)
	}
	for _, n := range []*dnode{a, b} {
		role := RoleInitiator
		if n == b {
			role = RoleRespondent
		}
		m := expRecord(t, n, sid, role)
		if f, _ := m["failed"].(map[string]any); f["remaining_disagreement_points"] != float64(1) {
			t.Errorf("%s failed = %v, want 1 point", n.name(), m["failed"])
		}
		ap, _ := m["approach"].(map[string]any)
		pos, _ := ap["positions"].(map[string]any)
		if ap["rounds_used"] != float64(1) || pos["initiator"] != "A: capped backoff" || pos["respondent"] != "B: fixed retry" {
			t.Errorf("%s approach = %v", n.name(), ap)
		}
		if m["team"] != testTeam {
			t.Errorf("%s team = %v", n.name(), m["team"])
		}
	}
}

// Test 10: an agreed record's worked.decision is the Decision's
// final_agreement.decision.
func TestF29WorkedFromDecision(t *testing.T) {
	a, b, _, sid := answered(t)
	pass(t, b, a, MailEntry)
	mustDeliver(t, b, a.self, a.ob.take(t, MailClose))
	want, _ := decisionFacts(record(t, b, sid).Decision)
	if want == "" {
		t.Fatal("the Decision has no final agreement")
	}
	var canon map[string]any
	if err := json.Unmarshal(record(t, a, sid).Decision, &canon); err != nil {
		t.Fatal(err)
	}
	for _, n := range []*dnode{a, b} {
		role := RoleInitiator
		if n == b {
			role = RoleRespondent
		}
		m := expRecord(t, n, sid, role)
		if w, _ := m["worked"].(map[string]any); w["decision"] != want {
			t.Errorf("%s worked = %v, want %q", n.name(), m["worked"], want)
		}
		if m["verification"] != "none" {
			t.Errorf("%s verification = %v", n.name(), m["verification"])
		}
	}
	if decision.Hash(record(t, a, sid).Decision) != record(t, b, sid).Hash {
		t.Fatal("the two Decisions differ")
	}
}
