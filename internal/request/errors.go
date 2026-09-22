package request

import "fmt"

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
