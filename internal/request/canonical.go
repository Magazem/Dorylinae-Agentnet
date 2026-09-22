package request

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// Canonical returns canonical(request): the deterministic JSON encoding of r
// per Docs/protocol/agent-card.md §Canonical serialisation, with absent
// optional members omitted (never null). It does not validate r; call
// Validate first.
func Canonical(r *Request) ([]byte, error) {
	obj := map[string]any{
		"v":         json.Number("1"),
		"id":        r.ID,
		"from":      r.From,
		"to":        r.To,
		"team":      r.Team,
		"type":      r.Type,
		"title":     r.Title,
		"brief":     r.Brief,
		"urgency":   r.Urgency,
		"artifacts": artifactsToValue(r.Artifacts),
		"created":   r.Created.UTC().Truncate(0).Format(timeFmt),
	}
	if r.UrgencyDeclared != "" {
		obj["urgency_declared"] = r.UrgencyDeclared
	}
	if r.UrgencyReason != "" {
		obj["urgency_reason"] = r.UrgencyReason
	}
	if r.RequestedGrant != nil {
		g := map[string]any{
			"action":   r.RequestedGrant.Action,
			"resource": r.RequestedGrant.Resource,
		}
		if r.RequestedGrant.Note != "" {
			g["note"] = r.RequestedGrant.Note
		}
		obj["requested_grant"] = g
	}
	if !r.Deadline.IsZero() {
		obj["deadline"] = r.Deadline.UTC().Truncate(0).Format(timeFmt)
	}
	return agentcard.CanonicalValue(obj)
}

func artifactsToValue(artifacts []Artifact) []any {
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

// BodyHash is the lowercase hex SHA-256 of canon, the output of Canonical.
func BodyHash(canon []byte) string {
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:])
}

// CheckSize enforces MaxRequestBody on canon, the output of Canonical. This
// is checked after the field rules (Validate), per Docs/protocol/request.md
// §Size limits.
func CheckSize(canon []byte) error {
	if len(canon) > MaxRequestBody {
		return &TooLargeError{Size: len(canon), Limit: MaxRequestBody}
	}
	return nil
}
