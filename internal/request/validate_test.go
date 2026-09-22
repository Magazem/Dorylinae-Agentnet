package request

import (
	"errors"
	"testing"
	"time"
)

// TestValidateFieldRules is a table test for every field rule in
// Docs/protocol/request.md §Request object.
func TestValidateFieldRules(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*Request)
		wantField string // "" means Validate must accept
	}{
		{"valid baseline", func(*Request) {}, ""},
		{"v not 1", func(r *Request) { r.V = 2 }, "v"},
		{"id malformed prefix", func(r *Request) { r.ID = "x-0123456789abcdef0123456789abcdef" }, "id"},
		{"id short", func(r *Request) { r.ID = "r-0123" }, "id"},
		{"id uppercase hex", func(r *Request) { r.ID = "r-0123456789ABCDEF0123456789abcdef" }, "id"},
		{"from not a key", func(r *Request) { r.From = "short" }, "from"},
		{"to not a key", func(r *Request) { r.To = "short" }, "to"},
		{"team malformed", func(r *Request) { r.Team = "bad-team" }, "team"},
		{"type invalid", func(r *Request) { r.Type = "bug" }, "type"},
		{"type review ok", func(r *Request) { r.Type = TypeReview }, ""},
		{"type question ok", func(r *Request) { r.Type = TypeQuestion }, ""},
		{"title empty", func(r *Request) { r.Title = "" }, "title"},
		{"title too long", func(r *Request) { r.Title = repeatRunes('a', maxTitleCodePoints+1) }, "title"},
		{"title control char", func(r *Request) { r.Title = "bad\x01title" }, "title"},
		{"brief empty", func(r *Request) { r.Brief = "" }, "brief"},
		{"brief too long", func(r *Request) { r.Brief = asciiFill(maxBriefBytes + 1) }, "brief"},
		{"brief control char", func(r *Request) { r.Brief = "bad\x01brief" }, "brief"},
		{"brief allows newline and tab", func(r *Request) { r.Brief = "line one\nindented\ttext" }, ""},
		{"urgency invalid", func(r *Request) { r.Urgency = "urgent" }, "urgency"},
		{"urgency_declared not high or blocking", func(r *Request) {
			r.Urgency = UrgencyNormal
			r.UrgencyDeclared = UrgencyLow
			r.UrgencyReason = "because"
		}, "urgency_declared"},
		{"urgency_declared without normal urgency", func(r *Request) {
			r.Urgency = UrgencyHigh
			r.UrgencyDeclared = UrgencyHigh
			r.UrgencyReason = "because"
		}, "urgency_declared"},
		{"urgency_declared valid downgrade", func(r *Request) {
			r.Urgency = UrgencyNormal
			r.UrgencyDeclared = UrgencyHigh
			r.UrgencyReason = "budget used"
		}, ""},
		{"urgency_reason required for urgency_declared", func(r *Request) {
			r.Urgency = UrgencyNormal
			r.UrgencyDeclared = UrgencyHigh
		}, "urgency_reason"},
		{"urgency_reason required for high", func(r *Request) { r.Urgency = UrgencyHigh }, "urgency_reason"},
		{"urgency_reason required for blocking", func(r *Request) { r.Urgency = UrgencyBlocking }, "urgency_reason"},
		{"urgency_reason present with high", func(r *Request) {
			r.Urgency = UrgencyHigh
			r.UrgencyReason = "release today"
		}, ""},
		{"urgency_reason too long", func(r *Request) {
			r.Urgency = UrgencyHigh
			r.UrgencyReason = repeatRunes('a', maxUrgencyReasonCodePoints+1)
		}, "urgency_reason"},
		{"urgency_reason optional at normal", func(r *Request) { r.Urgency = UrgencyNormal }, ""},
		{"artifacts too many", func(r *Request) {
			r.Artifacts = make([]Artifact, maxArtifacts+1)
			for i := range r.Artifacts {
				r.Artifacts[i] = Artifact{Path: "p"}
			}
		}, "artifacts"},
		{"artifact empty member set", func(r *Request) { r.Artifacts = []Artifact{{}} }, "artifacts[0]"},
		{"artifact url bad scheme", func(r *Request) {
			r.Artifacts = []Artifact{{URL: "ftp://example.com/x"}}
		}, "artifacts[0].url"},
		{"artifact url with space", func(r *Request) {
			r.Artifacts = []Artifact{{URL: "https://example.com/a b"}}
		}, "artifacts[0].url"},
		{"artifact url valid https", func(r *Request) {
			r.Artifacts = []Artifact{{URL: "https://example.com/a"}}
		}, ""},
		{"artifact branch space", func(r *Request) {
			r.Artifacts = []Artifact{{Branch: "feat x"}}
		}, "artifacts[0].branch"},
		{"artifact branch bad char", func(r *Request) {
			r.Artifacts = []Artifact{{Branch: "feat~x"}}
		}, "artifacts[0].branch"},
		{"artifact branch dotdot", func(r *Request) {
			r.Artifacts = []Artifact{{Branch: "feat/../x"}}
		}, "artifacts[0].branch"},
		{"artifact branch valid", func(r *Request) {
			r.Artifacts = []Artifact{{Branch: "feat/x"}}
		}, ""},
		{"artifact commit not hex", func(r *Request) {
			r.Artifacts = []Artifact{{Commit: "1a2b3g7"}}
		}, "artifacts[0].commit"},
		{"artifact commit uppercase", func(r *Request) {
			r.Artifacts = []Artifact{{Commit: "1A2B3C7"}}
		}, "artifacts[0].commit"},
		{"artifact commit too short", func(r *Request) {
			r.Artifacts = []Artifact{{Commit: "1a2b3c"}}
		}, "artifacts[0].commit"},
		{"artifact commit valid", func(r *Request) {
			r.Artifacts = []Artifact{{Commit: "1a2b3c7"}}
		}, ""},
		{"artifact path control char", func(r *Request) {
			r.Artifacts = []Artifact{{Path: "a\x01b"}}
		}, "artifacts[0].path"},
		{"artifact path valid", func(r *Request) {
			r.Artifacts = []Artifact{{Path: "src/main.go"}}
		}, ""},
		{"requested_grant action bad chars", func(r *Request) {
			r.RequestedGrant = &RequestedGrant{Action: "Repo.Read", Resource: "x"}
		}, "requested_grant.action"},
		{"requested_grant resource empty", func(r *Request) {
			r.RequestedGrant = &RequestedGrant{Action: "repo.read", Resource: ""}
		}, "requested_grant.resource"},
		{"requested_grant note too long", func(r *Request) {
			r.RequestedGrant = &RequestedGrant{Action: "repo.read", Resource: "x", Note: repeatRunes('a', maxGrantNoteCodePoints+1)}
		}, "requested_grant.note"},
		{"requested_grant valid", func(r *Request) {
			r.RequestedGrant = &RequestedGrant{Action: "repo.read", Resource: "github.com/o/r", Note: "hint"}
		}, ""},
		{"created zero", func(r *Request) { r.Created = time.Time{} }, "created"},
		{"deadline equal created", func(r *Request) { r.Deadline = r.Created }, "deadline"},
		{"deadline before created", func(r *Request) { r.Deadline = r.Created.Add(-time.Hour) }, "deadline"},
		{"deadline after created", func(r *Request) { r.Deadline = r.Created.Add(time.Hour) }, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validRequest()
			tc.mutate(r)
			err := Validate(r)
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want accept", err)
				}
				return
			}
			var fe *FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("Validate() = %v, want *FieldError for field %q", err, tc.wantField)
			}
			if fe.Field != tc.wantField {
				t.Fatalf("Validate() field = %q, want %q (%v)", fe.Field, tc.wantField, fe)
			}
		})
	}
}
