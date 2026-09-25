package request

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

const testCommitment = "a24d1e0306c3bd0a129a4f15da39d0fce4f36258c13b2c4c65ba0e2b6cb81ed2"

func validDebate() *Request {
	r := validRequest()
	r.Type = TypeDebate
	r.Brief = "Which retry policy?\nOptions below."
	r.Debate = &DebateMember{Commitment: testCommitment, Rounds: 2, TurnTimeoutS: 3600}
	return r
}

// Docs/protocol/debate.md §Request type debate: the debate member iff type
// debate, its value ranges, no requested_grant or run, context allowed, and
// the debate text rule on the topic (review 43 L1).
func TestDebateRequestRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Request)
		field  string
	}{
		{"valid", func(*Request) {}, ""},
		{"debate without the member", func(r *Request) { r.Debate = nil }, "debate"},
		{"task with a stray member", func(r *Request) { r.Type = TypeTask }, "debate"},
		{"question with a stray member", func(r *Request) { r.Type = TypeQuestion }, "debate"},
		{"commitment uppercase", func(r *Request) { r.Debate.Commitment = strings.ToUpper(testCommitment) }, "debate.commitment"},
		{"commitment short", func(r *Request) { r.Debate.Commitment = testCommitment[:63] }, "debate.commitment"},
		{"rounds 0", func(r *Request) { r.Debate.Rounds = 0 }, "debate.rounds"},
		{"rounds 1", func(r *Request) { r.Debate.Rounds = 1 }, ""},
		{"rounds 5", func(r *Request) { r.Debate.Rounds = 5 }, ""},
		{"rounds 6", func(r *Request) { r.Debate.Rounds = 6 }, "debate.rounds"},
		{"timeout 299", func(r *Request) { r.Debate.TurnTimeoutS = 299 }, "debate.turn_timeout_s"},
		{"timeout 300", func(r *Request) { r.Debate.TurnTimeoutS = 300 }, ""},
		{"timeout 86400", func(r *Request) { r.Debate.TurnTimeoutS = 86400 }, ""},
		{"timeout 86401", func(r *Request) { r.Debate.TurnTimeoutS = 86401 }, "debate.turn_timeout_s"},
		{"requested_grant", func(r *Request) { r.RequestedGrant = &RequestedGrant{Action: "fs.read", Resource: "x"} }, "requested_grant"},
		{"run", func(r *Request) { r.Run = &Run{Command: "test"} }, "run"},
		{"context", func(r *Request) { r.Context = []ContextFile{{Name: "a.go", Text: "package a"}} }, ""},
		{"topic C1 CSI", func(r *Request) { r.Brief = "a\u009bb" }, "brief"},
		{"topic C1 first", func(r *Request) { r.Brief = "a\u0080b" }, "brief"},
		{"topic U+2028", func(r *Request) { r.Brief = "a\u2028b" }, "brief"},
		{"topic U+2029", func(r *Request) { r.Brief = "a\u2029b" }, "brief"},
		{"topic ESC", func(r *Request) { r.Brief = "a\x1bb" }, "brief"},
		{"topic tab and newline", func(r *Request) { r.Brief = "a\tb\nc" }, ""},
	}
	for _, tc := range cases {
		r := validDebate()
		tc.mutate(r)
		err := Validate(r)
		if tc.field == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != tc.field {
			t.Errorf("%s: err = %v, want field %s", tc.name, err, tc.field)
		}
	}
	// A Phase 2 type keeps its Phase 2 topic rules (C1 allowed in a task brief).
	r := validRequest()
	r.Brief = "a\u009bb"
	if err := Validate(r); err != nil {
		t.Errorf("task brief with C1: %v", err)
	}
}

