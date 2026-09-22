package request

import (
	"fmt"
	"strings"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// artifactSpecKeys are the only members ParseArtifactSpec accepts, matching
// Artifact's fields.
var artifactSpecKeys = map[string]bool{"url": true, "branch": true, "commit": true, "path": true}

// ParseArtifactSpec parses one `--artifact SPEC` value (Docs/cli/request.md):
// either a JSON object starting with `{`, or space-separated `key=value`
// pairs with keys url, branch, commit and path. It only extracts the fields;
// call Validate (on the enclosing Request) to check their limits.
func ParseArtifactSpec(spec string) (Artifact, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" {
		return Artifact{}, fmt.Errorf("artifact spec is empty")
	}
	if strings.HasPrefix(trimmed, "{") {
		return parseArtifactSpecJSON(trimmed)
	}
	return parseArtifactSpecPairs(trimmed)
}

func parseArtifactSpecJSON(s string) (Artifact, error) {
	v, err := agentcard.ParseStrict([]byte(s))
	if err != nil {
		return Artifact{}, fmt.Errorf("artifact spec: invalid JSON: %w", err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return Artifact{}, fmt.Errorf("artifact spec: JSON must be an object")
	}
	var a Artifact
	for k, raw := range obj {
		if !artifactSpecKeys[k] {
			return Artifact{}, fmt.Errorf("artifact spec: unknown member %q", k)
		}
		s, ok := raw.(string)
		if !ok {
			return Artifact{}, fmt.Errorf("artifact spec: %q must be a string", k)
		}
		setArtifactField(&a, k, s)
	}
	if a == (Artifact{}) {
		return Artifact{}, fmt.Errorf("artifact spec: at least one of url, branch, commit, path is required")
	}
	return a, nil
}

func parseArtifactSpecPairs(s string) (Artifact, error) {
	var a Artifact
	seen := map[string]bool{}
	for _, tok := range strings.Fields(s) {
		key, val, ok := strings.Cut(tok, "=")
		if !ok || key == "" {
			return Artifact{}, fmt.Errorf("artifact spec: %q is not key=value", tok)
		}
		if !artifactSpecKeys[key] {
			return Artifact{}, fmt.Errorf("artifact spec: unknown member %q", key)
		}
		if seen[key] {
			return Artifact{}, fmt.Errorf("artifact spec: %q given twice", key)
		}
		seen[key] = true
		setArtifactField(&a, key, val)
	}
	if a == (Artifact{}) {
		return Artifact{}, fmt.Errorf("artifact spec: at least one of url, branch, commit, path is required")
	}
	return a, nil
}

func setArtifactField(a *Artifact, key, val string) {
	switch key {
	case "url":
		a.URL = val
	case "branch":
		a.Branch = val
	case "commit":
		a.Commit = val
	case "path":
		a.Path = val
	}
}
