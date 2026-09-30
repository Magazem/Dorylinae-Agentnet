package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

// Review 68 A7 (R55-119): --artifact is parsed strictly in both forms, and a
// bad spec is a usage error (exit 2) before anything is sent, for request,
// inbox complete and session alike.
func TestArtifactFlagStrict(t *testing.T) {
	refused := []string{
		`{"url":"https://x","comit":"1a2b3c4"}`,
		`{"URL":"https://x"}`,
		`{"url":""}`,
		`url=`,
		`url=a url=b`,
		`{"url":"a","url":"b"}`,
		`{"url":1}`,
		`{}`,
		`comit=1a2b3c4`,
		`{"url":"` + string(rune(0x5C)) + `ud800"}`,
	}
	for _, spec := range refused {
		if a, err := artifactParam(spec); err == nil {
			t.Errorf("artifactParam(%s) accepted: %+v", spec, a)
		} else if !strings.HasPrefix(err.Error(), "--artifact: ") {
			t.Errorf("artifactParam(%s): error %q lacks the --artifact: prefix", spec, err)
		}
		for _, args := range [][]string{
			{"request", "bob", "task", "--title", "t", "--brief", "b", "--artifact", spec},
			{"complete", "r-1", "--status", "done", "--summary", "s", "--artifact", spec},
			{"result", "s-1", "--status", "done", "--summary", "s", "--artifact", spec},
		} {
			var out, errb bytes.Buffer
			if code := run(args, &out, &errb); code != exitUsage || !strings.Contains(errb.String(), "--artifact") {
				t.Errorf("%v: exit %d, want %d for the artifact (stderr %q)", args, code, exitUsage, errb.String())
			}
		}
	}
	want := daemon.ArtifactParam{URL: "https://x", Commit: "1a2b3c4"}
	for _, spec := range []string{`url=https://x commit=1a2b3c4`, `{"url":"https://x","commit":"1a2b3c4"}`} {
		got, err := artifactParam(spec)
		if err != nil || got != want {
			t.Errorf("artifactParam(%s) = %+v, %v; want %+v", spec, got, err, want)
		}
	}
}
