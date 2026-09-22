package request

import (
	"errors"
	"fmt"
)

// FieldError names the request field that failed a rule in Docs/protocol/request.md
// §Request object or §Size limits. The sender maps this to `bad_request` with
// Field as the message's field name; the recipient maps any FieldError (and
// any other structural failure) to `bad_body`.
type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }

func fieldErr(field, format string, a ...any) *FieldError {
	return &FieldError{Field: field, Reason: fmt.Sprintf(format, a...)}
}

// TooLargeError is len(canonical(request)) over MaxRequestBody. The sender
// maps this to `request_too_large`; the recipient maps it to `bad_body`.
type TooLargeError struct {
	Size  int
	Limit int
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("canonical request is %d bytes, over the %d byte limit", e.Size, e.Limit)
}

// TooLargeCompleteError is len(canonical(complete body)) over MaxCompleteBody
// (Docs/protocol/request.md §Result payload (D14)). The recipient IPC maps
// this to `result_too_large`; the sender mirror maps it to `bad_body`.
type TooLargeCompleteError struct {
	Size  int
	Limit int
}

func (e *TooLargeCompleteError) Error() string {
	return fmt.Sprintf("canonical complete body is %d bytes, over the %d byte limit", e.Size, e.Limit)
}

// BadStateError is a lifecycle action refused because of the row's current
// state (Docs/protocol/request.md §State machine, §Cancel (OD-P1-11)). The
// caller maps this to IPC `bad_state`.
type BadStateError struct {
	State string // the row's current state
	Msg   string
}

func (e *BadStateError) Error() string { return e.Msg }

// ErrUnknownRequest is returned when no row matches the given id (and from,
// if given). The caller maps this to IPC `unknown_request`.
var ErrUnknownRequest = errors.New("request: unknown request")

// ErrAmbiguousRequest is returned when an id alone matches in rows from
// several peers. The caller maps this to IPC `ambiguous_request`.
var ErrAmbiguousRequest = errors.New("request: id matches requests from several peers; pass from")
