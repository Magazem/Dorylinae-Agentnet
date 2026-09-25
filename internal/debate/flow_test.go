package debate

import (
	"context"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// The commitment vector of Docs/protocol/debate.md §Commit-reveal.
func TestCommitmentVector(t *testing.T) {
	const reqID = "r-0123456789abcdef0123456789abcdef"
	sid := worksession.DeriveID(keyA, keyB, reqID)
	if sid != "s-36375782ceb6baea9cee4d4273dfb035" {
		t.Fatalf("sid = %s", sid)
	}
	v, err := agentcard.ParseStrict([]byte(specPosition))
	if err != nil {
		t.Fatal(err)
	}
	_, canon, err := DecodeEntry(KindPosition, v)
	if err != nil {
		t.Fatal(err)
	}
	if string(canon) != specPosition {
		t.Fatalf("vector position is not canonical:\n%s", canon)
	}
	const nonce = "404142434445464748494a4b4c4d4e4f505152535455565758595a5b5c5d5e5f"
	if !ValidNonce(nonce) {
		t.Fatal("vector nonce refused")
	}
	got := Commitment(sid, keyA, nonce, canon)
	if got != "a24d1e0306c3bd0a129a4f15da39d0fce4f36258c13b2c4c65ba0e2b6cb81ed2" {
		t.Fatalf("commitment = %s", got)
	}
	// Binding: another session, author, nonce or position changes it.
	for name, c := range map[string]string{
		"sid":      Commitment(worksession.DeriveID(keyA, keyB, "r-0123456789abcdef0123456789abcdee"), keyA, nonce, canon),
		"author":   Commitment(sid, keyB, nonce, canon),
		"nonce":    Commitment(sid, keyA, strings.Repeat("0", 64), canon),
		"position": Commitment(sid, keyA, nonce, append(canon[:len(canon):len(canon)], ' ')),
	} {
		if c == got {
			t.Errorf("changing the %s kept the commitment", name)
		}
	}
	for _, bad := range []string{strings.ToUpper(nonce), nonce[:63], nonce + "0", strings.Repeat("g", 64)} {
		if ValidNonce(bad) {
			t.Errorf("ValidNonce(%q) = true", bad)
		}
	}
	if a, b := NewNonce(), NewNonce(); !ValidNonce(a) || a == b {
		t.Errorf("NewNonce: %q %q", a, b)
	}
}

// A full debate: positions, one round with a challenge and a revision, a
// second round of passes (rule 1), the proposal and an accepting answer. Both
// sides agree on every turn, A reveals only after B's position, and the close
// ends B's mirror and request.
func TestDebateHappyPath(t *testing.T) {
	ctx := context.Background()
	a, b := newDNode(t, keyA), newDNode(t, keyB)
	reqID, sid := startDebate(t, a, b, 3)
	if v := view(t, a, sid); v.Phase != PhaseInvited || v.Role != RoleInitiator {
		t.Fatalf("A after start: %+v", v)
	}
	if v := view(t, b, reqID); v.Phase != PhaseInvited || v.Role != RoleRespondent || len(v.Transcript) != 0 {
		t.Fatalf("B after receipt: %+v", v)
	}
	// B's request carries only the commitment.
	var body string
	if err := b.db.QueryRow(`SELECT body FROM requests WHERE id = ?`, reqID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "A: capped backoff") || !strings.Contains(body, `"commitment"`) {
		t.Fatalf("B's request body: %s", body)
	}

	submit(t, b, sid, KindPosition, testPosition("B: fixed retry")) // one-step accept + position
	if st, _ := sessionState(t, b, sid); st != worksession.StateOpen {
		t.Fatalf("B session %s", st)
	}
	if a.ob.count(MailReveal) != 0 {
		t.Fatal("A revealed before applying B's position")
	}
	pass(t, b, a, request.KindAccept)
	if a.ob.count(MailReveal) != 0 {
		t.Fatal("A revealed on the accept alone")
	}
	pass(t, b, a, MailEntry)
	if a.ob.count(MailReveal) != 1 {
		t.Fatal("A did not reveal in the transaction that applied slot 1")
	}
	if v := view(t, b, sid); v.Waiting != "reveal" || v.Turn != "none" {
		t.Fatalf("B before the reveal: %+v", v)
	}
	pass(t, a, b, MailReveal)
	for _, n := range []*dnode{a, b} {
		v := view(t, n, sid)
		if v.Phase != PhaseRounds || v.NextSlot != 2 {
			t.Fatalf("%s after reveal: phase %s next %d", n.name(), v.Phase, v.NextSlot)
		}
	}
	if v := view(t, a, sid); v.Turn != "you" || v.Expect != KindMove {
		t.Fatalf("A's turn: %+v", v)
	}

	// Round 1: A challenges B's evidence; B challenges A's claim and revises.
	submit(t, a, sid, KindMove, challenge("evidence/0", "claim"))
	pass(t, a, b, MailEntry)
	rev := challenge("claim")
	rev["revision"] = testPosition("B: jittered retry")
	submit(t, b, sid, KindMove, rev)
	pass(t, b, a, MailEntry)
	// Round 2: both pass (rule 1).
	submit(t, a, sid, KindMove, pass0())
	pass(t, a, b, MailEntry)
	submit(t, b, sid, KindMove, pass0())
	pass(t, b, a, MailEntry)
	for _, n := range []*dnode{a, b} {
		if v := view(t, n, sid); v.Phase != PhaseConverge || v.NextSlot != 6 {
			t.Fatalf("%s converge: %s %d", n.name(), v.Phase, v.NextSlot)
		}
	}
	submit(t, a, sid, KindProposal, proposal())
	pass(t, a, b, MailEntry)
	submit(t, b, sid, KindAnswer, answer(true))
	pass(t, b, a, MailEntry)

	if p := phaseOf(t, a, sid); p != PhaseClosing {
		t.Fatalf("A phase %s, want closing", p)
	}
	if st, out := sessionState(t, a, sid); st != worksession.StateClosed || out != worksession.OutcomeAccepted {
		t.Fatalf("A session %s/%s", st, out)
	}
	if a.events.count(EventAgreed) != 1 {
		t.Error("A did not fire debate.agreed")
	}
	cl := a.ob.take(t, MailClose)
	if cl.body["outcome"] != OutcomeAgreed || cl.body["reason"] != ReasonAccepted || cl.body["entries"].(interface{ String() string }).String() != "8" {
		t.Fatalf("close body %v", cl.body)
	}
	mustDeliver(t, b, a.self, cl)
	v := view(t, b, sid)
	if v.Phase != PhaseClosed || v.Outcome != OutcomeAgreed || v.Reason != ReasonAccepted {
		t.Fatalf("B after close: %+v", v)
	}
	if st, out := sessionState(t, b, sid); st != worksession.StateClosed || out != worksession.OutcomeAccepted {
		t.Fatalf("B session %s/%s", st, out)
	}
	var state, note string
	if err := b.db.QueryRow(`SELECT state, note FROM requests WHERE direction = 'in' AND id = ?`, reqID).Scan(&state, &note); err != nil {
		t.Fatal(err)
	}
	if state != request.StateCompleted || note != "debate agreed" {
		t.Fatalf("B request %s %q", state, note)
	}
	if b.events.count(EventAgreed) != 1 {
		t.Error("B did not fire debate.agreed")
	}
	// Both transcripts hold the same bytes.
	va, vb := view(t, a, sid), view(t, b, sid)
	if len(va.Transcript) != 8 || len(vb.Transcript) != 8 {
		t.Fatalf("transcripts %d / %d", len(va.Transcript), len(vb.Transcript))
	}
	for i := range va.Transcript {
		ea, eb := va.Transcript[i], vb.Transcript[i]
		if ea.Slot != eb.Slot || ea.Author != eb.Author || ea.Kind != eb.Kind || ea.At != eb.At || string(ea.Entry) != string(eb.Entry) {
			t.Errorf("slot %d differs:\n A %+v\n B %+v", ea.Slot, ea, eb)
		}
	}
	for _, act := range []string{"debate.start", "debate.entry", "debate.entry_in", "debate.reveal", "debate.close"} {
		if !a.audit.has(act) {
			t.Errorf("A audit lacks %s", act)
		}
	}
	for _, act := range []string{"debate.entry", "debate.entry_in", "debate.reveal_in", "debate.close_in"} {
		if !b.audit.has(act) {
			t.Errorf("B audit lacks %s", act)
		}
	}
	for _, n := range []*dnode{a, b} {
		for _, secret := range []string{"capped backoff", "fixed retry", "This does not hold", "Use capped"} {
			if strings.Contains(n.audit.all(), secret) {
				t.Errorf("%s audit holds content %q", n.name(), secret)
			}
		}
	}
	_ = ctx
}
