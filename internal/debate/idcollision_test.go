package debate

import (
	"context"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
	"github.com/Magazem/Dorylinae-Agentnet/internal/worksession"
)

// TestStartRedrawsTakenID: R55-F20 acceptance test 5 for a debate. When the
// first drawn id is already used here, Start rebuilds the whole request from a
// new id: one request mail, carrying the new id, whose commitment binds the
// new session id, so B's reveal check passes.
func TestStartRedrawsTakenID(t *testing.T) {
	ctx := context.Background()
	a, b := newDNode(t, keyA), newDNode(t, keyB)
	taken, fresh := request.NewID(), request.NewID()
	// An in row from another peer holds the first id.
	if _, err := a.db.Exec(`INSERT INTO requests (direction, peer, id, team_id, type, urgency, urgency_declared, body, body_hash, state, state_seq, created, mail_id, updated)
VALUES ('in', 'PEER-C', ?, ?, 'task', 'normal', 'normal', '{}', 'h', 'pending', 0, '2026-09-25T10:00:00Z', 'm-1', '2026-09-25T10:00:00Z')`, taken, testTeam); err != nil {
		t.Fatal(err)
	}
	ids := []string{taken, fresh}
	a.req.NewIDFunc = func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	}
	out, err := a.ds.Start(ctx, StartParams{
		Submit: request.SubmitParams{
			From: a.self, To: b.self, Team: testTeam, Type: request.TypeDebate, Title: "Backoff",
			Brief: "How should the outbox retry?", Urgency: request.UrgencyNormal,
		},
		Position: testPosition("A: capped backoff"), Rounds: new(2),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sid := worksession.DeriveID(a.self, b.self, fresh)
	if out.Request.ID != fresh || out.Session != sid {
		t.Fatalf("started %s/%s, want %s/%s", out.Request.ID, out.Session, fresh, sid)
	}
	if n := a.ob.count("request"); n != 1 {
		t.Fatalf("request mails = %d, want 1", n)
	}
	var commitment, nonce, position string
	if err := a.db.QueryRow(`SELECT d.commitment, d.nonce, e.entry FROM debates d JOIN debate_entries e ON e.session = d.session AND e.slot = 0 WHERE d.session = ?`,
		sid).Scan(&commitment, &nonce, &position); err != nil {
		t.Fatalf("debate row for the fresh id: %v", err)
	}
	if want := Commitment(sid, a.self, nonce, []byte(position)); commitment != want {
		t.Fatalf("stored commitment %s does not verify for %s", commitment, sid)
	}
	m := a.ob.take(t, "request")
	body, _ := m.body["request"].(map[string]any)
	deb, _ := body["debate"].(map[string]any)
	if body["id"] != fresh || deb["commitment"] != commitment {
		t.Fatalf("request mail id %v commitment %v, want %s %s", body["id"], deb["commitment"], fresh, commitment)
	}
	if n := countDebates(t, a, taken); n != 0 {
		t.Errorf("debates for the taken id = %d, want 0", n)
	}
	// B verifies the commitment at the reveal: the debate opens.
	mustDeliver(t, b, a.self, m)
	submit(t, b, sid, KindPosition, testPosition("B: fixed retry"))
	pass(t, b, a, request.KindAccept)
	pass(t, b, a, MailEntry)
	pass(t, a, b, MailReveal)
	if got := phaseOf(t, b, sid); got != PhaseRounds {
		t.Fatalf("B's phase = %s, want %s", got, PhaseRounds)
	}
}

func countDebates(t *testing.T, n *dnode, requestID string) int {
	t.Helper()
	var c int
	if err := n.db.QueryRow(`SELECT COUNT(*) FROM debates WHERE request_id = ?`, requestID).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}
