package request

// Ticket 1.6b unit acceptance (Docs/review/11-phase1-tickets.md §1.6b,
// Docs/protocol/request.md §Inbox (1.6)): inbox_list order, deferred due,
// --all, team filter. Uses the newTestStore/deliverRequest/receivedRequest
// helpers of store_test.go (1.4c).

import (
	"context"
	"testing"
	"time"
)

// newIncoming builds a valid received Request from testFrom with a fresh id
// and the given urgency (setting urgency_reason when required).
func newIncoming(urgency string) *Request {
	r := receivedRequest()
	r.ID = NewID()
	r.Urgency = urgency
	if urgency == UrgencyHigh || urgency == UrgencyBlocking {
		r.UrgencyReason = "time sensitive"
	}
	return r
}

// TestInboxOrder is the 1.6 acceptance test: three requests from one new
// sender (low, high, normal, sent in that order) list as high, normal, low
// (Docs/protocol/request.md §Effective priority).
func TestInboxOrder(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	ctx := context.Background()

	low, high, normal := newIncoming(UrgencyLow), newIncoming(UrgencyHigh), newIncoming(UrgencyNormal)
	now := time.Now()
	if err := deliverRequest(t, s, low, now); err != nil {
		t.Fatal(err)
	}
	if err := deliverRequest(t, s, high, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := deliverRequest(t, s, normal, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}

	got, err := s.InboxList(ctx, InboxFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("InboxList = %d rows, want 3", len(got))
	}
	wantIDs := []string{high.ID, normal.ID, low.ID}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Errorf("row %d: id = %s, want %s (order %v)", i, got[i].ID, id, urgencies(got))
		}
	}
	if got[0].Priority <= got[1].Priority || got[1].Priority <= got[2].Priority {
		t.Errorf("priorities not descending: %v", priorities(got))
	}
}

func urgencies(vs []View) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Urgency
	}
	return out
}

func priorities(vs []View) []int {
	out := make([]int, len(vs))
	for i, v := range vs {
		out[i] = v.Priority
	}
	return out
}

// TestInboxOrderByAge: same priority, older first (by received_at, the time
// of receipt, not the request's declared created time).
func TestInboxOrderByAge(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	first, second := newIncoming(UrgencyNormal), newIncoming(UrgencyNormal)
	first.Created, second.Created = base.Add(-time.Hour), base.Add(-time.Hour)
	s.Now = func() time.Time { return base }
	if err := deliverRequest(t, s, first, base); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return base.Add(time.Minute) }
	if err := deliverRequest(t, s, second, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	got, err := s.InboxList(ctx, InboxFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != first.ID || got[1].ID != second.ID {
		t.Fatalf("InboxList order = %v, want [%s %s]", urgencies(got), first.ID, second.ID)
	}
}

// TestInboxDeferredDueFakeClock exercises the due transition precisely with a
// fake clock throughout, avoiding real-time flakiness.
func TestInboxDeferredDueFakeClock(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	ctx := context.Background()
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return base }

	id := storePendingIn(t, s)
	until := base.Add(time.Hour)
	if _, err := s.Defer(ctx, id, testFrom, until); err != nil {
		t.Fatal(err)
	}

	s.Now = func() time.Time { return base.Add(30 * time.Minute) }
	got, err := s.InboxList(ctx, InboxFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("before until: InboxList = %d rows, want 0", len(got))
	}

	s.Now = func() time.Time { return until.Add(time.Second) }
	got, err = s.InboxList(ctx, InboxFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != id || !got[0].Due {
		t.Fatalf("after until: InboxList = %+v, want one due row", got)
	}
}

// TestInboxAllAndCancelled: --all shows answered and cancelled requests; a
// cancelled one is not in the default list.
func TestInboxAllAndCancelled(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	ctx := context.Background()

	pending := newIncoming(UrgencyNormal)
	if err := deliverRequest(t, s, pending, time.Now()); err != nil {
		t.Fatal(err)
	}
	pendingID := pending.ID

	declined := newIncoming(UrgencyNormal)
	if err := deliverRequest(t, s, declined, time.Now()); err != nil {
		t.Fatal(err)
	}
	declinedID := declined.ID
	if _, err := s.Decline(ctx, declinedID, testFrom, "not now"); err != nil {
		t.Fatal(err)
	}

	cancelled := newIncoming(UrgencyNormal)
	if err := deliverRequest(t, s, cancelled, time.Now()); err != nil {
		t.Fatal(err)
	}
	cancelledID := cancelled.ID
	if err := deliverMirror(t, s, KindCancel, testFrom, time.Now(), map[string]any{
		"at": nowWire(), "request": cancelledID,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.InboxList(ctx, InboxFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != pendingID {
		t.Fatalf("default InboxList = %v, want only %s", idsOf(got), pendingID)
	}

	all, err := s.InboxList(ctx, InboxFilter{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("InboxList{All: true} = %d rows, want 3", len(all))
	}
	seen := map[string]string{}
	for _, v := range all {
		seen[v.ID] = v.State
	}
	if seen[declinedID] != StateDeclined || seen[cancelledID] != StateCancelled || seen[pendingID] != StatePending {
		t.Fatalf("states = %v", seen)
	}
}

func idsOf(vs []View) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.ID
	}
	return out
}

func nowWire() string { return wireTime(time.Now()) }

// TestInboxTeamFilter: the team filter narrows to that team only.
func TestInboxTeamFilter(t *testing.T) {
	s, _, _ := newTestStore(t, testTo, &policy{})
	ctx := context.Background()

	other := newIncoming(UrgencyNormal)
	other.Team = "t-" + repeatRunes('1', 32)
	if err := deliverRequest(t, s, other, time.Now()); err != nil {
		t.Fatal(err)
	}
	same := newIncoming(UrgencyNormal)
	if err := deliverRequest(t, s, same, time.Now()); err != nil {
		t.Fatal(err)
	}

	got, err := s.InboxList(ctx, InboxFilter{Team: testTeam})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != same.ID {
		t.Fatalf("InboxList{Team} = %v, want only %s", idsOf(got), same.ID)
	}
}

// TestUrgencyNote is a table test of the derivation of Docs/protocol/request.md
// §Urgency guards (1.7)'s inbox note.
func TestUrgencyNote(t *testing.T) {
	cases := []struct {
		downgradedBy, declared, want string
	}{
		{"", "", ""},
		{"sender", UrgencyHigh, "sent as normal: the sender's weekly budget of 5 high requests was used"},
		{"sender", UrgencyBlocking, "sent as normal: the sender's weekly budget of 2 blocking requests was used"},
		{"receiver", UrgencyHigh, "shown as normal: this sender has used its weekly budget of 5 high requests to you"},
		{"receiver", UrgencyBlocking, "shown as normal: this sender has used its weekly budget of 2 blocking requests to you"},
	}
	for _, c := range cases {
		if got := urgencyNote(c.downgradedBy, c.declared); got != c.want {
			t.Errorf("urgencyNote(%q, %q) = %q, want %q", c.downgradedBy, c.declared, got, c.want)
		}
	}
}
