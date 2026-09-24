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
	// MaxQuestionBody is the cap on len(canonical(request)) for a question
	// with context files (Docs/protocol/consult.md §Size limits, 320 KiB).
	MaxQuestionBody = 327680

	minContextFiles     = 1
	maxContextFiles     = 8
	minContextNameBytes = 1
	maxContextNameBytes = 255
	minContextTextBytes = 1
	maxContextTextBytes = 65536

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

// ContextFile is one context file of a question (Docs/protocol/consult.md
// §Context files).
type ContextFile struct {
	Name string
	Text string
}

// ContextBytes is the total size of the text of files, the "context_bytes"
// of the audit and the inbox list view.
func ContextBytes(files []ContextFile) int {
	n := 0
	for _, f := range files {
		n += len(f.Text)
	}
	return n
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
	Context         []ContextFile // nil if absent; question only
	Run             *Run          // nil if absent; own-device helper (Docs/protocol/device.md)
}

// Run is the request's "run" member (Docs/protocol/device.md §Running): the
// name of a command an own-device helper may have configured. It is a name
// only: no argument, path or environment ever comes from a request.
type Run struct {
	Command string
}

// ValidRunCommand reports whether s is a valid run.command: 1-64 characters
// from [a-z0-9._-] (Docs/protocol/device.md §Running).
func ValidRunCommand(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}
