package worksession

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// markRunner marks B's worker session of reqID as a run session, as the
// helper's receive transaction does after its auto-accept.
func markRunner(t *testing.T, b *node, reqID string) {
	t.Helper()
	tx, err := b.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := b.ws.MarkRunnerTx(context.Background(), tx, testA, reqID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func wantRunnerRefusal(t *testing.T, what string, err error) {
	t.Helper()
	var bse *BadStateError
	if !errors.As(err, &bse) || !strings.Contains(bse.Msg, "is a run session: the helper's runner reports it") {
		t.Fatalf("%s on a run session: err = %v, want the run-session bad_state", what, err)
	}
}

// R55-029 (§Run sessions), store level: on a run session an agent's result,
// request_complete shorthand and cancel are refused; the runner's are not.
func TestRunSessionRefusesAgent(t *testing.T) {
	ctx := context.Background()
	_, b, reqID, sid := setupAcceptedSession(t)
	markRunner(t, b, reqID)
	if v, err := b.ws.Get(ctx, sid); err != nil || !v.Runner {
		t.Fatalf("view = %+v, %v; want runner", v, err)
	}

	_, _, err := b.ws.SubmitResult(ctx, testA, reqID, validResult(), ByAgent)
	wantRunnerRefusal(t, "an agent's result", err)
	_, err = b.ws.CompleteShorthand(ctx, testA, reqID, "done", nil)
	wantRunnerRefusal(t, "request_complete", err)
	_, _, _, err = b.ws.SubmitCancel(ctx, sid, "", ByAgent)
	wantRunnerRefusal(t, "an agent's cancel", err)
	if n := b.ob.sentCount(KindResult) + b.ob.sentCount(KindCancel); n != 0 {
		t.Fatalf("a refused submission sent %d mails", n)
	}

	if ok, _, err := b.ws.SubmitResult(ctx, testA, reqID, validResult(), ByRunner); !ok || err != nil {
		t.Fatalf("the runner's result: %v", err)
	}
	b.ob.last(t, KindResult)
}

// A normal (agent-owned) session is not marked and accepts an agent result.
func TestWorkSessionNotRunner(t *testing.T) {
	_, b, reqID, sid := setupAcceptedSession(t)
	if v, err := b.ws.Get(context.Background(), sid); err != nil || v.Runner {
		t.Fatalf("view = %+v, %v; want no runner mark", v, err)
	}
	if ok, _, err := b.ws.SubmitResult(context.Background(), testA, reqID, validResult(), ByAgent); !ok || err != nil {
		t.Fatalf("agent result on a work session: %v", err)
	}
}

// §Run sessions, OD-F18-6 and review 69b F5: when A requests changes on a
// run session, B's daemon sends ws.cancel in the same transaction that
// applies the ws.state (it exists before After runs), a duplicate of that
// ws.state sends no second one, and A then closes the session cancelled.
func TestRunSessionNewRoundAutoCancel(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := setupAcceptedSession(t)
	markRunner(t, b, reqID)
	if ok, _, err := b.ws.SubmitResult(ctx, testA, reqID, validResult(), ByRunner); !ok || err != nil {
		t.Fatal(err)
	}
	if err := deliver(t, a, testB, b.ob.last(t, KindResult)); err != nil {
		t.Fatal(err)
	}
	deliverState(t, a, b) // awaiting_result
	if _, err := a.ws.RequestChanges(ctx, sid, "please run it again"); err != nil {
		t.Fatal(err)
	}
	st := a.ob.last(t, KindState)

	// Apply and commit only (a crash before After): the ws.cancel and the
	// requested mark are already there.
	applyOnly(t, b, testA, st)
	var cancel string
	if err := b.db.QueryRow(`SELECT cancel FROM work_sessions WHERE id = ?`, sid).Scan(&cancel); err != nil || cancel != "requested" {
		t.Fatalf("cancel = %q, %v; want requested right after commit", cancel, err)
	}
	var rows int
	if err := b.db.QueryRow(`SELECT COUNT(*) FROM outbox WHERE kind = ?`, KindCancel).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("ws.cancel outbox rows = %d, %v; want 1", rows, err)
	}
	cm := b.ob.last(t, KindCancel)

	// A duplicate of the ws.state sends nothing more.
	if err := deliver(t, b, testA, st); err != nil {
		t.Fatal(err)
	}
	if n := b.ob.sentCount(KindCancel); n != 0 {
		t.Fatalf("a duplicate ws.state sent %d more ws.cancel", n)
	}

	if err := deliver(t, a, testB, cm); err != nil {
		t.Fatal(err)
	}
	if av, err := a.ws.Get(ctx, sid); err != nil || av.State != StateClosed || av.Outcome != OutcomeCancelled {
		t.Fatalf("A's session = %+v, %v; want closed cancelled", av, err)
	}
}

// A new round on a normal session sends no ws.cancel.
func TestWorkSessionNewRoundNoAutoCancel(t *testing.T) {
	ctx := context.Background()
	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult())
	deliverState(t, a, b)
	if _, err := a.ws.RequestChanges(ctx, sid, "again"); err != nil {
		t.Fatal(err)
	}
	deliverState(t, a, b)
	if n := b.ob.sentCount(KindCancel); n != 0 {
		t.Fatalf("ws.cancel sent on a work session's new round: %d", n)
	}
}

