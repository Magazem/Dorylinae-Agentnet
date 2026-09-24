package worksession

// Ticket 2.5 acceptance, session side (Docs/protocol/consult.md §Answering):
// a result on a pending or deferred question accepts the request, opens the
// session and submits the result in ONE transaction.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// submitQuestion has A submit a question with one context file and delivers
// it to B (pending there); it returns the request id.
func submitQuestion(t *testing.T, a, b *node, typ string) string {
	t.Helper()
	p := request.SubmitParams{
		From: testA, To: testB, Team: testTeam, Type: typ, Title: "q", Brief: "What: is this safe?", Urgency: request.UrgencyNormal,
	}
	if typ == request.TypeQuestion {
		p.Context = []request.ContextFile{{Name: "outbox.go", Text: "package mail\n"}}
	}
	out, err := a.req.Submit(context.Background(), p)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := deliver(t, b, testA, a.ob.last(t, "request")); err != nil {
		t.Fatalf("deliver request: %v", err)
	}
	return out.Request.ID
}

func answer() *Result {
	return &Result{Status: request.ResultNA, Output: "Yes, it is safe.", Verification: VerificationNone}
}

func countOutbox(t *testing.T, n *node, kind string) int {
	t.Helper()
	var c int
	if err := n.db.QueryRow(`SELECT COUNT(*) FROM outbox WHERE kind = ?`, kind).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAnswerQuestionAcceptsOpensAndSubmits(t *testing.T) {
	for _, order := range []string{"accept first", "result first"} {
		t.Run(order, func(t *testing.T) {
			ctx := context.Background()
			a, b := newNode(t, testA), newNode(t, testB)
			b.clock = a.clock
			reqID := submitQuestion(t, a, b, request.TypeQuestion)
			sid := DeriveID(testA, testB, reqID)

			gotSid, mailID, err := b.ws.AnswerQuestion(ctx, reqID, testA, answer())
			if err != nil || gotSid != sid || mailID == "" {
				t.Fatalf("AnswerQuestion = %q, %q, %v; want %q", gotSid, mailID, err, sid)
			}
			// B: request accepted, session open (B's mirror: A owns the
			// transition), result stored, both mails queued.
			bv, err := b.req.Show(ctx, reqID, testA)
			if err != nil || bv.State != request.StateAccepted {
				t.Fatalf("B request = %+v, %v", bv, err)
			}
			sv, err := b.ws.Get(ctx, sid)
			if err != nil || sv.State != StateOpen || sv.Round != 1 || sv.Result == nil || sv.Result.Output != "Yes, it is safe." {
				t.Fatalf("B session = %+v, %v", sv, err)
			}
			if countOutbox(t, b, request.KindAccept) != 1 || countOutbox(t, b, KindResult) != 1 {
				t.Fatalf("outbox: want one request.accept and one ws.result")
			}
			acts := b.audit.actions()
			if !contains(acts, "request.accept") || !contains(acts, "ws.result") {
				t.Fatalf("audit = %v, want request.accept and ws.result", acts)
			}

			accept, result := b.ob.last(t, request.KindAccept), b.ob.last(t, KindResult)
			first, second := accept, result
			if order == "result first" {
				first, second = result, accept
			}
			if err := deliver(t, a, testB, first); err != nil {
				t.Fatal(err)
			}
			if err := deliver(t, a, testB, second); err != nil {
				t.Fatal(err)
			}
			av, err := a.ws.Get(ctx, sid)
			if err != nil || av.State != StateAwaitingResult || av.Result == nil || av.Result.Status != request.ResultNA {
				t.Fatalf("A session = %+v, %v", av, err)
			}
			ar, err := a.req.Show(ctx, reqID, "")
			if err != nil || ar.State != request.StateAccepted {
				t.Fatalf("A request = %+v, %v", ar, err)
			}

			// A accepts the answer: both sides closed / completed.
			if _, err := a.ws.AcceptResult(ctx, sid); err != nil {
				t.Fatal(err)
			}
			deliverState(t, a, b)
			if err := deliver(t, a, testB, b.ob.last(t, request.KindComplete)); err != nil {
				t.Fatalf("deliver request.complete: %v", err)
			}
			if v, _ := b.ws.Get(ctx, sid); v.State != StateClosed {
				t.Fatalf("B session after accept-result = %+v", v)
			}
			if v, _ := a.req.Show(ctx, reqID, ""); v.State != request.StateCompleted {
				t.Fatalf("A request after accept-result = %+v", v)
			}
		})
	}
}

func TestAnswerDeferredQuestion(t *testing.T) {
	ctx := context.Background()
	a, b := newNode(t, testA), newNode(t, testB)
	b.clock = a.clock
	reqID := submitQuestion(t, a, b, request.TypeQuestion)
	if _, err := b.req.Defer(ctx, reqID, testA, b.clock.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.ws.AnswerQuestion(ctx, reqID, testA, answer()); err != nil {
		t.Fatalf("answer of a deferred question: %v", err)
	}
	if v, _ := b.req.Show(ctx, reqID, testA); v.State != request.StateAccepted {
		t.Fatalf("state = %q, want accepted", v.State)
	}
}

// A failure of the result half (a body over the cap) undoes the accept and the
// session too: the question stays pending, with nothing queued.
func TestAnswerQuestionIsAtomic(t *testing.T) {
	ctx := context.Background()
	a, b := newNode(t, testA), newNode(t, testB)
	b.clock = a.clock
	reqID := submitQuestion(t, a, b, request.TypeQuestion)

	// 32768 quotes: valid output, but its canonical form is over 65536 bytes.
	huge := &Result{Status: request.ResultNA, Output: strings.Repeat(`"`, 32768), Verification: VerificationNone}
	_, _, err := b.ws.AnswerQuestion(ctx, reqID, testA, huge)
	var tl *TooLargeResultError
	if !errors.As(err, &tl) {
		t.Fatalf("err = %v, want *TooLargeResultError", err)
	}
	if v, _ := b.req.Show(ctx, reqID, testA); v.State != request.StatePending {
		t.Fatalf("state after a failed answer = %q, want pending", v.State)
	}
	if n := countWorkSessions(t, b); n != 0 {
		t.Fatalf("sessions after a failed answer = %d, want 0", n)
	}
	if n := countOutbox(t, b, request.KindAccept) + countOutbox(t, b, KindResult); n != 0 {
		t.Fatalf("queued mails after a failed answer = %d, want 0", n)
	}
	if acts := b.audit.actions(); contains(acts, "request.accept") || contains(acts, "ws.result") {
		t.Fatalf("audit after a failed answer = %v", acts)
	}
	// The same question can still be answered.
	if _, _, err := b.ws.AnswerQuestion(ctx, reqID, testA, answer()); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestAnswerQuestionRefusals(t *testing.T) {
	ctx := context.Background()
	var bse *request.BadStateError

	t.Run("not a question", func(t *testing.T) {
		a, b := newNode(t, testA), newNode(t, testB)
		b.clock = a.clock
		reqID := submitQuestion(t, a, b, request.TypeTask)
		if _, _, err := b.ws.AnswerQuestion(ctx, reqID, testA, answer()); !errors.As(err, &bse) {
			t.Fatalf("err = %v, want *request.BadStateError", err)
		}
		if v, _ := b.req.Show(ctx, reqID, testA); v.State != request.StatePending {
			t.Fatalf("state = %q, want pending", v.State)
		}
	})
	t.Run("already accepted", func(t *testing.T) {
		a, b := newNode(t, testA), newNode(t, testB)
		b.clock = a.clock
		reqID := submitQuestion(t, a, b, request.TypeQuestion)
		if _, err := b.req.Accept(ctx, reqID, testA); err != nil {
			t.Fatal(err)
		}
		if _, _, err := b.ws.AnswerQuestion(ctx, reqID, testA, answer()); !errors.As(err, &bse) {
			t.Fatalf("err = %v, want *request.BadStateError (use the normal flow)", err)
		}
	})
	t.Run("unknown request", func(t *testing.T) {
		b := newNode(t, testB)
		if _, _, err := b.ws.AnswerQuestion(ctx, "r-00000000000000000000000000000001", "", answer()); !errors.Is(err, request.ErrUnknownRequest) {
			t.Fatalf("err = %v, want ErrUnknownRequest", err)
		}
	})
	t.Run("invalid result", func(t *testing.T) {
		a, b := newNode(t, testA), newNode(t, testB)
		b.clock = a.clock
		reqID := submitQuestion(t, a, b, request.TypeQuestion)
		bad := &Result{Status: "maybe", Verification: VerificationNone}
		var fe *FieldError
		if _, _, err := b.ws.AnswerQuestion(ctx, reqID, testA, bad); !errors.As(err, &fe) {
			t.Fatalf("err = %v, want *FieldError", err)
		}
		if v, _ := b.req.Show(ctx, reqID, testA); v.State != request.StatePending {
			t.Fatalf("state = %q, want pending", v.State)
		}
	})
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
