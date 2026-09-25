package debate

import (
	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// Canonical returns canonical(entry) (Docs/protocol/agent-card.md §Canonical
// serialisation), with absent optional members omitted. It does not validate
// e; DecodeEntry does both.
func Canonical(e Entry) ([]byte, error) { return agentcard.CanonicalValue(e.value()) }

// CheckSize enforces MaxDebateEntry on canon, the output of Canonical. It is
// checked after the field caps (Docs/protocol/debate.md §Size caps).
func CheckSize(canon []byte) error {
	if len(canon) > MaxDebateEntry {
		return &TooLargeError{Size: len(canon), Limit: MaxDebateEntry}
	}
	return nil
}

func (p *Position) value() map[string]any {
	o := map[string]any{"claim": p.Claim, "argument": p.Argument}
	if len(p.Assumptions) > 0 {
		o["assumptions"] = stringsValue(p.Assumptions)
	}
	if len(p.Evidence) > 0 {
		o["evidence"] = evidenceValue(p.Evidence)
	}
	if len(p.RejectedAlternatives) > 0 {
		list := make([]any, len(p.RejectedAlternatives))
		for i, a := range p.RejectedAlternatives {
			list[i] = map[string]any{"option": a.Option, "reason": a.Reason}
		}
		o["rejected_alternatives"] = list
	}
	return o
}

func (m *Move) value() map[string]any {
	list := make([]any, len(m.Challenges))
	for i, c := range m.Challenges {
		co := map[string]any{"targets": stringsValue(c.Targets), "argument": c.Argument}
		if len(c.Evidence) > 0 {
			co["evidence"] = evidenceValue(c.Evidence)
		}
		list[i] = co
	}
	o := map[string]any{"challenges": list}
	if m.Revision != nil {
		o["revision"] = m.Revision.value()
	}
	return o
}

func (p *Proposal) value() map[string]any {
	ag := map[string]any{"decision": p.Agreement.Decision}
	if len(p.Agreement.Points) > 0 {
		ag["points"] = stringsValue(p.Agreement.Points)
	}
	if p.Agreement.Argument != "" {
		ag["argument"] = p.Agreement.Argument
	}
	o := map[string]any{"agreement": ag}
	if len(p.RemainingDisagreement) > 0 {
		o["remaining_disagreement"] = disagreementsValue(p.RemainingDisagreement)
	}
	if len(p.AffectedArtifacts) > 0 {
		o["affected_artifacts"] = request.ArtifactsValue(p.AffectedArtifacts)
	}
	return o
}

func (a *Answer) value() map[string]any {
	o := map[string]any{"accept": a.Accept}
	if len(a.RemainingDisagreement) > 0 {
		o["remaining_disagreement"] = disagreementsValue(a.RemainingDisagreement)
	}
	if a.Argument != "" {
		o["argument"] = a.Argument
	}
	return o
}

func stringsValue(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func evidenceValue(ev []Evidence) []any {
	out := make([]any, len(ev))
	for i, e := range ev {
		o := map[string]any{"kind": e.Kind, "ref": e.Ref}
		if e.Note != "" {
			o["note"] = e.Note
		}
		out[i] = o
	}
	return out
}

func disagreementsValue(ds []Disagreement) []any {
	out := make([]any, len(ds))
	for i, d := range ds {
		out[i] = map[string]any{"point": d.Point, "initiator": d.Initiator, "respondent": d.Respondent}
	}
	return out
}
