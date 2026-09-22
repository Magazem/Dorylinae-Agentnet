package request

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/mail"
)

// NewID returns a fresh request id: "r-" and 32 lowercase hex characters, 16
// bytes from crypto/rand (Docs/protocol/request.md §Request object).
func NewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return "r-" + hex.EncodeToString(b)
}

// wireTime formats t as the wire time (RFC 3339 UTC, whole seconds).
func wireTime(t time.Time) string { return t.UTC().Truncate(time.Second).Format(timeFmt) }

// storeTime formats t as the internal bookkeeping time (RFC 3339 UTC, milliseconds).
func storeTime(t time.Time) string { return t.UTC().Format(mail.StoreTimeFmt) }

// jsonObject marshals v to a JSON object string, for columns that store one
// (last_reply).
func jsonObject(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// WireBody is the mail body of kind "request": {"request": <object>}. r must
// already be valid (Validate).
func WireBody(r *Request) map[string]any {
	obj := map[string]any{
		"v": r.V, "id": r.ID, "from": r.From, "to": r.To, "team": r.Team,
		"type": r.Type, "title": r.Title, "brief": r.Brief, "urgency": r.Urgency,
		"artifacts": artifactsWire(r.Artifacts), "created": wireTime(r.Created),
	}
	if r.UrgencyDeclared != "" {
		obj["urgency_declared"] = r.UrgencyDeclared
	}
	if r.UrgencyReason != "" {
		obj["urgency_reason"] = r.UrgencyReason
	}
	if r.RequestedGrant != nil {
		g := map[string]any{"action": r.RequestedGrant.Action, "resource": r.RequestedGrant.Resource}
		if r.RequestedGrant.Note != "" {
			g["note"] = r.RequestedGrant.Note
		}
		obj["requested_grant"] = g
	}
	if !r.Deadline.IsZero() {
		obj["deadline"] = wireTime(r.Deadline)
	}
	return map[string]any{"request": obj}
}

func artifactsWire(artifacts []Artifact) []map[string]string {
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
