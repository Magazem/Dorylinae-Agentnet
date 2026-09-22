package request

import "testing"

func TestParseArtifactSpec(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		want    Artifact
		wantErr bool
	}{
		{"single key=value", "url=https://github.com/o/r/pull/12", Artifact{URL: "https://github.com/o/r/pull/12"}, false},
		{"space-separated pairs", "url=https://github.com/o/r/pull/12 branch=feat/x commit=1a2b3c4",
			Artifact{URL: "https://github.com/o/r/pull/12", Branch: "feat/x", Commit: "1a2b3c4"}, false},
		{"path pair", "path=src/main.go", Artifact{Path: "src/main.go"}, false},
		{"all four pairs", "url=https://x branch=b commit=c path=p",
			Artifact{URL: "https://x", Branch: "b", Commit: "c", Path: "p"}, false},
		{"json object", `{"url": "https://github.com/o/r/pull/12"}`, Artifact{URL: "https://github.com/o/r/pull/12"}, false},
		{"json object multiple members", `{"branch": "feat x", "commit": "1a2b3c4"}`,
			Artifact{Branch: "feat x", Commit: "1a2b3c4"}, false}, // value spaces need JSON form
		{"empty spec", "", Artifact{}, true},
		{"blank spec", "   ", Artifact{}, true},
		{"pair without equals", "urlvalue", Artifact{}, true},
		{"unknown key pair", "size=big", Artifact{}, true},
		{"unknown key json", `{"size": "big"}`, Artifact{}, true},
		{"duplicate key pair", "url=a url=b", Artifact{}, true},
		{"json not an object", `["a"]`, Artifact{}, true},
		{"json invalid", `{"url": }`, Artifact{}, true},
		{"json non-string value", `{"url": 1}`, Artifact{}, true},
		{"pairs no members", "  ", Artifact{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseArtifactSpec(tc.spec)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseArtifactSpec(%q) = %+v, want error", tc.spec, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseArtifactSpec(%q) = %v", tc.spec, err)
			}
			if got != tc.want {
				t.Fatalf("ParseArtifactSpec(%q) = %+v, want %+v", tc.spec, got, tc.want)
			}
		})
	}
}
