package worksession

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// resultMailFrom builds a ws.result body as B would send it.
func resultMailFrom(from, to, reqID string, round int, at time.Time, result *Result) sentMail {
	sid := DeriveID(to, from, reqID)
	return sentMail{to: to, kind: KindResult, body: map[string]any{
		"at": wireTime(at), "request": reqID, "result": resultWire(result), "round": round, "session": sid,
	}}
}

// TestResult_WrongRound: a session that never had an outgoing ws.state
// (nothing to resend yet) simply ignores a wrong-round result.
func TestResult_WrongRound(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	sm := resultMailFrom(testB, testA, reqID, 2, a.clock, validResult())
	if err := deliver(t, a, testB, sm); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	v, err := a.ws.Get(context.Background(), sid)
	if err != nil || v.State != StateOpen || v.Result != nil {
		t.Fatalf("A after wrong-round result = %+v, %v", v, err)
	}
	if got := a.audit.actions(); !containsAction(got, "ws.ignored") {
		t.Errorf("audit = %v, want ws.ignored", got)
	}
	_ = b
}

// TestResult_WrongState: a wrong-state result is ignored, and A re-sends its
// last_state (10-minute rule) so B catches up.
func TestResult_WrongState(t *testing.T) {
	a, b, reqID, sid := setupAcceptedSession(t)
	submitAndDeliverResult(t, a, b, reqID, validResult()) // now awaiting_result; last_state now set
	sm := resultMailFrom(testB, testA, reqID, 1, a.clock, validResult())
	if err := deliver(t, a, testB, sm); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	v, err := a.ws.Get(context.Background(), sid)
	if err != nil || v.State != StateAwaitingResult {
		t.Fatalf("A after wrong-state result = %+v, %v", v, err)
	}
	if got := a.audit.actions(); !containsAction(got, "ws.ignored") {
		t.Errorf("audit = %v, want ws.ignored", got)
	}
	a.ob.last(t, KindState) // the echoed last_state
}

func TestResult_WrongSessionID(t *testing.T) {
	a, _, reqID, _ := setupAcceptedSession(t)
	sm := resultMailFrom(testB, testA, reqID, 1, a.clock, validResult())
	sm.body["session"] = "s-00000000000000000000000000000000"
	err := deliver(t, a, testB, sm)
	if err == nil || !isBadBody(err) {
		t.Fatalf("wrong session id: err = %v, want bad_body", err)
	}
}

func TestResult_MovedToAnotherSession(t *testing.T) {
	a, _, reqID, sid := setupAcceptedSession(t)
	// A body whose session claims to be for a different peer entirely.
	otherSID := DeriveID(testA, testKey(9), reqID)
	sm := resultMailFrom(testB, testA, reqID, 1, a.clock, validResult())
	sm.body["session"] = otherSID
	err := deliver(t, a, testB, sm)
	if err == nil || !isBadBody(err) {
		t.Fatalf("moved session: err = %v, want bad_body", err)
	}
	if sid == otherSID {
		t.Fatal("test setup bug: derived ids collided")
	}
}

func TestResult_Orphan(t *testing.T) {
	a := newNode(t, testA)
	sm := resultMailFrom(testB, testA, "r-ffffffffffffffffffffffffffffffff", 1, a.clock, validResult())
	if err := deliver(t, a, testB, sm); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := a.audit.actions(); !containsAction(got, "ws.orphan") {
		t.Errorf("audit = %v, want ws.orphan", got)
	}
}

func isBadBody(err error) bool { return errors.Is(err, mail.ErrBadBody) }

// TestResult_Caps is the 2.1a acceptance item "result caps: every member at
// its limit and one over, verification values (human_accepted from B ->
// bad_body), total 65536/65537 -> result_too_large".
func TestResult_Caps(t *testing.T) {
	t.Run("summary at and over limit", func(t *testing.T) {
		a, b, reqID, _ := setupAcceptedSession(t)
		r := validResult()
		r.Summary = strings.Repeat("a", 280)
		if ok, err := b.ws.SubmitResult(context.Background(), testA, reqID, r); !ok || err != nil {
			t.Fatalf("at limit: ok=%v err=%v", ok, err)
		}
		r2 := validResult()
		r2.Summary = strings.Repeat("a", 281)
		var fe *FieldError
		if _, err := b.ws.SubmitResult(context.Background(), testA, reqID, r2); err == nil || !asFieldErr(err, &fe) {
			t.Fatalf("over limit: err = %v, want FieldError", err)
		}
		_ = a
	})

	t.Run("verification human_accepted from B is rejected", func(t *testing.T) {
		_, b, reqID, _ := setupAcceptedSession(t)
		r := validResult()
		r.Verification = VerificationHumanAccepted
		var fe *FieldError
		if _, err := b.ws.SubmitResult(context.Background(), testA, reqID, r); err == nil || !asFieldErr(err, &fe) {
			t.Fatalf("human_accepted from B: err = %v, want FieldError", err)
		}
	})

	t.Run("verification human_accepted from B on the wire is bad_body", func(t *testing.T) {
		a, _, reqID, _ := setupAcceptedSession(t)
		r := validResult()
		r.Verification = VerificationHumanAccepted
		sm := resultMailFrom(testB, testA, reqID, 1, a.clock, r)
		err := deliver(t, a, testB, sm)
		if err == nil || !isBadBody(err) {
			t.Fatalf("human_accepted on wire: err = %v, want bad_body", err)
		}
	})

	t.Run("output at and over limit", func(t *testing.T) {
		_, b, reqID, _ := setupAcceptedSession(t)
		r := validResult()
		r.Output = strings.Repeat("a", 32768)
		if ok, err := b.ws.SubmitResult(context.Background(), testA, reqID, r); !ok || err != nil {
			t.Fatalf("at limit: ok=%v err=%v", ok, err)
		}
		r2 := validResult()
		r2.Output = strings.Repeat("a", 32769)
		if _, err := b.ws.SubmitResult(context.Background(), testA, reqID, r2); err == nil {
			t.Fatal("over limit: want an error")
		}
	})

	t.Run("total body at and over MaxResultBody", func(t *testing.T) {
		_, b, reqID, _ := setupAcceptedSession(t)
		r := validResult()
		r.Output = strings.Repeat("a", 32768)
		var artifacts []request.Artifact
		for i := 0; i < 20; i++ {
			artifacts = append(artifacts, request.Artifact{URL: "https://x/" + strings.Repeat("a", 2030)})
		}
		r.Artifacts = artifacts
		if err := ValidateResult(r); err != nil {
			t.Fatalf("fields individually valid but ValidateResult failed: %v", err)
		}
		_, err := b.ws.SubmitResult(context.Background(), testA, reqID, r)
		var tl *TooLargeResultError
		if err == nil || !asTooLargeErr(err, &tl) {
			t.Fatalf("oversized total body: err = %v, want TooLargeResultError", err)
		}
	})

	t.Run("notes reuses the request note rule (1-2000 code points)", func(t *testing.T) {
		_, b, reqID, _ := setupAcceptedSession(t)
		r := validResult()
		r.Notes = strings.Repeat("a", 2001)
		if _, err := b.ws.SubmitResult(context.Background(), testA, reqID, r); err == nil {
			t.Fatal("over the note limit: want an error")
		}
	})
}

func asFieldErr(err error, target **FieldError) bool { return errors.As(err, target) }

func asTooLargeErr(err error, target **TooLargeResultError) bool { return errors.As(err, target) }
