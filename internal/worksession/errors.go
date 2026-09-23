package worksession

import (
	"errors"
	"fmt"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// FieldError names the result field that failed a rule in
// Docs/protocol/work-session.md §Result object (2.6). The sender maps this
// to bad_request; the recipient maps any FieldError to bad_body.
type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }

func fieldErr(field, format string, a ...any) *FieldError {
	return &FieldError{Field: field, Reason: fmt.Sprintf(format, a...)}
}

// TooLargeResultError is len(canonical(ws.result body)) over MaxResultBody.
type TooLargeResultError struct {
	Size  int
	Limit int
}

func (e *TooLargeResultError) Error() string {
	return fmt.Sprintf("canonical ws.result body is %d bytes, over the %d byte limit", e.Size, e.Limit)
}

// BadStateError is a transition refused because of the session's current
// state (Docs/protocol/work-session.md §Transitions). The caller maps this
// to IPC bad_state.
type BadStateError struct {
	State string
	Msg   string
}

func (e *BadStateError) Error() string { return e.Msg }

// Sentinel errors, mapped by the caller to their IPC codes.
var (
	ErrUnknownSession = errors.New("worksession: unknown session")
	ErrNotRequester   = errors.New("worksession: not the requester")
	ErrNotWorker      = errors.New("worksession: not the worker")
)

func badBody(format string, a ...any) error {
	return fmt.Errorf("worksession: "+format+": %w", append(a, mail.ErrBadBody)...)
}
