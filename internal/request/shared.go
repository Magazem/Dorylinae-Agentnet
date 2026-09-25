package request

// Exported helpers for packages that reuse the request rules (internal/debate:
// Docs/protocol/debate.md §Proposal takes artifacts "in the request artifact
// shape and rules"). They are thin wrappers; the request package's own
// behaviour is unchanged.

// HasControl reports whether s has a control character (U+0000-U+001F,
// U+007F) not present in allowed, or a U+FFFD replacement character.
func HasControl(s, allowed string) bool { return hasControl(s, allowed) }

// HasC1 reports whether s holds a C1 control character (U+0080-U+009F).
func HasC1(s string) bool { return hasC1(s) }

// ValidateTitle checks a request title (Docs/protocol/request.md §Request
// object: 1-120 code points, no control characters). Errors name "title".
func ValidateTitle(s string) error {
	return checkCodePoints("title", s, minTitleCodePoints, maxTitleCodePoints, "")
}

// ValidateArtifact checks one artifact against Docs/protocol/request.md
// §Artifacts. Errors name base[i].member.
func ValidateArtifact(base string, i int, a Artifact) error { return validateArtifact(base, i, a) }

// DecodeArtifacts strictly decodes a generically parsed artifact array
// (unknown members and non-string values refused). It does not validate the
// artifacts; call ValidateArtifact. Errors name base[i].member.
func DecodeArtifacts(base string, raw any) ([]Artifact, error) { return decodeArtifactsAt(base, raw) }

// ArtifactsValue is the generic (canonicalisable) form of artifacts, with
// empty members omitted.
func ArtifactsValue(artifacts []Artifact) []any { return artifactsToValue(artifacts) }
