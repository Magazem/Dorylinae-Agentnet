package cilint

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// workflows returns every .github/workflows/*.yml as lines (CR stripped:
// Windows checkouts may use CRLF).
func workflows(t *testing.T) map[string][]string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no workflows found")
	}
	out := map[string][]string{}
	for _, p := range paths {
		raw, err := os.ReadFile(p) //nolint:gosec // repository files
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(p)] = strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	}
	return out
}

func names(m map[string][]string) []string {
	var n []string
	for k := range m {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

func indent(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

func isComment(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), "#") }

// stepAt returns the lines of the YAML list item (a step) holding line i.
func stepAt(lines []string, i int) []string {
	start := i
	for start > 0 && !strings.HasPrefix(strings.TrimLeft(lines[start], " "), "- ") {
		start--
	}
	d := indent(lines[start])
	end := start + 1
	for end < len(lines) {
		l := lines[end]
		if strings.TrimSpace(l) != "" && !isComment(l) && indent(l) <= d {
			break
		}
		end++
	}
	return lines[start:end]
}

// job returns the lines of a top-level job (two-space indented key).
func job(t *testing.T, lines []string, name string) []string {
	t.Helper()
	key := regexp.MustCompile(`^  [A-Za-z0-9_-]+:\s*$`)
	for i, l := range lines {
		if l != "  "+name+":" {
			continue
		}
		end := i + 1
		for end < len(lines) && !key.MatchString(lines[end]) && (lines[end] == "" || indent(lines[end]) >= 2 || isComment(lines[end])) {
			end++
		}
		return lines[i:end]
	}
	t.Fatalf("no job %q", name)
	return nil
}

var (
	usesRe       = regexp.MustCompile(`^\s*(?:-\s+)?uses:\s*(\S+)(.*)$`)
	pinnedRe     = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(/[A-Za-z0-9_./-]+)?@[0-9a-f]{40}$`)
	versionNote  = regexp.MustCompile(`^\s+#\s*v[0-9]+(\.[0-9]+)*\s*$`)
	cacheFalseRe = regexp.MustCompile(`^\s*cache:\s*false\s*$`)
	noPersistRe  = regexp.MustCompile(`^\s*persist-credentials:\s*false\s*$`)
)

func hasLine(block []string, re *regexp.Regexp) bool {
	for _, l := range block {
		if !isComment(l) && re.MatchString(l) {
			return true
		}
	}
	return false
}

// R55-032: every action is pinned by a full commit SHA, with the version it
// names as a comment (Dependabot keeps both current).
func TestWorkflowsPinned(t *testing.T) {
	wf := workflows(t)
	for _, name := range names(wf) {
		for i, l := range wf[name] {
			if isComment(l) {
				continue
			}
			m := usesRe.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			if !pinnedRe.MatchString(m[1]) || !versionNote.MatchString(m[2]) {
				t.Errorf("%s:%d: %q is not pinned as owner/repo@<40-hex SHA> # vX.Y.Z", name, i+1, strings.TrimSpace(l))
			}
		}
	}
}

// R55-032 / O-185: no Go build cache in any release job (a poisoned cache
// entry would reach the release binaries).
func TestReleaseNoCache(t *testing.T) {
	lines := workflows(t)["release.yml"]
	n := 0
	for i, l := range lines {
		if m := usesRe.FindStringSubmatch(l); m != nil && !isComment(l) && strings.HasPrefix(m[1], "actions/setup-go@") {
			n++
			if !hasLine(stepAt(lines, i), cacheFalseRe) {
				t.Errorf("release.yml:%d: actions/setup-go without `cache: false`", i+1)
			}
		}
	}
	if n == 0 {
		t.Fatal("release.yml has no actions/setup-go step")
	}
}

// R55-110: every workflow sets its own token permissions, and no checkout
// leaves the token in .git/config.
func TestWorkflowPermissions(t *testing.T) {
	wf := workflows(t)
	top := regexp.MustCompile(`^permissions:`)
	for _, name := range names(wf) {
		lines := wf[name]
		if !hasLine(lines, top) {
			t.Errorf("%s: no top-level permissions: block", name)
		}
		for i, l := range lines {
			if m := usesRe.FindStringSubmatch(l); m != nil && !isComment(l) && strings.HasPrefix(m[1], "actions/checkout@") {
				if !hasLine(stepAt(lines, i), noPersistRe) {
					t.Errorf("%s:%d: actions/checkout without `persist-credentials: false`", name, i+1)
				}
			}
		}
	}
}

// R55-003: the sums job logs the digest of SHA256SUMS (and writes it to the
// summary and its outputs) before the first repository code in the job.
func TestReleaseLogsSumsDigestFirst(t *testing.T) {
	sums := job(t, workflows(t)["release.yml"], "sums")
	goCmd := regexp.MustCompile(`(^|[\s;&|(!])go\s`)
	logged, firstGo := -1, -1
	summary, output := false, false
	for i, l := range sums {
		if isComment(l) {
			continue
		}
		if logged < 0 && strings.Contains(l, `echo "release-sums-sha256: $d"`) {
			logged = i
		}
		if firstGo < 0 && goCmd.MatchString(l) {
			firstGo = i
		}
		summary = summary || (strings.Contains(l, "release-sums-sha256:") && strings.Contains(l, "GITHUB_STEP_SUMMARY"))
		output = output || strings.Contains(l, "sums_sha256=$d")
	}
	if logged < 0 {
		t.Fatal(`sums job does not echo "release-sums-sha256: $d"`)
	}
	if firstGo >= 0 && firstGo < logged {
		t.Errorf("sums job runs %q before logging the digest", strings.TrimSpace(sums[firstGo]))
	}
	if !summary || !output {
		t.Errorf("sums job: digest in step summary %v, in job outputs %v; want both", summary, output)
	}
}

// R55-003 / review 57a-7 (A10 made mechanical): the draft's notes can be
// edited by anyone who can write to the repository, so CI's notes carry no
// command or digest to copy; the draft job re-checks the logged digest.
func TestDraftNotesCarryNoCommands(t *testing.T) {
	draft := job(t, workflows(t)["release.yml"], "draft")
	var notes []string
	in := false
	for _, l := range draft {
		switch {
		case !in && regexp.MustCompile(`<<-?\s*['"]?EOF['"]?\s*$`).MatchString(l):
			in = true
		case in && strings.TrimSpace(l) == "EOF":
			in = false
		case in:
			notes = append(notes, l)
		}
	}
	if len(notes) == 0 {
		t.Fatal("no release notes heredoc in the draft job")
	}
	text := strings.Join(notes, "\n")
	for _, bad := range []string{"releasesign", "gh release", "release-sums-sha256"} {
		if strings.Contains(text, bad) {
			t.Errorf("draft notes contain %q:\n%s", bad, text)
		}
	}
	if !strings.Contains(text, "Docs/ops/release-signing.md") {
		t.Errorf("draft notes do not point to the runbook:\n%s", text)
	}
	if !strings.Contains(strings.Join(draft, "\n"), "needs.sums.outputs.sums_sha256") {
		t.Error("draft job does not compare SHA256SUMS with the sums job's logged digest")
	}
}
