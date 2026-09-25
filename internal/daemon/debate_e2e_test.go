package daemon_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/store"
)

// debatePair starts two paired daemons sharing a team, each exposing its
// debate store (daemon.Options.OnDebateReady): until 3.1b adds debate_submit,
// entries are driven through the store.
func debatePair(t *testing.T) (a, b *harnessNode, teamID string, aDS, bDS *atomic.Pointer[debate.Store]) {
	t.Helper()
	r := newHarnessRelay(t)
	a, b = newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	aDS, bDS = &atomic.Pointer[debate.Store]{}, &atomic.Pointer[debate.Store]{}
	a.OnDebateReady = func(s *debate.Store) { aDS.Store(s) }
	b.OnDebateReady = func(s *debate.Store) { bDS.Store(s) }
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	return a, b, harnessSharedTeam(t, a, b, "x"), aDS, bDS
}

func e2ePosition(claim string) json.RawMessage {
	return json.RawMessage(`{"argument":"Because.\nReally.","claim":"` + claim + `"}`)
}

func phaseIs(n *harnessNode, sid, phase string) func() bool {
	return func() bool {
		return n.count(`SELECT COUNT(*) FROM debates WHERE session = '`+sid+`' AND phase = '`+phase+`'`) == 1
	}
}

func nextIs(n *harnessNode, sid string, slot int) func() bool {
	return func() bool {
		var s int
		return n.query(`SELECT next_slot FROM debates WHERE session = '`+sid+`'`, &s) == nil && s == slot
	}
}

func dsSubmit(t *testing.T, p *atomic.Pointer[debate.Store], sid, kind, entry string) {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(entry), &v); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load().Submit(context.Background(), sid, "", kind, v); err != nil {
		t.Fatalf("Submit %s: %v", kind, err)
	}
}

