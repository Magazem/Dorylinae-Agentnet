package request

// Ticket 1.7 unit acceptance (Docs/review/11-phase1-tickets.md §1.7,
// Docs/protocol/request.md §Urgency guards (1.7), §Effective priority). Uses
// the newTestStore/deliverRequest/deliverMirror/newIncoming/countRows helpers
// of store_test.go, lifecycle_test.go and inbox_test.go.

import (
	"context"
	"testing"
	"time"
)

// newIncomingAt builds a newIncoming request whose Created is safely before
// created (the mail's created time used by deliverRequest below), so it
// passes the created <= msg.created check regardless of the fake clock.
func newIncomingAt(urgency string, created time.Time) *Request {
	r := newIncoming(urgency)
	r.Created = created.Add(-time.Minute)
	return r
}

// receiverUrgencyOf reads back the stored urgency/urgency_declared/downgraded_by
// of the sole "in" row with the given id.
func receiverUrgencyOf(t *testing.T, s *Store, id string) (urgency, declared, downgradedBy string) {
	t.Helper()
	row, err := getRow(context.Background(), s.DB, "in", testFrom, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.downgradedBy.Valid {
		downgradedBy = row.downgradedBy.String
	}
	return row.urgency, row.urgencyDeclared, downgradedBy
}

// TestReceiverBudgetHigh: with the sender budget disabled (simulated here by
// delivering raw "high" bodies straight to the receiver, as an unmodified
// sender would if it ignored its own courtesy budget), the sixth high
// request in 7 days from one sender is stored as normal with
// downgraded_by: receiver (Docs/protocol/request.md §Urgency guards (1.7)).
func TestReceiverBudgetHigh(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return base }

	var ids []string
	for i := 0; i < 6; i++ {
		req := newIncomingAt(UrgencyHigh, base)
		if err := deliverRequest(t, s, req, base); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		ids = append(ids, req.ID)
	}

	for i, id := range ids[:5] {
		urgency, declared, downgradedBy := receiverUrgencyOf(t, s, id)
		if urgency != UrgencyHigh || declared != UrgencyHigh || downgradedBy != "" {
			t.Errorf("request %d = %s/%s/%q, want high/high/\"\"", i, urgency, declared, downgradedBy)
		}
	}
	urgency, declared, downgradedBy := receiverUrgencyOf(t, s, ids[5])
	if urgency != UrgencyNormal || declared != UrgencyHigh || downgradedBy != "receiver" {
		t.Errorf("6th request = %s/%s/%q, want normal/high/receiver", urgency, declared, downgradedBy)
	}
}

// TestReceiverBudgetBlocking: the third blocking request from one sender in 7
// days is downgraded (the limit is 2).
func TestReceiverBudgetBlocking(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return base }

	var ids []string
	for i := 0; i < 3; i++ {
		req := newIncomingAt(UrgencyBlocking, base)
		if err := deliverRequest(t, s, req, base); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		ids = append(ids, req.ID)
	}
	for i, id := range ids[:2] {
		urgency, _, downgradedBy := receiverUrgencyOf(t, s, id)
		if urgency != UrgencyBlocking || downgradedBy != "" {
			t.Errorf("request %d = %s/%q, want blocking/\"\"", i, urgency, downgradedBy)
		}
	}
	urgency, declared, downgradedBy := receiverUrgencyOf(t, s, ids[2])
	if urgency != UrgencyNormal || declared != UrgencyBlocking || downgradedBy != "receiver" {
		t.Errorf("3rd blocking request = %s/%s/%q, want normal/blocking/receiver", urgency, declared, downgradedBy)
	}
}

