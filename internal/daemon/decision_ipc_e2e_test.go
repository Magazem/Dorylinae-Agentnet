package daemon_test

// Ticket 3.3b: decision_list, decision_show and debate_show's "decision"
// pointer, over the relay harness (Docs/protocol/decision.md §IPC). The
// derivation and signing themselves are 3.3a's decision_e2e_test.go
// (sameSignedDecision); this file only exercises the read IPC this ticket
// adds.

import (
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
)

func TestDecisionListAndShowE2E(t *testing.T) {
	a, b, teamID, aDS, bDS := debatePair(t)
	var res daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{To: b.key, Type: "debate", Team: teamID, Title: "Retries",
		Brief: "How should the outbox retry?", Debate: &daemon.DebateParam{Position: e2ePosition("Capped backoff"), Rounds: 1}}, &res)
	sid := res.Session

	// Before the debate closes, neither side has a Decision yet.
	var shownEarly daemon.DebateShowResult
	a.call("debate_show", map[string]any{"id": sid}, &shownEarly)
	if shownEarly.Debate.Decision != nil {
		t.Fatalf("debate_show before close already has a decision pointer: %+v", shownEarly.Debate.Decision)
	}
	var early daemon.DecisionListResult
	a.call("decision_list", map[string]any{}, &early)
	if len(early.Decisions) != 0 {
		t.Fatalf("decision_list before close: %+v, want none", early.Decisions)
	}
	if code, _ := callCode(t, a, "decision_show", map[string]any{"id": sid}); code != "unknown_decision" {
		t.Fatalf("decision_show before close: %q, want unknown_decision", code)
	}

	harnessWait(t, "B to store the debate", phaseIs(b, sid, debate.PhaseInvited))
	dsSubmit(t, bDS, sid, debate.KindPosition, `{"argument":"Simple.","claim":"Fixed retry"}`)
	harnessWait(t, "B to apply the reveal", phaseIs(b, sid, debate.PhaseRounds))
	dsSubmit(t, aDS, sid, debate.KindMove, `{"challenges":[]}`)
	harnessWait(t, "B to apply A's move", nextIs(b, sid, 3))
	dsSubmit(t, bDS, sid, debate.KindMove, `{"challenges":[]}`)
	harnessWait(t, "A to converge", phaseIs(a, sid, debate.PhaseConverge))
	dsSubmit(t, aDS, sid, debate.KindProposal, `{"agreement":{"decision":"Capped backoff with jitter"}}`)
	harnessWait(t, "B to apply the proposal", nextIs(b, sid, 5))
	dsSubmit(t, bDS, sid, debate.KindAnswer, `{"accept":true}`)
	harnessWait(t, "A to close (B signed)", phaseIs(a, sid, debate.PhaseClosed))
	harnessWait(t, "B to close", phaseIs(b, sid, debate.PhaseClosed))

	got := sameSignedDecision(t, a, b, sid, debate.OutcomeAgreed)

	for _, n := range []*harnessNode{a, b} {
		var list daemon.DecisionListResult
		n.call("decision_list", map[string]any{}, &list)
		if len(list.Decisions) != 1 || list.Decisions[0].State != "signed" || list.Decisions[0].Outcome != debate.OutcomeAgreed {
			t.Fatalf("%s decision_list: %+v", n.name, list.Decisions)
		}
		id := list.Decisions[0].ID

		var show daemon.DecisionShowResult
		n.call("decision_show", map[string]any{"id": sid}, &show)
		if show.State != "signed" || show.Signatures.Initiator == "" || show.Signatures.Respondent == "" {
			t.Fatalf("%s decision_show: state %s sigs %+v", n.name, show.State, show.Signatures)
		}
		if string(show.Decision) != got {
			t.Fatalf("%s decision_show.decision does not match the stored canonical bytes:\n%s\nvs\n%s", n.name, show.Decision, got)
		}
		if show.PeerNames["initiator"] == "" || show.PeerNames["respondent"] == "" {
			t.Fatalf("%s decision_show.peer_names incomplete: %+v", n.name, show.PeerNames)
		}
		if show.PeerFPs["initiator"] == "" || show.PeerFPs["respondent"] == "" {
			t.Fatalf("%s decision_show.peer_fingerprints incomplete: %+v", n.name, show.PeerFPs)
		}
		// Resolving by the Decision's own d-... id must give the same record.
		var showByID daemon.DecisionShowResult
		n.call("decision_show", map[string]any{"id": id}, &showByID)
		if string(showByID.Decision) != string(show.Decision) || showByID.Hash != show.Hash {
			t.Fatalf("%s: decision_show by d-id differs from by session", n.name)
		}

		var shown daemon.DebateShowResult
		n.call("debate_show", map[string]any{"id": sid}, &shown)
		if shown.Debate.Decision == nil {
			t.Fatalf("%s debate_show after close has no decision pointer", n.name)
		}
		if shown.Debate.Decision.State != "signed" || shown.Debate.Decision.Outcome != debate.OutcomeAgreed || len(shown.Debate.Decision.SignedBy) != 2 {
			t.Fatalf("%s debate_show.decision: %+v", n.name, shown.Debate.Decision)
		}
	}

	if code, _ := callCode(t, a, "decision_show", map[string]any{"id": "d-" + strings.Repeat("0", 32)}); code != "unknown_decision" {
		t.Fatalf("decision_show of an unknown id: %q, want unknown_decision", code)
	}

	var byPeer daemon.DecisionListResult
	a.call("decision_list", map[string]any{"peer": b.key}, &byPeer)
	if len(byPeer.Decisions) != 1 {
		t.Fatalf("decision_list filtered by peer: %+v", byPeer.Decisions)
	}
	var byState daemon.DecisionListResult
	a.call("decision_list", map[string]any{"state": "awaiting_peer"}, &byState)
	if len(byState.Decisions) != 0 {
		t.Fatalf("decision_list filtered by a state nothing has: %+v, want none", byState.Decisions)
	}

	// decision_show accepts the debate's request id too.
	var byReqID daemon.DecisionShowResult
	a.call("decision_show", map[string]any{"id": res.ID}, &byReqID)
	if byReqID.Hash == "" {
		t.Fatal("decision_show by request id failed")
	}
}
