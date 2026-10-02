package main

// A6 (spec 57 §7): `fetch` against a fake `gh`. The fake is this test binary,
// copied under the name gh(.exe) into a folder that is the whole PATH, so
// the real gh can never be reached. It serves canned API output keyed by its
// exact argv and records every argv it receives.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const fakeGHEnv = "RELEASESIGN_FAKE_GH_WORLD"

func TestMain(m *testing.M) {
	if w := os.Getenv(fakeGHEnv); w != "" && strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == "gh" {
		os.Exit(fakeGH(w, os.Args[1:]))
	}
	code := m.Run()
	if fakeGHBin != "" {
		_ = os.Remove(fakeGHBin)
		_ = os.Remove(filepath.Dir(fakeGHBin))
	}
	os.Exit(code)
}

type ghResp struct {
	Out    []byte
	Code   int
	Stderr string
}

func argvKey(args ...string) string { return strings.Join(args, "\x1f") }

// fakeGH answers one gh invocation from the world file.
func fakeGH(worldFile string, args []string) int {
	raw, err := os.ReadFile(worldFile) //nolint:gosec // test fixture
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "fake gh:", err)
		return 3
	}
	var world map[string]ghResp
	if err := json.Unmarshal(raw, &world); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "fake gh:", err)
		return 3
	}
	logLine, _ := json.Marshal(args)
	if f, err := os.OpenFile(worldFile+".log", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600); err == nil { //nolint:gosec // test fixture
		_, _ = f.Write(append(logLine, '\n'))
		_ = f.Close()
	}
	r, ok := world[argvKey(args...)]
	if !ok {
		_, _ = fmt.Fprintf(os.Stderr, "fake gh: HTTP 404: no canned response for %q\n", args)
		return 1
	}
	_, _ = os.Stdout.Write(r.Out)
	_, _ = io.WriteString(os.Stderr, r.Stderr)
	return r.Code
}

var (
	fakeGHOnce sync.Once
	fakeGHBin  string
)

// installFakeGH puts the fake gh first (and alone) on PATH.
func installFakeGH(t *testing.T) {
	t.Helper()
	fakeGHOnce.Do(func() {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		dir, err := os.MkdirTemp("", "releasesign-fakegh-")
		if err != nil {
			t.Fatal(err)
		}
		name := "gh"
		if runtime.GOOS == "windows" {
			name = "gh.exe"
		}
		b, err := os.ReadFile(self) //nolint:gosec // this test binary
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o700); err != nil { //nolint:gosec // an executable test helper
			t.Fatal(err)
		}
		fakeGHBin = filepath.Join(dir, name)
	})
	if fakeGHBin == "" {
		t.Fatal("no fake gh")
	}
	t.Setenv("PATH", filepath.Dir(fakeGHBin))
}

// --- the GitHub state a fetch sees ---

const (
	fxRepo   = "Magazem/Dorylinae-Agentnet"
	fxTag    = "v1.2.3"
	fxCommit = "0123456789abcdef0123456789abcdef01234567"
	fxOther  = "fedcba9876543210fedcba9876543210fedcba98"
	fxRunID  = 1001
	fxRelID  = 5001
)

type obj = map[string]any

type fixture struct {
	runPages    [][]obj       // release.yml push runs, one slice per page
	dryRun      obj           // GET runs/<id> for -dry-run
	attempts    map[int][]obj // attempt -> jobs
	logs        map[int]string
	releases    [][]obj
	assets      map[int][]byte
	tagRef      obj
	annotated   obj // git/tags/<sha> when tagRef is an annotated tag
	sums        []byte
	archives    map[string][]byte
	overrides   map[string]ghResp
	dropReqKeys []string
}

func hostedJob(id int, name string) obj {
	return obj{"id": id, "name": name, "status": "completed", "conclusion": "success",
		"runner_id": id + 1, "runner_name": fmt.Sprintf("GitHub Actions %d", id),
		"runner_group_id": 0, "runner_group_name": "GitHub Actions", "labels": []string{"ubuntu-latest"}}
}

// reusedJob is a successful job listed without runner data, as a "re-run
// failed jobs" attempt may list a job it reused (spec 57 §8 V5).
func reusedJob(id int, name string) obj {
	return obj{"id": id, "name": name, "status": "completed", "conclusion": "success",
		"runner_id": nil, "runner_name": nil, "runner_group_id": nil, "runner_group_name": nil, "labels": []string{"ubuntu-latest"}}
}