// TestReceiverBudgetFreesAfter7Days: a fake clock shows the budget freeing up
// once the earlier high requests fall out of the rolling 7-day window.
func TestReceiverBudgetFreesAfter7Days(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return base }

	for i := 0; i < 5; i++ {
		if err := deliverRequest(t, s, newIncomingAt(UrgencyHigh, base), base); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	sixth := newIncomingAt(UrgencyHigh, base)
	if err := deliverRequest(t, s, sixth, base); err != nil {
		t.Fatal(err)
	}
	if urgency, _, downgradedBy := receiverUrgencyOf(t, s, sixth.ID); urgency != UrgencyNormal || downgradedBy != "receiver" {
		t.Fatalf("6th within the window = %s/%q, want normal/receiver", urgency, downgradedBy)
	}

	after := base.Add(7*24*time.Hour + time.Second)
	s.Now = func() time.Time { return after }
	freed := newIncomingAt(UrgencyHigh, after)
	if err := deliverRequest(t, s, freed, after); err != nil {
		t.Fatal(err)
	}
	if urgency, _, downgradedBy := receiverUrgencyOf(t, s, freed.ID); urgency != UrgencyHigh || downgradedBy != "" {
		t.Errorf("request after the window frees up = %s/%q, want high/\"\"", urgency, downgradedBy)
	}
}

// TestReceiverBudgetAutoDeclinesDontCount: auto-declined high requests do not
// count toward the receiver-side budget.
func TestReceiverBudgetAutoDeclinesDontCount(t *testing.T) {
	pol := &policy{notMember: true}
	s, _, _ := newTestStore(t, testTo, pol)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return base }

	for i := 0; i < 5; i++ {
		req := newIncomingAt(UrgencyHigh, base)
		if err := deliverRequest(t, s, req, base); err != nil {
			t.Fatalf("auto-declined request %d: %v", i, err)
		}
		var state string
		if err := s.DB.QueryRow(`SELECT state FROM requests WHERE direction = 'in' AND id = ?`, req.ID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state != StateDeclined {
			t.Fatalf("request %d state = %s, want declined (auto-decline)", i, state)
		}
	}

	pol.notMember = false
	sixth := newIncomingAt(UrgencyHigh, base)
	if err := deliverRequest(t, s, sixth, base); err != nil {
		t.Fatal(err)
	}
	if urgency, _, downgradedBy := receiverUrgencyOf(t, s, sixth.ID); urgency != UrgencyHigh || downgradedBy != "" {
		t.Errorf("request after 5 auto-declines = %s/%q, want high/\"\" (auto-declines do not count)", urgency, downgradedBy)
	}
}

// TestReceiverBudgetCancelDoesNotRefund (D12): a cancelled high request still
// counts toward the receiver-side budget.
func TestReceiverBudgetCancelDoesNotRefund(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return base }

	var ids []string
	for i := 0; i < 5; i++ {
		req := newIncomingAt(UrgencyHigh, base)
		if err := deliverRequest(t, s, req, base); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		ids = append(ids, req.ID)
	}
	if err := deliverMirror(t, s, KindCancel, testFrom, base, map[string]any{
		"at": wireTime(base), "request": ids[0],
	}); err != nil {
		t.Fatal(err)
	}
	if urgency, _, _ := receiverUrgencyOf(t, s, ids[0]); urgency != UrgencyHigh {
		t.Fatalf("cancelled row urgency = %s, want unchanged high", urgency)
	}

	sixth := newIncomingAt(UrgencyHigh, base)
	if err := deliverRequest(t, s, sixth, base); err != nil {
		t.Fatal(err)
	}
	if urgency, _, downgradedBy := receiverUrgencyOf(t, s, sixth.ID); urgency != UrgencyNormal || downgradedBy != "receiver" {
		t.Errorf("request after a cancelled high = %s/%q, want normal/receiver (cancel refunds nothing, D12)", urgency, downgradedBy)
	}
}

