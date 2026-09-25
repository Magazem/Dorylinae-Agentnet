package debate

import (
	"context"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

const secretA = "A: capped backoff" // the claim of A's committed position

// holdsSecret reports whether any row of B's content tables contains A's
// position: the request body, the transcript, the inbox copies and the
// debate row.
func holdsSecret(t *testing.T, n *dnode) bool {
	t.Helper()
	for _, q := range []string{
		`SELECT COUNT(*) FROM requests WHERE instr(body || COALESCE(result, ''), ?) > 0`,
		`SELECT COUNT(*) FROM debate_entries WHERE instr(entry, ?) > 0`,
		`SELECT COUNT(*) FROM mail_inbox WHERE instr(signed, ?) > 0`,
		`SELECT COUNT(*) FROM debates WHERE instr(COALESCE(close_body, '') || COALESCE(last_state, ''), ?) > 0`,
	} {
		var c int
		if err := n.db.QueryRow(q, secretA).Scan(&c); err != nil {
			t.Fatal(err)
		}
		if c > 0 {
			return true
		}
	}
	return false
}

// B never has A's position before its own position is committed to its
// outbox: B's tables are inspected at every step, and an early reveal from a
// misbehaving A is neither stored nor kept in the inbox.
func TestRespondentNeverSeesPositionEarly(t *testing.T) {
	ctx := context.Background()
	a, b := newDNode(t, keyA), newDNode(t, keyB)
	reqID, sid := startDebate(t, a, b, 2)
	if holdsSecret(t, b) {
		t.Fatal("B holds A's position after receiving the request")
	}
	if _, err := b.req.Accept(ctx, reqID, keyA); err != nil {
		t.Fatal(err)
	}
	pass(t, b, a, request.KindAccept)
	if holdsSecret(t, b) {
		t.Fatal("B holds A's position after its accept")
	}
	// A misbehaving A reveals before B's position exists: ignored, and the
	// inbox copy is blank.
	var nonce, canon string
	if err := a.db.QueryRow(`SELECT d.nonce, e.entry FROM debates d JOIN debate_entries e ON e.session = d.session AND e.slot = 0 WHERE d.session = ?`, sid).Scan(&nonce, &canon); err != nil {
		t.Fatal(err)
	}
	pos, err := agentcard.ParseStrict([]byte(canon))
	if err != nil {
		t.Fatal(err)
	}
	early := map[string]any{"at": wireTime(a.clock), "nonce": nonce, "position": pos, "request": reqID, "session": sid}
	mustDeliver(t, b, keyA, sentMail{kind: MailReveal, body: early})
	if holdsSecret(t, b) {
		t.Fatal("B stored an early reveal")
	}
	if p := phaseOf(t, b, sid); p != PhasePositions {
		t.Fatalf("B phase %s after an early reveal", p)
	}
	if !b.audit.has("debate.ignored", `"kind":"debate.reveal"`, `"reason":"turn"`) {
		t.Error("early reveal not audited as ignored")
	}
	submit(t, b, sid, KindPosition, testPosition("B: fixed retry"))
	if holdsSecret(t, b) {
		t.Fatal("B holds A's position right after sending its own")
	}
	var queued int
	if err := b.db.QueryRow(`SELECT COUNT(*) FROM outbox WHERE kind = ?`, MailEntry).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("B's position is not in its outbox (%d, %v)", queued, err)
	}
	pass(t, b, a, MailEntry)
	pass(t, a, b, MailReveal)
	if !holdsSecret(t, b) {
		t.Fatal("B lacks A's position after the reveal")
	}
	if !b.audit.has("debate.reveal_in", `"ok":true`) {
		t.Error("reveal_in not audited")
	}
}

