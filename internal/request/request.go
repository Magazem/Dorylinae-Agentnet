// Package request implements the request object of Docs/protocol/request.md:
// its fields, size limits, canonical form, body_hash and effective priority.
// This package is pure validation and encoding. Submitting, receiving and
// lifecycle (the daemon, outbox and IPC) are 1.4c and later; change
// Docs/protocol/request.md first.
package request

import "time"

// Size and count limits from Docs/protocol/request.md §Size limits.
const (
	// MaxRequestBody is the cap on len(canonical(request)) (65536 bytes).
	MaxRequestBody = 65536

	minTitleCodePoints = 1
	maxTitleCodePoints = 120

	minBriefBytes = 1
	maxBriefBytes = 16384

	minUrgencyReasonCodePoints = 1
	maxUrgencyReasonCodePoints = 280

	minArtifacts = 0
	maxArtifacts = 20

	minURLBytes    = 1
	maxURLBytes    = 2048
	minBranchBytes = 1
	maxBranchBytes = 255
	minCommitHex   = 7
	maxCommitHex   = 64
	minPathBytes   = 1
	maxPathBytes   = 1024

	minGrantActionChars    = 1
	maxGrantActionChars    = 64
	minGrantResourceBytes  = 1
	maxGrantResourceBytes  = 512
	minGrantNoteCodePoints = 1
	maxGrantNoteCodePoints = 280
)

// Urgency values, in ascending order. Urgency.base below relies on this order.
const (
	UrgencyLow      = "low"
	UrgencyNormal   = "normal"
	UrgencyHigh     = "high"
	UrgencyBlocking = "blocking"
)

// Request types (kind `request`, member `type`).
const (
	TypeReview   = "review"
	TypeTask     = "task"
	TypeQuestion = "question"
)

// timeFmt is the wire time format: RFC 3339 UTC, "Z", whole seconds.
const timeFmt = "2006-01-02T15:04:05Z"

// Artifact is one pointer to code, with at least one member set.
// Docs/protocol/request.md §Artifacts.
type Artifact struct {
	URL    string // "" means absent
	Branch string
	Commit string
	Path   string
}

// RequestedGrant is the informational access hint. Docs/protocol/request.md
// §Requested grant.
type RequestedGrant struct {
	Action   string
	Resource string
	Note     string // "" means absent
}

// Request is the body of kind `request`: Docs/protocol/request.md §Request
// object. Optional members absent on the wire are the zero value here
// (empty string, nil slice/pointer, zero time.Time).
type Request struct {
	V               int
	ID              string
	From            string
	To              string
	Team            string
	Type            string
	Title           string
	Brief           string
	Urgency         string
	UrgencyDeclared string // "" if absent
	UrgencyReason   string // "" if absent
	Artifacts       []Artifact
	RequestedGrant  *RequestedGrant // nil if absent
	Deadline        time.Time       // zero if absent
	Created         time.Time
}
