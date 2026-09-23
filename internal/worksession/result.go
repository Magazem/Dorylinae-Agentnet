package worksession

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
	"github.com/Magazem/Dorylinae-Agentnet/internal/request"
)

// MaxResultBody is the cap on len(canonical(ws.result body)), the whole body
// including at/request/round/session, not just the result member
// (Docs/protocol/work-session.md §Result object (2.6)).
const MaxResultBody = 65536

// Result is ws.result's "result" member (Docs/protocol/work-session.md
// §Result object (2.6)): the D14 result (request.Result), extended by
// verification and notes. "" / nil mean absent.
type Result struct {
	Status       string
	Summary      string
	ExitCode     *int64
	Output       string
	Artifacts    []request.Artifact
	Verification string
	Notes        string
}

// resultMembers are the only members DecodeResult accepts.
var resultMembers = map[string]bool{
	"status": true, "summary": true, "exit_code": true, "output": true,
	"artifacts": true, "verification": true, "notes": true,
}

// d14Members is the subset of resultMembers request.DecodeResult understands.
var d14Members = []string{"status", "summary", "exit_code", "output", "artifacts"}

// DecodeResult strictly decodes a generically parsed result object into a
// Result, reusing request.DecodeResult for the D14 members it shares
// (Docs/protocol/work-session.md §Result object (2.6): "the D14 result,
// extended by two members"). It does not validate field limits; call
// ValidateResult.
func DecodeResult(body map[string]any) (*Result, error) {
	for k := range body {
		if !resultMembers[k] {
			return nil, fieldErr("result."+k, "is not a recognised member")
		}
	}
	d14 := make(map[string]any, len(d14Members))
	for _, k := range d14Members {
		if v, ok := body[k]; ok {
			d14[k] = v
		}
	}
	base, err := request.DecodeResult(d14)
	if err != nil {
		return nil, remapField(err, "result.")
	}
	r := &Result{Status: base.Status, Summary: base.Summary, ExitCode: base.ExitCode, Output: base.Output, Artifacts: base.Artifacts}

	verif, ok := body["verification"]
	if !ok {
		return nil, fieldErr("result.verification", "is required")
	}
	vs, ok := verif.(string)
	if !ok {
		return nil, fieldErr("result.verification", "must be a string")
	}
	r.Verification = vs

	if raw, ok := body["notes"]; ok {
		s, ok := raw.(string)
		if !ok {
			return nil, fieldErr("result.notes", "must be a string")
		}
		if s == "" {
			return nil, fieldErr("result.notes", "must be absent, not empty")
		}
		r.Notes = s
	}
	return r, nil
}

// remapField rewrites a *request.FieldError's Field to be prefixed for our
// own field names (request.DecodeResult already names its own fields
// "status", "artifacts[0].url", etc. without a "result." prefix).
func remapField(err error, prefix string) error {
	var fe *request.FieldError
	if errors.As(err, &fe) {
		return fieldErr(prefix+fe.Field, "%s", fe.Reason)
	}
	return err
}

// ValidateResult checks r against Docs/protocol/work-session.md §Result
// object (2.6): the D14 members and notes via request.ValidateComplete (the
// same rule as the request note), plus verification. verification must be
// "none" or "tests_passed": "human_accepted" is never sent by B and is
// rejected here (A sets it locally, never through this path). It does not
// check the total body cap; call CheckResultSize on the canonical body.
func ValidateResult(r *Result) error {
	base := &request.Result{Status: r.Status, Summary: r.Summary, ExitCode: r.ExitCode, Output: r.Output, Artifacts: r.Artifacts}
	if err := request.ValidateComplete(r.Notes, base); err != nil {
		var fe *request.FieldError
		if errors.As(err, &fe) && fe.Field == "note" {
			return fieldErr("result.notes", "%s", fe.Reason)
		}
		return remapField(err, "result.")
	}
	switch r.Verification {
	case VerificationNone, VerificationTestsPassed:
	default:
		return fieldErr("result.verification", "must be none or tests_passed")
	}
	return nil
}

