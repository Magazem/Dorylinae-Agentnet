package debate

import (
	"strconv"
	"strings"

	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// ValidatePosition checks p against Docs/protocol/debate.md §Position. It
// returns the first violation as a *FieldError.
func ValidatePosition(p *Position) error { return validatePosition("", p) }

func validatePosition(base string, p *Position) error {
	if err := checkLine(member(base, "claim"), p.Claim, maxLine); err != nil {
		return err
	}
	if len(p.Assumptions) > maxAssumptions {
		return fieldErr(member(base, "assumptions"), "must hold 1-%d items", maxAssumptions)
	}
	for i, a := range p.Assumptions {
		if err := checkLine(index(member(base, "assumptions"), i), a, maxLine); err != nil {
			return err
		}
	}
	if err := validateEvidence(member(base, "evidence"), p.Evidence, maxEvidence); err != nil {
		return err
	}
	ra := member(base, "rejected_alternatives")
	if len(p.RejectedAlternatives) > maxRejected {
		return fieldErr(ra, "must hold 1-%d items", maxRejected)
	}
	for i, alt := range p.RejectedAlternatives {
		if err := checkLine(member(index(ra, i), "option"), alt.Option, maxOption); err != nil {
			return err
		}
		if err := checkLine(member(index(ra, i), "reason"), alt.Reason, maxLine); err != nil {
			return err
		}
	}
	return checkArgument(member(base, "argument"), p.Argument)
}

func validateEvidence(field string, ev []Evidence, limit int) error {
	if len(ev) > limit {
		return fieldErr(field, "must hold 1-%d items", limit)
	}
	for i, e := range ev {
		f := index(field, i)
		if !evidenceKinds[e.Kind] {
			return fieldErr(member(f, "kind"), "must be file, commit, url, test, doc or measurement")
		}
		if err := checkLine(member(f, "ref"), e.Ref, maxRef); err != nil {
			return err
		}
		if e.Note != "" {
			if err := checkLine(member(f, "note"), e.Note, maxLine); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateMove checks m against §Move and §Challenge, including the syntax and
// distinctness of every target. Whether each target exists in the other
// side's current position needs the transcript: CheckTargets.
func ValidateMove(m *Move) error {
	if len(m.Challenges) > maxChallenges {
		return fieldErr("challenges", "must hold 0-%d challenges", maxChallenges)
	}
	for i, c := range m.Challenges {
		base := index("challenges", i)
		tf := member(base, "targets")
		if len(c.Targets) < 1 || len(c.Targets) > maxTargets {
			return fieldErr(tf, "must hold 1-%d targets", maxTargets)
		}
		seen := make(map[string]bool, len(c.Targets))
		for j, t := range c.Targets {
			if _, err := ParseTarget(t); err != nil {
				return fieldErr(index(tf, j), "%s", err.Error())
			}
			if seen[t] {
				return fieldErr(index(tf, j), "duplicates an earlier target")
			}
			seen[t] = true
		}
		if err := checkArgument(member(base, "argument"), c.Argument); err != nil {
			return err
		}
		if err := validateEvidence(member(base, "evidence"), c.Evidence, maxChallengeEv); err != nil {
			return err
		}
	}
	if m.Revision != nil {
		return validatePosition("revision", m.Revision)
	}
	return nil
}

// CheckTargets checks that every target of m names an item that exists in
// other, the other side's current position (§Targets). Call it after
// ValidateMove.
func CheckTargets(m *Move, other *Position) error {
	for i, c := range m.Challenges {
		for j, s := range c.Targets {
			f := index(member(index("challenges", i), "targets"), j)
			t, err := ParseTarget(s)
			if err != nil {
				return fieldErr(f, "%s", err.Error())
			}
			if !t.ExistsIn(other) {
				return fieldErr(f, "names %s, which the other side's current position does not have", s)
			}
		}
	}
	return nil
}

// ValidateProposal checks p against §Proposal.
func ValidateProposal(p *Proposal) error {
	if err := checkLine("agreement.decision", p.Agreement.Decision, maxLine); err != nil {
		return err
	}
	if len(p.Agreement.Points) > maxPoints {
		return fieldErr("agreement.points", "must hold 1-%d items", maxPoints)
	}
	for i, pt := range p.Agreement.Points {
		if err := checkLine(index("agreement.points", i), pt, maxLine); err != nil {
			return err
		}
	}
	if p.Agreement.Argument != "" {
		if err := checkArgument("agreement.argument", p.Agreement.Argument); err != nil {
			return err
		}
	}
	if err := validateDisagreements(p.RemainingDisagreement); err != nil {
		return err
	}
	if len(p.AffectedArtifacts) > maxArtifacts {
		return fieldErr("affected_artifacts", "must hold 1-%d artifacts", maxArtifacts)
	}
	for i, a := range p.AffectedArtifacts {
		if err := request.ValidateArtifact("affected_artifacts", i, a); err != nil {
			return err
		}
		if err := checkArtifactText(index("affected_artifacts", i), a); err != nil {
			return err
		}
	}
	return nil
}

// checkArtifactText applies the debate text and free-text rules on top of
// the request artifact rules: no C1 or U+2028/U+2029, no leading or trailing
// white space.
func checkArtifactText(base string, a request.Artifact) error {
	for _, m := range [...]struct{ name, v string }{
		{"url", a.URL}, {"branch", a.Branch}, {"commit", a.Commit}, {"path", a.Path},
	} {
		if m.v == "" {
			continue
		}
		f := member(base, m.name)
		if badDebateText(m.v, "") {
			return fieldErr(f, "must not contain control characters")
		}
		if err := checkTrimmed(f, m.v); err != nil {
			return err
		}
	}
	return nil
}

func validateDisagreements(ds []Disagreement) error {
	const f = "remaining_disagreement"
	if len(ds) > maxDisagreement {
		return fieldErr(f, "must hold 1-%d items", maxDisagreement)
	}
	for i, d := range ds {
		b := index(f, i)
		if err := checkLine(member(b, "point"), d.Point, maxLine); err != nil {
			return err
		}
		if err := checkLine(member(b, "initiator"), d.Initiator, maxLine); err != nil {
			return err
		}
		if err := checkLine(member(b, "respondent"), d.Respondent, maxLine); err != nil {
			return err
		}
	}
	return nil
}

// ValidateAnswer checks a against §Answer.
func ValidateAnswer(a *Answer) error {
	if err := validateDisagreements(a.RemainingDisagreement); err != nil {
		return err
	}
	if a.Argument != "" {
		return checkArgument("argument", a.Argument)
	}
	return nil
}

// Validate checks e against its kind's schema (field caps only; the size cap
// is CheckSize on the canonical bytes).
func Validate(e Entry) error {
	switch x := e.(type) {
	case *Position:
		return ValidatePosition(x)
	case *Move:
		return ValidateMove(x)
	case *Proposal:
		return ValidateProposal(x)
	case *Answer:
		return ValidateAnswer(x)
	}
	return fieldErr("kind", "must be position, move, proposal or answer")
}

// Target is one parsed challenge target (§Targets). Index is -1 for claim and
// argument.
type Target struct {
	Member string
	Index  int
}

// indexed are the members a target may index, with the most items each can
// hold.
var indexed = map[string]int{
	"assumptions":           maxAssumptions,
	"evidence":              maxEvidence,
	"rejected_alternatives": maxRejected,
}

// ParseTarget parses a target's wire form: "claim", "argument", or
// "assumptions/<i>", "evidence/<i>", "rejected_alternatives/<i>" with <i> a
// decimal index without leading zeros.
func ParseTarget(s string) (Target, error) {
	if s == "claim" || s == "argument" {
		return Target{Member: s, Index: -1}, nil
	}
	name, digits, ok := strings.Cut(s, "/")
	limit, known := indexed[name]
	if !ok || !known {
		return Target{}, fieldErr("target", "must be claim, argument, assumptions/<i>, evidence/<i> or rejected_alternatives/<i>")
	}
	if digits == "" || len(digits) > 2 || (len(digits) > 1 && digits[0] == '0') {
		return Target{}, fieldErr("target", "index must be a decimal number without leading zeros")
	}
	n := 0
	for i := 0; i < len(digits); i++ {
		c := digits[i]
		if c < '0' || c > '9' {
			return Target{}, fieldErr("target", "index must be a decimal number without leading zeros")
		}
		n = n*10 + int(c-'0')
	}
	if n >= limit {
		return Target{}, fieldErr("target", "index %d is out of range for %s", n, name)
	}
	return Target{Member: name, Index: n}, nil
}

// String is the target's wire form.
func (t Target) String() string {
	if t.Index < 0 {
		return t.Member
	}
	return t.Member + "/" + strconv.Itoa(t.Index)
}

// ExistsIn reports whether the item t names exists in p.
func (t Target) ExistsIn(p *Position) bool {
	switch t.Member {
	case "claim", "argument":
		return t.Index == -1
	case "assumptions":
		return t.Index >= 0 && t.Index < len(p.Assumptions)
	case "evidence":
		return t.Index >= 0 && t.Index < len(p.Evidence)
	case "rejected_alternatives":
		return t.Index >= 0 && t.Index < len(p.RejectedAlternatives)
	}
	return false
}
