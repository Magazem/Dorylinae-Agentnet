// Package debate holds the debate entry schemas of Docs/protocol/debate.md
// §Messages (3.2): the typed entries, their strict decoders and validators,
// target parsing and resolution, and the canonical encoding with its size
// cap. Both sides call the same functions: the sender at IPC (errors map to
// bad_request naming Field, or entry_too_large) and the recipient in Apply
// (every error maps to bad_body).
package debate

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// Entry kinds (Docs/protocol/debate.md §Turns).
const (
	KindPosition = "position"
	KindMove     = "move"
	KindProposal = "proposal"
	KindAnswer   = "answer"
)

// MaxDebateEntry caps len(canonical(entry)) (Docs/protocol/debate.md §Size caps).
const MaxDebateEntry = 32768

// Field caps (Docs/protocol/debate.md §Position ... §Disagreement).
const (
	maxLine         = 280  // claim, assumption, note, reason, decision, point, views
	maxOption       = 120  // rejected_alternatives[].option
	maxRef          = 1024 // evidence[].ref
	maxArgument     = 4000 // every argument
	maxConstraint   = 500  // constraint text (§Human constraints)
	maxAssumptions  = 10
	maxEvidence     = 10 // on a position
	maxChallengeEv  = 5  // on a challenge
	maxRejected     = 5
	maxTargets      = 5
	maxChallenges   = 3
	maxPoints       = 10
	maxDisagreement = 10
	maxArtifacts    = 20
	minTopicBytes   = 1
	maxTopicBytes   = 16384
)

// evidenceKinds are the allowed evidence[].kind values (§Evidence).
var evidenceKinds = map[string]bool{
	"file": true, "commit": true, "url": true, "test": true, "doc": true, "measurement": true,
}

// FieldError names the entry member that broke a rule, as a path relative to
// the entry ("claim", "evidence[2].ref", "revision.assumptions[0]",
// "challenges[1].targets[0]"). It is the request package's type so callers
// map both the same way.
type FieldError = request.FieldError

func fieldErr(field, format string, a ...any) *FieldError {
	return &FieldError{Field: field, Reason: fmt.Sprintf(format, a...)}
}

// TooLargeError is a canonical entry over MaxDebateEntry. The sender maps it to
// entry_too_large, the recipient to bad_body.
type TooLargeError struct {
	Size  int
	Limit int
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("canonical entry is %d bytes, over the %d byte limit", e.Size, e.Limit)
}

// ErrNotCanonical is an entry whose bytes are not its own canonical encoding
// (ParseEntry).
var ErrNotCanonical = errors.New("debate: entry is not canonical JSON")

// Entry is one of *Position, *Move, *Proposal, *Answer.
type Entry interface {
	Kind() string
	value() map[string]any
}

// Evidence is a pointer, never fetched (§Evidence). Note is "" when absent.
type Evidence struct {
	Kind string
	Ref  string
	Note string
}

// Alternative is one rejected_alternatives item.
type Alternative struct {
	Option string
	Reason string
}

// Position is an opening position or a revision (§Position). Empty slices
// mean the optional member is absent.
type Position struct {
	Claim                string
	Assumptions          []string
	Evidence             []Evidence
	RejectedAlternatives []Alternative
	Argument             string
}

// Challenge attacks items of the other side's current position (§Challenge).
// Targets hold the wire form ("claim", "evidence/0", ...).
type Challenge struct {
	Targets  []string
	Argument string
	Evidence []Evidence
}

// Move is one round's entry (§Move). No challenges and no revision is a pass.
type Move struct {
	Challenges []Challenge
	Revision   *Position
}

// Agreement is a proposal's agreement member. Argument is "" when absent.
type Agreement struct {
	Decision string
	Points   []string
	Argument string
}

// Disagreement is one remaining point with each side's view (§Disagreement).
type Disagreement struct {
	Point      string
	Initiator  string
	Respondent string
}

// Proposal is A's converge entry (§Proposal).
type Proposal struct {
	Agreement             Agreement
	RemainingDisagreement []Disagreement
	AffectedArtifacts     []request.Artifact
}

// Answer is B's converge entry (§Answer). Argument is "" when absent.
type Answer struct {
	Accept                bool
	RemainingDisagreement []Disagreement
	Argument              string
}

// Kind returns KindPosition.
func (*Position) Kind() string { return KindPosition }

// Kind returns KindMove.
func (*Move) Kind() string { return KindMove }

// Kind returns KindProposal.
func (*Proposal) Kind() string { return KindProposal }

// Kind returns KindAnswer.
func (*Answer) Kind() string { return KindAnswer }

// ValidKind reports whether kind is one of the four entry kinds.
func ValidKind(kind string) bool {
	switch kind {
	case KindPosition, KindMove, KindProposal, KindAnswer:
		return true
	}
	return false
}

func member(base, name string) string {
	if base == "" {
		return name
	}
	return base + "." + name
}

func index(base string, i int) string { return base + "[" + strconv.Itoa(i) + "]" }