func TestDebateCanonicalAndDecode(t *testing.T) {
	r := validDebate()
	canon, err := Canonical(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(canon), `"debate":{"commitment":"`+testCommitment+`","rounds":2,"turn_timeout_s":3600}`) {
		t.Fatalf("canonical: %s", canon)
	}
	v, err := agentcard.ParseStrict(canon)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(v.(map[string]any))
	if err != nil {
		t.Fatal(err)
	}
	if *back.Debate != *r.Debate {
		t.Fatalf("round trip %+v", back.Debate)
	}
	for name, mutate := range map[string]func(d map[string]any){
		"extra member":    func(d map[string]any) { d["nonce"] = "x" },
		"missing rounds":  func(d map[string]any) { delete(d, "rounds") },
		"rounds a string": func(d map[string]any) { d["rounds"] = "2" },
		"null commitment": func(d map[string]any) { d["commitment"] = nil },
		"missing timeout": func(d map[string]any) { delete(d, "turn_timeout_s") },
		"member not object": func(d map[string]any) {
			for k := range d {
				delete(d, k)
			}
			d["x"] = 1
		},
	} {
		m := clone(t, v).(map[string]any)
		mutate(m["debate"].(map[string]any))
		if _, err := Decode(m); err == nil {
			t.Errorf("%s: Decode accepted", name)
		}
	}
	m := clone(t, v).(map[string]any)
	m["debate"] = nil
	if _, err := Decode(m); err == nil {
		t.Error("null debate member accepted")
	}
	// A debate with context has the question cap.
	d := validDebate()
	d.Context = []ContextFile{{Name: "a", Text: strings.Repeat("a", MaxRequestBody)}}
	c, err := Canonical(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckSizeFor(d, c); err != nil {
		t.Errorf("debate with context over 64 KiB: %v", err)
	}
}

func clone(t *testing.T, v any) any {
	t.Helper()
	b, err := agentcard.CanonicalValue(v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := agentcard.ParseStrict(b)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type nopDebates struct{ received, ended int }

func (n *nopDebates) ReceivedTx(context.Context, *sql.Tx, *Request, time.Time) error {
	n.received++
	return nil
}

func (n *nopDebates) EndedTx(context.Context, *sql.Tx, string, string, string, time.Time) error {
	n.ended++
	return nil
}

// On receipt, a Phase 2-shaped debate body (type debate without the member,
// or a stray member on another type) is bad_body and stores nothing; so is
// any debate at a daemon without debates wired (what a Phase 2 daemon
// answers). A valid one reaches the debate hook in the receive transaction.
func TestDebateReceivePath(t *testing.T) {
	now := time.Now()
	for name, req := range map[string]*Request{
		"type debate without the member": func() *Request { r := validDebate(); r.Debate = nil; return r }(),
		"stray member on a task":         func() *Request { r := validDebate(); r.Type = TypeTask; return r }(),
		"C1 in the topic":                func() *Request { r := validDebate(); r.Brief = "x\u0085y"; return r }(),
	} {
		s, _, _ := newTestStore(t, testTo, &policy{})
		hooks := &nopDebates{}
		s.Debates = hooks
		req.Created = now.UTC().Truncate(time.Second).Add(-time.Minute)
		if err := deliverRequest(t, s, req, now); !errors.Is(err, mail.ErrBadBody) {
			t.Errorf("%s: err = %v, want bad_body", name, err)
		}
		if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests`); n != 0 || hooks.received != 0 {
			t.Errorf("%s: %d rows stored, hook called %d", name, n, hooks.received)
		}
	}
	s, _, _ := newTestStore(t, testTo, &policy{})
	req := validDebate()
	req.Created = now.UTC().Truncate(time.Second).Add(-time.Minute)
	if err := deliverRequest(t, s, req, now); !errors.Is(err, mail.ErrBadBody) {
		t.Errorf("debate without hooks: err = %v, want bad_body", err)
	}
	s, _, _ = newTestStore(t, testTo, &policy{})
	hooks := &nopDebates{}
	s.Debates = hooks
	if err := deliverRequest(t, s, req, now); err != nil || hooks.received != 1 {
		t.Fatalf("valid debate: %v, hook %d", err, hooks.received)
	}
	if n := countRows(t, s.DB, `SELECT COUNT(*) FROM requests WHERE type = 'debate' AND state = 'pending'`); n != 1 {
		t.Fatalf("stored %d", n)
	}
}
