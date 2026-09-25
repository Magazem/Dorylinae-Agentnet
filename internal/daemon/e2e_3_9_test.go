package daemon_test

// Ticket 3.9 acceptance (Docs/review/42-phase3-tickets.md §3.9): one e2e test
// runs a debate with 2 rounds, a constraint from each side, a revision, a
// proposal and an accepted answer, then a second debate forced to escalate;
// both Decisions verify offline with two signatures; agentnet log --verify is
// ok on both sides and the --session view (audit_list{session}) shows the
// debate's rows; experience records exist on both sides of both debates. A
// second test (TestPhase3AuditHasNoContent) extends the 2.9 no-content
// invariant with Phase 3's content fields.

import (
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/audit"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
)

// TestPhase3E2E drives the whole Phase 3 lifecycle over a relay between two
// real daemons: a 2-round debate that agrees (with a human constraint from
// each side and a revision in round 2), then a second debate forced to
// escalate, and checks the Decisions, the audit chain, the session view and
// the experience records ticket 3.9 asks for.
func TestPhase3E2E(t *testing.T) {
	a, b, teamID := newConstrainPair(t)

	// --- Debate 1: 2 rounds, a constraint from each side, a revision,
	// agreement. ---
	var res daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "debate", Team: teamID, Title: "How should the outbox retry?",
		Brief:   "Pick a retry strategy for the outbox sender.",
		Context: []daemon.ContextParam{{Name: "outbox.go", Text: "// current sender loop\n"}},
		Debate: &daemon.DebateParam{
			Position: []byte(`{"claim":"Capped backoff","argument":"Simple and bounded.",` +
				`"assumptions":["Retries are rare"],` +
				`"evidence":[{"kind":"file","ref":"internal/daemon/outbox.go","note":"current retry loop"}]}`),
			Rounds: 2,
		},
	}, &res)
	sid := res.Session
	harnessWait(t, "B to store the debate", phaseIs(b.harnessNode, sid, debate.PhaseInvited))

	dsSubmit(t, b.ds, sid, debate.KindPosition,
		`{"claim":"Fixed retry","argument":"Predictable and simple.","assumptions":["Retries are rare"]}`)
	harnessWait(t, "A to reveal", phaseIs(a.harnessNode, sid, debate.PhaseRounds))
	harnessWait(t, "B to apply the reveal", phaseIs(b.harnessNode, sid, debate.PhaseRounds))

	// A human constraint from each side, approved through the fake window
	// (OD-P3-3).
	ca := a.constrain(t, sid, "Must stay compatible with Go 1.22")
	a.approve(t, ca.ID)
	harnessWait(t, "A's constraint to be active", func() bool { return a.constraints(sid, "active") == 1 })
	harnessWait(t, "B to receive A's constraint", func() bool { return b.constraints(sid, "active") == 1 })
	cb := b.constrain(t, sid, "No new third-party dependency")
	b.approve(t, cb.ID)
	harnessWait(t, "A to receive B's constraint", func() bool { return a.constraints(sid, "active") == 2 })
	harnessWait(t, "B's constraint to be active", func() bool { return b.constraints(sid, "active") == 2 })

	// Round 1: both sides challenge, so the debate does not converge early
	// (rule 1 needs two consecutive empty moves; internal/debate/turns.go).
	dsSubmit(t, a.ds, sid, debate.KindMove,
		`{"challenges":[{"targets":["claim"],"argument":"Fixed retry does not back off under load."}]}`)
	harnessWait(t, "B to apply A's round-1 move", nextIs(b.harnessNode, sid, 3))
	dsSubmit(t, b.ds, sid, debate.KindMove,
		`{"challenges":[{"targets":["claim"],"argument":"Capped backoff adds latency."}]}`)
	harnessWait(t, "A to reach round 2", nextIs(a.harnessNode, sid, 4))

	// Round 2: A revises its position (the ticket's "a revision"); B passes,
	// and the debate converges because round 2 (the max) is now used, not
	// because of two empty moves in a row.
	dsSubmit(t, a.ds, sid, debate.KindMove,
		`{"challenges":[],"revision":{"claim":"Capped backoff with jitter","argument":"Jitter avoids synchronized retries."}}`)
	harnessWait(t, "B to apply A's revision", nextIs(b.harnessNode, sid, 5))
	dsSubmit(t, b.ds, sid, debate.KindMove, `{"challenges":[]}`)
	harnessWait(t, "A to converge", phaseIs(a.harnessNode, sid, debate.PhaseConverge))
	harnessWait(t, "B to converge", phaseIs(b.harnessNode, sid, debate.PhaseConverge))

	dsSubmit(t, a.ds, sid, debate.KindProposal,
		`{"agreement":{"decision":"Capped backoff with jitter","points":["Bounded retries","Avoids thundering herd"],"argument":"Best of both."}}`)
	harnessWait(t, "B to apply the proposal", nextIs(b.harnessNode, sid, 7))
	dsSubmit(t, b.ds, sid, debate.KindAnswer, `{"accept":true,"argument":"Agreed, this addresses my concern."}`)
	harnessWait(t, "A to close", phaseIs(a.harnessNode, sid, debate.PhaseClosed))
	harnessWait(t, "B to close", phaseIs(b.harnessNode, sid, debate.PhaseClosed))

	// Both Decisions verify offline with two signatures (OD-P3-5, ticket 3.3a).
	sameSignedDecision(t, a.harnessNode, b.harnessNode, sid, debate.OutcomeAgreed)

	// Experience records exist on both sides (ticket 3.7).
	harnessWait(t, "A's experience record (debate 1)", func() bool {
		return a.count(`SELECT COUNT(*) FROM experience_records WHERE session = '`+sid+`' AND role = 'initiator'`) == 1
	})
	harnessWait(t, "B's experience record (debate 1)", func() bool {
		return b.count(`SELECT COUNT(*) FROM experience_records WHERE session = '`+sid+`' AND role = 'respondent'`) == 1
	})

	// --- Debate 2: forced to escalate (OD-P3-5 amendment / plan 3.5). ---
	var res2 daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "debate", Team: teamID, Title: "Which serializer?",
		Brief:  "Pick a wire format for the new endpoint.",
		Debate: &daemon.DebateParam{Position: []byte(`{"claim":"Protobuf","argument":"Smaller and typed."}`), Rounds: 1},
	}, &res2)
	sid2 := res2.Session
	harnessWait(t, "B to store the second debate", phaseIs(b.harnessNode, sid2, debate.PhaseInvited))
	dsSubmit(t, b.ds, sid2, debate.KindPosition, `{"claim":"JSON","argument":"Simple and debuggable."}`)
	harnessWait(t, "A to reveal (debate 2)", phaseIs(a.harnessNode, sid2, debate.PhaseRounds))
	dsSubmit(t, a.ds, sid2, debate.KindMove, `{"challenges":[]}`)
	harnessWait(t, "B to apply A's move (debate 2)", nextIs(b.harnessNode, sid2, 3))
	dsSubmit(t, b.ds, sid2, debate.KindMove, `{"challenges":[]}`)
	harnessWait(t, "A to converge (debate 2)", phaseIs(a.harnessNode, sid2, debate.PhaseConverge))
	dsSubmit(t, a.ds, sid2, debate.KindProposal, `{"agreement":{"decision":"Protobuf"}}`)
	harnessWait(t, "B to apply the proposal (debate 2)", nextIs(b.harnessNode, sid2, 5))
	dsSubmit(t, b.ds, sid2, debate.KindAnswer,
		`{"accept":false,"remaining_disagreement":[{"point":"Readability","initiator":"Typed is enough","respondent":"JSON is easier to debug"}]}`)
	harnessWait(t, "B to close (debate 2)", phaseIs(b.harnessNode, sid2, debate.PhaseClosed))
	harnessWait(t, "A to close (debate 2)", phaseIs(a.harnessNode, sid2, debate.PhaseClosed))

	sameSignedDecision(t, a.harnessNode, b.harnessNode, sid2, debate.OutcomeEscalated)

	harnessWait(t, "A's experience record (debate 2)", func() bool {
		return a.count(`SELECT COUNT(*) FROM experience_records WHERE session = '`+sid2+`' AND role = 'initiator'`) == 1
	})
	harnessWait(t, "B's experience record (debate 2)", func() bool {
		return b.count(`SELECT COUNT(*) FROM experience_records WHERE session = '`+sid2+`' AND role = 'respondent'`) == 1
	})

	// agentnet log --verify (audit_verify) is ok on both sides.
	for _, n := range []*harnessNode{a.harnessNode, b.harnessNode} {
		var ver daemon.AuditVerifyResult
		n.call("audit_verify", nil, &ver)
		if ver.Verify.Status != audit.StatusOK {
			t.Fatalf("%s: log --verify = %+v, want ok", n.name, ver.Verify)
		}
	}

	// The --session view (audit_list{session}) shows both debates' rows on
	// both sides.
	for _, n := range []*harnessNode{a.harnessNode, b.harnessNode} {
		for _, s := range []string{sid, sid2} {
			var list daemon.AuditListResult
			n.call("audit_list", audit.ListParams{Session: s}, &list)
			if len(list.Events) == 0 {
				t.Fatalf("%s: audit_list{session: %s} is empty", n.name, s)
			}
			foundDebate := false
			for _, e := range list.Events {
				if strings.HasPrefix(e.Action, "debate.") || strings.HasPrefix(e.Action, "decision.") {
					foundDebate = true
					break
				}
			}
			if !foundDebate {
				t.Fatalf("%s: audit_list{session: %s} has no debate/decision row: %+v", n.name, s, list.Events)
			}
		}
	}
}

