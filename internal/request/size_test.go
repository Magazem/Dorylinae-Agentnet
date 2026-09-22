package request

import (
	"errors"
	"strings"
	"testing"
)

// TestSizeCaps is a table test for every cap in Docs/protocol/request.md
// §Size limits: accepted at the limit, rejected with the documented field one
// over.
func TestSizeCaps(t *testing.T) {
	cases := []struct {
		name  string
		build func() *Request
		field string // "" for the total-body case (checked by CheckSize, not Validate)
	}{
		{"title at 120 code points", func() *Request {
			r := validRequest()
			r.Title = repeatRunes('a', 120)
			return r
		}, ""},
		{"title over at 121 code points", func() *Request {
			r := validRequest()
			r.Title = repeatRunes('a', 121)
			return r
		}, "title"},
		{"title 120 four-byte code points is 480 bytes", func() *Request {
			r := validRequest()
			r.Title = repeatRunes('\U0001F600', 120) // U+1F600, 4 bytes each
			return r
		}, ""},
		{"brief at 16384 bytes", func() *Request {
			r := validRequest()
			r.Brief = asciiFill(maxBriefBytes)
			return r
		}, ""},
		{"brief over at 16385 bytes", func() *Request {
			r := validRequest()
			r.Brief = asciiFill(maxBriefBytes + 1)
			return r
		}, "brief"},
		{"brief multi-byte char at the byte limit", func() *Request {
			r := validRequest()
			r.Brief = asciiFill(maxBriefBytes-2) + "é" // 'é' is 2 bytes: total 16384
			return r
		}, ""},
		{"brief multi-byte char straddling the byte limit", func() *Request {
			r := validRequest()
			r.Brief = asciiFill(maxBriefBytes-1) + "é" // total 16385
			return r
		}, "brief"},
		{"urgency_reason at 280 code points", func() *Request {
			r := validRequest()
			r.Urgency = UrgencyHigh
			r.UrgencyReason = repeatRunes('a', maxUrgencyReasonCodePoints)
			return r
		}, ""},
		{"urgency_reason over at 281 code points", func() *Request {
			r := validRequest()
			r.Urgency = UrgencyHigh
			r.UrgencyReason = repeatRunes('a', maxUrgencyReasonCodePoints+1)
			return r
		}, "urgency_reason"},
		{"0 artifacts", func() *Request {
			r := validRequest()
			r.Artifacts = nil
			return r
		}, ""},
		{"20 artifacts", func() *Request {
			r := validRequest()
			r.Artifacts = make([]Artifact, maxArtifacts)
			for i := range r.Artifacts {
				r.Artifacts[i] = Artifact{Path: "p"}
			}
			return r
		}, ""},
		{"21 artifacts over", func() *Request {
			r := validRequest()
			r.Artifacts = make([]Artifact, maxArtifacts+1)
			for i := range r.Artifacts {
				r.Artifacts[i] = Artifact{Path: "p"}
			}
			return r
		}, "artifacts"},
		{"artifact url at 2048 bytes", func() *Request {
			r := validRequest()
			r.Artifacts = []Artifact{{URL: padURL(maxURLBytes)}}
			return r
		}, ""},
		{"artifact url over at 2049 bytes", func() *Request {
			r := validRequest()
			r.Artifacts = []Artifact{{URL: padURL(maxURLBytes + 1)}}
			return r
		}, "artifacts[0].url"},
		{"artifact branch at 255 bytes", func() *Request {
			r := validRequest()
			r.Artifacts = []Artifact{{Branch: asciiFill(maxBranchBytes)}}
			return r
		}, ""},
		{"artifact branch over at 256 bytes", func() *Request {
			r := validRequest()
			r.Artifacts = []Artifact{{Branch: asciiFill(maxBranchBytes + 1)}}
			return r
		}, "artifacts[0].branch"},
		{"artifact commit at min 7 hex chars", func() *Request {
			r := validRequest()
			r.Artifacts = []Artifact{{Commit: strings.Repeat("a", minCommitHex)}}
			return r
		}, ""},
		{"artifact commit under min at 6 hex chars", func() *Request {
			r := validRequest()
			r.Artifacts = []Artifact{{Commit: strings.Repeat("a", minCommitHex-1)}}
			return r
		}, "artifacts[0].commit"},
		{"artifact commit at max 64 hex chars", func() *Request {
			r := validRequest()
			r.Artifacts = []Artifact{{Commit: strings.Repeat("a", maxCommitHex)}}
			return r
		}, ""},
		{"artifact commit over max at 65 hex chars", func() *Request {
			r := validRequest()
			r.Artifacts = []Artifact{{Commit: strings.Repeat("a", maxCommitHex+1)}}
			return r
		}, "artifacts[0].commit"},
		{"artifact path at 1024 bytes", func() *Request {
			r := validRequest()
			r.Artifacts = []Artifact{{Path: asciiFill(maxPathBytes)}}
			return r
		}, ""},
		{"artifact path over at 1025 bytes", func() *Request {
			r := validRequest()
			r.Artifacts = []Artifact{{Path: asciiFill(maxPathBytes + 1)}}
			return r
		}, "artifacts[0].path"},
		{"requested_grant.action at 64 chars", func() *Request {
			r := validRequest()
			r.RequestedGrant = &RequestedGrant{Action: strings.Repeat("a", maxGrantActionChars), Resource: "x"}
			return r
		}, ""},
		{"requested_grant.action over at 65 chars", func() *Request {
			r := validRequest()
			r.RequestedGrant = &RequestedGrant{Action: strings.Repeat("a", maxGrantActionChars+1), Resource: "x"}
			return r
		}, "requested_grant.action"},
		{"requested_grant.resource at 512 bytes", func() *Request {
			r := validRequest()
			r.RequestedGrant = &RequestedGrant{Action: "a", Resource: asciiFill(maxGrantResourceBytes)}
			return r
		}, ""},
		{"requested_grant.resource over at 513 bytes", func() *Request {
			r := validRequest()
			r.RequestedGrant = &RequestedGrant{Action: "a", Resource: asciiFill(maxGrantResourceBytes + 1)}
			return r
		}, "requested_grant.resource"},
		{"requested_grant.note at 280 code points", func() *Request {
			r := validRequest()
			r.RequestedGrant = &RequestedGrant{Action: "a", Resource: "x", Note: repeatRunes('a', maxGrantNoteCodePoints)}
			return r
		}, ""},
		{"requested_grant.note over at 281 code points", func() *Request {
			r := validRequest()
			r.RequestedGrant = &RequestedGrant{Action: "a", Resource: "x", Note: repeatRunes('a', maxGrantNoteCodePoints+1)}
			return r
		}, "requested_grant.note"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.build()
			err := Validate(r)
			if tc.field == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want accept", err)
				}
				return
			}
			var fe *FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("Validate() = %v, want *FieldError for field %q", err, tc.field)
			}
			if fe.Field != tc.field {
				t.Fatalf("Validate() field = %q, want %q (%v)", fe.Field, tc.field, fe)
			}
		})
	}
}

