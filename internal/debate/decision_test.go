package debate

import (
	"bytes"
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

// Ticket 3.3a (Docs/review/42-phase3-tickets.md §3.3a): the Decision's
// derivation, the signing exchange (debate.close / debate.sign), refusal
// and silence, at the store level. The e2e run over a relay is in
// internal/daemon.

func record(t *testing.T, n *dnode, sid string) DecisionRecord {
	t.Helper()
	d, err := n.ds.Decision(context.Background(), sid)
	if err != nil {
		t.Fatalf("%s Decision: %v", n.name(), err)
	}
	return d
}

func noRecord(t *testing.T, n *dnode, sid string) {
	t.Helper()
	if _, err := n.ds.Decision(context.Background(), sid); !errors.Is(err, ErrUnknownDecision) {
		t.Fatalf("%s has a Decision (%v)", n.name(), err)
	}
}

// verified checks d as a signed file with decision.Verify.
func verified(t *testing.T, d DecisionRecord) decision.Result {
	t.Helper()
	sigs := map[string]any{"initiator": d.SigInitiator}
	if d.SigRespondent != "" {
		sigs["respondent"] = d.SigRespondent
	}
	file, err := json.Marshal(map[string]any{"decision": json.RawMessage(d.Decision), "hash": d.Hash, "signatures": sigs})
	if err != nil {
		t.Fatal(err)
	}
	return decision.Verify(file, DecisionSchema())
}

// bothSigned checks the 3.3 acceptance: the same Decision bytes on both
// sides, both signatures stored on both sides, and a complete verification.
func bothSigned(t *testing.T, a, b *dnode, sid, outcome, reason string) DecisionRecord {
	t.Helper()
	da, db := record(t, a, sid), record(t, b, sid)
	if !bytes.Equal(da.Decision, db.Decision) || da.Hash != db.Hash {
		t.Fatalf("Decisions differ:\nA %s\nB %s", da.Decision, db.Decision)
	}
	for _, d := range []DecisionRecord{da, db} {
		if d.State != DecisionSigned || d.SigInitiator == "" || d.SigRespondent == "" || d.PeerHash != "" {
			t.Fatalf("%s: state %s sigs %q %q", d.Role, d.State, d.SigInitiator, d.SigRespondent)
		}
	}
	if da.SigInitiator != db.SigInitiator || da.SigRespondent != db.SigRespondent {
		t.Fatal("signatures differ between the sides")
	}
	res := verified(t, da)
	if !res.Valid || !res.Complete {
		t.Fatalf("verify: %+v", res)
	}
	if o, err := decisionOutcome(da.Decision); err != nil || o != outcome || !strings.Contains(string(da.Decision), `"reason":"`+reason+`"`) {
		t.Fatalf("outcome %s (%v), want %s/%s", o, err, outcome, reason)
	}
	for _, n := range []*dnode{a, b} {
		if v := view(t, n, sid); v.Phase != PhaseClosed || v.Outcome != outcome || v.Reason != reason {
			t.Fatalf("%s view %s %s/%s", n.name(), v.Phase, v.Outcome, v.Reason)
		}
		if !n.audit.has("decision.create", da.Hash) {
			t.Fatalf("%s: decision.create not audited:\n%s", n.name(), n.audit.all())
		}
	}
	if !a.audit.has("decision.sign_in", da.ID) {
		t.Fatal("A did not audit decision.sign_in")
	}
	return da
}

// noContentInAudit checks that no audit row carries the debate's text.
func noContentInAudit(t *testing.T, ns ...*dnode) {
	t.Helper()
	for _, n := range ns {
		all := n.audit.all()
		for _, marker := range []string{"capped backoff", "fixed retry", "outbox retry", "Backoff", "See the outbox", "Load is steady"} {
			if strings.Contains(all, marker) {
				t.Fatalf("%s audit holds %q:\n%s", n.name(), marker, all)
			}
		}
	}
}

// 3.3: a debate ends with the same Decision bytes on both sides and both
// signatures stored on both sides. B's clock is skewed by hours and nothing
// local enters the Decision. B offline: A's Decision is awaiting_peer until
// the close reaches B and B's signature comes back.
func TestDecisionAgreed(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	b.advance(5*time.Hour + 17*time.Second) // skewed clocks
	pc := constrain(t, b, sid, "Keep the wire format")
	pass(t, b, a, MailConstraint)
	toConverge(t, a, b, sid)
	cl := closeTo(t, a, b, sid)
	if cl.body["decision"] == nil || cl.body["sig"] == nil {
		t.Fatalf("close without decision and sig: %v", cl.body)
	}
	// Silence: B has not signed yet.
	da := record(t, a, sid)
	if da.State != DecisionAwaitingPeer || da.SigRespondent != "" || da.Hash != cl.body["decision"] || da.SigInitiator != cl.body["sig"] {
		t.Fatalf("A before B signs: %+v", da)
	}
	if res := verified(t, da); !res.Valid || res.Complete {
		t.Fatalf("single-signed Decision must verify unconfirmed: %+v", res)
	}
	if v := view(t, a, sid); v.Phase != PhaseClosing || v.Waiting != "signature" {
		t.Fatalf("A view %+v", v)
	}
	a.advance(48 * time.Hour) // B offline for two days
	mustDeliver(t, b, a.self, cl)
	if b.ob.count(MailSign) != 1 {
		t.Fatal("B did not send debate.sign")
	}
	pass(t, b, a, MailSign)
	d := bothSigned(t, a, b, sid, OutcomeAgreed, ReasonAccepted)
	if !strings.Contains(string(d.Decision), pc.ID) || !strings.Contains(string(d.Decision), `"by":"respondent"`) {
		t.Fatalf("human decision missing: %s", d.Decision)
	}
	if a.events.count(EventAgreed) != 1 || b.events.count(EventAgreed) != 1 {
		t.Fatal("debate.agreed fires once on each side")
	}
	if st, o := sessionState(t, b, sid); st != worksession.StateClosed || o != worksession.OutcomeAccepted {
		t.Fatalf("B session %s/%s", st, o)
	}
	noContentInAudit(t, a, b)
	// A duplicate sign changes nothing.
	mustDeliver(t, a, b.self, sentMail{kind: MailSign, body: map[string]any{
		"at": wireTime(b.clock), "decision": d.Hash, "request": view(t, a, sid).RequestID, "session": sid, "sig": d.SigRespondent,
	}})
	if !a.audit.has("debate.ignored", MailSign, `"reason":"closed"`) {
		t.Fatal("duplicate debate.sign not ignored")
	}
}

// 3.5: a forced disagreement (accept: false) ends escalated, and the Decision
// is produced and signed by both.
func TestDecisionEscalatedSigned(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	toConverge(t, a, b, sid)
	submit(t, b, sid, KindAnswer, map[string]any{"accept": false, "remaining_disagreement": []any{
		map[string]any{"point": "Jitter", "initiator": "Not needed", "respondent": "Needed"},
	}})
	pass(t, b, a, MailEntry)
	pass(t, a, b, MailClose)
	pass(t, b, a, MailSign)
	d := bothSigned(t, a, b, sid, OutcomeEscalated, ReasonRejected)
	if !strings.Contains(string(d.Decision), `"remaining_disagreement":[{"initiator":"Not needed"`) ||
		strings.Contains(string(d.Decision), "final_agreement") {
		t.Fatalf("escalated Decision: %s", d.Decision)
	}
	if a.events.count(EventEscalated) != 1 || b.events.count(EventEscalated) != 1 {
		t.Fatal("debate.escalated fires once on each side")
	}
}

// A timeout after both positions ends escalated/timeout, signed by both. The
// close counts A's move, which B has not received yet: the close overtook
// it, so B holds the close until the move arrives.
func TestDecisionTimeoutHeldClose(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	submit(t, a, sid, KindMove, challenge("assumptions/0"))
	// B never moves; A's sweep closes escalated/timeout with 3 entries.
	a.advance(2 * time.Hour)
	if n, err := a.ds.Sweep(context.Background()); err != nil || n != 1 {
		t.Fatalf("Sweep: %d %v", n, err)
	}
	cl := a.ob.take(t, MailClose)
	if cl.body["entries"] != json.Number("3") || cl.body["reason"] != ReasonTimeout {
		t.Fatalf("close %v", cl.body)
	}
	// The close overtakes A's move: B holds it.
	mustDeliver(t, b, a.self, cl)
	if p := phaseOf(t, b, sid); p != PhaseRounds || b.ob.count(MailSign) != 0 {
		t.Fatalf("B phase %s, signs %d: the close was not held", p, b.ob.count(MailSign))
	}
	pass(t, a, b, MailEntry)
	pass(t, b, a, MailSign)
	bothSigned(t, a, b, sid, OutcomeEscalated, ReasonTimeout)
}

// A close that overtakes the reveal is held until slot 0 arrives; B's own
// entry A never applied (sent after A's timeout) is late: not in the
// Decision, shown late, audited.
func TestDecisionCloseOvertakesReveal(t *testing.T) {
	a, b := newDNode(t, keyA), newDNode(t, keyB)
	_, sid := startDebate(t, a, b, 1)
	submit(t, b, sid, KindPosition, testPosition("B: fixed retry"))
	pass(t, b, a, request.KindAccept)
	pass(t, b, a, MailEntry)
	reveal := a.ob.take(t, MailReveal)
	a.advance(2 * time.Hour)
	if n, err := a.ds.Sweep(context.Background()); err != nil || n != 1 {
		t.Fatalf("Sweep: %d %v", n, err)
	}
	cl := a.ob.take(t, MailClose)
	mustDeliver(t, b, a.self, cl)
	if p := phaseOf(t, b, sid); p != PhasePositions {
		t.Fatalf("B phase %s: close not held", p)
	}
	mustDeliver(t, b, a.self, reveal)
	pass(t, b, a, MailSign)
	d := bothSigned(t, a, b, sid, OutcomeEscalated, ReasonTimeout)
	if strings.Contains(string(d.Decision), `"rounds"`) {
		t.Fatalf("Decision has rounds: %s", d.Decision)
	}
}

// B's move sent after A's timeout (A never applied it) is late on B.
func TestDecisionLateOwnEntry(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	submit(t, a, sid, KindMove, pass0())
	pass(t, a, b, MailEntry)
	a.advance(2 * time.Hour)
	if _, err := a.ds.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	submit(t, b, sid, KindMove, pass0()) // too late: A already closed
	pass(t, a, b, MailClose)
	pass(t, b, a, MailSign)
	d := bothSigned(t, a, b, sid, OutcomeEscalated, ReasonTimeout)
	if strings.Contains(string(d.Decision), `"respondent":{"challenges"`) {
		t.Fatalf("B's late move is in the Decision: %s", d.Decision)
	}
	v := view(t, b, sid)
	if last := v.Transcript[len(v.Transcript)-1]; last.Slot != 3 || last.State != stateLate {
		t.Fatalf("B's late entry %+v", last)
	}
	if !b.audit.has("debate.ignored", `"reason":"late"`) {
		t.Fatal("late entry not audited")
	}
}

// A cancelled debate has no Decision.
func TestDecisionNoneWhenCancelled(t *testing.T) {
	a, b := newDNode(t, keyA), newDNode(t, keyB)
	reqID, sid := startDebate(t, a, b, 1)
	if _, err := b.req.Accept(context.Background(), reqID, keyA); err != nil {
		t.Fatal(err)
	}
	pass(t, b, a, request.KindAccept)
	a.advance(2 * time.Hour)
	if _, err := a.ds.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	cl := a.ob.take(t, MailClose)
	if _, ok := cl.body["decision"]; ok {
		t.Fatal("a cancelled close carries a decision")
	}
	mustDeliver(t, b, a.self, cl)
	noRecord(t, a, sid)
	noRecord(t, b, sid)
	if v := view(t, b, sid); v.Phase != PhaseClosed || v.Outcome != OutcomeCancelled {
		t.Fatalf("B %+v", v)
	}
}

// B refuses (peer_refused on both) a close that claims agreement without B's
// accept, carries a wrong hash or a bad signature, cuts an A entry B holds,
// claims a timeout although B's answer was applied on A, or counts a B slot
// B never sent (refused at once, not held; review 43 H2).
func TestDecisionRefusals(t *testing.T) {
	agreedClose := func(t *testing.T) (a, b *dnode, sid string, cl sentMail) {
		a, b, _, sid = openPositions(t, 1)
		toConverge(t, a, b, sid)
		return a, b, sid, closeTo(t, a, b, sid)
	}
	movedClose := func(t *testing.T) (a, b *dnode, sid string, cl sentMail) {
		a, b, _, sid = openPositions(t, 1)
		submit(t, a, sid, KindMove, pass0())
		pass(t, a, b, MailEntry)
		a.advance(2 * time.Hour)
		if _, err := a.ds.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
		return a, b, sid, a.ob.take(t, MailClose)
	}
	cases := []struct {
		name   string
		setup  func(t *testing.T) (a, b *dnode, sid string, cl sentMail)
		forge  func(a *dnode, cl sentMail)
		reason string
	}{
		{"agreed without B's accept", func(t *testing.T) (*dnode, *dnode, string, sentMail) {
			a, b, _, sid := openPositions(t, 1)
			toConverge(t, a, b, sid)
			submit(t, b, sid, KindAnswer, answer(false))
			pass(t, b, a, MailEntry)
			return a, b, sid, a.ob.take(t, MailClose)
		}, func(_ *dnode, cl sentMail) { cl.body["outcome"], cl.body["reason"] = OutcomeAgreed, ReasonAccepted }, "outcome"},
		{"wrong hash", agreedClose, func(_ *dnode, cl sentMail) { cl.body["decision"] = strings.Repeat("0", 64) }, "hash"},
		{"bad signature", agreedClose, func(_ *dnode, cl sentMail) {
			sig := []byte(cl.body["sig"].(string))
			sig[3] ^= 1
			cl.body["sig"] = string(sig)
		}, "signature"},
		{"signature by another key", agreedClose, func(_ *dnode, cl sentMail) {
			priv, _ := seedPriv(keyB)()
			cl.body["sig"] = decision.Sign(priv, []byte("{}"))
		}, "signature"},
		{"cuts an A entry B holds", movedClose, func(_ *dnode, cl sentMail) { cl.body["entries"] = json.Number("2") }, "cut"},
		{"timeout although B's answer was applied", agreedClose, func(_ *dnode, cl sentMail) {
			cl.body["outcome"], cl.body["reason"] = OutcomeEscalated, ReasonTimeout
		}, "outcome"},
		{"counts a B slot B never sent", movedClose, func(_ *dnode, cl sentMail) { cl.body["entries"] = json.Number("4") }, "unsent"},
		{"fewer than two entries", movedClose, func(_ *dnode, cl sentMail) { cl.body["entries"] = json.Number("1") }, "entries"},
		// Review 47 M1: a Decision closed before it opened fails verify
		// step 5, so B never signs one.
		{"closed before opened", agreedClose, func(_ *dnode, cl sentMail) { cl.body["at"] = "2000-01-01T00:00:00Z" }, "time"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b, sid, cl := c.setup(t)
			c.forge(a, cl)
			mustDeliver(t, b, a.self, cl)
			if p := phaseOf(t, b, sid); p != PhaseBroken {
				t.Fatalf("B phase %s, want broken (refused at once)", p)
			}
			if !b.audit.has("decision.refuse", `"reason":"`+c.reason+`"`) || b.events.count(EventBroken) != 1 {
				t.Fatalf("B audit:\n%s", b.audit.all())
			}
			if st, o := sessionState(t, b, sid); st != worksession.StateClosed || o != worksession.OutcomeCancelled {
				t.Fatalf("B session %s/%s", st, o)
			}
			var db DecisionRecord
			if c.reason == "entries" || c.reason == "time" {
				// One entry holds one position (or the close is before the
				// request): B has no Decision of its own, and refuses with
				// the hash of the empty message.
				noRecord(t, b, sid)
				db.Hash = decision.Hash(nil)
			} else {
				db = record(t, b, sid)
				if db.State != DecisionPeerRefused || db.SigRespondent != "" || db.PeerHash != cl.body["decision"] {
					t.Fatalf("B record %+v", db)
				}
			}
			sm := b.ob.take(t, MailSign)
			if sm.body["refused"] != refusedMismatch || sm.body["decision"] != db.Hash {
				t.Fatalf("debate.sign %v", sm.body)
			}
			mustDeliver(t, a, b.self, sm)
			da := record(t, a, sid)
			if da.State != DecisionPeerRefused || da.PeerHash != db.Hash || da.SigRespondent != "" {
				t.Fatalf("A record %+v", da)
			}
			if !a.audit.has("decision.refuse", `"reason":"mismatch"`) || a.events.count(EventBroken) != 1 {
				t.Fatalf("A audit:\n%s", a.audit.all())
			}
			if phaseOf(t, a, sid) != PhaseClosed {
				t.Fatal("A left in closing")
			}
		})
	}
}