// applyOnly runs sm's Apply on n and commits, without After (a crash between
// commit and After).
func applyOnly(t *testing.T, n *node, from string, sm sentMail) {
	t.Helper()
	ctx := context.Background()
	k := kindFor(n, sm.kind)
	raw, err := json.Marshal(sm.body)
	if err != nil {
		t.Fatal(err)
	}
	v, err := agentcard.ParseStrict(raw)
	if err != nil {
		t.Fatal(err)
	}
	op := &mail.Opened{Msg: mail.Msg{V: 1, ID: mail.NewID(), From: from, To: n.self, Created: n.clock, Kind: sm.kind, Body: v.(map[string]any)}}
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Apply(ctx, tx, op); err != nil {
		_ = tx.Rollback()
		t.Fatalf("apply %s: %v", sm.kind, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Review 78 S2: a run session whose ws.result ended failed (rejected) or
// expired is not stuck: the start-up rescan lists it, and the runner cancels
// it. A non-run session, or one already cancelling, is left alone.
func TestUndeliveredRunResultCancelled(t *testing.T) {
	for _, final := range []string{"rejected", "expired"} {
		t.Run(final, func(t *testing.T) {
			ctx := context.Background()
			_, b, reqID, sid := setupAcceptedSession(t)
			markRunner(t, b, reqID)
			ok, mailID, err := b.ws.SubmitResult(ctx, testA, reqID, validResult(), ByRunner)
			if !ok || err != nil {
				t.Fatal(err)
			}
			b.ob.last(t, KindResult)
			if final == "rejected" {
				b.ob.OnAck(&mail.Opened{Msg: mail.Msg{From: testA, Kind: "ack", Body: map[string]any{mail.AckRejected: []any{mailID}}}})
			} else if _, err := b.db.Exec(`UPDATE outbox SET state = 'expired' WHERE id = ?`, mailID); err != nil {
				t.Fatal(err)
			}
			ids, err := b.ws.UndeliveredRunResults(ctx)
			if err != nil || len(ids) != 1 || ids[0] != mailID {
				t.Fatalf("rescan = %v, %v; want [%s]", ids, err, mailID)
			}
			sent, err := b.ws.CancelUndeliveredRunResult(ctx, mailID)
			if err != nil || !sent {
				t.Fatalf("cancel = %v, %v; want sent", sent, err)
			}
			b.ob.last(t, KindCancel)
			if v, err := b.ws.Get(ctx, sid); err != nil || v.Cancel != "requested" {
				t.Fatalf("view = %+v, %v; want cancel requested", v, err)
			}
			// Once cancelling, nothing more.
			if sent, err := b.ws.CancelUndeliveredRunResult(ctx, mailID); err != nil || sent {
				t.Fatalf("second cancel = %v, %v; want none", sent, err)
			}
			if ids, err := b.ws.UndeliveredRunResults(ctx); err != nil || len(ids) != 0 {
				t.Fatalf("rescan after cancel = %v, %v; want none", ids, err)
			}
		})
	}
}

// A non-run session's failed result is left to its agent.
func TestUndeliveredResultNotRunSession(t *testing.T) {
	ctx := context.Background()
	_, b, reqID, _ := setupAcceptedSession(t)
	_, mailID, err := b.ws.SubmitResult(ctx, testA, reqID, validResult(), ByAgent)
	if err != nil {
		t.Fatal(err)
	}
	b.ob.OnAck(&mail.Opened{Msg: mail.Msg{From: testA, Kind: "ack", Body: map[string]any{mail.AckRejected: []any{mailID}}}})
	if sent, err := b.ws.CancelUndeliveredRunResult(ctx, mailID); err != nil || sent {
		t.Fatalf("cancel on a work session = %v, %v; want none", sent, err)
	}
}