// padURL returns a valid https URL whose total byte length is exactly n.
func padURL(n int) string {
	const prefix = "https://example.com/"
	if n < len(prefix) {
		n = len(prefix)
	}
	return prefix + strings.Repeat("a", n-len(prefix))
}

// nearMaxRequest returns a Request whose every field is individually valid
// (mostly at its own maximum, using '"' filler so JSON escaping doubles the
// canonical bytes, per Docs/protocol/request.md §Size limits: "a brief of
// 16384 \" characters is 32768 bytes canonical"), except Brief, left for the
// caller to tune the total canonical size.
func nearMaxRequest() *Request {
	r := validRequest()
	r.Title = repeatRunes('"', maxTitleCodePoints)
	r.Urgency = UrgencyHigh
	r.UrgencyReason = repeatRunes('"', maxUrgencyReasonCodePoints)
	r.RequestedGrant = &RequestedGrant{
		Action:   strings.Repeat("a", maxGrantActionChars),
		Resource: repeatRunes('"', maxGrantResourceBytes),
		Note:     repeatRunes('"', maxGrantNoteCodePoints),
	}
	arts := make([]Artifact, maxArtifacts)
	for i := range arts {
		arts[i] = Artifact{
			Path:   repeatRunes('"', maxPathBytes),
			Branch: repeatRunes('"', maxBranchBytes),
			Commit: strings.Repeat("a", maxCommitHex),
		}
	}
	r.Artifacts = arts
	return r
}

// TestTotalBodyCap covers the whole-canonical-request cap
// (Docs/protocol/request.md §Size limits): built from fields that are each
// individually valid, 65536 bytes accepted, 65537 rejected with
// request_too_large (a TooLargeError). Brief is tuned by binary search: every
// other field is fixed near its own maximum (nearMaxRequest), so JSON
// escaping alone pushes the total over 64 KiB well before any single field
// cap is hit, as the spec notes.
func TestTotalBodyCap(t *testing.T) {
	build := func(briefLen int) *Request {
		r := nearMaxRequest()
		r.Brief = strings.Repeat("a", briefLen)
		return r
	}
	canonLen := func(r *Request) int {
		t.Helper()
		if err := Validate(r); err != nil {
			t.Fatalf("Validate: %v", err)
		}
		canon, err := Canonical(r)
		if err != nil {
			t.Fatalf("Canonical: %v", err)
		}
		return len(canon)
	}

	lo, hi := 1, maxBriefBytes
	if got := canonLen(build(lo)); got > MaxRequestBody {
		t.Fatalf("canonical size at brief=%d is already %d, over %d", lo, got, MaxRequestBody)
	}
	if got := canonLen(build(hi)); got < MaxRequestBody+1 {
		t.Fatalf("canonical size at brief=%d is only %d, want to reach %d", hi, got, MaxRequestBody+1)
	}
	// Binary search for the brief length whose canonical size is exactly
	// MaxRequestBody (each extra unescaped brief byte adds exactly one
	// canonical byte).
	for lo < hi {
		mid := (lo + hi) / 2
		if canonLen(build(mid)) < MaxRequestBody {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	atLimit := build(lo)
	canon, err := Canonical(atLimit)
	if err != nil {
		t.Fatalf("Canonical(at limit): %v", err)
	}
	if len(canon) != MaxRequestBody {
		t.Fatalf("canonical size = %d, want exactly %d", len(canon), MaxRequestBody)
	}
	if err := CheckSize(canon); err != nil {
		t.Fatalf("CheckSize(at limit) = %v, want accept", err)
	}

	over := build(lo + 1)
	if err := Validate(over); err != nil {
		t.Fatalf("Validate(over, still field-valid): %v", err)
	}
	canon, err = Canonical(over)
	if err != nil {
		t.Fatalf("Canonical(over): %v", err)
	}
	if len(canon) != MaxRequestBody+1 {
		t.Fatalf("canonical size = %d, want exactly %d", len(canon), MaxRequestBody+1)
	}
	var tooLarge *TooLargeError
	if err := CheckSize(canon); !errors.As(err, &tooLarge) {
		t.Fatalf("CheckSize(over) = %v, want *TooLargeError", err)
	} else if tooLarge.Size != MaxRequestBody+1 || tooLarge.Limit != MaxRequestBody {
		t.Fatalf("TooLargeError = %+v, want size %d limit %d", tooLarge, MaxRequestBody+1, MaxRequestBody)
	}
}
