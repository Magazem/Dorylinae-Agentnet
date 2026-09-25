package debate

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

var entryOf = map[string]func() map[string]any{
	KindPosition: func() map[string]any { return testPosition("Probe position") },
	KindMove:     func() map[string]any { return challenge("claim") },
	KindProposal: proposal,
	KindAnswer:   func() map[string]any { return answer(true) },
}

func entryRows(t *testing.T, n *dnode, sid string) int {
	t.Helper()
	var c int
	if err := n.db.QueryRow(`SELECT COUNT(*) FROM debate_entries WHERE session = ?`, sid).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

// probeIPC submits every kind on n except allowed and requires each to be
// refused with not_your_turn or bad_state, changing nothing.
func probeIPC(t *testing.T, n *dnode, sid, allowed, label string) {
	t.Helper()
	before, sent := entryRows(t, n, sid), n.ob.count(MailEntry)
	for _, kind := range []string{KindPosition, KindMove, KindProposal, KindAnswer} {
		if kind == allowed {
			continue
		}
		_, err := n.ds.Submit(context.Background(), sid, "", kind, entryOf[kind]())
		var nyt *NotYourTurnError
		var bse *BadStateError
		var rbs *request.BadStateError
		if !errors.As(err, &nyt) && !errors.As(err, &bse) && !errors.As(err, &rbs) {
			t.Errorf("%s: %s Submit %s: err = %v, want not_your_turn or bad_state", label, n.name(), kind, err)
		}
	}
	if entryRows(t, n, sid) != before || n.ob.count(MailEntry) != sent {
		t.Errorf("%s: a refused submit on %s changed the transcript or sent mail", label, n.name())
	}
}

// probeReceipt delivers a debate.entry from the peer for every slot 1-13 but
// skip, and for B only slots up to its next one (later ones are held, which
// TestEntriesOvertaking covers). Each must be ignored: no error, no new row,
// debate.ignored audited.
func probeReceipt(t *testing.T, n *dnode, from, sid, reqID string, skip int, label string) {
	t.Helper()
	next := view(t, n, sid).NextSlot
	for slot := 1; slot <= MaxSlot; slot++ {
		if slot == skip || (n.self == keyB && slot > next && slot >= 2) {
			continue
		}
		kind := KindMove
		if slot == 1 {
			kind = KindPosition
		}
		before := entryRows(t, n, sid)
		body := map[string]any{"at": wireTime(n.clock), "entry": entryOf[kind](), "kind": kind, "request": reqID, "session": sid, "slot": slot}
		if err := deliver(t, n, from, sentMail{kind: MailEntry, body: body}); err != nil {
			t.Errorf("%s: %s slot %d: err = %v, want ignored", label, n.name(), slot, err)
			continue
		}
		if entryRows(t, n, sid) != before {
			t.Errorf("%s: %s applied or held slot %d out of turn", label, n.name(), slot)
		}
	}
	if !n.audit.has("debate.ignored", `"kind":"debate.entry"`) {
		t.Errorf("%s: %s audited no debate.ignored", label, n.name())
	}
}

type turnStep struct {
	author string // "A" or "B"
	kind   string
	entry  map[string]any
}

// TestDebateTurns walks debates through every phase and, before each step,
// checks that only the listed next slot is accepted: at IPC every other kind
// and the other side are not_your_turn/bad_state, and on receipt every other
// slot and author is debate.ignored. After each step both sides compute the
// same next slot and phase (the converge rules depend only on the
// transcript).
func TestDebateTurns(t *testing.T) {
	cases := []struct {
		name     string
		rounds   int
		steps    []turnStep
		converge int // the proposal slot
	}{
		{"rule 1: two passes in round 1", 3, []turnStep{
			{"A", KindMove, pass0()}, {"B", KindMove, pass0()},
			{"A", KindProposal, proposal()}, {"B", KindAnswer, answer(false)},
		}, 4},
		{"rule 1: passes across rounds, A twice in a row", 3, []turnStep{
			{"A", KindMove, challenge("claim")}, {"B", KindMove, pass0()}, {"A", KindMove, pass0()},
			{"A", KindProposal, proposal()}, {"B", KindAnswer, answer(true)},
		}, 5},
		{"rule 2: rounds used up", 2, []turnStep{
			{"A", KindMove, challenge("claim")}, {"B", KindMove, challenge("assumptions/0")},
			{"A", KindMove, challenge("evidence/0")}, {"B", KindMove, challenge("argument")},
			{"A", KindProposal, proposal()}, {"B", KindAnswer, answer(true)},
		}, 6},
		{"rule 2: one round", 1, []turnStep{
			{"A", KindMove, pass0()}, {"B", KindMove, challenge("claim")},
			{"A", KindProposal, proposal()}, {"B", KindAnswer, answer(false)},
		}, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b := newDNode(t, keyA), newDNode(t, keyB)
			reqID, sid := startDebate(t, a, b, tc.rounds)

			// invited: nothing but B's position (the one-step accept).
			probeIPC(t, a, sid, "", "invited")
			if _, err := b.ds.Submit(context.Background(), sid, "", KindMove, challenge("claim")); err == nil {
				t.Fatal("invited: B's move accepted")
			}
			probeReceipt(t, a, keyB, sid, reqID, 1, "invited A") // slot 1 before the accept is M3: TestSlot1BeforeAccept
			// positions, B to write its position.
			if _, err := b.req.Accept(context.Background(), reqID, keyA); err != nil {
				t.Fatal(err)
			}
			pass(t, b, a, request.KindAccept)
			probeIPC(t, a, sid, "", "positions A")
			probeIPC(t, b, sid, KindPosition, "positions B")
			probeReceipt(t, a, keyB, sid, reqID, 1, "positions A")
			probeReceipt(t, b, keyA, sid, reqID, 0, "positions B")
			submit(t, b, sid, KindPosition, testPosition("B: fixed retry"))
			// B's position sent, the reveal outstanding: B has no turn.
			probeIPC(t, b, sid, "", "reveal outstanding B")
			pass(t, b, a, MailEntry)
			pass(t, a, b, MailReveal)
			same(t, a, b, sid, "after reveal")

			for i, st := range tc.steps {
				label := fmt.Sprintf("step %d (%s %s)", i, st.author, st.kind)
				author, other := a, b
				if st.author == "B" {
					author, other = b, a
				}
				next := view(t, author, sid).NextSlot
				if st.kind == KindProposal && next != tc.converge {
					t.Fatalf("%s: proposal slot %d, want %d", label, next, tc.converge)
				}
				probeIPC(t, author, sid, st.kind, label)
				probeIPC(t, other, sid, "", label)
				probeReceipt(t, a, keyB, sid, reqID, map[bool]int{true: next, false: 0}[st.author == "B"], label+" on A")
				probeReceipt(t, b, keyA, sid, reqID, map[bool]int{true: next, false: 0}[st.author == "A"], label+" on B")
				if st.kind == KindProposal || st.kind == KindAnswer {
					// §Turns: a move in the converge slot is bad_state at IPC.
					_, err := author.ds.Submit(context.Background(), sid, "", KindMove, pass0())
					var bse *BadStateError
					if !errors.As(err, &bse) {
						t.Errorf("%s: move in the converge slot: err = %v, want bad_state", label, err)
					}
				}
				submit(t, author, sid, st.kind, st.entry)
				if st.kind == KindAnswer {
					pass(t, b, a, MailEntry)
					break
				}
				pass(t, author, other, MailEntry)
				same(t, a, b, sid, label)
			}
			if p := phaseOf(t, a, sid); p != PhaseClosing {
				t.Fatalf("A phase %s, want closing", p)
			}
			// closing/closed: nothing is accepted any more.
			probeIPC(t, a, sid, "", "closing A")
			probeReceipt(t, a, keyB, sid, reqID, 0, "closing A")
			pass(t, a, b, MailClose)
			probeIPC(t, b, sid, "", "closed B")
			if p := phaseOf(t, b, sid); p != PhaseClosed {
				t.Fatalf("B phase %s, want closed", p)
			}
		})
	}
}