// A debate.sign with a wrong hash or a bad signature makes A peer_refused.
func TestDecisionBadSignOnA(t *testing.T) {
	for _, c := range []struct {
		name, reason string
		forge        func(sm sentMail)
	}{
		{"wrong hash", "hash", func(sm sentMail) { sm.body["decision"] = strings.Repeat("1", 64) }},
		{"bad signature", "signature", func(sm sentMail) {
			sig := []byte(sm.body["sig"].(string))
			sig[5] ^= 1
			sm.body["sig"] = string(sig)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b, _, sid := openPositions(t, 1)
			toConverge(t, a, b, sid)
			mustDeliver(t, b, a.self, closeTo(t, a, b, sid))
			sm := b.ob.take(t, MailSign)
			c.forge(sm)
			mustDeliver(t, a, b.self, sm)
			da := record(t, a, sid)
			if da.State != DecisionPeerRefused || da.PeerHash != sm.body["decision"] || da.SigRespondent != "" {
				t.Fatalf("A record %+v", da)
			}
			if !a.audit.has("decision.refuse", `"reason":"`+c.reason+`"`) {
				t.Fatalf("A audit:\n%s", a.audit.all())
			}
		})
	}
}

// debate.sign bodies are checked strictly.
func TestSignBodyChecks(t *testing.T) {
	a, _, reqID, sid := openPositions(t, 1)
	base := func() map[string]any {
		return map[string]any{"at": wireTime(a.clock), "decision": strings.Repeat("a", 64), "request": reqID, "session": sid, "sig": "x"}
	}
	for name, mut := range map[string]func(m map[string]any){
		"sig and refused": func(m map[string]any) { m["refused"] = refusedMismatch },
		"neither":         func(m map[string]any) { delete(m, "sig") },
		"other refusal":   func(m map[string]any) { delete(m, "sig"); m["refused"] = "no" },
		"uppercase hash":  func(m map[string]any) { m["decision"] = strings.Repeat("A", 64) },
		"unknown member":  func(m map[string]any) { m["note"] = "x" },
		"empty signature": func(m map[string]any) { m["sig"] = "" },
		"session moved":   func(m map[string]any) { m["session"] = "s-00000000000000000000000000000000" },
	} {
		body := base()
		mut(body)
		if err := deliver(t, a, keyB, sentMail{kind: MailSign, body: body}); err == nil {
			t.Errorf("%s: accepted", name)
		} else {
			wantBadBody(t, err)
		}
	}
	// A valid body while A is not closing is ignored.
	mustDeliver(t, a, keyB, sentMail{kind: MailSign, body: base()})
	if !a.audit.has("debate.ignored", MailSign) {
		t.Fatal("sign while open not ignored")
	}
}

