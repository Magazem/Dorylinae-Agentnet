package daemon_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/approval"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
)

// constrainNode is a harness node with a fake approval notifier and window
// (the human confirms in the fake window, never a real dialog).
type constrainNode struct {
	*harnessNode
	ds       *atomic.Pointer[debate.Store]
	notifier *fakeApprovalNotifier
	win      *fakeWindowRunner
	run      *invRun // the audit inventory's record of called methods
}

func newConstrainPair(t *testing.T) (a, b *constrainNode, teamID string) {
	t.Helper()
	r, run := newHarnessRelay(t), newInvRun()
	mk := func(name string) *constrainNode {
		n := &constrainNode{harnessNode: newHarnessNode(t, name, r), ds: &atomic.Pointer[debate.Store]{},
			notifier: &fakeApprovalNotifier{}, win: newFakeWindowRunner(), run: run}
		n.ApprovalNotify = n.notifier
		n.ApprovalWindow = n.win
		n.OnDebateReady = func(s *debate.Store) { n.ds.Store(s) }
		return n
	}
	a, b = mk("alice"), mk("bob")
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a.harnessNode, b.harnessNode)
	return a, b, harnessSharedTeam(t, a.harnessNode, b.harnessNode, "x")
}

// constrain calls debate_constrain and returns the pending approval.
func (n *constrainNode) constrain(t *testing.T, sid, text string) approval.View {
	t.Helper()
	var res daemon.DebateConstrainResult
	n.run.call(n.harnessNode, "debate_constrain", daemon.DebateConstrainParams{ID: sid, Text: text}, &res)
	if res.Approval.ID == "" || res.Approval.Kind != approval.KindDebateConstraint || res.Approval.State != approval.StatePending {
		t.Fatalf("%s: debate_constrain = %+v", n.name, res.Approval)
	}
	return res.Approval
}

// approve types the code into the fake window, as the human would.
func (n *constrainNode) approve(t *testing.T, id string) {
	t.Helper()
	n.win.answer(id, "approve", n.notifier.lastCode(t))
}

func (n *constrainNode) lastSummary() string {
	n.win.mu.Lock()
	defer n.win.mu.Unlock()
	if len(n.win.starts) == 0 {
		return ""
	}
	return n.win.starts[len(n.win.starts)-1].summary
}

func (n *constrainNode) constraints(sid, state string) int {
	return n.count(`SELECT COUNT(*) FROM debate_constraints WHERE session = '` + sid + `' AND state = '` + state + `'`)
}