// A reveal that does not match the commitment breaks the debate on B: no
// more entries, debate.reveal_bad, debate.broken, a ws.cancel to A, and in
// the same transaction B's mirror closes and B completes the request (review
// 43 L3). A later debate.close from A is stored and changes nothing.
func TestBadReveal(t *testing.T) {
	cases := map[string]func(t *testing.T, a, b *dnode, sid string, body map[string]any){
		"changed position": func(_ *testing.T, _, _ *dnode, _ string, body map[string]any) {
			body["position"].(map[string]any)["claim"] = "A: something else"
		},
		"changed nonce": func(_ *testing.T, _, _ *dnode, _ string, body map[string]any) {
			body["nonce"] = strings.Repeat("ab", 32)
		},
		"malformed nonce": func(_ *testing.T, _, _ *dnode, _ string, body map[string]any) {
			body["nonce"] = strings.ToUpper(body["nonce"].(string))
		},
		"short nonce": func(_ *testing.T, _, _ *dnode, _ string, body map[string]any) {
			body["nonce"] = body["nonce"].(string)[:62]
		},
		"nonce not a string": func(_ *testing.T, _, _ *dnode, _ string, body map[string]any) {
			body["nonce"] = 7
		},
		"position fails the schema": func(_ *testing.T, _, _ *dnode, _ string, body map[string]any) {
			body["position"].(map[string]any)["extra"] = "x"
		},
		"position with a newline in the claim": func(_ *testing.T, _, _ *dnode, _ string, body map[string]any) {
			body["position"].(map[string]any)["claim"] = "A: capped\nbackoff"
		},
		"position not an object": func(_ *testing.T, _, _ *dnode, _ string, body map[string]any) {
			body["position"] = "A: capped backoff"
		},
		"another session's commitment": func(t *testing.T, _, b *dnode, sid string, body map[string]any) {
			canon, _ := agentcard.CanonicalValue(body["position"])
			other := worksession.DeriveID(keyA, keyB, "r-00000000000000000000000000000001")
			setCommitment(t, b, sid, Commitment(other, keyA, body["nonce"].(string), canon))
		},
		"the commitment claimed by B": func(t *testing.T, _, b *dnode, sid string, body map[string]any) {
			canon, _ := agentcard.CanonicalValue(body["position"])
			setCommitment(t, b, sid, Commitment(sid, keyB, body["nonce"].(string), canon))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a, b := newDNode(t, keyA), newDNode(t, keyB)
			reqID, sid := startDebate(t, a, b, 2)
			submit(t, b, sid, KindPosition, testPosition("B: fixed retry"))
			pass(t, b, a, request.KindAccept)
			pass(t, b, a, MailEntry)
			rv := a.ob.take(t, MailReveal)
			mutate(t, a, b, sid, rv.body)
			mustDeliver(t, b, keyA, rv)

			if p := phaseOf(t, b, sid); p != PhaseBroken {
				t.Fatalf("B phase %s, want broken", p)
			}
			if !b.audit.has("debate.reveal_bad", sid) || !b.audit.has("debate.reveal_in", `"ok":false`) {
				t.Errorf("audit:\n%s", b.audit.all())
			}
			if b.events.count(EventBroken) != 1 {
				t.Error("debate.broken not notified")
			}
			if holdsSecret(t, b) && name == "changed position" {
				t.Error("B kept the bad reveal")
			}
			var n int
			_ = b.db.QueryRow(`SELECT COUNT(*) FROM debate_entries WHERE session = ? AND slot = 0`, sid).Scan(&n)
			if n != 0 {
				t.Error("B stored slot 0 from a bad reveal")
			}
			if st, out := sessionState(t, b, sid); st != worksession.StateClosed || out != worksession.OutcomeCancelled {
				t.Errorf("B session %s/%s", st, out)
			}
			var state, note string
			if err := b.db.QueryRow(`SELECT state, note FROM requests WHERE direction = 'in' AND id = ?`, reqID).Scan(&state, &note); err != nil {
				t.Fatal(err)
			}
			if state != request.StateCompleted || note != "session cancelled" {
				t.Errorf("B request %s %q", state, note)
			}
			// No more entries on B, in either direction.
			if _, err := b.ds.Submit(context.Background(), sid, "", KindMove, pass0()); err == nil {
				t.Error("a broken debate accepted an entry")
			}
			// A learns of it through ws.cancel: cancelled, no Decision.
			pass(t, b, a, worksession.KindCancel)
			pass(t, b, a, request.KindComplete)
			v := view(t, a, sid)
			if v.Phase != PhaseClosed || v.Outcome != OutcomeCancelled {
				t.Errorf("A after ws.cancel: %s %s", v.Phase, v.Outcome)
			}
			// A's close is stored for the record on B and changes nothing.
			pass(t, a, b, MailClose)
			var closeBody string
			if err := b.db.QueryRow(`SELECT COALESCE(close_body, '') FROM debates WHERE session = ?`, sid).Scan(&closeBody); err != nil {
				t.Fatal(err)
			}
			if p := phaseOf(t, b, sid); p != PhaseBroken || !strings.Contains(closeBody, `"cancelled"`) {
				t.Errorf("B after A's close: phase %s close_body %q", p, closeBody)
			}
		})
	}
}

