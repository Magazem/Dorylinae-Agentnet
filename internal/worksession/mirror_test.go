package worksession

import (
	"context"
	"testing"
)

// stateMailFrom builds a ws.state body as A would send it.
func stateMailFrom(from, to, reqID string, round, seq int, state, outcome, changes, verification string, at string) sentMail {
	sid := DeriveID(from, to, reqID) // from = A, to = B
	body := map[string]any{"at": at, "request": reqID, "round": round, "seq": seq, "session": sid, "state": state}
	if outcome != "" {
		body["outcome"] = outcome
	}
	if changes != "" {
		body["changes"] = changes
	}
	if verification != "" {
		body["verification"] = verification
	}
	return sentMail{to: to, kind: KindState, body: body}
}

// TestMirror_SeqOutOfOrder: out-of-order ws.state ends in the highest seq
// (Docs/protocol/work-session.md §Mirror (on B)).
func TestMirror_SeqOutOfOrder(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	at := wireTime(a.clock)

	// seq 3 arrives first.
	sm3 := stateMailFrom(testA, testB, reqID, 1, 3, StateAwaitingResult, "", "", "", at)
	if err := deliver(t, b, testA, sm3); err != nil {
		t.Fatalf("deliver seq 3: %v", err)
	}
	v, err := b.ws.Get(context.Background(), sid)
	if err != nil || v.Seq != 3 || v.State != StateAwaitingResult {
		t.Fatalf("after seq 3 = %+v, %v", v, err)
	}

	// seq 2 arrives late: ignored, seq stays 3.
	sm2 := stateMailFrom(testA, testB, reqID, 1, 2, StateOpen, "", "", "", at)
	if err := deliver(t, b, testA, sm2); err != nil {
		t.Fatalf("deliver seq 2: %v", err)
	}
	v, err = b.ws.Get(context.Background(), sid)
	if err != nil || v.Seq != 3 || v.State != StateAwaitingResult {
		t.Fatalf("after late seq 2 = %+v, %v; want unchanged", v, err)
	}

	// A duplicate of seq 3 itself is also ignored (seq <= row.seq).
	if err := deliver(t, b, testA, sm3); err != nil {
		t.Fatalf("deliver dup seq 3: %v", err)
	}
	v, err = b.ws.Get(context.Background(), sid)
	if err != nil || v.Seq != 3 {
		t.Fatalf("after dup seq 3 = %+v, %v", v, err)
	}

	// seq 4 arrives next: applied.
	sm4 := stateMailFrom(testA, testB, reqID, 2, 4, StateOpen, "", "changes text", "", at)
	if err := deliver(t, b, testA, sm4); err != nil {
		t.Fatalf("deliver seq 4: %v", err)
	}
	v, err = b.ws.Get(context.Background(), sid)
	if err != nil || v.Seq != 4 || v.State != StateOpen || v.Changes != "changes text" {
		t.Fatalf("after seq 4 = %+v, %v", v, err)
	}
}

// TestMirror_Orphan: a ws.state for a session B has never heard of is
// ignored and audited ws.orphan (Docs/protocol/work-session.md §Ordering).
func TestMirror_Orphan(t *testing.T) {
	b := newNode(t, testB)
	sm := stateMailFrom(testA, testB, "r-ffffffffffffffffffffffffffffffff", 1, 1, StateOpen, "", "", "", wireTime(b.clock))
	if err := deliver(t, b, testA, sm); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := b.audit.actions(); !containsAction(got, "ws.orphan") {
		t.Errorf("audit = %v, want ws.orphan", got)
	}
}