// p3AuditMarkers are unique strings placed in every Phase 3 content field:
// the topic, title, context file, positions, arguments, evidence refs and
// notes, challenges, a revision, the proposal, the answer, a disagreement and
// a human constraint from each side. Ticket 3.9's acceptance is that none of
// them reach audit_events on either side.
var p3AuditMarkers = []string{
	"P3AUDIT-TITLE-9f2a", "P3AUDIT-BRIEF-1d7c",
	"P3AUDIT-CTXNAME-3b81.go", "P3AUDIT-CTXTEXT-6e40",
	"P3AUDIT-CLAIM-A-2c19", "P3AUDIT-ARGUMENT-A-88b3", "P3AUDIT-ASSUMPTION-a015",
	"P3AUDIT-EVREF-c274", "P3AUDIT-EVNOTE-5f9d",
	"P3AUDIT-CLAIM-B-e611", "P3AUDIT-ARGUMENT-B-04aa",
	"P3AUDIT-CHALLENGE-A-7bd2", "P3AUDIT-CHALLENGE-B-9c3e",
	"P3AUDIT-REVISION-CLAIM-1a6f", "P3AUDIT-REVISION-ARG-de90",
	"P3AUDIT-PROPOSAL-DECISION-4477", "P3AUDIT-PROPOSAL-POINT-88ee", "P3AUDIT-PROPOSAL-ARG-b312",
	"P3AUDIT-ANSWER-ARG-2f5c",
	"P3AUDIT-CONSTRAINT-A-e02b", "P3AUDIT-CONSTRAINT-B-71dc",
	"P3AUDIT-CLAIM2-A-33aa", "P3AUDIT-ARGUMENT2-A-5511", "P3AUDIT-CLAIM2-B-77bc", "P3AUDIT-ARGUMENT2-B-99de",
	"P3AUDIT-PROPOSAL2-DECISION-1234",
	"P3AUDIT-DISAGREE-POINT-a1b2", "P3AUDIT-DISAGREE-INIT-c3d4", "P3AUDIT-DISAGREE-RESP-e5f6",
}

