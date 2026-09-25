package debate

import (
	"errors"
	"strconv"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// r builds a one-rune string from a code point, so the test sources hold no
// invisible characters.
func r(cp rune) string { return string(cp) }

// specPosition is the position of the commitment vector in
// Docs/protocol/debate.md §Commit–reveal.
const specPosition = `{"argument":"Retries should back off exponentially, capped at 10 minutes.","assumptions":["Clock skew between peers is under 5 s"],"claim":"Use capped exponential backoff for outbox retries","evidence":[{"kind":"file","ref":"internal/mail/outbox.go"}],"rejected_alternatives":[{"option":"Fixed 30 s retry","reason":"Floods the relay after an outage"}]}`

// fullPosition has every optional member, one item each.
func fullPosition() map[string]any {
	return map[string]any{
		"claim":       "Use capped exponential backoff",
		"assumptions": []any{"Clock skew is under 5 s"},
		"evidence": []any{map[string]any{
			"kind": "file", "ref": "internal/mail/outbox.go", "note": "The retry loop",
		}},
		"rejected_alternatives": []any{map[string]any{"option": "Fixed retry", "reason": "Floods the relay"}},
		"argument":              "Backoff spreads load.\nIt is capped.",
	}
}

func fullMove() map[string]any {
	return map[string]any{
		"challenges": []any{map[string]any{
			"targets":  []any{"claim"},
			"argument": "The cap is too long.",
			"evidence": []any{map[string]any{"kind": "measurement", "ref": "p99 12 min", "note": "From staging"}},
		}},
		"revision": fullPosition(),
	}
}

func fullProposal() map[string]any {
	return map[string]any{
		"agreement": map[string]any{
			"decision": "Capped backoff at 5 minutes",
			"points":   []any{"Jitter of 10 percent"},
			"argument": "Both sides accept the cap.",
		},
		"remaining_disagreement": []any{map[string]any{
			"point": "Jitter size", "initiator": "10 percent", "respondent": "20 percent",
		}},
		"affected_artifacts": []any{map[string]any{"url": "https://example.com/x", "path": "internal/mail/outbox.go"}},
	}
}

func fullAnswer() map[string]any {
	return map[string]any{
		"accept": true,
		"remaining_disagreement": []any{map[string]any{
			"point": "Jitter size", "initiator": "10 percent", "respondent": "20 percent",
		}},
		"argument": "Agreed.\n\tWith one note.",
	}
}

var fullEntries = []struct {
	kind string
	full func() map[string]any
}{
	{KindPosition, fullPosition},
	{KindMove, fullMove},
	{KindProposal, fullProposal},
	{KindAnswer, fullAnswer},
}

// clone deep-copies a generic value through its canonical form.
func clone(t testing.TB, v any) any {
	t.Helper()
	b, err := agentcard.CanonicalValue(v)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	out, err := agentcard.ParseStrict(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return out
}

// node is one member or array element of a generic value.
type node struct {
	path   string
	key    string         // member name; for an array element, the array's member name
	parent map[string]any // nil for array elements
	val    any
	set    func(any)
}

// walk visits every member and element below v, depth first.
func walk(v any, path, key string, visit func(n node)) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			p := k
			if path != "" {
				p = path + "." + k
			}
			visit(node{path: p, key: k, parent: x, val: child, set: func(nv any) { x[k] = nv }})
			walk(child, p, k, visit)
		}
	case []any:
		for i, child := range x {
			p := path + "[" + strconv.Itoa(i) + "]"
			visit(node{path: p, key: key, val: child, set: func(nv any) { x[i] = nv }})
			walk(child, p, key, visit)
		}
	}
}

// paths lists every node path of v.
func paths(v any) []node {
	var out []node
	walk(v, "", "", func(n node) { out = append(out, n) })
	return out
}

// mutate clones base, applies fn at path, and decodes the result.
func mutate(t *testing.T, kind string, base map[string]any, path string, fn func(n node)) error {
	t.Helper()
	c := clone(t, base)
	found := false
	walk(c, "", "", func(n node) {
		if n.path == path && !found {
			found = true
			fn(n)
		}
	})
	if !found {
		t.Fatalf("%s: no path %q", kind, path)
	}
	_, _, err := DecodeEntry(kind, c)
	return err
}

// wantField checks err is a *FieldError naming field.
func wantField(t *testing.T, err error, field string) {
	t.Helper()
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("want *FieldError at %s, got %v", field, err)
	}
	if fe.Field != field {
		t.Fatalf("want FieldError at %s, got %s (%s)", field, fe.Field, fe.Reason)
	}
}
