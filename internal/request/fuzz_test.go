package request

import (
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/agentcard"
)

// FuzzValidateCanonicalRoundTrip fuzzes the field values of an otherwise
// valid Request. For any input, either Validate rejects it, or it accepts a
// Request whose Canonical bytes decode (agentcard.ParseStrict, then Decode,
// which calls Validate again) back to a Request with an identical Canonical
// encoding: canonical(request) round trips.
func FuzzValidateCanonicalRoundTrip(f *testing.F) {
	f.Add("A title", "A brief.\nWith a line.", "normal", "", "", "https://example.com/x", "", "")
	f.Add("", "", "high", "", "", "", "", "")
	f.Add(repeatRunes('a', 200), asciiFill(20000), "blocking", "high", "reason", "bad url", "feat~x", "1a2b3c")
	f.Add("Title", "Brief", "normal", "", "", "", "", "")

	f.Fuzz(func(t *testing.T, title, brief, urgency, urgencyDeclared, urgencyReason, url, branch, commit string) {
		r := validRequest()
		r.Title = title
		r.Brief = brief
		r.Urgency = urgency
		r.UrgencyDeclared = urgencyDeclared
		r.UrgencyReason = urgencyReason
		if url != "" || branch != "" || commit != "" {
			r.Artifacts = []Artifact{{URL: url, Branch: branch, Commit: commit}}
		} else {
			r.Artifacts = nil
		}

		err := Validate(r)
		if err != nil {
			return // Validate correctly rejected a malformed value; nothing more to check.
		}

		canon1, err := Canonical(r)
		if err != nil {
			t.Fatalf("Canonical after Validate accepted: %v", err)
		}

		v, err := agentcard.ParseStrict(canon1)
		if err != nil {
			t.Fatalf("ParseStrict(canonical(request)): %v", err)
		}
		body, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("canonical(request) did not parse to an object")
		}

		r2, err := Decode(body)
		if err != nil {
			t.Fatalf("Decode(canonical(request)) rejected a value Validate accepted: %v", err)
		}

		canon2, err := Canonical(r2)
		if err != nil {
			t.Fatalf("Canonical after round-trip Decode: %v", err)
		}
		if string(canon1) != string(canon2) {
			t.Fatalf("canonical round trip mismatch:\n  first:  %s\n  second: %s", canon1, canon2)
		}
	})
}
