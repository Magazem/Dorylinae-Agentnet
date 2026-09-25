package debate

// The turn engine (Docs/protocol/debate.md §Turns). Every rule depends on the
// transcript only, so the initiator (authoritative) and the respondent
// (mirror) compute the same next slot from the same entries.

// Roles of a debate's two sides (debates.role, debate_entries.author).
const (
	RoleInitiator  = "initiator"
	RoleRespondent = "respondent"
)

// Phases (debates.phase, §State).
const (
	PhaseInvited   = "invited"
	PhasePositions = "positions"
	PhaseRounds    = "rounds"
	PhaseConverge  = "converge"
	PhaseClosing   = "closing"
	PhaseClosed    = "closed"
	PhaseBroken    = "broken"
)

// MaxSlot is the highest slot: 2 positions, 2 x 5 moves, a proposal and an
// answer (§Size caps, "Entries per debate").
const MaxSlot = 13

// Meta is what the turn engine needs of one entry of the transcript.
type Meta struct {
	Author     string
	Kind       string
	Challenges int // moves only: len(challenges)
}

// Turn is the next slot to fill. Done means the answer is in: nothing but
// the close follows.
type Turn struct {
	Phase  string // PhasePositions, PhaseRounds or PhaseConverge; "" when Done
	Slot   int
	Kind   string
	Author string
	Round  int // the move's round (1-based); 0 outside PhaseRounds
	Done   bool
}

// Next computes the next turn from the entries present, keyed by slot. On the
// initiator these are the applied entries; on the respondent the applied
// ones and its own sent ones (a sent entry counts as applied for turn
// purposes, §Submitting entries step 5). rounds is the debate's maximum.
//
// Order: slot 1 (the respondent's position), then slot 0 (the initiator's
// revealed position), then moves from slot 2 (round k: the initiator at 2k,
// the respondent at 2k+1), then the proposal (initiator) and the answer
// (respondent). After each move the next slot is the proposal when the last
// two moves both have no challenges (rule 1), or when the respondent's move of
// round `rounds` is in (rule 2).
func Next(entries map[int]Meta, rounds int) Turn {
	if _, ok := entries[1]; !ok {
		return Turn{Phase: PhasePositions, Slot: 1, Kind: KindPosition, Author: RoleRespondent}
	}
	if _, ok := entries[0]; !ok {
		return Turn{Phase: PhasePositions, Slot: 0, Kind: KindPosition, Author: RoleInitiator}
	}
	s := 2
	prevEmpty, havePrev := false, false
	for {
		e, ok := entries[s]
		if !ok {
			return Turn{Phase: PhaseRounds, Slot: s, Kind: KindMove, Author: moveAuthor(s), Round: s / 2}
		}
		empty := e.Challenges == 0
		if (havePrev && prevEmpty && empty) || (s%2 == 1 && s/2 >= rounds) {
			break
		}
		prevEmpty, havePrev = empty, true
		s++
	}
	p := s + 1
	if _, ok := entries[p]; !ok {
		return Turn{Phase: PhaseConverge, Slot: p, Kind: KindProposal, Author: RoleInitiator}
	}
	if _, ok := entries[p+1]; !ok {
		return Turn{Phase: PhaseConverge, Slot: p + 1, Kind: KindAnswer, Author: RoleRespondent}
	}
	return Turn{Slot: p + 2, Done: true}
}

// moveAuthor is the author of the move in slot s: the initiator moves first
// in every round.
func moveAuthor(s int) string {
	if s%2 == 0 {
		return RoleInitiator
	}
	return RoleRespondent
}

// RoundsUsed is the number of the current or last round a transcript
// reached (0 before the first move), for views.
func RoundsUsed(entries map[int]Meta) int {
	n := 0
	for s, e := range entries {
		if e.Kind == KindMove && s/2 > n {
			n = s / 2
		}
	}
	return n
}
