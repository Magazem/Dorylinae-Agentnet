package daemon_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/decision"
)

type decisionRow struct {
	decision, hash, state string
	sigI, sigR            sql.NullString
}

func readDecision(t *testing.T, n *harnessNode, sid string) decisionRow {
	t.Helper()
	var d decisionRow
	if err := n.query(`SELECT decision, hash, state, sig_initiator, sig_respondent FROM decisions WHERE session = '`+sid+`'`,
		&d.decision, &d.hash, &d.state, &d.sigI, &d.sigR); err != nil {
		t.Fatalf("%s: read Decision: %v", n.name, err)
	}
	return d
}

// sameSignedDecision is the 3.3 acceptance over a relay: the same Decision
// bytes on both sides, both signatures stored on both sides, and the signed
// file verifies with two signatures. It returns the Decision.
func sameSignedDecision(t *testing.T, a, b *harnessNode, sid, outcome string) string {
	t.Helper()
	harnessWait(t, "A's Decision to be signed", func() bool {
		return a.count(`SELECT COUNT(*) FROM decisions WHERE session = '`+sid+`' AND state = 'signed'`) == 1
	})
	da, db := readDecision(t, a, sid), readDecision(t, b, sid)
	if da.decision != db.decision || da.hash != db.hash {
		t.Fatalf("Decisions differ:\nA %s\nB %s", da.decision, db.decision)
	}
	if db.state != debate.DecisionSigned || da.sigI != db.sigI || da.sigR != db.sigR || !da.sigI.Valid || !da.sigR.Valid {
		t.Fatalf("signatures: A %+v, B %+v", da, db)
	}
	file, err := json.Marshal(map[string]any{"decision": json.RawMessage(da.decision), "hash": da.hash,
		"signatures": map[string]string{"initiator": da.sigI.String, "respondent": da.sigR.String}})
	if err != nil {
		t.Fatal(err)
	}
	res := decision.Verify(file, debate.DecisionSchema())
	if !res.Valid || !res.Complete || res.Initiator != a.key || res.Respondent != b.key {
		t.Fatalf("verify: %+v", res)
	}
	if !strings.Contains(da.decision, `"outcome":"`+outcome+`"`) {
		t.Fatalf("outcome: %s", da.decision)
	}
	for _, n := range []*harnessNode{a, b} {
		if c := n.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'decision.create' AND instr(detail, '` + da.hash + `') > 0`); c != 1 {
			t.Errorf("%s: decision.create audited %d times", n.name, c)
		}
	}
	if c := a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'decision.sign_in'`); c < 1 {
		t.Error("A did not audit decision.sign_in")
	}
	return da.decision
}

// 3.5 over a relay: a forced disagreement (accept: false) ends escalated, and
// the Decision is produced and signed by both. The remaining disagreement is
// in it; no audit row holds debate content.
func TestDecisionEscalatedE2E(t *testing.T) {
	a, b, teamID, aDS, bDS := debatePair(t)
	var res daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{To: b.key, Type: "debate", Team: teamID, Title: "Retries",
		Brief: "How should the outbox retry?", Debate: &daemon.DebateParam{Position: e2ePosition("Capped backoff"), Rounds: 1}}, &res)
	sid := res.Session
	harnessWait(t, "B to store the debate", phaseIs(b, sid, debate.PhaseInvited))
	dsSubmit(t, bDS, sid, debate.KindPosition, `{"argument":"Simple.","claim":"Fixed retry"}`)
	harnessWait(t, "B to apply the reveal", phaseIs(b, sid, debate.PhaseRounds))
	dsSubmit(t, aDS, sid, debate.KindMove, `{"challenges":[]}`)
	harnessWait(t, "B to apply A's move", nextIs(b, sid, 3))
	dsSubmit(t, bDS, sid, debate.KindMove, `{"challenges":[]}`)
	harnessWait(t, "A to converge", phaseIs(a, sid, debate.PhaseConverge))
	dsSubmit(t, aDS, sid, debate.KindProposal, `{"agreement":{"decision":"Capped backoff with jitter"}}`)
	harnessWait(t, "B to apply the proposal", nextIs(b, sid, 5))
	dsSubmit(t, bDS, sid, debate.KindAnswer,
		`{"accept":false,"remaining_disagreement":[{"initiator":"Backoff is enough","point":"Jitter","respondent":"Jitter is needed"}]}`)
	harnessWait(t, "B to close", phaseIs(b, sid, debate.PhaseClosed))
	harnessWait(t, "A to close (B signed)", phaseIs(a, sid, debate.PhaseClosed))
	d := sameSignedDecision(t, a, b, sid, debate.OutcomeEscalated)
	if !strings.Contains(d, `"reason":"rejected"`) || !strings.Contains(d, `"remaining_disagreement":[{"initiator":"Backoff is enough"`) ||
		strings.Contains(d, "final_agreement") {
		t.Fatalf("escalated Decision: %s", d)
	}
	for _, n := range []*harnessNode{a, b} {
		if c := n.count(`SELECT COUNT(*) FROM audit_events WHERE instr(detail, 'Capped') > 0 OR instr(detail, 'Jitter') > 0 OR instr(detail, 'outbox retry') > 0`); c != 0 {
			t.Errorf("%s: %d audit rows hold debate content", n.name, c)
		}
	}
}
