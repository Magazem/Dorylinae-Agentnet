package request

import (
	"encoding/json"
	"time"
)

// allowedMembers are the only members Decode accepts, per
// Docs/protocol/request.md §Request object ("No other members are allowed").
var allowedMembers = map[string]bool{
	"v": true, "id": true, "from": true, "to": true, "team": true, "type": true,
	"title": true, "brief": true, "urgency": true, "urgency_declared": true,
	"urgency_reason": true, "artifacts": true, "requested_grant": true,
	"deadline": true, "created": true, "context": true,
}

var requiredMembers = []string{
	"v", "id", "from", "to", "team", "type", "title", "brief", "urgency", "artifacts", "created",
}

// Decode strictly decodes a generically parsed request object (as produced by
// agentcard.ParseStrict on canonical(request), or on the wire body's
// "request" member) into a Request, and validates it (Validate). It rejects
// unknown members and optional members present as null (Docs/protocol/request.md
// §Request object: "Optional members are absent, never null").
func Decode(body map[string]any) (*Request, error) {
	for k := range body {
		if !allowedMembers[k] {
			return nil, fieldErr(k, "is not a recognised member")
		}
	}
	for _, k := range requiredMembers {
		if _, ok := body[k]; !ok {
			return nil, fieldErr(k, "is required")
		}
	}

	r := &Request{}

	v, err := decodeInt(body, "v")
	if err != nil {
		return nil, err
	}
	r.V = v

	if r.ID, err = decodeString(body, "id", false); err != nil {
		return nil, err
	}
	if r.From, err = decodeString(body, "from", false); err != nil {
		return nil, err
	}
	if r.To, err = decodeString(body, "to", false); err != nil {
		return nil, err
	}
	if r.Team, err = decodeString(body, "team", false); err != nil {
		return nil, err
	}
	if r.Type, err = decodeString(body, "type", false); err != nil {
		return nil, err
	}
	if r.Title, err = decodeString(body, "title", false); err != nil {
		return nil, err
	}
	if r.Brief, err = decodeString(body, "brief", false); err != nil {
		return nil, err
	}
	if r.Urgency, err = decodeString(body, "urgency", false); err != nil {
		return nil, err
	}
	if r.UrgencyDeclared, err = decodeString(body, "urgency_declared", true); err != nil {
		return nil, err
	}
	if r.UrgencyReason, err = decodeString(body, "urgency_reason", true); err != nil {
		return nil, err
	}
	if r.Artifacts, err = decodeArtifacts(body["artifacts"]); err != nil {
		return nil, err
	}
	if raw, ok := body["requested_grant"]; ok {
		g, err := decodeGrant(raw)
		if err != nil {
			return nil, err
		}
		r.RequestedGrant = g
	}
	if raw, ok := body["deadline"]; ok {
		t, err := decodeTime("deadline", raw)
		if err != nil {
			return nil, err
		}
		r.Deadline = t
	}
	if raw, ok := body["context"]; ok {
		files, err := decodeContext(raw)
		if err != nil {
			return nil, err
		}
		r.Context = files
	}
	created, err := decodeTime("created", body["created"])
	if err != nil {
		return nil, err
	}
	r.Created = created

	if err := Validate(r); err != nil {
		return nil, err
	}
	return r, nil
}

func decodeString(body map[string]any, field string, optional bool) (string, error) {
	raw, ok := body[field]
	if !ok {
		if optional {
			return "", nil
		}
		return "", fieldErr(field, "is required")
	}
	s, ok := raw.(string)
	if !ok {
		return "", fieldErr(field, "must be a string")
	}
	return s, nil
}

func decodeInt(body map[string]any, field string) (int, error) {
	raw, ok := body[field]
	if !ok {
		return 0, fieldErr(field, "is required")
	}
	n, ok := raw.(json.Number)
	if !ok {
		return 0, fieldErr(field, "must be an integer")
	}
	i64, err := n.Int64()
	if err != nil {
		return 0, fieldErr(field, "must be an integer")
	}
	return int(i64), nil
}

func decodeTime(field string, raw any) (time.Time, error) {
	s, ok := raw.(string)
	if !ok {
		return time.Time{}, fieldErr(field, "must be a string")
	}
	// time.Parse accepts a fractional second even though the layout has none,
	// so require the string to round-trip exactly.
	t, err := time.Parse(timeFmt, s)
	if err != nil || t.Format(timeFmt) != s {
		return time.Time{}, fieldErr(field, "must be RFC 3339 UTC with Z and whole seconds")
	}
	return t, nil
}

func decodeArtifacts(raw any) ([]Artifact, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, fieldErr("artifacts", "must be an array")
	}
	out := make([]Artifact, 0, len(list))
	for i, el := range list {
		obj, ok := el.(map[string]any)
		if !ok {
			return nil, fieldErr(artifactIndex(i), "must be an object")
		}
		var a Artifact
		for k, v := range obj {
			if !artifactSpecKeys[k] {
				return nil, fieldErr(artifactIndex(i), "has unknown member %q", k)
			}
			s, ok := v.(string)
			if !ok {
				return nil, fieldErr(artifactField(i, k), "must be a string")
			}
			setArtifactField(&a, k, s)
		}
		out = append(out, a)
	}
	return out, nil
}

func decodeContext(raw any) ([]ContextFile, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, fieldErr("context", "must be an array")
	}
	out := make([]ContextFile, 0, len(list))
	for i, el := range list {
		obj, ok := el.(map[string]any)
		if !ok {
			return nil, fieldErr("context["+itoa(i)+"]", "must be an object")
		}
		for k := range obj {
			if k != "name" && k != "text" {
				return nil, fieldErr("context["+itoa(i)+"]", "has unknown member %q", k)
			}
		}
		name, ok := obj["name"].(string)
		if !ok {
			return nil, fieldErr("context["+itoa(i)+"].name", "is required and must be a string")
		}
		text, ok := obj["text"].(string)
		if !ok {
			return nil, fieldErr("context["+itoa(i)+"].text", "is required and must be a string")
		}
		out = append(out, ContextFile{Name: name, Text: text})
	}
	return out, nil
}

func decodeGrant(raw any) (*RequestedGrant, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, fieldErr("requested_grant", "must be an object")
	}
	for k := range obj {
		switch k {
		case "action", "resource", "note":
		default:
			return nil, fieldErr("requested_grant", "has unknown member %q", k)
		}
	}
	g := &RequestedGrant{}
	action, ok := obj["action"].(string)
	if !ok {
		return nil, fieldErr("requested_grant.action", "is required and must be a string")
	}
	g.Action = action
	resource, ok := obj["resource"].(string)
	if !ok {
		return nil, fieldErr("requested_grant.resource", "is required and must be a string")
	}
	g.Resource = resource
	if raw, ok := obj["note"]; ok {
		note, ok := raw.(string)
		if !ok {
			return nil, fieldErr("requested_grant.note", "must be a string")
		}
		g.Note = note
	}
	return g, nil
}