func artifactsValue(artifacts []request.Artifact) []any {
	out := make([]any, 0, len(artifacts))
	for _, a := range artifacts {
		m := map[string]any{}
		if a.URL != "" {
			m["url"] = a.URL
		}
		if a.Branch != "" {
			m["branch"] = a.Branch
		}
		if a.Commit != "" {
			m["commit"] = a.Commit
		}
		if a.Path != "" {
			m["path"] = a.Path
		}
		out = append(out, m)
	}
	return out
}

func artifactsWire(artifacts []request.Artifact) []map[string]string {
	out := make([]map[string]string, 0, len(artifacts))
	for _, a := range artifacts {
		m := map[string]string{}
		if a.URL != "" {
			m["url"] = a.URL
		}
		if a.Branch != "" {
			m["branch"] = a.Branch
		}
		if a.Commit != "" {
			m["commit"] = a.Commit
		}
		if a.Path != "" {
			m["path"] = a.Path
		}
		out = append(out, m)
	}
	return out
}

// resultCanonicalValue is the canonical-form value of r (agentcard leaves:
// string, json.Number, bool, []any, map[string]any).
func resultCanonicalValue(r *Result) map[string]any {
	m := map[string]any{"status": r.Status, "verification": r.Verification}
	if r.Summary != "" {
		m["summary"] = r.Summary
	}
	if r.ExitCode != nil {
		m["exit_code"] = json.Number(itoa64(*r.ExitCode))
	}
	if r.Output != "" {
		m["output"] = r.Output
	}
	if len(r.Artifacts) > 0 {
		m["artifacts"] = artifactsValue(r.Artifacts)
	}
	if r.Notes != "" {
		m["notes"] = r.Notes
	}
	return m
}

// resultWire is r's wire form for outbound bodies.
func resultWire(r *Result) map[string]any {
	m := map[string]any{"status": r.Status, "verification": r.Verification}
	if r.Summary != "" {
		m["summary"] = r.Summary
	}
	if r.ExitCode != nil {
		m["exit_code"] = *r.ExitCode
	}
	if r.Output != "" {
		m["output"] = r.Output
	}
	if len(r.Artifacts) > 0 {
		m["artifacts"] = artifactsWire(r.Artifacts)
	}
	if r.Notes != "" {
		m["notes"] = r.Notes
	}
	return m
}

// CanonicalResult returns canonical(result): the "result" member's canonical
// form, stored in the work_sessions.result column.
func CanonicalResult(r *Result) ([]byte, error) {
	return agentcard.CanonicalValue(resultCanonicalValue(r))
}

// resultBodyCanonical returns canonical(ws.result body): {at, request,
// result, round, session}, for the total-size cap.
func resultBodyCanonical(sid, reqID string, round int, at time.Time, r *Result) ([]byte, error) {
	body := map[string]any{
		"at": wireTime(at), "request": reqID, "result": resultCanonicalValue(r),
		"round": json.Number(itoa(round)), "session": sid,
	}
	return agentcard.CanonicalValue(body)
}

// ResultBytes is the derived size of Docs/protocol/work-session.md §Result
// object (2.6).
func ResultBytes(canon []byte) int { return len(canon) }

// OutputBytes is the derived size of Docs/protocol/work-session.md §Result
// object (2.6).
func OutputBytes(output string) int { return len(output) }

// CheckResultSize enforces MaxResultBody on canon, the canonical form of the
// whole ws.result body (resultBodyCanonical).
func CheckResultSize(canon []byte) error {
	if len(canon) > MaxResultBody {
		return &TooLargeResultError{Size: len(canon), Limit: MaxResultBody}
	}
	return nil
}

func itoa(i int) string { return itoa64(int64(i)) }

func itoa64(i int64) string {
	neg := i < 0
	if neg {
		i = -i
	}
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