// Ticket 3.4 end to end over the relay: debate_constrain refuses bad text
// and bad states; a pending constraint is stored and sent nowhere, and a
// rejected one never appears; an approved one (fake window) shows in both
// sides' debate_show views; one confirmed after the debate left converge is
// rejected with reason precondition; no audit row holds constraint text; and
// mail_submit cannot send debate.constraint.
func TestDebateConstrainE2E(t *testing.T) {
	a, b, teamID := newConstrainPair(t)
	var res daemon.RequestSubmitResult
	a.call("request_submit", daemon.RequestSubmitParams{To: b.key, Type: "debate", Team: teamID, Title: "Retries", Brief: "How should the outbox retry?",
		Debate: &daemon.DebateParam{Position: e2ePosition("Capped backoff"), Rounds: 1}}, &res)
	sid := res.Session
	harnessWait(t, "B to store the debate", phaseIs(b.harnessNode, sid, debate.PhaseInvited))
	if code, msg := callCode(t, a.harnessNode, "debate_constrain", daemon.DebateConstrainParams{ID: sid, Text: "Too early"}); code != "bad_state" {
		t.Fatalf("invited: %q (%s), want bad_state", code, msg)
	}

	dsSubmit(t, b.ds, sid, debate.KindPosition, `{"argument":"Simple.","claim":"Fixed retry"}`)
	// dsSubmit calls Store.Submit directly (bypassing the IPC layer, as the
	// rest of this test does for speed); mark it for the inventory below,
	// which asserts debate_submit was exercised and audited.
	a.run.mark("debate_submit")
	harnessWait(t, "A to reach rounds", phaseIs(a.harnessNode, sid, debate.PhaseRounds))
	harnessWait(t, "B to reach rounds", phaseIs(b.harnessNode, sid, debate.PhaseRounds))

	// Review 43 H3: visible characters only.
	for name, text := range map[string]string{
		"zero-width space": "No new" + string(rune(0x200b)) + "dependency",
		"bidi control":     "No new " + string(rune(0x202e)) + "dependency",
		"U+FEFF":           string(rune(0xfeff)) + "No new dependency",
		"tag character":    "No new dependency" + string(rune(0xe0041)),
		"empty":            "",
	} {
		if code, msg := callCode(t, a.harnessNode, "debate_constrain", daemon.DebateConstrainParams{ID: sid, Text: text}); code != "bad_request" {
			t.Errorf("%s: %q (%s), want bad_request", name, code, msg)
		}
	}
	if code, _ := callCode(t, a.harnessNode, "debate_constrain", daemon.DebateConstrainParams{ID: "s-0123456789abcdef0123456789abcdef", Text: "x"}); code != "unknown_session" {
		t.Fatalf("unknown debate: %q", code)
	}
	if n := a.win.startCount(); n != 0 {
		t.Fatalf("a refused constraint opened %d approval windows", n)
	}

	// Pending, then rejected: stored nowhere, sent nowhere.
	const text1 = `Must stay compatible with "Go 1.22"`
	pend := a.constrain(t, sid, text1)
	if s := a.lastSummary(); !strings.Contains(s, `"Must stay compatible with \"Go 1.22\""`) || !strings.Contains(s, sid) || !strings.Contains(s, "bob") {
		t.Fatalf("approval summary %q lacks the quoted text, the session or the peer", s)
	}
	if n := a.count(`SELECT COUNT(*) FROM debate_constraints`) + a.count(`SELECT COUNT(*) FROM outbox WHERE kind = 'debate.constraint'`); n != 0 {
		t.Fatalf("a pending constraint left %d rows", n)
	}
	a.call("approval_reject", map[string]string{"id": pend.ID}, nil)
	if n := a.count(`SELECT COUNT(*) FROM debate_constraints`) + a.count(`SELECT COUNT(*) FROM outbox WHERE kind = 'debate.constraint'`); n != 0 {
		t.Fatalf("a rejected constraint left %d rows", n)
	}

	// Approved on A: active on both sides, in both views.
	ok := a.constrain(t, sid, text1)
	a.approve(t, ok.ID)
	harnessWait(t, "A to store the approved constraint", func() bool { return a.constraints(sid, "active") == 1 })
	harnessWait(t, "B to receive it", func() bool { return b.constraints(sid, "active") == 1 })
	// Approved on B: reaches A.
	okB := b.constrain(t, sid, "No new dependency")
	b.approve(t, okB.ID)
	harnessWait(t, "A to receive B's constraint", func() bool { return a.constraints(sid, "active") == 2 })
	va, err := a.ds.Load().Get(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	vb, err := b.ds.Load().Get(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(va.Constraints) != 2 || len(vb.Constraints) != 2 {
		t.Fatalf("views: A %+v, B %+v", va.Constraints, vb.Constraints)
	}
	for i := range va.Constraints {
		if va.Constraints[i] != vb.Constraints[i] {
			t.Errorf("constraint %d differs: A %+v, B %+v", i, va.Constraints[i], vb.Constraints[i])
		}
	}
	// Both approvals may fall in the same second, so the (at, id) order is
	// not known here: find A's by its text.
	found := false
	for _, c := range va.Constraints {
		if c.Text == text1 {
			found = c.Author == debate.RoleInitiator && c.State == debate.ConstraintActive
		}
	}
	if !found {
		t.Fatalf("A's constraint missing or wrong: %+v", va.Constraints)
	}

	// Pending while the debate closes: confirmed afterwards, it is rejected
	// (precondition) and stored nowhere.
	late := a.constrain(t, sid, "Pending at the close")
	dsSubmit(t, a.ds, sid, debate.KindMove, `{"challenges":[]}`)
	harnessWait(t, "B to apply A's move", nextIs(b.harnessNode, sid, 3))
	dsSubmit(t, b.ds, sid, debate.KindMove, `{"challenges":[]}`)
	harnessWait(t, "A to converge", phaseIs(a.harnessNode, sid, debate.PhaseConverge))
	dsSubmit(t, a.ds, sid, debate.KindProposal, `{"agreement":{"decision":"Capped backoff with jitter"}}`)
	harnessWait(t, "B to apply the proposal", nextIs(b.harnessNode, sid, 5))
	dsSubmit(t, b.ds, sid, debate.KindAnswer, `{"accept":true}`)
	harnessWait(t, "A to close", phaseIs(a.harnessNode, sid, debate.PhaseClosing))
	harnessWait(t, "B to close", phaseIs(b.harnessNode, sid, debate.PhaseClosed))
	a.approve(t, late.ID)
	harnessWait(t, "the late approval to be rejected", func() bool {
		return a.count(`SELECT COUNT(*) FROM approvals WHERE id = '`+late.ID+`' AND state = 'rejected'`) == 1
	})
	if n := a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'approval.reject' AND instr(detail, 'precondition') > 0 AND instr(detail, '` + late.ID + `') > 0`); n != 1 {
		t.Fatalf("precondition rejection audited %d times", n)
	}
	if a.constraints(sid, "active") != 2 || b.constraints(sid, "active") != 2 {
		t.Fatal("the late constraint was stored")
	}
	if code, _ := callCode(t, a.harnessNode, "debate_constrain", daemon.DebateConstrainParams{ID: sid, Text: "After the end"}); code != "bad_state" {
		t.Fatalf("closing: %q, want bad_state", code)
	}
	if code, _ := callCode(t, b.harnessNode, "debate_constrain", daemon.DebateConstrainParams{ID: res.ID, Text: "After the end"}); code != "bad_state" {
		t.Fatalf("closed (by request id): %q, want bad_state", code)
	}

	for _, n := range []*constrainNode{a, b} {
		if c := n.count(`SELECT COUNT(*) FROM audit_events WHERE instr(detail, 'Go 1.22') > 0 OR instr(detail, 'dependency') > 0 OR instr(detail, 'Pending at') > 0`); c != 0 {
			t.Errorf("%s: %d audit rows hold constraint text", n.name, c)
		}
	}
	if n := a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'debate.constraint' AND instr(detail, '` + ok.ID + `') > 0`); n != 1 {
		t.Errorf("A audited debate.constraint %d times for its approval", n)
	}
	if n := a.count(`SELECT COUNT(*) FROM audit_events WHERE action = 'debate.constraint_in'`); n != 1 {
		t.Errorf("A audited debate.constraint_in %d times", n)
	}
	if code, _ := callCode(t, a.harnessNode, "mail_submit", daemon.MailSubmitParams{To: b.key, Kind: debate.MailConstraint, Body: []byte(`{}`)}); code != "bad_request" {
		t.Errorf("mail_submit debate.constraint: %q, want bad_request", code)
	}
	// Ticket 3.6b's inventory, debate half: every debate method was called
	// and audited on the node the inventory names.
	checkInventory(t, a.run, methodInventory, debateKeys(methodInventory), true, a.harnessNode, b.harnessNode)
}