// same requires both sides to compute the same phase and next slot.
func same(t *testing.T, a, b *dnode, sid, label string) {
	t.Helper()
	va, vb := view(t, a, sid), view(t, b, sid)
	if va.Phase != vb.Phase || va.NextSlot != vb.NextSlot {
		t.Fatalf("%s: A %s/%d, B %s/%d", label, va.Phase, va.NextSlot, vb.Phase, vb.NextSlot)
	}
	if (va.Turn == "you") == (vb.Turn == "you") && va.Phase != PhaseClosing {
		t.Fatalf("%s: turns A %s B %s", label, va.Turn, vb.Turn)
	}
}

// §Turns: an entry of the wrong kind in the peer's slot (a move where the
// proposal or answer is expected) is bad_body on receipt.
func TestWrongKindInSlotIsBadBody(t *testing.T) {
	a, b, reqID, sid := openPositions(t, 1)
	submit(t, a, sid, KindMove, pass0())
	pass(t, a, b, MailEntry)
	submit(t, b, sid, KindMove, pass0())
	pass(t, b, a, MailEntry)
	// Converge at slot 4 (A's proposal): A sends a move there instead.
	body := map[string]any{"at": wireTime(b.clock), "entry": pass0(), "kind": KindMove, "request": reqID, "session": sid, "slot": 4}
	wantBadBody(t, deliver(t, b, keyA, sentMail{kind: MailEntry, body: body}))
	submit(t, a, sid, KindProposal, proposal())
	pass(t, a, b, MailEntry)
	body = map[string]any{"at": wireTime(a.clock), "entry": pass0(), "kind": KindMove, "request": reqID, "session": sid, "slot": 5}
	wantBadBody(t, deliver(t, a, keyB, sentMail{kind: MailEntry, body: body}))
}