func setCommitment(t *testing.T, n *dnode, sid, c string) {
	t.Helper()
	if _, err := n.db.Exec(`UPDATE debates SET commitment = ? WHERE session = ?`, c, sid); err != nil {
		t.Fatal(err)
	}
}

// Entries overtaking each other on B are held and applied in order: A's
// round-1 move before the reveal, and the proposal before A's second
// consecutive entry.
func TestEntriesOvertaking(t *testing.T) {
	a, b := newDNode(t, keyA), newDNode(t, keyB)
	_, sid := startDebate(t, a, b, 3)
	submit(t, b, sid, KindPosition, testPosition("B: fixed retry"))
	pass(t, b, a, request.KindAccept)
	pass(t, b, a, MailEntry)
	submit(t, a, sid, KindMove, challenge("claim"))
	// The move overtakes the reveal.
	mustDeliver(t, b, keyA, a.ob.take(t, MailEntry))
	if v := view(t, b, sid); v.Phase != PhasePositions || len(v.Transcript) != 1 {
		t.Fatalf("B applied an entry before the reveal: %+v", v)
	}
	var st string
	if err := b.db.QueryRow(`SELECT state FROM debate_entries WHERE session = ? AND slot = 2`, sid).Scan(&st); err != nil || st != stateEarly {
		t.Fatalf("slot 2 state %q (%v), want early", st, err)
	}
	pass(t, a, b, MailReveal)
	if v := view(t, b, sid); v.NextSlot != 3 || v.Turn != "you" || len(v.Transcript) != 3 {
		t.Fatalf("B after the reveal: %+v", v)
	}
	// B passes, A passes (rule 1 fires on A's pass), A proposes: the
	// proposal overtakes A's pass.
	submit(t, b, sid, KindMove, pass0())
	pass(t, b, a, MailEntry)
	submit(t, a, sid, KindMove, pass0())
	submit(t, a, sid, KindProposal, proposal())
	move := a.ob.take(t, MailEntry)
	mustDeliver(t, b, keyA, a.ob.take(t, MailEntry)) // the proposal first
	if v := view(t, b, sid); v.NextSlot != 4 {
		t.Fatalf("B next %d while the pass is missing", v.NextSlot)
	}
	mustDeliver(t, b, keyA, move)
	if v := view(t, b, sid); v.NextSlot != 6 || v.Expect != KindAnswer || v.Phase != PhaseConverge {
		t.Fatalf("B after both: %+v", v)
	}
	// A duplicate slot is ignored.
	dup := map[string]any{"at": wireTime(a.clock), "entry": pass0(), "kind": KindMove, "request": view(t, a, sid).RequestID, "session": sid, "slot": 4}
	mustDeliver(t, b, keyA, sentMail{kind: MailEntry, body: dup})
	if !b.audit.has("debate.ignored", `"reason":"duplicate"`) {
		t.Error("duplicate not ignored")
	}
}

// Every body's session must be the derived id for the pair and request: a
// body moved to another session (or another request) is bad_body.
func TestBodyMovedToAnotherSession(t *testing.T) {
	a, b, reqID, sid := openPositions(t, 2)
	submit(t, a, sid, KindMove, pass0())
	m := a.ob.take(t, MailEntry)
	moved := func(k, v string) sentMail {
		body := map[string]any{}
		for key, val := range m.body {
			body[key] = val
		}
		body[k] = v
		return sentMail{kind: MailEntry, body: body}
	}
	other := "r-00000000000000000000000000000002"
	wantBadBody(t, deliver(t, b, keyA, moved("session", worksession.DeriveID(keyA, keyB, other))))
	wantBadBody(t, deliver(t, b, keyA, moved("request", other)))
	// A reversed pair (the body claims B is the initiator) names a debate B
	// does not have: ignored.
	mustDeliver(t, b, keyA, moved("session", worksession.DeriveID(keyB, keyA, reqID)))
	if !b.audit.has("debate.ignored", `"reason":"unknown"`) {
		t.Error("reversed-pair body not ignored")
	}
	// From a third key.
	wantBadBody(t, deliver(t, b, seedKey(0x40), m))
	mustDeliver(t, b, keyA, m)
	if v := view(t, b, sid); v.NextSlot != 3 {
		t.Fatalf("the genuine body did not apply: %+v", v)
	}
}