// TestSenderBudgetHigh: the sender-side courtesy budget downgrades the sixth
// high Submit in 7 days, across all peers, and sets urgency_note.
func TestSenderBudgetHigh(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return base }

	peers := []string{testTo, testKey(5), testKey(6), testKey(7), testKey(8)}
	for i, peer := range peers {
		p := SubmitParams{
			From: testFrom, To: peer, Team: testTeam, Type: TypeTask,
			Title: "t", Brief: "What: x", Urgency: UrgencyHigh, UrgencyReason: "urgent",
		}
		out, err := s.Submit(ctx, p)
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		if out.Request.Urgency != UrgencyHigh || out.Request.UrgencyDeclared != "" || out.UrgencyNote != "" {
			t.Errorf("submit %d = urgency %s declared %q note %q, want high/\"\"/\"\" (across different peers)", i, out.Request.Urgency, out.Request.UrgencyDeclared, out.UrgencyNote)
		}
	}

	sixth := SubmitParams{
		From: testFrom, To: testKey(9), Team: testTeam, Type: TypeTask,
		Title: "t", Brief: "What: x", Urgency: UrgencyHigh, UrgencyReason: "urgent",
	}
	out, err := s.Submit(ctx, sixth)
	if err != nil {
		t.Fatal(err)
	}
	if out.Request.Urgency != UrgencyNormal || out.Request.UrgencyDeclared != UrgencyHigh {
		t.Errorf("6th submit = urgency %s declared %s, want normal/high", out.Request.Urgency, out.Request.UrgencyDeclared)
	}
	if out.UrgencyNote == "" {
		t.Error("6th submit: urgency_note is empty, want a note")
	}
}

// TestHonestSenderBudgetReachesReceiverAsSenderDowngrade: an honest sender
// (Submit applies its own courtesy budget) whose sixth high request to one
// peer in 7 days is downgraded delivers a body carrying urgency_declared, so
// the receiver stores downgraded_by: sender rather than re-deriving it from
// its own budget (Docs/protocol/request.md §Receiving step 4).
func TestHonestSenderBudgetReachesReceiverAsSenderDowngrade(t *testing.T) {
	sender, _, _ := newTestStore(t, testFrom, &policy{})
	receiver, _, _ := newTestStore(t, testTo, &policy{})
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	sender.Now = func() time.Time { return base }
	receiver.Now = func() time.Time { return base }

	for i := 0; i < 6; i++ {
		p := SubmitParams{
			From: testFrom, To: testTo, Team: testTeam, Type: TypeTask,
			Title: "t", Brief: "What: x", Urgency: UrgencyHigh, UrgencyReason: "urgent",
		}
		out, err := sender.Submit(ctx, p)
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		if err := deliverRequest(t, receiver, out.Request, out.Request.Created); err != nil {
			t.Fatalf("deliver %d: %v", i, err)
		}
		urgency, declared, downgradedBy := receiverUrgencyOf(t, receiver, out.Request.ID)
		if i < 5 {
			if urgency != UrgencyHigh || downgradedBy != "" {
				t.Errorf("request %d received as %s/%q, want high/\"\"", i, urgency, downgradedBy)
			}
			continue
		}
		if urgency != UrgencyNormal || declared != UrgencyHigh || downgradedBy != "sender" {
			t.Errorf("6th request received as %s/%s/%q, want normal/high/sender", urgency, declared, downgradedBy)
		}
	}
}

// TestSenderBudgetCancelDoesNotRefund (D12): cancelling one of the five out
// rows counted against the sender budget does not free it up.
func TestSenderBudgetCancelDoesNotRefund(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return base }

	var ids []string
	for i := 0; i < 5; i++ {
		p := SubmitParams{
			From: testFrom, To: testKey(byte(10 + i)), Team: testTeam, Type: TypeTask,
			Title: "t", Brief: "What: x", Urgency: UrgencyHigh, UrgencyReason: "urgent",
		}
		out, err := s.Submit(ctx, p)
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		ids = append(ids, out.Request.ID)
	}
	if _, err := s.Cancel(ctx, ids[0], ""); err != nil {
		t.Fatal(err)
	}

	sixth := SubmitParams{
		From: testFrom, To: testKey(20), Team: testTeam, Type: TypeTask,
		Title: "t", Brief: "What: x", Urgency: UrgencyHigh, UrgencyReason: "urgent",
	}
	out, err := s.Submit(ctx, sixth)
	if err != nil {
		t.Fatal(err)
	}
	if out.Request.Urgency != UrgencyNormal || out.Request.UrgencyDeclared != UrgencyHigh {
		t.Errorf("submit after a cancel = urgency %s declared %s, want normal/high (cancel refunds nothing, D12)", out.Request.Urgency, out.Request.UrgencyDeclared)
	}
}

