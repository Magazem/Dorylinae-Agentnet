package debate

import (
	"bytes"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// Decoding is strict (Docs/protocol/debate.md §Messages): exactly the listed
// members, optional members absent (never null, never an empty array), every
// value of its declared type. The decoders work on values produced by
// agentcard.ParseStrict (or the IPC parser, which yields the same shapes) and
// validate what they decode.

// DecodeEntry decodes, validates and canonically encodes a generically parsed
// entry of the given kind. It checks the field caps first, then
// MaxDebateEntry (*TooLargeError). The returned bytes are canonical(entry),
// the form that is stored, sent and signed.
func DecodeEntry(kind string, v any) (Entry, []byte, error) {
	var (
		e   Entry
		err error
	)
	switch kind {
	case KindPosition:
		e, err = DecodePosition(v)
	case KindMove:
		e, err = DecodeMove(v)
	case KindProposal:
		e, err = DecodeProposal(v)
	case KindAnswer:
		e, err = DecodeAnswer(v)
	default:
		return nil, nil, fieldErr("kind", "must be position, move, proposal or answer")
	}
	if err != nil {
		return nil, nil, err
	}
	canon, err := Canonical(e)
	if err != nil {
		return nil, nil, err
	}
	if err := CheckSize(canon); err != nil {
		return nil, nil, err
	}
	return e, canon, nil
}

// ParseEntry is DecodeEntry on raw bytes that must already be canonical (a
// stored entry, or a reveal's position): anything else, including a valid
// entry in another encoding, is ErrNotCanonical.
func ParseEntry(kind string, data []byte) (Entry, error) {
	v, err := agentcard.ParseStrict(data)
	if err != nil {
		return nil, fieldErr("entry", "%s", err.Error())
	}
	e, canon, err := DecodeEntry(kind, v)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(canon, data) {
		return nil, ErrNotCanonical
	}
	return e, nil
}

// DecodePosition strictly decodes and validates a position.
func DecodePosition(v any) (*Position, error) {
	p, err := decodePosition("", v)
	if err != nil {
		return nil, err
	}
	if err := ValidatePosition(p); err != nil {
		return nil, err
	}
	return p, nil
}

// DecodeMove strictly decodes and validates a move (not its targets against
// the transcript: CheckTargets).
func DecodeMove(v any) (*Move, error) {
	o, err := object("", v, []string{"challenges"}, []string{"revision"})
	if err != nil {
		return nil, err
	}
	list, err := array("challenges", o["challenges"], false)
	if err != nil {
		return nil, err
	}
	m := &Move{Challenges: make([]Challenge, 0, len(list))}
	for i, el := range list {
		base := index("challenges", i)
		co, err := object(base, el, []string{"targets", "argument"}, []string{"evidence"})
		if err != nil {
			return nil, err
		}
		var c Challenge
		if c.Targets, err = strList(member(base, "targets"), co["targets"], false); err != nil {
			return nil, err
		}
		if c.Argument, err = str(member(base, "argument"), co["argument"]); err != nil {
			return nil, err
		}
		if raw, ok := co["evidence"]; ok {
			if c.Evidence, err = decodeEvidence(member(base, "evidence"), raw); err != nil {
				return nil, err
			}
		}
		m.Challenges = append(m.Challenges, c)
	}
	if raw, ok := o["revision"]; ok {
		if m.Revision, err = decodePosition("revision", raw); err != nil {
			return nil, err
		}
	}
	if err := ValidateMove(m); err != nil {
		return nil, err
	}
	return m, nil
}

// DecodeProposal strictly decodes and validates a proposal.
func DecodeProposal(v any) (*Proposal, error) {
	o, err := object("", v, []string{"agreement"}, []string{"remaining_disagreement", "affected_artifacts"})
	if err != nil {
		return nil, err
	}
	ao, err := object("agreement", o["agreement"], []string{"decision"}, []string{"points", "argument"})
	if err != nil {
		return nil, err
	}
	p := &Proposal{}
	if p.Agreement.Decision, err = str("agreement.decision", ao["decision"]); err != nil {
		return nil, err
	}
	if raw, ok := ao["points"]; ok {
		if p.Agreement.Points, err = strList("agreement.points", raw, true); err != nil {
			return nil, err
		}
	}
	if raw, ok := ao["argument"]; ok {
		if p.Agreement.Argument, err = nonEmpty("agreement.argument", raw); err != nil {
			return nil, err
		}
	}
	if raw, ok := o["remaining_disagreement"]; ok {
		if p.RemainingDisagreement, err = decodeDisagreements(raw); err != nil {
			return nil, err
		}
	}
	if raw, ok := o["affected_artifacts"]; ok {
		if p.AffectedArtifacts, err = decodeArtifacts(raw); err != nil {
			return nil, err
		}
	}
	if err := ValidateProposal(p); err != nil {
		return nil, err
	}
	return p, nil
}

// DecodeAnswer strictly decodes and validates an answer.
func DecodeAnswer(v any) (*Answer, error) {
	o, err := object("", v, []string{"accept"}, []string{"remaining_disagreement", "argument"})
	if err != nil {
		return nil, err
	}
	a := &Answer{}
	b, ok := o["accept"].(bool)
	if !ok {
		return nil, typeErr("accept", o["accept"], "a boolean")
	}
	a.Accept = b
	if raw, ok := o["remaining_disagreement"]; ok {
		if a.RemainingDisagreement, err = decodeDisagreements(raw); err != nil {
			return nil, err
		}
	}
	if raw, ok := o["argument"]; ok {
		if a.Argument, err = nonEmpty("argument", raw); err != nil {
			return nil, err
		}
	}
	if err := ValidateAnswer(a); err != nil {
		return nil, err
	}
	return a, nil
}

func decodePosition(base string, v any) (*Position, error) {
	o, err := object(base, v, []string{"claim", "argument"},
		[]string{"assumptions", "evidence", "rejected_alternatives"})
	if err != nil {
		return nil, err
	}
	p := &Position{}
	if p.Claim, err = str(member(base, "claim"), o["claim"]); err != nil {
		return nil, err
	}
	if p.Argument, err = str(member(base, "argument"), o["argument"]); err != nil {
		return nil, err
	}
	if raw, ok := o["assumptions"]; ok {
		if p.Assumptions, err = strList(member(base, "assumptions"), raw, true); err != nil {
			return nil, err
		}
	}
	if raw, ok := o["evidence"]; ok {
		if p.Evidence, err = decodeEvidence(member(base, "evidence"), raw); err != nil {
			return nil, err
		}
	}
	if raw, ok := o["rejected_alternatives"]; ok {
		f := member(base, "rejected_alternatives")
		list, err := array(f, raw, true)
		if err != nil {
			return nil, err
		}
		for i, el := range list {
			ao, err := object(index(f, i), el, []string{"option", "reason"}, nil)
			if err != nil {
				return nil, err
			}
			var alt Alternative
			if alt.Option, err = str(member(index(f, i), "option"), ao["option"]); err != nil {
				return nil, err
			}
			if alt.Reason, err = str(member(index(f, i), "reason"), ao["reason"]); err != nil {
				return nil, err
			}
			p.RejectedAlternatives = append(p.RejectedAlternatives, alt)
		}
	}
	return p, nil
}

func decodeEvidence(field string, raw any) ([]Evidence, error) {
	list, err := array(field, raw, true)
	if err != nil {
		return nil, err
	}
	out := make([]Evidence, 0, len(list))
	for i, el := range list {
		f := index(field, i)
		o, err := object(f, el, []string{"kind", "ref"}, []string{"note"})
		if err != nil {
			return nil, err
		}
		var e Evidence
		if e.Kind, err = str(member(f, "kind"), o["kind"]); err != nil {
			return nil, err
		}
		if e.Ref, err = str(member(f, "ref"), o["ref"]); err != nil {
			return nil, err
		}
		if raw, ok := o["note"]; ok {
			if e.Note, err = nonEmpty(member(f, "note"), raw); err != nil {
				return nil, err
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func decodeDisagreements(raw any) ([]Disagreement, error) {
	const field = "remaining_disagreement"
	list, err := array(field, raw, true)
	if err != nil {
		return nil, err
	}
	out := make([]Disagreement, 0, len(list))
	for i, el := range list {
		f := index(field, i)
		o, err := object(f, el, []string{"point", "initiator", "respondent"}, nil)
		if err != nil {
			return nil, err
		}
		var d Disagreement
		if d.Point, err = str(member(f, "point"), o["point"]); err != nil {
			return nil, err
		}
		if d.Initiator, err = str(member(f, "initiator"), o["initiator"]); err != nil {
			return nil, err
		}
		if d.Respondent, err = str(member(f, "respondent"), o["respondent"]); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// decodeArtifacts reuses the request artifact decoder, and refuses an empty
// member, which the typed form cannot tell from an absent one.
func decodeArtifacts(raw any) ([]request.Artifact, error) {
	const field = "affected_artifacts"
	list, err := array(field, raw, true)
	if err != nil {
		return nil, err
	}
	for i, el := range list {
		if o, ok := el.(map[string]any); ok {
			for k, mv := range o {
				if s, ok := mv.(string); ok && s == "" {
					return nil, fieldErr(member(index(field, i), k), "must not be empty")
				}
			}
		}
	}
	return request.DecodeArtifacts(field, list)
}

// object checks that v is an object with every required member and no member
// outside required and optional.
func object(field string, v any, required, optional []string) (map[string]any, error) {
	o, ok := v.(map[string]any)
	if !ok {
		return nil, typeErr(field, v, "an object")
	}
	for k := range o {
		if !contains(required, k) && !contains(optional, k) {
			return nil, fieldErr(member(field, k), "is not a recognised member")
		}
	}
	for _, k := range required {
		if _, ok := o[k]; !ok {
			return nil, fieldErr(member(field, k), "is required")
		}
	}
	return o, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// array checks that v is an array; optional arrays must not be empty (an
// absent member is the empty form).
func array(field string, v any, optional bool) ([]any, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, typeErr(field, v, "an array")
	}
	if optional && len(list) == 0 {
		return nil, fieldErr(field, "must not be empty when present (omit it instead)")
	}
	return list, nil
}

func strList(field string, v any, optional bool) ([]string, error) {
	list, err := array(field, v, optional)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list))
	for i, el := range list {
		s, err := str(index(field, i), el)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func str(field string, v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", typeErr(field, v, "a string")
	}
	return s, nil
}

// nonEmpty decodes an optional string member, which must not be "" (the typed
// form uses "" for absent).
func nonEmpty(field string, v any) (string, error) {
	s, err := str(field, v)
	if err == nil && s == "" {
		return "", fieldErr(field, "must not be empty when present (omit it instead)")
	}
	return s, err
}

func typeErr(field string, v any, want string) error {
	if field == "" {
		field = "entry"
	}
	if v == nil {
		return fieldErr(field, "must be %s, not null", want)
	}
	return fieldErr(field, "must be %s", want)
}