// A debate end to end over the relay (ticket 3.1a): request_submit with the
// debate member and its refusals, the idempotent retry (review 43 L11), B's
// one-step accept + position, the automatic reveal, a round of passes (rule
// 1), the proposal and an accepting answer; A reaches closing, B closes its
// mirror and completes the request, and both transcripts hold the same bytes.
// mail_submit cannot send debate kinds, and a sensitive grant to the peer
// blocks the next start (quarantine_active).
func TestDebateE2E(t *testing.T) {
	a, b, teamID, aDS, bDS := debatePair(t)

	base := func() daemon.RequestSubmitParams {
		return daemon.RequestSubmitParams{To: b.key, Type: "debate", Team: teamID, Title: "Retries", Brief: "How should the outbox retry?",
			Debate: &daemon.DebateParam{Position: e2ePosition("Capped backoff"), Rounds: 1}}
	}
	for name, mutate := range map[string]func(p *daemon.RequestSubmitParams){
		"debate member on a task": func(p *daemon.RequestSubmitParams) { p.Type = "task" },
		"debate without a member": func(p *daemon.RequestSubmitParams) { p.Debate = nil },
		"requested_grant": func(p *daemon.RequestSubmitParams) {
			p.RequestedGrant = &daemon.GrantParam{Action: "fs.read", Resource: "x"}
		},
		"run":                      func(p *daemon.RequestSubmitParams) { p.Run = &daemon.RunParam{Command: "test"} },
		"bad position":             func(p *daemon.RequestSubmitParams) { p.Debate.Position = e2ePosition("two\\nlines") },
		"rounds 6":                 func(p *daemon.RequestSubmitParams) { p.Debate.Rounds = 6 },
		"U+2028 in the topic":      func(p *daemon.RequestSubmitParams) { p.Brief = "a\u2028b" },
		"position not an object":   func(p *daemon.RequestSubmitParams) { p.Debate.Position = json.RawMessage(`"x"`) },
		"turn timeout below 300 s": func(p *daemon.RequestSubmitParams) { p.Debate.TurnTimeoutS = 299 },
	} {
		p := base()
		mutate(&p)
		if code, msg := callCode(t, a, "request_submit", p); code != "bad_request" {
			t.Errorf("%s: code %q (%s), want bad_request", name, code, msg)
		}
	}
	if n := a.count(`SELECT COUNT(*) FROM requests`) + a.count(`SELECT COUNT(*) FROM debates`); n != 0 {
		t.Fatalf("refused submits left %d rows", n)
	}

	p := base()
	p.IdempotencyKey = "debate-1"
	var res, again daemon.RequestSubmitResult
	a.call("request_submit", p, &res)
	a.call("request_submit", p, &again)
	if !again.Duplicate || again.ID != res.ID || again.Session != res.Session {
		t.Fatalf("idempotent retry: %+v vs %+v", again, res)
	}
	p.Debate.Position = e2ePosition("Something else")
	if code, _ := callCode(t, a, "request_submit", p); code != "idempotency_conflict" {
		t.Fatalf("retry with another position: %q, want idempotency_conflict", code)
	}
	if n := a.count(`SELECT COUNT(*) FROM debates`); n != 1 {
		t.Fatalf("%d debates on A", n)
	}
	sid := res.Session
	harnessWait(t, "B to store the debate", phaseIs(b, sid, debate.PhaseInvited))
	if n := b.count(`SELECT COUNT(*) FROM requests WHERE instr(body, 'Capped backoff') > 0`); n != 0 {
		t.Fatal("B's request holds A's position")
	}

	dsSubmit(t, bDS, sid, debate.KindPosition, `{"argument":"Simple.","claim":"Fixed retry"}`)
	harnessWait(t, "A to apply B's position and reveal", phaseIs(a, sid, debate.PhaseRounds))
	harnessWait(t, "B to apply the reveal", phaseIs(b, sid, debate.PhaseRounds))
	dsSubmit(t, aDS, sid, debate.KindMove, `{"challenges":[]}`)
	harnessWait(t, "B to apply A's move", nextIs(b, sid, 3))
	dsSubmit(t, bDS, sid, debate.KindMove, `{"challenges":[]}`)
	harnessWait(t, "A to converge", phaseIs(a, sid, debate.PhaseConverge))
	dsSubmit(t, aDS, sid, debate.KindProposal, `{"agreement":{"decision":"Capped backoff with jitter"}}`)
	harnessWait(t, "B to apply the proposal", nextIs(b, sid, 5))
	dsSubmit(t, bDS, sid, debate.KindAnswer, `{"accept":true}`)
	harnessWait(t, "A to close", phaseIs(a, sid, debate.PhaseClosing))
	harnessWait(t, "B to close", phaseIs(b, sid, debate.PhaseClosed))
	harnessWait(t, "B's request to complete", func() bool {
		return b.count(`SELECT COUNT(*) FROM requests WHERE direction = 'in' AND id = '`+res.ID+`' AND state = 'completed' AND note = 'debate agreed'`) == 1
	})
	harnessWait(t, "A's request to complete", func() bool {
		return a.count(`SELECT COUNT(*) FROM requests WHERE direction = 'out' AND id = '`+res.ID+`' AND state = 'completed'`) == 1
	})
	if n := a.count(`SELECT COUNT(*) FROM work_sessions WHERE id = '` + sid + `' AND kind = 'debate' AND state = 'closed' AND outcome = 'accepted'`); n != 1 {
		t.Fatal("A's debate session is not closed accepted")
	}
	va, err := aDS.Load().Get(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	vb, err := bDS.Load().Get(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(va.Transcript) != 6 || len(vb.Transcript) != 6 || vb.Outcome != debate.OutcomeAgreed {
		t.Fatalf("A %d entries, B %d entries, B outcome %q", len(va.Transcript), len(vb.Transcript), vb.Outcome)
	}
	for i := range va.Transcript {
		if string(va.Transcript[i].Entry) != string(vb.Transcript[i].Entry) || va.Transcript[i].At != vb.Transcript[i].At {
			t.Errorf("slot %d differs", va.Transcript[i].Slot)
		}
	}
	for _, n := range []*harnessNode{a, b} {
		if c := n.count(`SELECT COUNT(*) FROM audit_events WHERE instr(detail, 'Capped') > 0 OR instr(detail, 'Fixed retry') > 0 OR instr(detail, 'jitter') > 0`); c != 0 {
			t.Errorf("%s: %d audit rows hold debate content", n.name, c)
		}
	}
	if n := a.count(`SELECT COUNT(*) FROM audit_events WHERE action IN ('debate.start', 'debate.reveal', 'debate.close', 'debate.entry_in')`); n < 4 {
		t.Errorf("A audited %d debate rows", n)
	}

	// mail_submit never sends the daemon's debate kinds (review 36 L7).
	for _, kind := range []string{debate.MailEntry, debate.MailReveal, debate.MailClose, "debate.x", "decision.x"} {
		if code, _ := callCode(t, a, "mail_submit", daemon.MailSubmitParams{To: b.key, Kind: kind, Body: []byte(`{}`)}); code != "bad_request" {
			t.Errorf("mail_submit %s: %q, want bad_request", kind, code)
		}
	}

	// The peer-wide quarantine clause from B's side blocks B's accept
	// (request_accept), and from A's side A's next start (OD-P3-4).
	var res2 daemon.RequestSubmitResult
	a.call("request_submit", base(), &res2)
	harnessWait(t, "B to store the second debate", phaseIs(b, res2.Session, debate.PhaseInvited))
	seedGrant(t, b, a.key)
	if code, msg := callCode(t, b, "request_accept", map[string]string{"id": res2.ID}); code != "quarantine_active" {
		t.Fatalf("accept under the quarantine clause: %q (%s)", code, msg)
	}
	if n := b.count(`SELECT COUNT(*) FROM requests WHERE id = '` + res2.ID + `' AND state = 'pending'`); n != 1 {
		t.Fatal("the refused accept changed the request")
	}
	seedGrant(t, a, b.key)
	if code, msg := callCode(t, a, "request_submit", base()); code != "quarantine_active" {
		t.Fatalf("start under the quarantine clause: %q (%s)", code, msg)
	}
}

// seedGrant stores on n an issued, active, sensitive grant to peer that
// expires in an hour: the rule-2 test holds for peer from n's side.
func seedGrant(t *testing.T, n *harnessNode, peer string) {
	t.Helper()
	st, err := store.Open(context.Background(), n.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	exp := time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	if _, err := st.DB().Exec(`INSERT INTO grants (id, direction, peer, session, action, label, sensitive, nbf, exp, token, state, approval, created, updated)
		VALUES ('g-00000000000000000000000000000001', 'issued', ?, 's-00000000000000000000000000000000', 'fs.read', 'x', 1, ?, ?, '{}', 'active', 'a-1', ?, ?)`,
		peer, exp, exp, exp, exp); err != nil {
		t.Fatal(err)
	}
}
