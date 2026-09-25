// Package worksession implements the work session object of
// Docs/protocol/work-session.md: the bounded, persisted piece of work an
// accepted request becomes. Its id starts with "s-", its mail kinds and
// audit actions start with "ws.". Change that document first.
//
// The work session is authoritative on the requester (A) and mirrored on
// the worker (B); both roles share the work_sessions table (migration 14).
// This package depends on internal/request (it reuses request.ValidateComplete
// and calls request.Store.CompleteInTx when a session closes), so
// internal/request cannot depend back on it: the reverse hooks it needs
// (opening a session on accept, the early-complete and request_complete
// shorthand paths) are declared as interfaces in internal/request and
// satisfied here.
package worksession

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

// States (Docs/protocol/work-session.md §State machine).
const (
	StateOpen           = "open"
	StateAwaitingResult = "awaiting_result"
	StateQuarantined    = "quarantined"
	StateClosed         = "closed"
)

// Outcomes, set only when state is closed.
const (
	OutcomeAccepted  = "accepted"
	OutcomeCancelled = "cancelled"
)

// Session kinds (migration 19, Docs/protocol/debate.md §Model): a debate's
// session carries no result, grant or change request.
const (
	SessionKindWork   = "work"
	SessionKindDebate = "debate"
)

// Roles: which side of the session this daemon plays.
const (
	RoleRequester = "requester"
	RoleWorker    = "worker"
)

// Mail kinds (Docs/protocol/work-session.md §Kinds).
const (
	KindResult = "ws.result"
	KindState  = "ws.state"
	KindCancel = "ws.cancel"
)

// Verification values (Docs/protocol/work-session.md §Result object (2.6),
// §Accept-result).
const (
	VerificationNone          = "none"
	VerificationTestsPassed   = "tests_passed"
	VerificationHumanAccepted = "human_accepted"
)

// idTag is the session id derivation tag (Docs/protocol/work-session.md
// §Session id).
const idTag = "dorylinae-ws-id-v1\n"

var idPattern = regexp.MustCompile(`^s-[0-9a-f]{32}$`)

// ValidID reports whether s has the session id format.
func ValidID(s string) bool { return idPattern.MatchString(s) }

// DeriveID returns the session id for a request from a to b
// (Docs/protocol/work-session.md §Session id):
//
//	sid = "s-" ++ lowercase-hex( SHA-256("dorylinae-ws-id-v1\n" ++ a ++ "\n" ++ b ++ "\n" ++ requestID)[0:16] )
func DeriveID(a, b, requestID string) string {
	h := sha256.New()
	h.Write([]byte(idTag))
	h.Write([]byte(a))
	h.Write([]byte("\n"))
	h.Write([]byte(b))
	h.Write([]byte("\n"))
	h.Write([]byte(requestID))
	sum := h.Sum(nil)
	return "s-" + hex.EncodeToString(sum[:16])
}
