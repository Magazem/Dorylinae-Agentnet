package request

import (
	"encoding/json"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// Result is the optional result payload of request.complete
// (Docs/protocol/request.md §Result payload (D14)).
const (
	ResultPass    = "pass"
	ResultFail    = "fail"
	ResultPartial = "partial"
	ResultNA      = "n/a"

	// MaxCompleteBody is the cap on len(canonical(complete body)) (65536 bytes).
	MaxCompleteBody = 65536

	minSummaryCodePoints = 1
	maxSummaryCodePoints = 280

	minOutputBytes = 1
	maxOutputBytes = 32768

	minResultArtifacts = 1
	maxResultArtifacts = 20

	minNoteCodePoints = 1
	maxNoteCodePoints = 2000

	minExitCode = -2147483648
	maxExitCode = 4294967295
)

// Result is request.complete's optional "result" member
// (Docs/protocol/request.md §Result payload (D14)).
type Result struct {
	Status    string
	Summary   string // "" if absent
	ExitCode  *int64 // nil if absent
	Output    string // "" if absent
	Artifacts []Artifact
}

// ValidateComplete checks note and result against Docs/protocol/request.md
// §Result payload (D14) (and the pre-existing note limit), except the total
// complete-body cap (CheckCompleteSize, which needs the canonical bytes). It
// returns the first violation as a *FieldError.
func ValidateComplete(note string, result *Result) error {
	if note != "" {
		if err := checkCodePoints("note", note, minNoteCodePoints, maxNoteCodePoints, ""); err != nil {
			return err
		}
	}
	if result == nil {
		return nil
	}
	switch result.Status {
	case ResultPass, ResultFail, ResultPartial, ResultNA:
	default:
		return fieldErr("result.status", "must be pass, fail, partial or n/a")
	}
	if result.Summary != "" {
		if err := checkCodePoints("result.summary", result.Summary, minSummaryCodePoints, maxSummaryCodePoints, ""); err != nil {
			return err
		}
	}
	if result.ExitCode != nil {
		if *result.ExitCode < minExitCode || *result.ExitCode > maxExitCode {
			return fieldErr("result.exit_code", "must be -2147483648 to 4294967295")
		}
	}
	if result.Output != "" {
		if err := checkBytes("result.output", result.Output, minOutputBytes, maxOutputBytes); err != nil {
			return err
		}
		if hasControl(result.Output, "\n\t") {
			return fieldErr("result.output", "must not contain control characters other than \\n and \\t")
		}
	}
	if result.Artifacts != nil {
		if len(result.Artifacts) < minResultArtifacts || len(result.Artifacts) > maxResultArtifacts {
			return fieldErr("result.artifacts", "must hold 1-%d artifacts", maxResultArtifacts)
		}
		for i, a := range result.Artifacts {
			if err := validateArtifact("result.artifacts", i, a); err != nil {
				return err
			}
		}
	}
	return nil
}

// resultAllowedMembers are the only members DecodeResult accepts.
var resultAllowedMembers = map[string]bool{
	"status": true, "summary": true, "exit_code": true, "output": true, "artifacts": true,
}

// DecodeResult strictly decodes a generically parsed result object (as
// produced by agentcard.ParseStrict) into a Result. It rejects unknown
// members and members present as null. It does not validate the field
// limits; call ValidateComplete.
func DecodeResult(body map[string]any) (*Result, error) {
	for k := range body {
		if !resultAllowedMembers[k] {
			return nil, fieldErr("result."+k, "is not a recognised member")
		}
	}
	rawStatus, ok := body["status"]
	if !ok {
		return nil, fieldErr("result.status", "is required")
	}
	status, ok := rawStatus.(string)
	if !ok {
		return nil, fieldErr("result.status", "must be a string")
	}
	r := &Result{Status: status}
	if raw, ok := body["summary"]; ok {
		s, ok := raw.(string)
		if !ok {
			return nil, fieldErr("result.summary", "must be a string")
		}
		r.Summary = s
	}
	if raw, ok := body["exit_code"]; ok {
		n, ok := raw.(json.Number)
		if !ok {
			return nil, fieldErr("result.exit_code", "must be an integer")
		}
		i64, err := n.Int64()
		if err != nil {
			return nil, fieldErr("result.exit_code", "must be an integer")
		}
		r.ExitCode = &i64
	}
	if raw, ok := body["output"]; ok {
		s, ok := raw.(string)
		if !ok {
			return nil, fieldErr("result.output", "must be a string")
		}
		r.Output = s
	}
	if raw, ok := body["artifacts"]; ok {
		list, ok := raw.([]any)
		if !ok {
			return nil, fieldErr("result.artifacts", "must be an array")
		}
		artifacts := make([]Artifact, 0, len(list))
		for i, el := range list {
			obj, ok := el.(map[string]any)
			if !ok {
				return nil, fieldErr("result.artifacts["+itoa(i)+"]", "must be an object")
			}
			var a Artifact
			for k, v := range obj {
				if !artifactSpecKeys[k] {
					return nil, fieldErr("result.artifacts["+itoa(i)+"]", "has unknown member %q", k)
				}
				s, ok := v.(string)
				if !ok {
					return nil, fieldErr("result.artifacts["+itoa(i)+"]."+k, "must be a string")
				}
				setArtifactField(&a, k, s)
			}
			artifacts = append(artifacts, a)
		}
		r.Artifacts = artifacts
	}
	return r, nil
}

// resultWire is result's wire form for outbound bodies (json.Marshal
// handles plain Go types, so exit_code is a real int64).
func resultWire(r *Result) map[string]any {
	m := map[string]any{"status": r.Status}
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
	return m
}

// jsonInt returns a json.Number for a small non-negative integer (seq).
func jsonInt(i int) json.Number { return json.Number(itoa(i)) }

// canonicalMap is agentcard.CanonicalValue restricted to map[string]any, for
// building the canonical form of a wire body from Go-native values (m's
// leaves must already be string, json.Number, bool, []any or map[string]any).
func canonicalMap(m map[string]any) ([]byte, error) { return agentcard.CanonicalValue(m) }

// CanonicalResult returns canonical(result) (Docs/protocol/request.md
// §Result payload (D14)), stored in the requests.result column. It does not
// validate r; call ValidateComplete first.
func CanonicalResult(r *Result) ([]byte, error) {
	return agentcard.CanonicalValue(resultCanonicalValue(r))
}

func resultCanonicalValue(r *Result) map[string]any {
	m := map[string]any{"status": r.Status}
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
		m["artifacts"] = artifactsToValue(r.Artifacts)
	}
	return m
}

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

// ResultBytes and OutputBytes are the derived sizes of Docs/protocol/request.md
// §Result payload (D14): result_bytes = len(canonical(result)),
// output_bytes = len(output) in UTF-8 (0 when absent).
func ResultBytes(canon []byte) int { return len(canon) }

// OutputBytes returns len(output) in UTF-8 bytes.
func OutputBytes(output string) int { return len(output) }

// CheckCompleteSize enforces MaxCompleteBody on canon, the canonical form of
// the whole complete body (including "result" if present). Checked after the
// field rules, per Docs/protocol/request.md §Result payload (D14).
func CheckCompleteSize(canon []byte) error {
	if len(canon) > MaxCompleteBody {
		return &TooLargeCompleteError{Size: len(canon), Limit: MaxCompleteBody}
	}
	return nil
}
