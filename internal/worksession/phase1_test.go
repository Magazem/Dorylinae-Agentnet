package worksession

import (
	"context"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// TestPhase1RequesterFallback: a ws.result whose outbox row ends
// failed/unsupported_kind (a fake Phase 1 peer that acks unsupported)
// completes B's request with the D14 part and closes B's mirror
// (Docs/protocol/work-session.md §Early complete and Phase 1 workers,
// "Phase 1 requester").
func TestPhase1RequesterFallback(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	_ = a // A never actually understands ws.* in this scenario

	result := validResult()
	result.Summary = "worked around it"
	if ok, _, err := b.ws.SubmitResult(context.Background(), testA, reqID, result, ByAgent); !ok || err != nil {
		t.Fatalf("SubmitResult: ok=%v err=%v", ok, err)
	}
	_ = b.ob.last(t, KindResult)

	// Simulate A being a Phase 1 daemon: it acked the mail as unsupported,
	// which the real mail.Outbox.OnAck turns into failed/unsupported_kind.
	if _, err := b.db.Exec(`UPDATE outbox SET state = 'failed', error = 'unsupported_kind' WHERE kind = ?`, KindResult); err != nil {
		t.Fatal(err)
	}

	if err := b.ws.CheckPhase1Fallback(context.Background(), testA, reqID); err != nil {
		t.Fatalf("CheckPhase1Fallback: %v", err)
	}
	bv, err := b.ws.Get(context.Background(), sid)
	if err != nil || bv.State != StateClosed || bv.Outcome != OutcomeCancelled {
		t.Fatalf("B's mirror after fallback = %+v, %v", bv, err)
	}
	brv, err := b.req.Show(context.Background(), reqID, testA)
	if err != nil || brv.State != "completed" || brv.Result == nil || brv.Result.Summary != "worked around it" {
		t.Fatalf("B's request after fallback = %+v, %v", brv, err)
	}
	if got := b.audit.actions(); !containsAction(got, "ws.close") {
		t.Errorf("audit = %v, want ws.close", got)
	}
	// No mail is sent to the Phase 1 peer as part of the fallback (it would
	// not understand ws.* anyway).
	if n := b.ob.sentCount(KindState); n != 0 {
		t.Errorf("ws.state sent to a Phase 1 peer: %d", n)
	}
}

// TestPhase1RequesterFallback_NoOpWhenSupported: without a failed
// unsupported_kind mail, the fallback does nothing.
func TestPhase1RequesterFallback_NoOpWhenSupported(t *testing.T) {
	_, b, reqID, sid := setupAcceptedSession(t)
	if err := b.ws.CheckPhase1Fallback(context.Background(), testA, reqID); err != nil {
		t.Fatal(err)
	}
	bv, err := b.ws.Get(context.Background(), sid)
	if err != nil || bv.State != StateOpen {
		t.Fatalf("session changed with no failed mail: %+v, %v", bv, err)
	}
}

// acceptAnother opens one more accepted session between the same nodes.
func acceptAnother(t *testing.T, a, b *node) (reqID, sid string) {
	t.Helper()
	ctx := context.Background()
	outcome, err := a.req.Submit(ctx, request.SubmitParams{
		From: testA, To: testB, Team: testTeam, Type: request.TypeTask, Title: "t2", Brief: "What: y", Urgency: request.UrgencyNormal,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	reqID = outcome.Request.ID
	if err := deliver(t, b, testA, a.ob.last(t, "request")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.req.Accept(ctx, reqID, testA); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, a, testB, b.ob.last(t, request.KindAccept)); err != nil {
		t.Fatal(err)
	}
	return reqID, DeriveID(testA, testB, reqID)
}

// R55-059: a ws.result acked rejected (a bad body, a known kind) fails the
// outbox row failed/rejected and does not trigger the Phase 1 fallback: both
// of B's sessions with A stay open. The control, acked unsupported, closes
// both.
func TestRejectedAckDoesNotTriggerFallback(t *testing.T) {
	for _, tc := range []struct {
		member, errText string
		closes          bool
	}{
		{mail.AckRejected, mail.ErrTextRejected, false},
		{mail.AckUnsupported, mail.ErrTextUnsupportedKind, true},
	} {
		t.Run(tc.member, func(t *testing.T) {
			ctx := context.Background()
			a, b, reqID, sid := setupAcceptedSession(t)
			_, sid2 := acceptAnother(t, a, b)
			ok, mailID, err := b.ws.SubmitResult(ctx, testA, reqID, validResult(), ByAgent)
			if !ok || err != nil {
				t.Fatal(err)
			}
			b.ob.OnAck(&mail.Opened{Msg: mail.Msg{From: testA, Kind: "ack", Body: map[string]any{tc.member: []any{mailID}}}})
			var state, errText string
			if err := b.db.QueryRow(`SELECT state, error FROM outbox WHERE id = ?`, mailID).Scan(&state, &errText); err != nil {
				t.Fatal(err)
			}
			if state != mail.StateFailed || errText != tc.errText {
				t.Fatalf("outbox row = %s/%s, want failed/%s", state, errText, tc.errText)
			}
			b.ws.CheckPhase1FallbackForPeer(ctx, testA)
			for _, id := range []string{sid, sid2} {
				v, err := b.ws.Get(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if closed := v.State == StateClosed; closed != tc.closes {
					t.Fatalf("session %s state %s, want closed=%v", id, v.State, tc.closes)
				}
			}
		})
	}
}

// R55-061: the start-up rescan finds every peer with an open worker work
// session.
func TestPeersWithOpenWorkerSessions(t *testing.T) {
	a, b, _, sid := setupAcceptedSession(t)
	peers, err := b.ws.PeersWithOpenWorkerSessions(context.Background())
	if err != nil || len(peers) != 1 || peers[0] != testA {
		t.Fatalf("peers = %v, %v; want [A]", peers, err)
	}
	if peers, err := a.ws.PeersWithOpenWorkerSessions(context.Background()); err != nil || len(peers) != 0 {
		t.Fatalf("requester side peers = %v, %v; want none", peers, err)
	}
	if _, err := b.db.Exec(`UPDATE work_sessions SET state = 'closed', outcome = 'cancelled' WHERE id = ?`, sid); err != nil {
		t.Fatal(err)
	}
	if peers, err := b.ws.PeersWithOpenWorkerSessions(context.Background()); err != nil || len(peers) != 0 {
		t.Fatalf("peers after close = %v, %v; want none", peers, err)
	}
}