// TestPhase3AuditHasNoContent is the ticket 3.9 acceptance: drive a debate
// with every content field carrying a unique marker (an agreed debate with a
// revision and a constraint from each side, then a second debate forced to
// escalate, carrying a remaining disagreement), then scan every audit_events
// row on both sides and assert no marker appears anywhere. It follows
// TestPhase2AuditHasNoContent's shape (e2e_2_9_test.go) and reuses
// allAuditRows (e2e_1_9_test.go).
func TestPhase3AuditHasNoContent(t *testing.T) {
	a, b, teamID := newConstrainPair(t)

	var res daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "debate", Team: teamID, Title: "P3AUDIT-TITLE-9f2a", Brief: "P3AUDIT-BRIEF-1d7c",
		Context: []daemon.ContextParam{{Name: "P3AUDIT-CTXNAME-3b81.go", Text: "P3AUDIT-CTXTEXT-6e40"}},
		Debate: &daemon.DebateParam{
			Position: []byte(`{"claim":"P3AUDIT-CLAIM-A-2c19","argument":"P3AUDIT-ARGUMENT-A-88b3",` +
				`"assumptions":["P3AUDIT-ASSUMPTION-a015"],` +
				`"evidence":[{"kind":"file","ref":"P3AUDIT-EVREF-c274","note":"P3AUDIT-EVNOTE-5f9d"}]}`),
			Rounds: 2,
		},
	}, &res)
	sid := res.Session
	harnessWait(t, "B to store the debate", phaseIs(b.harnessNode, sid, debate.PhaseInvited))

	dsSubmit(t, b.ds, sid, debate.KindPosition, `{"claim":"P3AUDIT-CLAIM-B-e611","argument":"P3AUDIT-ARGUMENT-B-04aa"}`)
	harnessWait(t, "A to reveal", phaseIs(a.harnessNode, sid, debate.PhaseRounds))
	harnessWait(t, "B to apply the reveal", phaseIs(b.harnessNode, sid, debate.PhaseRounds))

	ca := a.constrain(t, sid, "P3AUDIT-CONSTRAINT-A-e02b")
	a.approve(t, ca.ID)
	harnessWait(t, "A's constraint to be active", func() bool { return a.constraints(sid, "active") == 1 })
	harnessWait(t, "B to receive A's constraint", func() bool { return b.constraints(sid, "active") == 1 })
	cb := b.constrain(t, sid, "P3AUDIT-CONSTRAINT-B-71dc")
	b.approve(t, cb.ID)
	harnessWait(t, "A to receive B's constraint", func() bool { return a.constraints(sid, "active") == 2 })
	harnessWait(t, "B's constraint to be active", func() bool { return b.constraints(sid, "active") == 2 })

	dsSubmit(t, a.ds, sid, debate.KindMove, `{"challenges":[{"targets":["claim"],"argument":"P3AUDIT-CHALLENGE-A-7bd2"}]}`)
	harnessWait(t, "B to apply A's round-1 move", nextIs(b.harnessNode, sid, 3))
	dsSubmit(t, b.ds, sid, debate.KindMove, `{"challenges":[{"targets":["claim"],"argument":"P3AUDIT-CHALLENGE-B-9c3e"}]}`)
	harnessWait(t, "A to reach round 2", nextIs(a.harnessNode, sid, 4))

	dsSubmit(t, a.ds, sid, debate.KindMove,
		`{"challenges":[],"revision":{"claim":"P3AUDIT-REVISION-CLAIM-1a6f","argument":"P3AUDIT-REVISION-ARG-de90"}}`)
	harnessWait(t, "B to apply A's revision", nextIs(b.harnessNode, sid, 5))
	dsSubmit(t, b.ds, sid, debate.KindMove, `{"challenges":[]}`)
	harnessWait(t, "A to converge", phaseIs(a.harnessNode, sid, debate.PhaseConverge))

	dsSubmit(t, a.ds, sid, debate.KindProposal,
		`{"agreement":{"decision":"P3AUDIT-PROPOSAL-DECISION-4477","points":["P3AUDIT-PROPOSAL-POINT-88ee"],"argument":"P3AUDIT-PROPOSAL-ARG-b312"}}`)
	harnessWait(t, "B to apply the proposal", nextIs(b.harnessNode, sid, 7))
	dsSubmit(t, b.ds, sid, debate.KindAnswer, `{"accept":true,"argument":"P3AUDIT-ANSWER-ARG-2f5c"}`)
	harnessWait(t, "A to close", phaseIs(a.harnessNode, sid, debate.PhaseClosed))
	harnessWait(t, "B to close", phaseIs(b.harnessNode, sid, debate.PhaseClosed))
	sameSignedDecision(t, a.harnessNode, b.harnessNode, sid, debate.OutcomeAgreed)
	harnessWait(t, "A's experience record (debate 1)", func() bool {
		return a.count(`SELECT COUNT(*) FROM experience_records WHERE session = '`+sid+`' AND role = 'initiator'`) == 1
	})
	harnessWait(t, "B's experience record (debate 1)", func() bool {
		return b.count(`SELECT COUNT(*) FROM experience_records WHERE session = '`+sid+`' AND role = 'respondent'`) == 1
	})

	// A second debate, forced to escalate, carrying a remaining disagreement.
	var res2 daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{
		To: b.key, Type: "debate", Team: teamID, Title: "second", Brief: "second",
		Debate: &daemon.DebateParam{Position: []byte(`{"claim":"P3AUDIT-CLAIM2-A-33aa","argument":"P3AUDIT-ARGUMENT2-A-5511"}`), Rounds: 1},
	}, &res2)
	sid2 := res2.Session
	harnessWait(t, "B to store the second debate", phaseIs(b.harnessNode, sid2, debate.PhaseInvited))
	dsSubmit(t, b.ds, sid2, debate.KindPosition, `{"claim":"P3AUDIT-CLAIM2-B-77bc","argument":"P3AUDIT-ARGUMENT2-B-99de"}`)
	harnessWait(t, "A to reveal (debate 2)", phaseIs(a.harnessNode, sid2, debate.PhaseRounds))
	dsSubmit(t, a.ds, sid2, debate.KindMove, `{"challenges":[]}`)
	harnessWait(t, "B to apply A's move (debate 2)", nextIs(b.harnessNode, sid2, 3))
	dsSubmit(t, b.ds, sid2, debate.KindMove, `{"challenges":[]}`)
	harnessWait(t, "A to converge (debate 2)", phaseIs(a.harnessNode, sid2, debate.PhaseConverge))
	dsSubmit(t, a.ds, sid2, debate.KindProposal, `{"agreement":{"decision":"P3AUDIT-PROPOSAL2-DECISION-1234"}}`)
	harnessWait(t, "B to apply the proposal (debate 2)", nextIs(b.harnessNode, sid2, 5))
	dsSubmit(t, b.ds, sid2, debate.KindAnswer,
		`{"accept":false,"remaining_disagreement":[{"point":"P3AUDIT-DISAGREE-POINT-a1b2","initiator":"P3AUDIT-DISAGREE-INIT-c3d4","respondent":"P3AUDIT-DISAGREE-RESP-e5f6"}]}`)
	harnessWait(t, "B to close (debate 2)", phaseIs(b.harnessNode, sid2, debate.PhaseClosed))
	harnessWait(t, "A to close (debate 2)", phaseIs(a.harnessNode, sid2, debate.PhaseClosed))
	sameSignedDecision(t, a.harnessNode, b.harnessNode, sid2, debate.OutcomeEscalated)
	harnessWait(t, "A's experience record (debate 2)", func() bool {
		return a.count(`SELECT COUNT(*) FROM experience_records WHERE session = '`+sid2+`' AND role = 'initiator'`) == 1
	})
	harnessWait(t, "B's experience record (debate 2)", func() bool {
		return b.count(`SELECT COUNT(*) FROM experience_records WHERE session = '`+sid2+`' AND role = 'respondent'`) == 1
	})

	for _, n := range []*harnessNode{a.harnessNode, b.harnessNode} {
		_, details := allAuditRows(t, n)
		for _, d := range details {
			for _, m := range p3AuditMarkers {
				if strings.Contains(d, m) {
					t.Fatalf("%s: audit detail %q contains marker %q", n.name, d, m)
				}
			}
		}
	}
}
