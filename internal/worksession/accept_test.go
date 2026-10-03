package worksession

import (
	"context"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// TestAcceptOpensSessionBothSides is the 2.1a acceptance item "accept opens
// the session in the same transaction ... A creates its row on
// request.accept, or on a ws.result that overtakes it".
func TestAcceptOpensSessionBothSides(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	av, err := a.ws.Get(context.Background(), sid)
	if err != nil || av.Role != RoleRequester || av.State != StateOpen || av.RequestID != reqID {
		t.Fatalf("A's row = %+v, %v", av, err)
	}
	bv, err := b.ws.Get(context.Background(), sid)
	if err != nil || bv.Role != RoleWorker || bv.State != StateOpen || bv.RequestID != reqID {
		t.Fatalf("B's row = %+v, %v", bv, err)
	}
	// The request stays accepted on both sides while the session is open.
	av2, err := a.req.Show(context.Background(), reqID, "")
	if err != nil || av2.State != "accepted" {
		t.Fatalf("A's request = %+v, %v", av2, err)
	}
	bv2, err := b.req.Show(context.Background(), reqID, testA)
	if err != nil || bv2.State != "accepted" {
		t.Fatalf("B's request = %+v, %v", bv2, err)
	}
}

// TestAcceptOpensSessionIdempotent: a ws.result that overtakes the accept
// creates A's session row first; the accept, when it later arrives, creates
// nothing new (Docs/protocol/work-session.md §Ordering).
func TestAcceptOpensSessionIdempotent(t *testing.T) {
	ctx := context.Background()
	a := newNode(t, testA)
	b := newNode(t, testB)
	outcome, err := a.req.Submit(ctx, request.SubmitParams{
		From: testA, To: testB, Team: testTeam, Type: request.TypeTask, Title: "t", Brief: "What: x", Urgency: request.UrgencyNormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	reqID := outcome.Request.ID
	if err := deliver(t, b, testA, a.ob.last(t, "request")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.req.Accept(ctx, reqID, testA); err != nil {
		t.Fatal(err)
	}
	acceptMail := b.ob.last(t, "request.accept")

	// B submits a result before A ever sees the accept.
	if ok, _, err := b.ws.SubmitResult(ctx, testA, reqID, validResult(), ByAgent); !ok || err != nil {
		t.Fatalf("SubmitResult: ok=%v err=%v", ok, err)
	}
	if err := deliver(t, a, testB, b.ob.last(t, KindResult)); err != nil {
		t.Fatalf("deliver ws.result before accept: %v", err)
	}
	sid := DeriveID(testA, testB, reqID)
	v, err := a.ws.Get(ctx, sid)
	if err != nil || v.State != StateAwaitingResult {
		t.Fatalf("session after early result = %+v, %v", v, err)
	}

	// The accept arrives late: it must not reset or duplicate the row.
	if err := deliver(t, a, testB, acceptMail); err != nil {
		t.Fatalf("deliver late accept: %v", err)
	}
	v2, err := a.ws.Get(ctx, sid)
	if err != nil || v2.State != StateAwaitingResult || v2.Seq != v.Seq {
		t.Fatalf("session after late accept = %+v, %v; want unchanged from %+v", v2, err, v)
	}
	if n := countWorkSessions(t, a); n != 1 {
		t.Fatalf("A has %d session rows, want 1", n)
	}
	// R55-121, review 84b F4 (T13.2): the overtaking result opened A's row, so
	// it wrote the one ws.open (actor daemon); the accept that follows wrote
	// none.
	if opens := a.audit.byAction("ws.open"); len(opens) != 1 || opens[0].actor != "daemon" ||
		!strings.Contains(opens[0].detail, `"role":"requester"`) || !strings.Contains(opens[0].detail, sid) {
		t.Fatalf("A's ws.open rows = %+v, want exactly one by daemon for %s", opens, sid)
	}
	// B's accept opened its row: ws.open by the accept's own actor (cli).
	if opens := b.audit.byAction("ws.open"); len(opens) != 1 || opens[0].actor != "cli" || !strings.Contains(opens[0].detail, `"role":"worker"`) {
		t.Fatalf("B's ws.open rows = %+v, want exactly one by cli", opens)
	}
}

// TestAutoAcceptWritesWsOpenAsDaemon (T13.1, review 84b F5): an own-device
// helper's auto-accept opens B's session row with actor daemon, not cli.
func TestAutoAcceptWritesWsOpenAsDaemon(t *testing.T) {
	ctx := context.Background()
	a := newNode(t, testA)
	b := newNode(t, testB)
	outcome, err := a.req.Submit(ctx, request.SubmitParams{
		From: testA, To: testB, Team: testTeam, Type: request.TypeTask, Title: "t", Brief: "What: x", Urgency: request.UrgencyNormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	reqID := outcome.Request.ID
	if err := deliver(t, b, testA, a.ob.last(t, "request")); err != nil {
		t.Fatal(err)
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.req.AutoAcceptInTx(ctx, tx, reqID, testA); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	opens := b.audit.byAction("ws.open")
	if len(opens) != 1 || opens[0].actor != "daemon" || !strings.Contains(opens[0].detail, `"role":"worker"`) {
		t.Fatalf("B's ws.open rows = %+v, want exactly one by daemon", opens)
	}
}
