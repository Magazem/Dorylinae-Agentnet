package debate

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// Ticket 3.4 (Docs/review/42-phase3-tickets.md §3.4): human constraints at
// the store level. The approval gate itself is tested through a daemon in
// internal/daemon (debate_constrain_e2e_test.go); here AddConstraintTx is
// what the approval's Perform runs.

// constrain adds text as n's constraint, as an approved debate_constrain
// does: Prepare, then the precondition and Perform in one transaction.
func constrain(t *testing.T, n *dnode, sid, text string) PendingConstraint {
	t.Helper()
	pc, err := n.ds.PrepareConstraint(context.Background(), sid, text)
	if err != nil {
		t.Fatalf("%s PrepareConstraint: %v", n.name(), err)
	}
	if err := confirmConstraint(n, pc); err != nil {
		t.Fatalf("%s confirm: %v", n.name(), err)
	}
	return pc
}

func confirmConstraint(n *dnode, pc PendingConstraint) error {
	ctx := context.Background()
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := n.ds.ConstraintPreconditionTx(ctx, tx, pc); err != nil {
		return err
	}
	if err := n.ds.AddConstraintTx(ctx, tx, pc, "a-00000000000000000000000000000001"); err != nil {
		return err
	}
	return tx.Commit()
}

// activeIDs is the sorted ids of n's constraints in state.
func constraintIDsIn(t *testing.T, n *dnode, sid, state string) []string {
	t.Helper()
	rows, err := n.db.Query(`SELECT id FROM debate_constraints WHERE session = ? AND state = ? ORDER BY id`, sid, state)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// toConverge plays one round of passes: the debate is in converge on both
// sides with A to propose.
func toConverge(t *testing.T, a, b *dnode, sid string) {
	t.Helper()
	submit(t, a, sid, KindMove, pass0())
	pass(t, a, b, MailEntry)
	submit(t, b, sid, KindMove, pass0())
	pass(t, b, a, MailEntry)
	submit(t, a, sid, KindProposal, proposal())
	pass(t, a, b, MailEntry)
}

// A constraint approved on either side is stored active on both with the
// same id, author, at and text, and shows in both debate_show views; the
// receiver audits debate.constraint_in and notifies debate.constraint; no
// audit row holds the text.
func TestConstraintBothSides(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	pcA := constrain(t, a, sid, "Must stay compatible with Go 1.22")
	b.advance(time.Second) // B's constraint is the later one
	pcB := constrain(t, b, sid, "No new dependency — «ever»")
	if a.ob.count(MailConstraint) != 1 || b.ob.count(MailConstraint) != 1 {
		t.Fatal("each side should send one debate.constraint")
	}
	pass(t, a, b, MailConstraint)
	pass(t, b, a, MailConstraint)
	va, vb := view(t, a, sid), view(t, b, sid)
	if len(va.Constraints) != 2 || fmt.Sprint(va.Constraints) != fmt.Sprint(vb.Constraints) {
		t.Fatalf("views differ:\nA %+v\nB %+v", va.Constraints, vb.Constraints)
	}
	if c := va.Constraints[0]; c.ID != pcA.ID || c.Author != RoleInitiator || c.State != ConstraintActive || c.Text != pcA.Text {
		t.Fatalf("first constraint %+v", c)
	}
	if c := va.Constraints[1]; c.ID != pcB.ID || c.Author != RoleRespondent {
		t.Fatalf("second constraint %+v", c)
	}
	if a.events.count(EventConstraint) != 1 || b.events.count(EventConstraint) != 1 {
		t.Fatal("each receiver notifies debate.constraint once")
	}
	if !a.audit.has("debate.constraint_in", pcB.ID) || !b.audit.has("debate.constraint_in", pcA.ID) {
		t.Fatalf("constraint_in not audited:\n%s\n%s", a.audit.all(), b.audit.all())
	}
	for _, n := range []*dnode{a, b} {
		if s := n.audit.all(); strings.Contains(s, "Go 1.22") || strings.Contains(s, "dependency") {
			t.Fatalf("%s audit holds constraint text:\n%s", n.name(), s)
		}
	}
	// A duplicate delivery changes nothing.
	body := map[string]any{"at": va.Constraints[0].At, "id": pcA.ID, "request": va.RequestID, "session": sid, "text": pcA.Text}
	mustDeliver(t, b, a.self, sentMail{to: b.self, kind: MailConstraint, body: body})
	if len(view(t, b, sid).Constraints) != 2 || !b.audit.has("debate.ignored", `"reason":"duplicate"`) {
		t.Fatal("duplicate constraint not ignored")
	}
}

// Review 43 H3: invisible characters are bad_request at debate_constrain
// (FieldError text) and bad_body on receipt.
func TestConstraintVisibleOnly(t *testing.T) {
	a, b, reqID, sid := openPositions(t, 1)
	for name, text := range map[string]string{
		"zero-width space": "No new" + string(rune(0x200b)) + "dependency",
		"bidi override":    "No new " + string(rune(0x202e)) + "dependency",
		"BOM":              string(rune(0xfeff)) + "No new dependency",
		"tag character":    "No new dependency\U000E0041",
		// Graphic but invisible (review 46 H1).
		"variation selector":     "No new dependency" + string(rune(0xfe0f)),
		"VS supplement (a byte)": "No new dependency\U000E0141\U000E0142",
		"CGJ":                    "No new" + string(rune(0x034f)) + "dependency",
		"Hangul filler":          "No new " + string(rune(0x3164)) + " dependency",
		"no-break space":         "No new" + string(rune(0x00a0)) + "dependency",
		"em space":               "No new" + string(rune(0x2003)) + "dependency",
		"blank Braille":          "No new dependency " + string(rune(0x2800)),
		"newline":                "No new\ndependency",
		"C1":                     "No new\u009bdependency",
		"U+2028":                 "No new" + string(rune(0x2028)) + "dependency",
		"empty":                  "",
		"leading space":          " No new dependency",
		"501 code points":        strings.Repeat("x", 501),
	} {
		_, err := a.ds.PrepareConstraint(context.Background(), sid, text)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != "text" {
			t.Errorf("%s: Prepare err = %v, want a FieldError on text", name, err)
		}
		body := map[string]any{"at": "2026-09-25T10:00:00Z", "id": "c-0123456789abcdef0123456789abcdef", "request": reqID, "session": sid, "text": text}
		err = deliver(t, b, a.self, sentMail{to: b.self, kind: MailConstraint, body: body})
		if err == nil {
			t.Errorf("%s: receipt accepted", name)
			continue
		}
		wantBadBody(t, err)
	}
	if n := len(constraintIDsIn(t, b, sid, ConstraintActive)); n != 0 {
		t.Fatalf("B stored %d constraints", n)
	}
	// 500 visible code points, including non-ASCII, pass.
	constrain(t, a, sid, strings.Repeat("é", 500))
	// Combining marks and non-Latin scripts stay allowed.
	constrain(t, a, sid, "Garder le résultat, 日本語 ok")
}

// A constraint is added only in positions, rounds or converge (bad_state);
// one confirmed after the debate left converge fails its precondition.
func TestConstraintPhases(t *testing.T) {
	a, b := newDNode(t, keyA), newDNode(t, keyB)
	_, sid := startDebate(t, a, b, 1)
	var bse *BadStateError
	if _, err := a.ds.PrepareConstraint(context.Background(), sid, "Too early"); !errors.As(err, &bse) {
		t.Fatalf("invited: %v, want bad_state", err)
	}
	if _, err := a.ds.PrepareConstraint(context.Background(), "s-0123456789abcdef0123456789abcdef", "Nowhere"); !errors.Is(err, ErrUnknownDebate) {
		t.Fatalf("unknown: %v", err)
	}
	submit(t, b, sid, KindPosition, testPosition("B: fixed retry"))
	pass(t, b, a, "request.accept")
	pass(t, b, a, MailEntry)
	pass(t, a, b, MailReveal)
	toConverge(t, a, b, sid)
	pending, err := a.ds.PrepareConstraint(context.Background(), sid, "Waiting for the human")
	if err != nil {
		t.Fatal(err)
	}
	submit(t, b, sid, KindAnswer, answer(true))
	pass(t, b, a, MailEntry)
	if phaseOf(t, a, sid) != PhaseClosing {
		t.Fatal("A did not close")
	}
	if err := confirmConstraint(a, pending); !errors.As(err, &bse) {
		t.Fatalf("confirm after the close: %v, want bad_state (precondition)", err)
	}
	if _, err := a.ds.PrepareConstraint(context.Background(), sid, "Too late"); !errors.As(err, &bse) {
		t.Fatalf("closing: %v, want bad_state", err)
	}
	if n := len(constraintIDsIn(t, a, sid, ConstraintActive)); n != 0 || a.ob.count(MailConstraint) != 0 {
		t.Fatal("a refused constraint was stored or sent")
	}
}

// The sender refuses the 11th (constraint_limit); a receiver never drops a
// valid one for the limit: B stores it active, A as excess (not shown,
// never listed); at most 20 are stored.
func TestConstraintLimits(t *testing.T) {
	a, b, reqID, sid := openPositions(t, 1)
	for i := 0; i < MaxActiveConstraints; i++ {
		constrain(t, a, sid, fmt.Sprintf("Rule %d", i))
	}
	if _, err := a.ds.PrepareConstraint(context.Background(), sid, "Rule 11"); !errors.Is(err, ErrConstraintLimit) {
		t.Fatalf("A's 11th: %v, want constraint_limit", err)
	}
	pass(t, a, b, MailConstraint)
	if _, err := b.ds.PrepareConstraint(context.Background(), sid, "Rule 11"); !errors.Is(err, ErrConstraintLimit) {
		t.Fatalf("B's 11th: %v, want constraint_limit", err)
	}
	fake := func(i int) sentMail {
		return sentMail{kind: MailConstraint, body: map[string]any{
			"at": "2026-09-25T10:00:00Z", "id": fmt.Sprintf("c-%032x", i), "request": reqID, "session": sid, "text": fmt.Sprintf("Peer rule %d", i),
		}}
	}
	// B (receiver of A's) keeps an 11th from A active: A's close decides.
	mustDeliver(t, b, a.self, fake(1))
	if n := len(constraintIDsIn(t, b, sid, ConstraintActive)); n != 11 {
		t.Fatalf("B holds %d active, want 11", n)
	}
	// A keeps an 11th from B as excess: not in the view, audited ignored.
	mustDeliver(t, a, b.self, fake(2))
	if ex := constraintIDsIn(t, a, sid, ConstraintExcess); len(ex) != 1 {
		t.Fatalf("A excess %v", ex)
	}
	if len(view(t, a, sid).Constraints) != 10 || !a.audit.has("debate.ignored", `"reason":"limit"`) {
		t.Fatal("A shows the excess constraint or did not audit it")
	}
	if a.events.count(EventConstraint) != 0 {
		t.Fatal("an excess constraint notified")
	}
	// At most 20 stored per debate: the 21st is ignored.
	for i := 3; i < 12; i++ {
		mustDeliver(t, b, a.self, fake(i))
	}
	if n := b.count(sid); n != 20 {
		t.Fatalf("B stores %d, want 20", n)
	}
	mustDeliver(t, b, a.self, fake(99))
	if n := b.count(sid); n != 20 || !b.audit.has("debate.ignored", `"reason":"limit"`) {
		t.Fatalf("B stores %d after the 21st, want 20 and an ignored audit", n)
	}
}

func (n *dnode) count(sid string) int {
	var c int
	if err := n.db.QueryRow(`SELECT COUNT(*) FROM debate_constraints WHERE session = ?`, sid).Scan(&c); err != nil {
		n.t.Fatal(err)
	}
	return c
}

// closeTo drives a converged debate (A has proposed) to A's accepting close
// and returns the close mail, not yet delivered to B.
func closeTo(t *testing.T, a, b *dnode, sid string) sentMail {
	t.Helper()
	submit(t, b, sid, KindAnswer, answer(true))
	pass(t, b, a, MailEntry)
	if phaseOf(t, a, sid) != PhaseClosing {
		t.Fatal("A did not close")
	}
	return a.ob.take(t, MailClose)
}

func closeList(m sentMail) []string {
	var ids []string
	for _, v := range m.body["constraints"].([]any) {
		ids = append(ids, v.(string))
	}
	return ids
}

// visibleActive is the sorted ids of the constraints n's view shows active.
func visibleActive(t *testing.T, n *dnode, sid string) []string {
	var ids []string
	for _, c := range view(t, n, sid).Constraints {
		if c.State == ConstraintActive {
			ids = append(ids, c.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// Review 43 H2: both sides add their 10th at the same time. A stores B's as
// excess, B stores A's active; A's close lists A's ten, and after it both
// sides hold the same set (B's own 10th is late).
func TestConstraintBothTenth(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	for i := 0; i < MaxActiveConstraints-1; i++ {
		constrain(t, a, sid, fmt.Sprintf("Rule %d", i))
	}
	pass(t, a, b, MailConstraint)
	pcA := constrain(t, a, sid, "A's tenth")
	pcB := constrain(t, b, sid, "B's tenth")
	pass(t, a, b, MailConstraint)
	pass(t, b, a, MailConstraint)
	if ex := constraintIDsIn(t, a, sid, ConstraintExcess); len(ex) != 1 || ex[0] != pcB.ID {
		t.Fatalf("A excess %v, want B's tenth", ex)
	}
	toConverge(t, a, b, sid)
	cl := closeTo(t, a, b, sid)
	listed := closeList(cl)
	if len(listed) != 10 || !contains(listed, pcA.ID) || contains(listed, pcB.ID) {
		t.Fatalf("close lists %v", listed)
	}
	mustDeliver(t, b, a.self, cl)
	if phaseOf(t, b, sid) != PhaseClosed {
		t.Fatal("B did not close")
	}
	if late := constraintIDsIn(t, b, sid, ConstraintLate); len(late) != 1 || late[0] != pcB.ID {
		t.Fatalf("B late %v, want B's tenth", late)
	}
	sort.Strings(listed)
	ga, gb := visibleActive(t, a, sid), visibleActive(t, b, sid)
	if fmt.Sprint(ga) != fmt.Sprint(listed) || fmt.Sprint(gb) != fmt.Sprint(listed) {
		t.Fatalf("sets differ:\nclose %v\nA %v\nB %v", listed, ga, gb)
	}
}

// Review 43 H2: a close that overtakes an A-authored constraint mail is held
// on B until the constraint arrives, then applied with it listed.
func TestConstraintCloseOvertakes(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	toConverge(t, a, b, sid)
	pc := constrain(t, a, sid, "Keep the wire format")
	cl := closeTo(t, a, b, sid)
	if !contains(closeList(cl), pc.ID) {
		t.Fatal("the close does not list A's constraint")
	}
	mustDeliver(t, b, a.self, cl)
	if phaseOf(t, b, sid) == PhaseClosed {
		t.Fatal("B applied a close listing a constraint it does not hold")
	}
	var held int
	if err := b.db.QueryRow(`SELECT COUNT(*) FROM debates WHERE session = ? AND close_body IS NOT NULL`, sid).Scan(&held); err != nil || held != 1 {
		t.Fatalf("close not held: %d %v", held, err)
	}
	pass(t, a, b, MailConstraint)
	if phaseOf(t, b, sid) != PhaseClosed {
		t.Fatal("B did not apply the held close once the constraint arrived")
	}
	if got := visibleActive(t, b, sid); len(got) != 1 || got[0] != pc.ID {
		t.Fatalf("B active %v", got)
	}
	if !b.audit.has("debate.close_in") {
		t.Fatal("close_in not audited")
	}
}

// A constraint that reaches A after the close is in neither record: A
// ignores it ("closed"), B marks its own unlisted constraint late.
func TestConstraintLateOnB(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	toConverge(t, a, b, sid)
	cl := closeTo(t, a, b, sid)
	pc := constrain(t, b, sid, "Added after A closed") // B still in converge
	pass(t, b, a, MailConstraint)
	if n := a.count(sid); n != 0 || !a.audit.has("debate.ignored", `"reason":"closed"`, MailConstraint) {
		t.Fatalf("A stored %d constraints after its close", n)
	}
	mustDeliver(t, b, a.self, cl)
	if late := constraintIDsIn(t, b, sid, ConstraintLate); len(late) != 1 || late[0] != pc.ID {
		t.Fatalf("B late %v", late)
	}
	if v := view(t, b, sid); len(v.Constraints) != 1 || v.Constraints[0].State != ConstraintLate {
		t.Fatalf("B view %+v", v.Constraints)
	}
	if len(visibleActive(t, a, sid)) != 0 || len(visibleActive(t, b, sid)) != 0 {
		t.Fatal("a late constraint is active somewhere")
	}
	if _, err := b.ds.PrepareConstraint(context.Background(), sid, "After the end"); err == nil {
		t.Fatal("closed debate accepted a constraint")
	}
}

// Review 46 L2: a cancelled close carries no Decision, so B applies it at
// once even when it lists an A constraint B has not received; the late mail
// is then ignored ("closed").
func TestConstraintCancelledCloseNotHeld(t *testing.T) {
	a, b, _, sid := openPositions(t, 1)
	toConverge(t, a, b, sid)
	pc := constrain(t, a, sid, "Keep the wire format")
	cl := closeTo(t, a, b, sid)
	if !contains(closeList(cl), pc.ID) {
		t.Fatal("the close does not list A's constraint")
	}
	cl.body["outcome"], cl.body["reason"] = OutcomeCancelled, ReasonCancelled
	delete(cl.body, "decision")
	delete(cl.body, "sig")
	mustDeliver(t, b, a.self, cl)
	if v := view(t, b, sid); v.Phase != PhaseClosed || v.Outcome != OutcomeCancelled {
		t.Fatalf("B did not apply the cancelled close at once: %+v", v)
	}
	pass(t, a, b, MailConstraint)
	if n := b.count(sid); n != 0 || !b.audit.has("debate.ignored", `"reason":"closed"`, MailConstraint) {
		t.Fatalf("B stored %d constraints after the cancelled close", n)
	}
}
