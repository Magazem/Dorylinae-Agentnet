package daemon_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
)

// R55-F29 test 1 (R55-116): absent (or null) rounds and turn_timeout_s take
// the defaults; an explicit default is a different params_hash than absent.
// (A present 0 is bad_request in TestDebateE2E's refusal table.)
func TestDebateF29AbsentDefaults(t *testing.T) {
	a, b, teamID, _, _ := debatePair(t)
	submit := func(key string, dbt map[string]any) (string, string) {
		params := map[string]any{"to": b.key, "type": "debate", "team": teamID, "title": "Retries",
			"brief": "How should the outbox retry?", "idempotency_key": key, "debate": dbt}
		code, msg := callCode(t, a, "request_submit", params)
		if code != "" {
			return code, msg
		}
		var res daemon.RequestSubmitResult
		a.call("request_submit", params, &res)
		return "", res.Session
	}
	pos := e2ePosition("Capped backoff")
	for key, dbt := range map[string]map[string]any{
		"absent": {"position": pos},
		"null":   {"position": pos, "rounds": nil, "turn_timeout_s": nil},
	} {
		code, sid := submit(key, dbt)
		if code != "" {
			t.Fatalf("%s: %s", key, sid)
		}
		var rounds, timeout int
		if err := a.query(`SELECT rounds_max, turn_timeout_s FROM debates WHERE session = '`+sid+`'`, &rounds, &timeout); err != nil ||
			rounds != debate.DefaultRounds || timeout != debate.DefaultTurnTimeoutS {
			t.Fatalf("%s: rounds_max %d, turn_timeout_s %d (%v)", key, rounds, timeout, err)
		}
	}
	if code, _ := submit("absent", map[string]any{"position": pos, "rounds": 2}); code != "idempotency_conflict" {
		t.Fatalf("explicit default with absent's key: %q, want idempotency_conflict", code)
	}
}

// R55-F29 test 5 (R55-169): debate_show and debate_list sweep first, with the
// gate ready and A's clock past the deadline, without a sweep tick. On B,
// debate_show past its display deadline changes nothing.
func TestDebateF29ViewsSweepFirst(t *testing.T) {
	r := newHarnessRelay(t)
	a, b := newHarnessNode(t, "alice", r), newHarnessNode(t, "bob", r)
	var aSkew, bSkew atomic.Int64
	aDS, bDS := &atomic.Pointer[debate.Store]{}, &atomic.Pointer[debate.Store]{}
	// OnDebateReady runs before the sweep and IPC start: setting the fields
	// here does not race them.
	a.OnDebateReady = func(s *debate.Store) {
		s.TimeoutsReady = func() bool { return true }
		s.Now = func() time.Time { return time.Now().Add(time.Duration(aSkew.Load())) }
		aDS.Store(s)
	}
	b.OnDebateReady = func(s *debate.Store) {
		s.Now = func() time.Time { return time.Now().Add(time.Duration(bSkew.Load())) }
		bDS.Store(s)
	}
	a.start()
	b.start()
	waitRelayConnected(t, r, a.key, b.key)
	harnessPair(t, a, b)
	teamID := harnessSharedTeam(t, a, b, "x")

	var sids []string
	for _, claim := range []string{"Capped backoff", "Fixed retry"} {
		var res daemon.RequestSubmitResult
		a.call("request_submit", daemon.RequestSubmitParams{To: b.key, Type: "debate", Team: teamID, Title: "Retries",
			Brief: "How should the outbox retry?", Debate: &daemon.DebateParam{Position: e2ePosition(claim), Rounds: new(1)}}, &res)
		sid := res.Session
		harnessWait(t, "B to store the debate", phaseIs(b, sid, debate.PhaseInvited))
		dsSubmit(t, bDS, sid, debate.KindPosition, `{"argument":"Simple.","claim":"B"}`)
		harnessWait(t, "A in rounds", phaseIs(a, sid, debate.PhaseRounds))
		harnessWait(t, "B in rounds", phaseIs(b, sid, debate.PhaseRounds))
		sids = append(sids, sid)
	}

	bSkew.Store(int64(2 * time.Hour))
	var bShow daemon.DebateShowResult
	b.call("debate_show", map[string]any{"id": sids[0]}, &bShow)
	if bShow.Debate.Phase != debate.PhaseRounds {
		t.Fatalf("B debate_show past the deadline: %s, want rounds", bShow.Debate.Phase)
	}

	aSkew.Store(int64(2 * time.Hour))
	var show daemon.DebateShowResult
	a.call("debate_show", map[string]any{"id": sids[0]}, &show)
	if (show.Debate.Phase != debate.PhaseClosing && show.Debate.Phase != debate.PhaseClosed) || show.Debate.Reason != debate.ReasonTimeout {
		t.Fatalf("A debate_show: %s/%s, want closing or closed / timeout", show.Debate.Phase, show.Debate.Reason)
	}
	var list daemon.DebateListResult
	a.call("debate_list", map[string]any{}, &list)
	if len(list.Debates) != 2 {
		t.Fatalf("debate_list: %d debates", len(list.Debates))
	}
	for _, d := range list.Debates {
		if d.Reason != debate.ReasonTimeout {
			t.Fatalf("debate_list %s: %s/%q, want timeout", d.Session, d.Phase, d.Reason)
		}
	}
}
