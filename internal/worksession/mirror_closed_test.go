package worksession

import (
	"context"
	"strings"
	"testing"
)

// Review 55 R55-067 (C22-01, O-131): closed is final on B. A misbehaving A's
// closed -> open -> closed sequence closes B's mirror once; the later states
// are ignored (ws.ignored, reason closed) and applied without error, so no
// mail is left unacked to be resent.
func TestMirrorClosedIsFinal(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	at := wireTime(a.clock)
	steps := []sentMail{
		stateMailFrom(testA, testB, reqID, 1, 1, StateClosed, OutcomeCancelled, "", "", at),
		stateMailFrom(testA, testB, reqID, 1, 2, StateOpen, "", "", "", at),
		stateMailFrom(testA, testB, reqID, 1, 3, StateClosed, OutcomeCancelled, "", "", at),
	}
	for i, sm := range steps {
		if err := deliver(t, b, testA, sm); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		v, err := b.ws.Get(context.Background(), sid)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != StateClosed || v.Seq != 1 {
			t.Fatalf("step %d: state %s seq %d, want closed at seq 1", i+1, v.State, v.Seq)
		}
	}
	b.audit.mu.Lock()
	defer b.audit.mu.Unlock()
	ignored := 0
	for _, e := range b.audit.entries {
		if e.action == "ws.ignored" && strings.Contains(e.detail, `"reason":"closed"`) {
			ignored++
		}
	}
	if ignored != 2 {
		t.Fatalf("ws.ignored closed audits = %d, want 2", ignored)
	}
}