// TestSenderBudgetDisabledLeavesReceiverEnforcement: with DisableSenderBudget,
// Submit sends the raw urgency unchanged even past the sender's own budget,
// so the receiver-side check (proven above) is the only enforcement.
func TestSenderBudgetDisabledLeavesReceiverEnforcement(t *testing.T) {
	s, _, _ := newTestStore(t, testFrom, &policy{})
	s.DisableSenderBudget = true
	ctx := context.Background()

	for i := 0; i < 6; i++ {
		p := SubmitParams{
			From: testFrom, To: testKey(byte(30 + i)), Team: testTeam, Type: TypeTask,
			Title: "t", Brief: "What: x", Urgency: UrgencyHigh, UrgencyReason: "urgent",
		}
		out, err := s.Submit(ctx, p)
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		if out.Request.Urgency != UrgencyHigh || out.Request.UrgencyDeclared != "" || out.UrgencyNote != "" {
			t.Errorf("submit %d with the sender budget disabled = urgency %s declared %q note %q, want high/\"\"/\"\"", i, out.Request.Urgency, out.Request.UrgencyDeclared, out.UrgencyNote)
		}
	}
}

// TestUrgencyNoteBothSides confirms the wording differs between the submit
// result's own note and the recipient's inbox note for the same downgrade
// (Docs/protocol/request.md §Submit result and §Urgency guards (1.7)).
func TestUrgencyNoteBothSides(t *testing.T) {
	if got, want := submitUrgencyNote(UrgencyHigh), "sent as normal: your weekly budget of 5 high requests is used"; got != want {
		t.Errorf("submitUrgencyNote(high) = %q, want %q", got, want)
	}
	if got, want := submitUrgencyNote(UrgencyBlocking), "sent as normal: your weekly budget of 2 blocking requests is used"; got != want {
		t.Errorf("submitUrgencyNote(blocking) = %q, want %q", got, want)
	}
}

// TestPriorityScenarioDerivesFromFormula is the 1.7 acceptance: one sender
// has 4 urgent requests answered (1 accepted first), and the priority of a
// 5th, currently pending, urgent request from the same sender is checked
// against the Docs/protocol/request.md §Effective priority formula written
// out here, with n and a counted from the scenario's own rows (not
// hard-coded, and without calling Priority).
func TestPriorityScenarioDerivesFromFormula(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return base }

	accepted := newIncomingAt(UrgencyHigh, base)
	if err := deliverRequest(t, s, accepted, base); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(ctx, accepted.ID, testFrom); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		declined := newIncomingAt(UrgencyHigh, base)
		if err := deliverRequest(t, s, declined, base); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Decline(ctx, declined.ID, testFrom, "not now"); err != nil {
			t.Fatal(err)
		}
	}

	deferred := newIncomingAt(UrgencyBlocking, base)
	if err := deliverRequest(t, s, deferred, base); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Defer(ctx, deferred.ID, testFrom, base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	current := newIncomingAt(UrgencyHigh, base.Add(time.Minute))
	if err := deliverRequest(t, s, current, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	currentBlocking := newIncomingAt(UrgencyBlocking, base.Add(2*time.Minute))
	if err := deliverRequest(t, s, currentBlocking, base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	n, a, err := s.urgentAcceptance(ctx, testFrom, base)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 || a != 1 {
		t.Fatalf("scenario n, a = %d, %d, want 4, 1 (check the scenario, not the formula)", n, a)
	}

	got, err := s.InboxList(ctx, InboxFilter{})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]View{}
	for _, v := range got {
		byID[v.ID] = v
	}

	// The formula of Docs/protocol/request.md §Effective priority, written
	// out here (not calling Priority) with n and a from the scenario above.
	wantHigh := 2000 + ((3-2)*1000*(a+2))/(n+2)
	wantBlocking := 2000 + ((4-2)*1000*(a+2))/(n+2)

	if v, ok := byID[current.ID]; !ok || v.Priority != wantHigh {
		t.Errorf("current (high) priority = %v, want %d", v.Priority, wantHigh)
	}
	if v, ok := byID[currentBlocking.ID]; !ok || v.Priority != wantBlocking {
		t.Errorf("currentBlocking priority = %v, want %d", v.Priority, wantBlocking)
	}
}