func goodJobs() []obj {
	return []obj{hostedJob(2001, "meta"), hostedJob(2002, "test"), hostedJob(2003, "build (linux, amd64)"),
		hostedJob(2004, "build (windows, arm64)"), hostedJob(3001, "sums"), hostedJob(2005, "install-sh"),
		hostedJob(2006, "homebrew"), hostedJob(2007, "draft")}
}

var (
	esc = string(rune(27))
	bom = string([]byte{0xEF, 0xBB, 0xBF})
)

// sumsLog is a sums job log as GitHub serves it: BOM, timestamps, CRLF, the
// echoed script (coloured, $d unexpanded) and the expanded line.
func sumsLog(digests ...string) string {
	var b strings.Builder
	b.WriteString(bom + "2026-09-28T17:35:05.9015371Z ##[group]Run set -eu\r\n")
	b.WriteString("2026-09-28T17:35:05.9016127Z " + esc + "[36;1md=$(sha256sum dist/SHA256SUMS | cut -d' ' -f1)" + esc + "[0m\r\n")
	b.WriteString("2026-09-28T17:35:05.9016129Z " + esc + "[36;1mecho \"release-sums-sha256: $d\"" + esc + "[0m\r\n")
	b.WriteString("2026-09-28T17:35:05.9016200Z ##[endgroup]\r\n")
	for _, d := range digests {
		b.WriteString("2026-09-28T17:35:06.0000001Z release-sums-sha256: " + d + "\r\n")
	}
	b.WriteString("2026-09-28T17:35:06.1000000Z 0000  agentnet_1.2.3_linux_amd64.tar.gz\r\n")
	return b.String()
}

func newFixture(version, salt string) *fixture {
	sums, archives := releaseFiles(version, salt)
	f := &fixture{sums: sums, archives: archives, assets: map[int][]byte{}, overrides: map[string]ghResp{}}
	f.runPages = [][]obj{{
		{"id": 900, "event": "push", "path": releaseWorkflow, "head_branch": "v1.2.2", "head_sha": fxOther,
			"status": "completed", "conclusion": "success", "run_attempt": 1},
		{"id": fxRunID, "event": "push", "path": releaseWorkflow, "head_branch": fxTag, "head_sha": fxCommit,
			"status": "completed", "conclusion": "success", "run_attempt": 1},
	}}
	f.attempts = map[int][]obj{1: goodJobs()}
	f.logs = map[int]string{
		3001: sumsLog(sha256Of(sums)),
		2003: "2026-09-28T17:34:50.0000000Z go version go1.27.0 linux/amd64\n",
	}
	assets := []obj{{"id": 6000, "name": "SHA256SUMS", "size": len(sums)}}
	f.assets[6000] = sums
	i := 6001
	for name, b := range archives {
		assets = append(assets, obj{"id": i, "name": name, "size": len(b)})
		f.assets[i] = b
		i++
	}
	f.releases = [][]obj{{
		{"id": 4000, "tag_name": "v1.2.2", "draft": false, "assets": []obj{}},
		{"id": fxRelID, "tag_name": fxTag, "draft": true, "assets": assets},
	}}
	f.tagRef = obj{"ref": "refs/tags/" + fxTag, "object": obj{"type": "commit", "sha": fxCommit}}
	return f
}

