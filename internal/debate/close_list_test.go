package debate

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Review 55 R55-021 (C24-01): a modified A repeats one constraint id B holds in
// its close's "constraints" list. B refuses the close as a bad body (the list
// must be the sorted, unique ids A holds, at most 10), instead of deriving a
// Decision over MaxDecision and rolling the mail back unacked on every resend.
func TestCloseRepeatedConstraintListIsBadBody(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	toConverge(t, a, b, sid)
	pc := constrain(t, b, sid, strings.Repeat("\U0001D400", 500)) // 500 code points, 2000 bytes
	cl := closeTo(t, a, b, sid)
	list := make([]any, 400)
	for i := range list {
		list[i] = pc.ID
	}
	cl.body["constraints"] = list
	wantBadBody(t, deliver(t, b, a.self, cl))
	if ph := phaseOf(t, b, sid); ph == PhaseClosed || ph == PhaseBroken {
		t.Fatalf("B phase %s after a bad-body close", ph)
	}
}

// The close list is refused unless it is strictly ascending (sorted, no
// repeat) and holds at most MaxActiveConstraints ids; the valid list is
// accepted.
func TestCloseConstraintListRules(t *testing.T) {
	ids := make([]string, MaxActiveConstraints+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("c-%032x", i+1)
	}
	asAny := func(xs ...string) []any {
		out := make([]any, len(xs))
		for i, x := range xs {
			out[i] = x
		}
		return out
	}
	cases := []struct {
		name string
		list []any
	}{
		{"repeat", asAny(ids[0], ids[0])},
		{"unsorted", asAny(ids[1], ids[0])},
		{"too many", asAny(ids...)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b, _, sid := openPositions(t, 1)
			toConverge(t, a, b, sid)
			cl := closeTo(t, a, b, sid)
			cl.body["constraints"] = c.list
			wantBadBody(t, deliver(t, b, a.self, cl))
		})
	}
	// Control: A's own sorted list of two constraints is applied and signed.
	a, b, _, sid := openPositions(t, 1)
	toConverge(t, a, b, sid)
	p1 := constrain(t, a, sid, "Rule one")
	p2 := constrain(t, a, sid, "Rule two")
	pass(t, a, b, MailConstraint)
	cl := closeTo(t, a, b, sid)
	want := []string{p1.ID, p2.ID}
	sort.Strings(want)
	if got := closeList(cl); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("A's close lists %v, want %v", got, want)
	}
	mustDeliver(t, b, a.self, cl)
	if ph := phaseOf(t, b, sid); ph != PhaseClosed {
		t.Fatalf("B phase %s after a valid close, want closed", ph)
	}
}

// Review 55 R55-070 (C24-02) and review 70 M1: a constraint for a debate
// still invited is ignored with reason "state" on B (B has not accepted, so
// no A constraint can be legitimate) and on A once its out request is gone.
// On A with the request still pending, a B constraint that overtook B's
// accept and slot-1 entry is stored and stays active once the debate opens.
func TestConstraintWhileInvited(t *testing.T) {
	fake := func(reqID, sid string, i int) sentMail {
		return sentMail{kind: MailConstraint, body: map[string]any{
			"at": "2026-09-25T10:00:00Z", "id": fmt.Sprintf("c-%032x", i), "request": reqID, "session": sid, "text": fmt.Sprintf("Early rule %d", i),
		}}
	}
	wantIgnored := func(t *testing.T, n *dnode, sid string) {
		t.Helper()
		if c := n.count(sid); c != 0 {
			t.Fatalf("%s stored %d constraints while invited", n.name(), c)
		}
		if !n.audit.has("debate.ignored", `"reason":"state"`) {
			t.Fatalf("%s did not audit debate.ignored state", n.name())
		}
		if n.events.count(EventConstraint) != 0 {
			t.Fatalf("%s notified an ignored constraint", n.name())
		}
	}

	t.Run("B invited", func(t *testing.T) {
		a, b := newDNode(t, keyA), newDNode(t, keyB)
		reqID, sid := startDebate(t, a, b, 1)
		if ph := phaseOf(t, b, sid); ph != PhaseInvited {
			t.Fatalf("B phase %s, want invited", ph)
		}
		mustDeliver(t, b, a.self, fake(reqID, sid, 1))
		wantIgnored(t, b, sid)
	})

	t.Run("A invited, B constraint overtakes the accept", func(t *testing.T) {
		a, b := newDNode(t, keyA), newDNode(t, keyB)
		_, sid := startDebate(t, a, b, 1)
		submit(t, b, sid, KindPosition, testPosition("B: fixed retry"))
		pc := constrain(t, b, sid, "Keep the retry cap")
		pass(t, b, a, MailConstraint) // before the accept and the entry
		if ph := phaseOf(t, a, sid); ph != PhaseInvited {
			t.Fatalf("A phase %s, want invited", ph)
		}
		if got := constraintIDsIn(t, a, sid, ConstraintActive); len(got) != 1 || got[0] != pc.ID {
			t.Fatalf("A active constraints %v, want [%s]", got, pc.ID)
		}
		pass(t, b, a, "request.accept")
		pass(t, b, a, MailEntry)
		if got := visibleActive(t, a, sid); len(got) != 1 || got[0] != pc.ID {
			t.Fatalf("A shows %v after the accept, want [%s]", got, pc.ID)
		}
	})

	t.Run("A invited, request gone", func(t *testing.T) {
		a, b := newDNode(t, keyA), newDNode(t, keyB)
		reqID, sid := startDebate(t, a, b, 1)
		if _, err := a.db.Exec(`UPDATE requests SET state = 'declined' WHERE direction = 'out' AND id = ?`, reqID); err != nil {
			t.Fatal(err)
		}
		mustDeliver(t, a, b.self, fake(reqID, sid, 1))
		wantIgnored(t, a, sid)
	})
}
