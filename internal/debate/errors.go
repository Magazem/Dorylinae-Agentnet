package debate

import (
	"errors"
	"fmt"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// Errors of the IPC-facing methods, mapped by the daemon to the codes of
// Docs/protocol/debate.md §IPC.
var (
	// ErrUnknownDebate: no debate with that session or request id
	// (unknown_session).
	ErrUnknownDebate = errors.New("debate: unknown debate")
	// ErrQuarantineActive: the peer-wide quarantine clause holds for the
	// peer from this side (quarantine_active, §Quarantine interplay).
	ErrQuarantineActive = errors.New("debate: a sensitive grant to this peer is less than 7 days past its expiry")
)

// BadStateError refuses an action because of the debate's phase (bad_state).
type BadStateError struct {
	Phase string
	Msg   string
}

func (e *BadStateError) Error() string { return e.Msg }

// NotYourTurnError is an entry for a slot that is not the caller's
// (not_your_turn): Expect and Author name the next slot's kind and author.
type NotYourTurnError struct {
	Slot   int
	Expect string
	Author string
}

func (e *NotYourTurnError) Error() string {
	return fmt.Sprintf("not your turn: slot %d expects a %s from the %s", e.Slot, e.Expect, e.Author)
}

func badBody(format string, a ...any) error {
	return fmt.Errorf("debate: "+format+": %w", append(a, mail.ErrBadBody)...)
}