// Review 43 H2: a close that overtakes an A-authored constraint is held and
// applied when the constraint arrives, and both sides sign with it listed.
func TestDecisionHeldForConstraint(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	toConverge(t, a, b, sid)
	pc := constrain(t, a, sid, "Keep the wire format")
	mustDeliver(t, b, a.self, closeTo(t, a, b, sid))
	if b.ob.count(MailSign) != 0 || phaseOf(t, b, sid) == PhaseClosed {
		t.Fatal("B signed before it held the listed constraint")
	}
	pass(t, a, b, MailConstraint)
	pass(t, b, a, MailSign)
	d := bothSigned(t, a, b, sid, OutcomeAgreed, ReasonAccepted)
	if !strings.Contains(string(d.Decision), `"human_decisions":[{"at":`) || !strings.Contains(string(d.Decision), pc.ID) {
		t.Fatalf("constraint missing: %s", d.Decision)
	}
	if strings.Contains(a.audit.all()+b.audit.all(), "wire format") {
		t.Fatal("constraint text in audit")
	}
}

// Review 47 M1: A's clock went back after the request was created. The
// close carries the request's created as its at, so B signs and the
// Decision verifies (opened <= closed).
func TestDecisionClockWentBack(t *testing.T) {
	a, b, reqID, sid := openPositions(t, 1)
	var created string
	if err := a.db.QueryRow(`SELECT created FROM requests WHERE direction = 'out' AND id = ?`, reqID).Scan(&created); err != nil {
		t.Fatal(err)
	}
	toConverge(t, a, b, sid)
	a.advance(-72 * time.Hour)
	cl := closeTo(t, a, b, sid)
	if cl.body["at"] != created {
		t.Fatalf("close at %v, want the request's created %s", cl.body["at"], created)
	}
	mustDeliver(t, b, a.self, cl)
	pass(t, b, a, MailSign)
	d := bothSigned(t, a, b, sid, OutcomeAgreed, ReasonAccepted)
	if !strings.Contains(string(d.Decision), `"closed":"`+created+`"`) {
		t.Fatalf("closed is not the request's created: %s", d.Decision)
	}
}

// Review 47 L3: a stored identity key that does not match the card signs
// nothing: the close fails instead of carrying a signature B would refuse.
func TestDecisionKeyMismatchSignsNothing(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	a.ds.Priv = seedPriv(keyB)
	toConverge(t, a, b, sid)
	submit(t, b, sid, KindAnswer, answer(true))
	if err := deliver(t, a, b.self, b.ob.take(t, MailEntry)); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("close with a mismatched key: %v", err)
	}
	noRecord(t, a, sid)
}
