package worksession

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"
)

// spyRevoke records every (sid) RevokeGrants was called for, under lock (the
// mirror's applyState and the requester's closeSessionTx call it inside
// their own transactions, potentially from different goroutines in a real
// daemon, though this test drives them sequentially).
type spyRevoke struct {
	mu    sync.Mutex
	calls []string
}

func (s *spyRevoke) hook(_ context.Context, _ *sql.Tx, sid string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, sid)
	return nil
}

func (s *spyRevoke) count(sid string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.calls {
		if c == sid {
			n++
		}
	}
	return n
}

// TestCancelRevokesGrantsInClosingTx is the 2.2c hook contract: A's own
// close (here, Cancel) calls RevokeGrants inside the same transaction as the
// session's close (Docs/protocol/grant.md §Session end).
func TestCancelRevokesGrantsInClosingTx(t *testing.T) {
	a, b, _, sid := setupAcceptedSession(t)
	spyA := &spyRevoke{}
	a.ws.RevokeGrants = spyA.hook

	if _, err := a.ws.Cancel(context.Background(), sid, "no longer needed"); err != nil {
		t.Fatal(err)
	}
	if spyA.count(sid) != 1 {
		t.Fatalf("RevokeGrants called %d times for %s, want 1", spyA.count(sid), sid)
	}

	// B's mirror also revokes its held grants once it learns of the close.
	spyB := &spyRevoke{}
	b.ws.RevokeGrants = spyB.hook
	if err := deliver(t, b, testA, a.ob.last(t, KindState)); err != nil {
		t.Fatalf("deliver ws.state: %v", err)
	}
	if spyB.count(sid) != 1 {
		t.Fatalf("B's RevokeGrants called %d times for %s, want 1", spyB.count(sid), sid)
	}

	// A later, redundant ws.state (should not happen from a well-behaved A,
	// but the hook must stay idempotent-safe) must not be double-counted by
	// a second close: the mirror only calls the hook on the step into closed.
}

// TestDiscardRevokesGrants covers the OD-P2-6 (c) discard edge from
// quarantined: it also closes the session and must revoke its grants.
func TestDiscardRevokesGrants(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	a.ws.Quarantine = alwaysQuarantine
	_ = submitAndDeliverResult(t, a, b, reqID, validResult())

	spyA := &spyRevoke{}
	a.ws.RevokeGrants = spyA.hook
	if _, err := a.ws.Discard(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	if spyA.count(sid) != 1 {
		t.Fatalf("RevokeGrants called %d times, want 1", spyA.count(sid))
	}
}

// TestAcceptResultRevokesGrants covers the ordinary accepted close.
func TestAcceptResultRevokesGrants(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	_ = submitAndDeliverResult(t, a, b, reqID, validResult())

	spyA := &spyRevoke{}
	a.ws.RevokeGrants = spyA.hook
	if _, err := a.ws.AcceptResult(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	if spyA.count(sid) != 1 {
		t.Fatalf("RevokeGrants called %d times, want 1", spyA.count(sid))
	}
}