func goodFixture() *fixture { return newFixture("1.2.3", "") }

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func (f *fixture) world() map[string]ghResp {
	w := map[string]ghResp{}
	api := func(args ...string) string { return argvKey(append([]string{"api"}, args...)...) }
	total := 0
	for _, p := range f.runPages {
		total += len(p)
	}
	var runs []byte
	for _, p := range f.runPages {
		runs = append(runs, mustJSON(obj{"total_count": total, "workflow_runs": p})...)
	}
	w[api("--paginate", "repos/"+fxRepo+"/actions/workflows/release.yml/runs?event=push&per_page=100")] = ghResp{Out: runs}
	if f.dryRun != nil {
		w[api("repos/"+fxRepo+"/actions/runs/"+fmt.Sprint(f.dryRun["id"]))] = ghResp{Out: mustJSON(f.dryRun)}
	}
	for n, jobs := range f.attempts {
		w[api("--paginate", fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d/jobs?per_page=100", fxRepo, fxRunID, n))] =
			ghResp{Out: mustJSON(obj{"total_count": len(jobs), "jobs": jobs})}
	}
	for id, l := range f.logs {
		w[api("--allow-escape-sequences", fmt.Sprintf("repos/%s/actions/jobs/%d/logs", fxRepo, id))] = ghResp{Out: []byte(l)}
	}
	var rels []byte
	for _, p := range f.releases {
		rels = append(rels, mustJSON(p)...)
	}
	w[api("--paginate", "repos/"+fxRepo+"/releases?per_page=100")] = ghResp{Out: rels}
	for id, b := range f.assets {
		w[api("--allow-escape-sequences", "-H", "Accept: application/octet-stream", fmt.Sprintf("repos/%s/releases/assets/%d", fxRepo, id))] = ghResp{Out: b}
	}
	w[api("repos/"+fxRepo+"/git/ref/tags/"+fxTag)] = ghResp{Out: mustJSON(f.tagRef)}
	if f.annotated != nil {
		sha := f.tagRef["object"].(obj)["sha"].(string)
		w[api("repos/"+fxRepo+"/git/tags/"+sha)] = ghResp{Out: mustJSON(f.annotated)}
	}
	for k, v := range f.overrides {
		w[k] = v
	}
	for _, k := range f.dropReqKeys {
		delete(w, k)
	}
	return w
}

type fetchResult struct {
	code           int
	stdout, stderr string
	out            string
	calls          [][]string
}

// runFetch runs `releasesign fetch` against the fixture. On a refusal it
// also checks that nothing was written.
func runFetch(t *testing.T, f *fixture, args ...string) fetchResult {
	t.Helper()
	installFakeGH(t)
	dir := t.TempDir()
	worldFile := filepath.Join(dir, "world.json")
	if err := os.WriteFile(worldFile, mustJSON(f.world()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeGHEnv, worldFile)
	out := filepath.Join(dir, "rel")
	full := append([]string{"fetch", "-out", out}, args...)
	var so, se bytes.Buffer
	code := run(full, &so, &se)
	r := fetchResult{code: code, stdout: so.String(), stderr: se.String(), out: out}
	if raw, err := os.ReadFile(worldFile + ".log"); err == nil { //nolint:gosec // test temp dir
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			var a []string
			if err := json.Unmarshal([]byte(line), &a); err != nil {
				t.Fatal(err)
			}
			r.calls = append(r.calls, a)
		}
	}
	if code != 0 {
		if _, err := os.Lstat(out); err == nil {
			t.Fatalf("a refused fetch wrote %s", out)
		}
	}
	return r
}

var releaseArgs = []string{"-tag", fxTag, "-commit", fxCommit}

func TestFetchAcceptsGoodRun(t *testing.T) {
	f := goodFixture()
	r := runFetch(t, f, releaseArgs...)
	if r.code != 0 {
		t.Fatalf("rc=%d stderr=%s", r.code, r.stderr)
	}
	digest := sha256Of(f.sums)
	if !strings.Contains(r.stdout, "expected sha256: "+digest+"\n") {
		t.Fatalf("stdout does not print the expected digest:\n%s", r.stdout)
	}
	for _, want := range []string{"https://github.com/" + fxRepo + "/actions/runs/1001", fxCommit, "go version go1.27.0 linux/amd64"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, r.stdout)
		}
	}
	// Exactly the seven assets, byte for byte, and nothing else.
	entries, err := os.ReadDir(r.out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 7 {
		t.Fatalf("fetch wrote %d files, want 7", len(entries))
	}
	want := map[string][]byte{"SHA256SUMS": f.sums}
	for n, b := range f.archives {
		want[n] = b
	}
	for n, b := range want {
		got, err := os.ReadFile(filepath.Join(r.out, n)) //nolint:gosec // test temp dir
		if err != nil || !bytes.Equal(got, b) {
			t.Errorf("%s: not written as served (%v)", n, err)
		}
	}
	// What fetch downloaded is signable with the digest it printed.
	key := filepath.Join(t.TempDir(), "k")
	runOK(t, "keygen", "-out", key)
	runOK(t, "sign", "-key", key, "-expect-sha256", digest, "-archives", r.out, filepath.Join(r.out, "SHA256SUMS"))
}

// gh is run with an argv list: every call is one of the exact argument
// vectors the fixture serves (the fake answers only those), values from the
// command line reach gh as single arguments, and values with shell
// metacharacters are refused before gh runs at all.
func TestFetchInvokesGhWithArgv(t *testing.T) {
	r := runFetch(t, goodFixture(), releaseArgs...)
	if r.code != 0 {
		t.Fatalf("rc=%d stderr=%s", r.code, r.stderr)
	}
	first := []string{"api", "--paginate", "repos/" + fxRepo + "/actions/workflows/release.yml/runs?event=push&per_page=100"}
	if len(r.calls) == 0 || strings.Join(r.calls[0], "\x00") != strings.Join(first, "\x00") {
		t.Fatalf("first gh call %q, want %q", r.calls[0], first)
	}
	for _, c := range r.calls {
		if c[0] != "api" {
			t.Errorf("gh call %q is not `gh api`", c)
		}
	}
	for name, args := range map[string][]string{
		"repo":   {"-repo", "a/b;touch pwned", "-tag", fxTag, "-commit", fxCommit},
		"commit": {"-tag", fxTag, "-commit", fxCommit[:39] + "$"},
		"tag":    {"-tag", "v1.2.3$(id)", "-commit", fxCommit},
	} {
		r := runFetch(t, goodFixture(), args...)
		if r.code != 2 || len(r.calls) != 0 {
			t.Errorf("%s: rc=%d, %d gh calls; want a usage error before any gh call", name, r.code, len(r.calls))
		}
	}
}

func TestFetchRefuses(t *testing.T) {
	type tc struct {
		mutate func(f *fixture)
		args   []string
		want   string
	}
	runs := func(f *fixture) []obj { return f.runPages[len(f.runPages)-1] }
	cand := func(f *fixture) obj { return runs(f)[1] }
	draft := func(f *fixture) obj { return f.releases[0][1] }
	api := func(args ...string) string { return argvKey(append([]string{"api"}, args...)...) }
	cases := map[string]tc{
		"no run": {mutate: func(f *fixture) { f.runPages[0] = f.runPages[0][:1] }, want: "found 0"},
		"two runs at the commit": {mutate: func(f *fixture) {
			f.runPages[0] = append(f.runPages[0], obj{"id": 1002, "event": "push", "path": releaseWorkflow,
				"head_branch": fxTag, "head_sha": fxCommit, "status": "completed", "conclusion": "success", "run_attempt": 1})
		}, want: "found 2"},
		"tag moved (another push run)": {mutate: func(f *fixture) {
			f.runPages[0] = append(f.runPages[0], obj{"id": 1003, "event": "push", "path": releaseWorkflow,
				"head_branch": fxTag, "head_sha": fxOther, "status": "completed", "conclusion": "success", "run_attempt": 1})
		}, want: "tag was moved"},
		"tag moved, on the second page": {mutate: func(f *fixture) {
			f.runPages = append(f.runPages, []obj{{"id": 1003, "event": "push", "path": releaseWorkflow,
				"head_branch": fxTag, "head_sha": fxOther, "status": "completed", "conclusion": "failure", "run_attempt": 1}})
		}, want: "tag was moved"},
		"gh error mid-pagination": {mutate: func(f *fixture) {
			k := api("--paginate", "repos/"+fxRepo+"/actions/workflows/release.yml/runs?event=push&per_page=100")
			w := f.world()[k]
			f.overrides[k] = ghResp{Out: w.Out, Code: 1, Stderr: "HTTP 502: Bad Gateway (page 2)"}
		}, want: "HTTP 502"},
		"short run list": {mutate: func(f *fixture) {
			k := api("--paginate", "repos/"+fxRepo+"/actions/workflows/release.yml/runs?event=push&per_page=100")
			f.overrides[k] = ghResp{Out: mustJSON(obj{"total_count": 5, "workflow_runs": f.runPages[0]})}
		}, want: "incomplete"},
		"workflow_dispatch run":   {mutate: func(f *fixture) { cand(f)["event"] = "workflow_dispatch" }, want: "triggered by"},
		"other workflow":          {mutate: func(f *fixture) { cand(f)["path"] = ".github/workflows/ci.yml" }, want: "is workflow"},
		"run failed":              {mutate: func(f *fixture) { cand(f)["conclusion"] = "failure" }, want: "want completed/success"},
		"run still in progress":   {mutate: func(f *fixture) { cand(f)["status"] = "in_progress"; cand(f)["conclusion"] = nil }, want: "want completed/success"},
		"non-hex head_sha":        {mutate: func(f *fixture) { cand(f)["head_sha"] = strings.Repeat("z", 40) }, want: "malformed head_sha"},
		"non-numeric run_attempt": {mutate: func(f *fixture) { cand(f)["run_attempt"] = "1; id" }, want: "malformed"},
		"self-hosted runner": {mutate: func(f *fixture) {
			j := f.attempts[1][2]
			j["runner_group_name"], j["runner_group_id"], j["runner_name"] = "Default", 1, "my-box"
		}, want: "not a GitHub-hosted runner"},
		"hosted name, other group id": {mutate: func(f *fixture) { f.attempts[1][2]["runner_group_id"] = 7 }, want: "not a GitHub-hosted runner"},
		"a job failed":                {mutate: func(f *fixture) { f.attempts[1][6]["conclusion"] = "failure" }, want: `"homebrew"`},
		"self-hosted in attempt 1 only, partial re-run as attempt 2": {mutate: func(f *fixture) {
			cand(f)["run_attempt"] = 2
			f.attempts[1][2]["runner_group_name"], f.attempts[1][2]["runner_group_id"] = "Default", 1
			f.attempts[1][6]["conclusion"] = "failure"
			f.attempts[2] = []obj{hostedJob(2106, "homebrew"), hostedJob(2107, "draft")}
		}, want: "attempt 1 job \"build (linux, amd64)\""},
		"non-numeric job id": {mutate: func(f *fixture) { f.attempts[1][0]["id"] = -5 }, want: "malformed job id"},
		"no digest line":     {mutate: func(f *fixture) { f.logs[3001] = sumsLog() }, want: "no release-sums-sha256 line"},
		"only the unexpanded script line": {mutate: func(f *fixture) {
			f.logs[3001] = "2026-09-28T17:35:05.9Z echo \"release-sums-sha256: $d\"\n"
		}, want: "no release-sums-sha256 line"},
		"two digest lines": {mutate: func(f *fixture) {
			f.logs[3001] = sumsLog(sha256Of(f.sums), strings.Repeat("0", 64))
		}, want: "2 different"},
		"draft SHA256SUMS is not the logged one": {mutate: func(f *fixture) {
			other := newFixture("1.2.3", "trojan")
			f.releases, f.assets, f.archives = other.releases, other.assets, other.archives
		}, want: "digest check"},
		"draft archive does not match its line": {mutate: func(f *fixture) {
			for id, b := range f.assets {
				if id != 6000 && bytes.Contains(b, []byte("linux_amd64")) {
					f.assets[id] = []byte("trojan")
				}
			}
		}, want: "archive check"},
		"two releases with the tag": {mutate: func(f *fixture) {
			f.releases = append(f.releases, []obj{{"id": 5002, "tag_name": fxTag, "draft": true, "assets": draft(f)["assets"]}})
		}, want: "found 2"},
		"release is not a draft": {mutate: func(f *fixture) { draft(f)["draft"] = false }, want: "not a draft"},
		"extra asset on the draft": {mutate: func(f *fixture) {
			draft(f)["assets"] = append(draft(f)["assets"].([]obj), obj{"id": 6100, "name": "install.sh", "size": 100})
		}, want: `"install.sh"`},
		"archive asset missing": {mutate: func(f *fixture) {
			draft(f)["assets"] = draft(f)["assets"].([]obj)[:6]
		}, want: "the draft has no"},
		"non-numeric asset id": {mutate: func(f *fixture) { draft(f)["assets"].([]obj)[0]["id"] = "6000/../../x" }, want: "malformed"},
		"asset without a size": {mutate: func(f *fixture) { delete(draft(f)["assets"].([]obj)[1], "size") }, want: "no size for asset"},
		"negative asset size":  {mutate: func(f *fixture) { draft(f)["assets"].([]obj)[1]["size"] = -1 }, want: "malformed size"},
		"SHA256SUMS listed over its cap": {mutate: func(f *fixture) {
			draft(f)["assets"].([]obj)[0]["size"] = 1 << 20
		}, want: "SHA256SUMS is 1048576 bytes, over the 65536-byte cap"},
		"archive listed over its cap": {mutate: func(f *fixture) {
			draft(f)["assets"].([]obj)[1]["size"] = 300 << 20
		}, want: "is 314572800 bytes, over the 209715200-byte cap"},
		"successful job with no runner": {mutate: func(f *fixture) {
			f.attempts[1][1] = reusedJob(2002, "test")
		}, want: "attempt 1 job \"test\" (2002) succeeded but reports no runner"},
		"re-run lists a no-runner job no earlier attempt ran": {mutate: func(f *fixture) {
			cand(f)["run_attempt"] = 2
			f.attempts[1][6]["conclusion"] = "failure"
			f.attempts[2] = []obj{reusedJob(2999, "build (linux, amd64)"), hostedJob(2106, "homebrew"), hostedJob(2107, "draft")}
		}, want: "attempt 2 job \"build (linux, amd64)\" (2999) succeeded but reports no runner"},
		"re-run lists as reused a job that failed earlier": {mutate: func(f *fixture) {
			cand(f)["run_attempt"] = 2
			f.attempts[1][6]["conclusion"] = "failure"
			f.attempts[2] = []obj{reusedJob(2006, "homebrew"), hostedJob(2107, "draft")}
		}, want: "attempt 2 job \"homebrew\" (2006) succeeded but reports no runner"},
		"tag on GitHub moved after the run": {mutate: func(f *fixture) {
			f.tagRef["object"] = obj{"type": "commit", "sha": fxOther}
		}, want: "it was moved"},
		"annotated tag on GitHub moved": {mutate: func(f *fixture) {
			f.tagRef["object"] = obj{"type": "tag", "sha": strings.Repeat("c", 40)}
			f.annotated = obj{"object": obj{"type": "commit", "sha": fxOther}}
		}, want: "it was moved"},
		"dry-run tag without -dry-run": {args: []string{"-tag", "dry-run-1001", "-commit", fxCommit}, want: "needs -dry-run"},
		"release tag with -dry-run":    {args: []string{"-dry-run", "-tag", fxTag, "-commit", fxCommit}, want: "dry-run-<run id>"},
		"short commit":                 {args: []string{"-tag", fxTag, "-commit", fxCommit[:7]}, want: "-commit must be"},
	}
	for name, c := range cases {
		t.Run(strings.ReplaceAll(name, " ", "_"), func(t *testing.T) {
			f := goodFixture()
			if c.mutate != nil {
				c.mutate(f)
			}
			args := c.args
			if args == nil {
				args = releaseArgs
			}
			r := runFetch(t, f, args...)
			if r.code == 0 {
				t.Fatalf("fetch accepted it:\n%s", r.stdout)
			}
			if !strings.Contains(r.stderr, c.want) {
				t.Fatalf("stderr %q, want %q", r.stderr, c.want)
			}
			if strings.Contains(r.stdout, "expected sha256") {
				t.Fatal("a refused fetch printed an expected digest")
			}
		})
	}
}

// A partial re-run on hosted runners is accepted (OD-R55F3-7 (a)): runners
// of every attempt are checked and the digest of attempt 1's sums counts.
func TestFetchAcceptsHostedPartialRerun(t *testing.T) {
	f := goodFixture()
	f.runPages[0][1]["run_attempt"] = 2
	f.attempts[1][6]["conclusion"] = "failure"
	f.attempts[2] = []obj{hostedJob(2106, "homebrew"), hostedJob(2107, "draft")}
	r := runFetch(t, f, releaseArgs...)
	if r.code != 0 {
		t.Fatalf("rc=%d stderr=%s", r.code, r.stderr)
	}
	jobs := 0
	for _, c := range r.calls {
		if strings.Contains(c[len(c)-1], "/jobs?") {
			jobs++
		}
	}
	if jobs != 2 {
		t.Fatalf("listed the jobs of %d attempts, want 2", jobs)
	}
}

// A partial re-run that lists the jobs it reused without runner data is
// accepted when attempt 1 ran each of them, by id, on a hosted runner
// (review 62 F3S-02).
func TestFetchAcceptsRerunListingReusedJobsWithoutRunner(t *testing.T) {
	f := goodFixture()
	f.runPages[0][1]["run_attempt"] = 2
	f.attempts[1][6]["conclusion"] = "failure"
	var a2 []obj
	for _, j := range f.attempts[1] {
		switch j["name"] {
		case "homebrew":
			a2 = append(a2, hostedJob(2106, "homebrew"))
		case "draft":
			a2 = append(a2, hostedJob(2107, "draft"))
		default:
			a2 = append(a2, reusedJob(j["id"].(int), j["name"].(string)))
		}
	}
	f.attempts[2] = a2
	r := runFetch(t, f, releaseArgs...)
	if r.code != 0 {
		t.Fatalf("rc=%d stderr=%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "expected sha256: "+sha256Of(f.sums)+"\n") {
		t.Fatalf("stdout does not print the expected digest:\n%s", r.stdout)
	}
}

// Output that grows past its cap is refused even when the listed asset size
// is small (review 62 F3S-01): the size field is not trusted alone.
func TestFetchRefusesOutputOverCap(t *testing.T) {
	for name, c := range map[string]struct {
		cap  *int64
		want string
	}{
		"SHA256SUMS": {&maxSumsBytes, "/releases/assets/6000: the output is larger than the 64-byte cap"},
		"archive":    {&maxArchiveBytes, "the output is larger than the 64-byte cap"},
		"sums log":   {&maxLogBytes, "/actions/jobs/3001/logs: the output is larger than the 64-byte cap"},
		"API answer": {&maxAPIBytes, "per_page=100: the output is larger than the 64-byte cap"},
	} {
		t.Run(strings.ReplaceAll(name, " ", "_"), func(t *testing.T) {
			old := *c.cap
			*c.cap = 64
			defer func() { *c.cap = old }()
			f := goodFixture()
			for _, a := range f.releases[0][1]["assets"].([]obj) {
				a["size"] = 1 // a lying size
			}
			if name == "archive" {
				for id := range f.assets {
					if id != 6000 {
						f.assets[id] = bytes.Repeat([]byte("x"), 65)
					}
				}
			}
			r := runFetch(t, f, releaseArgs...)
			if r.code == 0 {
				t.Fatalf("fetch accepted it:\n%s", r.stdout)
			}
			if !strings.Contains(r.stderr, c.want) {
				t.Fatalf("stderr %q, want %q", r.stderr, c.want)
			}
		})
	}
}

func TestFetchAnnotatedTag(t *testing.T) {
	f := goodFixture()
	f.tagRef["object"] = obj{"type": "tag", "sha": strings.Repeat("c", 40)}
	f.annotated = obj{"object": obj{"type": "commit", "sha": fxCommit}}
	if r := runFetch(t, f, releaseArgs...); r.code != 0 {
		t.Fatalf("rc=%d stderr=%s", r.code, r.stderr)
	}
}

func TestFetchRefusesExistingOut(t *testing.T) {
	installFakeGH(t)
	out := t.TempDir()
	code, stderr := runCode(append([]string{"fetch", "-out", out}, releaseArgs...)...)
	if code != 2 || !strings.Contains(stderr, "already exists") {
		t.Fatalf("rc=%d stderr=%q", code, stderr)
	}
}

func dryFixture() *fixture {
	f := newFixture("0.0.1", "")
	f.runPages = [][]obj{{}}
	f.dryRun = obj{"id": fxRunID, "event": "workflow_dispatch", "path": releaseWorkflow, "head_branch": "main",
		"head_sha": fxCommit, "status": "completed", "conclusion": "success", "run_attempt": 1}
	f.releases[0][1]["tag_name"] = "dry-run-1001"
	f.dropReqKeys = []string{argvKey("api", "repos/"+fxRepo+"/git/ref/tags/"+fxTag)}
	return f
}

func TestFetchDryRun(t *testing.T) {
	args := []string{"-dry-run", "-tag", "dry-run-1001", "-commit", fxCommit}
	r := runFetch(t, dryFixture(), args...)
	if r.code != 0 {
		t.Fatalf("rc=%d stderr=%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "TEST key") {
		t.Fatalf("no TEST-key banner:\n%s", r.stdout)
	}
	for name, mutate := range map[string]func(f *fixture){
		"a push run":        func(f *fixture) { f.dryRun["event"] = "push" },
		"another commit":    func(f *fixture) { f.dryRun["head_sha"] = fxOther },
		"another run id":    func(f *fixture) { f.dryRun["id"] = 1002 },
		"a runner not ours": func(f *fixture) { f.attempts[1][4]["runner_group_name"] = "Default" },
	} {
		f := dryFixture()
		mutate(f)
		if r := runFetch(t, f, args...); r.code == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}
