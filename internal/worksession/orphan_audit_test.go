package worksession

import (
	"strings"
	"testing"
)

// R55-167: each of the three ws.orphan paths (ws.result and ws.cancel on A,
// ws.state on B) audits the derived session id.
func TestOrphanAuditHasSession(t *testing.T) {
	const unknownReq = "r-ffffffffffffffffffffffffffffffff"
	sid := DeriveID(testA, testB, unknownReq)
	wantOrphan := func(t *testing.T, n *node, kind string) {
		t.Helper()
		n.audit.mu.Lock()
		defer n.audit.mu.Unlock()
		for _, e := range n.audit.entries {
			if e.action == "ws.orphan" && strings.Contains(e.detail, `"kind":"`+kind+`"`) {
				if !strings.Contains(e.detail, `"session":"`+sid+`"`) {
					t.Fatalf("ws.orphan for %s has no session: %s", kind, e.detail)
				}
				return
			}
		}
		t.Fatalf("no ws.orphan for %s: %v", kind, n.audit.entries)
	}

	a := newNode(t, testA)
	res := sentMail{kind: KindResult, body: map[string]any{
		"at": wireTime(a.clock), "request": unknownReq, "round": 1, "session": sid,
		"result": map[string]any{"status": "pass", "verification": "none"},
	}}
	if err := deliver(t, a, testB, res); err != nil {
		t.Fatal(err)
	}
	wantOrphan(t, a, KindResult)
	if err := deliver(t, a, testB, cancelMailFrom(testB, testA, unknownReq, "", wireTime(a.clock))); err != nil {
		t.Fatal(err)
	}
	wantOrphan(t, a, KindCancel)

	b := newNode(t, testB)
	st := sentMail{kind: KindState, body: map[string]any{
		"at": wireTime(b.clock), "request": unknownReq, "round": 1, "seq": 1, "session": sid, "state": StateOpen,
	}}
	if err := deliver(t, b, testA, st); err != nil {
		t.Fatal(err)
	}
	wantOrphan(t, b, KindState)
}
