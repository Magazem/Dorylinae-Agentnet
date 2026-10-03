package daemon

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/debate"
	"github.com/Magazem/Dorylinae-Agentnet/internal/ipc"
)

// R55-F29 test 3 (R55-116): params_hash leaves out absent values, so every
// valid submit hashes the same bytes as before the change (golden values
// computed by the old code), and an explicit default differs from absent.
func TestDebateParamsHashAcrossTheChange(t *testing.T) {
	pos := map[string]any{"claim": "c", "argument": "a"}
	p := RequestSubmitParams{Type: "debate", Title: "t", Brief: "b", Debate: &DebateParam{Position: json.RawMessage(`{}`)}}
	const (
		goldenAbsent  = "32c4b2a4efcec432ced9c98881d36a3e447de27cc8aae6ba252c1b532c60c42c"
		goldenRounds3 = "2cfedf7c111d28c48ff979b4832cb70eb12d8d6873d13a92489f3aca3f17cadb"
	)
	if got := submitParamsHash("key", "team-x", p, pos); got != goldenAbsent {
		t.Errorf("absent: %s, want %s", got, goldenAbsent)
	}
	p.Debate.Rounds = new(3)
	if got := submitParamsHash("key", "team-x", p, pos); got != goldenRounds3 {
		t.Errorf("rounds 3: %s, want %s", got, goldenRounds3)
	}
	p.Debate.Rounds = new(debate.DefaultRounds)
	if got := submitParamsHash("key", "team-x", p, pos); got == goldenAbsent {
		t.Error("an explicit default hashes like an absent value")
	}
}

// R55-F29 test 9 / plan 14: ws_cancel maps a debate hook's refusal (B
// abandoning after its answer) to bad_state, not internal.
func TestSessionErrorMapsDebateBadState(t *testing.T) {
	err := sessionError(&debate.BadStateError{Phase: debate.PhaseConverge, Msg: "you answered: wait"})
	var ie *ipc.Error
	if !errors.As(err, &ie) || ie.Code != CodeBadState || ie.Message != "you answered: wait" {
		t.Fatalf("sessionError = %v, want bad_state", err)
	}
}
