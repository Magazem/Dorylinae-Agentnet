package main

// fetch (ticket R55-F3, spec Docs/review/57-r55-f3-spec.md §5.2): find the
// tag's own release.yml run, check it was built from the owner's LOCAL
// commit on GitHub-hosted runners, read the SHA-256 of SHA256SUMS that the
// run logged, and download the draft only if it matches. It is the only
// subcommand that uses the network, always through `gh` with an argv list
// (never a shell), and it never reads a key. Every check fails closed.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultRepo     = "Magazem/Dorylinae-Agentnet"
	releaseWorkflow = ".github/workflows/release.yml"
	// hostedGroup is the runner group every GitHub-hosted job reports
	// (runner_group_id 0). A self-hosted runner lands in another group.
	hostedGroup = "GitHub Actions"
	maxAttempts = 100
)

var (
	repoRe       = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	sha40Re      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	idRe         = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
	releaseTagRe = regexp.MustCompile(`^v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)
	dryTagRe     = regexp.MustCompile(`^dry-run-([1-9][0-9]{0,19})$`)
	digestLineRe = regexp.MustCompile(`^release-sums-sha256: ([0-9a-f]{64})$`)
	logStampRe   = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?Z `)
	goVersionRe  = regexp.MustCompile(`go version go[0-9][0-9A-Za-z.+-]* [a-z0-9]+/[a-z0-9]+`)
)

// ghTimeout bounds one gh call (an archive download included).
var ghTimeout = 10 * time.Minute

// Size caps (review 62 F3S-01): every gh output is read into memory, so each
// is bounded. A draft asset whose listed size is over its cap is refused
// before it is downloaded; a download that grows past the cap anyway stops
// gh and is refused. The six archives are a few tens of MiB each.
var (
	maxAPIBytes     int64 = 32 << 20  // one API answer, all pages
	maxLogBytes     int64 = 16 << 20  // one job log
	maxSumsBytes    int64 = 64 << 10  // SHA256SUMS
	maxArchiveBytes int64 = 200 << 20 // one release archive
)

// gh runs the GitHub CLI with an argv list, never through a shell, and
// returns its stdout, at most maxAPIBytes of it. Any non-zero exit (an API
// error, a page that failed mid-pagination) is an error.
func gh(args ...string) ([]byte, error) { return ghCapped(maxAPIBytes, args...) }

// capWriter buffers at most limit bytes; one byte more stops the gh process.
type capWriter struct {
	buf    bytes.Buffer
	limit  int64
	over   bool
	cancel context.CancelFunc
}

func (w *capWriter) Write(p []byte) (int, error) {
	if int64(w.buf.Len())+int64(len(p)) > w.limit {
		w.over = true
		w.cancel()
		return 0, errors.New("size cap exceeded")
	}
	return w.buf.Write(p)
}

// maxStderrBytes bounds the gh stderr kept for an error message.
const maxStderrBytes = 64 << 10

// headWriter keeps the first limit bytes and silently discards the rest.
type headWriter struct {
	buf   bytes.Buffer
	limit int
}

func (w *headWriter) Write(p []byte) (int, error) {
	if room := w.limit - w.buf.Len(); room > 0 {
		w.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// ghCapped is gh with a cap of limit bytes on stdout.
func ghCapped(limit int64, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...) //nolint:gosec // fixed program; every argument is validated; no shell
	out := &capWriter{limit: limit, cancel: cancel}
	errb := &headWriter{limit: maxStderrBytes}
	cmd.Stdout, cmd.Stderr = out, errb
	err := cmd.Run()
	if out.over {
		return nil, fmt.Errorf("gh %s: the output is larger than the %d-byte cap; refusing", strings.Join(args, " "), limit)
	}
	if err != nil {
		return nil, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, printable(strings.TrimSpace(errb.buf.String())))
	}
	return out.buf.Bytes(), nil
}

// printable drops control characters (terminal escapes) from text that came
// from GitHub before it reaches the owner's terminal.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return '?'
		}
		return r
	}, s)
}

// decodeAll decodes every JSON value in b: `gh api --paginate` prints one
// value per page.
func decodeAll[T any](b []byte) ([]T, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	var all []T
	for {
		var v T
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("malformed API response: %w", err)
		}
		all = append(all, v)
	}
	if len(all) == 0 {
		return nil, errors.New("empty API response")
	}
	return all, nil
}

func decodeOne[T any](b []byte) (T, error) {
	var zero T
	all, err := decodeAll[T](b)
	if err != nil {
		return zero, err
	}
	if len(all) != 1 {
		return zero, fmt.Errorf("API returned %d values, want 1", len(all))
	}
	return all[0], nil
}

// apiID renders an id from the API, which must be a positive decimal.
func apiID(what string, n json.Number) (string, error) {
	if !idRe.MatchString(string(n)) {
		return "", fmt.Errorf("API returned a malformed %s id %q; refusing", what, printable(string(n)))
	}
	return string(n), nil
}

type ghRun struct {
	ID         json.Number `json:"id"`
	Event      string      `json:"event"`
	Path       string      `json:"path"`
	HeadBranch string      `json:"head_branch"`
	HeadSHA    string      `json:"head_sha"`
	Status     string      `json:"status"`
	Conclusion string      `json:"conclusion"`
	RunAttempt json.Number `json:"run_attempt"`
}

type ghRunsPage struct {
	TotalCount json.Number `json:"total_count"`
	Runs       []ghRun     `json:"workflow_runs"`
}

type ghJob struct {
	ID              json.Number  `json:"id"`
	Name            string       `json:"name"`
	Status          string       `json:"status"`
	Conclusion      string       `json:"conclusion"`
	RunnerID        *json.Number `json:"runner_id"`
	RunnerName      string       `json:"runner_name"`
	RunnerGroupID   *json.Number `json:"runner_group_id"`
	RunnerGroupName string       `json:"runner_group_name"`
}

type ghJobsPage struct {
	TotalCount json.Number `json:"total_count"`
	Jobs       []ghJob     `json:"jobs"`
}

type ghAsset struct {
	ID   json.Number  `json:"id"`
	Name string       `json:"name"`
	Size *json.Number `json:"size"`
}

type ghRelease struct {
	ID      json.Number `json:"id"`
	TagName string      `json:"tag_name"`
	Draft   bool        `json:"draft"`
	Assets  []ghAsset   `json:"assets"`
}

type ghGitObject struct {
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"object"`
}

type fetcher struct {
	repo, tag, commit string
	dry               bool
}

const refuseHint = "do not sign; see Docs/ops/release-signing.md, \"If a check fails\""

func cmdFetch(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	repo := fs.String("repo", defaultRepo, "GitHub repository OWNER/REPO")
	tag := fs.String("tag", "", "release tag vX.Y.Z (with -dry-run: dry-run-<run id>)")
	commit := fs.String("commit", "", "the tag's commit from YOUR clone: git rev-parse 'vX.Y.Z^{commit}' (40 hex)")
	out := fs.String("out", "", "new folder to download SHA256SUMS and the six archives into")
	dry := fs.Bool("dry-run", false, "accept a workflow_dispatch dry run (sign its result with a TEST key only)")
	if err := parse("fetch", args, fs, 0); err != nil {
		return err
	}
	if !repoRe.MatchString(*repo) {
		return usageError{fmt.Sprintf("-repo must be OWNER/REPO, got %q", *repo)}
	}
	if !sha40Re.MatchString(*commit) {
		return usageError{"-commit must be the tag's 40-character lowercase commit from your own clone: git rev-parse 'vX.Y.Z^{commit}'"}
	}
	if *out == "" {
		return usageError{"-out is required"}
	}
	runID := ""
	if *dry {
		m := dryTagRe.FindStringSubmatch(*tag)
		if m == nil {
			return usageError{fmt.Sprintf("with -dry-run, -tag must be dry-run-<run id>, got %q", *tag)}
		}
		runID = m[1]
	} else if !releaseTagRe.MatchString(*tag) {
		return usageError{fmt.Sprintf("-tag must be vX.Y.Z, got %q (a dry-run-<id> draft needs -dry-run)", *tag)}
	}
	if _, err := os.Lstat(*out); err == nil {
		return usageError{fmt.Sprintf("-out %s already exists; fetch writes into a new folder", *out)}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	f := fetcher{repo: *repo, tag: *tag, commit: *commit, dry: *dry}
	if f.dry {
		_, _ = fmt.Fprintln(stdout, "DRY RUN: accepting a workflow_dispatch run. Sign what this fetches ONLY with a TEST key, never the release key.")
	}
	var run ghRun
	var err error
	if f.dry {
		run, err = f.dryRun(runID)
	} else {
		run, err = f.findRun()
	}
	if err != nil {
		return err
	}
	id, err := f.checkRun(run)
	if err != nil {
		return err
	}
	sumsJobs, buildJob, err := f.checkJobs(id, run.RunAttempt)
	if err != nil {
		return err
	}
	logged := map[string]bool{}
	for _, j := range sumsJobs {
		raw, err := ghCapped(maxLogBytes, "api", "--allow-escape-sequences", "repos/"+f.repo+"/actions/jobs/"+j+"/logs")
		if err != nil {
			return fmt.Errorf("log check: sums job %s: %w", j, err)
		}
		d, err := logDigest(raw)
		if err != nil {
			return fmt.Errorf("log check: sums job %s: %w; %s", j, err, refuseHint)
		}
		logged[d] = true
	}
	relID, files, digest, err := f.fetchDraft(logged)
	if err != nil {
		return err
	}
	if !f.dry {
		if err := f.checkTagRef(); err != nil {
			return err
		}
	}
	goLine := "unavailable"
	if buildJob != "" {
		if raw, err := ghCapped(maxLogBytes, "api", "--allow-escape-sequences", "repos/"+f.repo+"/actions/jobs/"+buildJob+"/logs"); err == nil {
			if m := goVersionRe.Find(raw); m != nil {
				goLine = string(m)
			}
		}
	}

	if err := os.Mkdir(*out, 0o750); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := writeOut(filepath.Join(*out, n), files[n], false); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(stdout, "run:     https://github.com/%s/actions/runs/%s (attempt %s)\n", f.repo, id, run.RunAttempt)
	_, _ = fmt.Fprintf(stdout, "commit:  %s\n", f.commit)
	_, _ = fmt.Fprintf(stdout, "release: %s (draft %s)\n", relID, f.tag)
	_, _ = fmt.Fprintf(stdout, "build:   %s (information only)\n", goLine)
	_, _ = fmt.Fprintf(stdout, "wrote:   %s (SHA256SUMS and the six archives)\n", *out)
	_, _ = fmt.Fprintf(stdout, "expected sha256: %s\n", digest)
	_, _ = fmt.Fprintln(stdout, "Now open the run URL: its commit must start the same as COMMIT and its summary must show this release-sums-sha256. Copy the digest for `sign -expect-sha256` from the run summary.")
	return nil
}

// findRun lists every push run of release.yml (all pages) and filters them
// on the client: exactly one run for the tag may exist, at the owner's
// commit. Another push run for the tag means the tag was moved on GitHub.
func (f fetcher) findRun() (ghRun, error) {
	raw, err := gh("api", "--paginate", "repos/"+f.repo+"/actions/workflows/release.yml/runs?event=push&per_page=100")
	if err != nil {
		return ghRun{}, fmt.Errorf("run check: listing release.yml push runs: %w", err)
	}
	pages, err := decodeAll[ghRunsPage](raw)
	if err != nil {
		return ghRun{}, fmt.Errorf("run check: %w", err)
	}
	var runs []ghRun
	for _, p := range pages {
		runs = append(runs, p.Runs...)
	}
	if total, err := strconv.Atoi(string(pages[0].TotalCount)); err != nil || total != len(runs) {
		return ghRun{}, fmt.Errorf("run check: the run list is incomplete (%d runs, total_count %q); refusing", len(runs), printable(string(pages[0].TotalCount)))
	}
	var cands []ghRun
	var others []string
	for _, r := range runs {
		if r.HeadBranch != f.tag {
			continue
		}
		if !sha40Re.MatchString(r.HeadSHA) {
			return ghRun{}, fmt.Errorf("run check: API returned a malformed head_sha %q; refusing", printable(r.HeadSHA))
		}
		if r.HeadSHA == f.commit {
			cands = append(cands, r)
		} else {
			others = append(others, fmt.Sprintf("run %s at %s", printable(string(r.ID)), r.HeadSHA))
		}
	}
	if len(others) > 0 {
		return ghRun{}, fmt.Errorf("run check: %s has push run(s) built from another commit than %s: %s. The tag was moved on GitHub; %s",
			f.tag, f.commit, strings.Join(others, ", "), refuseHint)
	}
	if len(cands) != 1 {
		return ghRun{}, fmt.Errorf("run check: want exactly one push run of release.yml for %s at %s, found %d", f.tag, f.commit, len(cands))
	}
	return cands[0], nil
}

// dryRun reads the run named by a dry-run-<id> tag.
func (f fetcher) dryRun(id string) (ghRun, error) {
	raw, err := gh("api", "repos/"+f.repo+"/actions/runs/"+id)
	if err != nil {
		return ghRun{}, fmt.Errorf("run check: %w", err)
	}
	r, err := decodeOne[ghRun](raw)
	if err != nil {
		return ghRun{}, fmt.Errorf("run check: %w", err)
	}
	if string(r.ID) != id {
		return ghRun{}, fmt.Errorf("run check: asked for run %s, API returned %q", id, printable(string(r.ID)))
	}
	return r, nil
}

// checkRun checks the candidate run's own fields and returns its id.
func (f fetcher) checkRun(r ghRun) (string, error) {
	id, err := apiID("run", r.ID)
	if err != nil {
		return "", fmt.Errorf("run check: %w", err)
	}
	wantEvent := "push"
	if f.dry {
		wantEvent = "workflow_dispatch"
	}
	switch {
	case r.Event != wantEvent:
		return "", fmt.Errorf("run check: run %s was triggered by %q, want %q; %s", id, printable(r.Event), wantEvent, refuseHint)
	case r.Path != releaseWorkflow:
		return "", fmt.Errorf("run check: run %s is workflow %q, want %s; %s", id, printable(r.Path), releaseWorkflow, refuseHint)
	case !f.dry && r.HeadBranch != f.tag:
		return "", fmt.Errorf("run check: run %s is for %q, want %s", id, printable(r.HeadBranch), f.tag)
	case r.HeadSHA != f.commit:
		return "", fmt.Errorf("run check: run %s was built from %q, not COMMIT %s; %s", id, printable(r.HeadSHA), f.commit, refuseHint)
	case r.Status != "completed" || r.Conclusion != "success":
		return "", fmt.Errorf("run check: run %s is %q/%q, want completed/success (wait for the draft job to finish)", id, printable(r.Status), printable(r.Conclusion))
	}
	return id, nil
}

// checkJobs checks the jobs of EVERY attempt of the run (a "re-run failed
// jobs" attempt reuses earlier jobs and their artifacts, spec review 57a-1):
// each job that got a runner ran on a GitHub-hosted one, and every job of the
// latest attempt succeeded. A successful job with no runner field set at all
// is accepted only as a reused job: the same job id, under the same name,
// succeeded with runner data in an earlier attempt (review 62 F3S-02,
// review 93 L-1). It returns the ids of the sums jobs of
// all attempts and one build job (for the Go version line).
func (f fetcher) checkJobs(runID string, attemptN json.Number) ([]string, string, error) {
	attempts, err := strconv.Atoi(string(attemptN))
	if err != nil || attempts < 1 || attempts > maxAttempts || !idRe.MatchString(string(attemptN)) {
		return nil, "", fmt.Errorf("job check: API returned a malformed run_attempt %q; refusing", printable(string(attemptN)))
	}
	var sums []string
	seenSums := map[string]bool{}
	ranOK := map[string]string{} // job id -> name, for jobs that succeeded on a checked runner in an earlier attempt
	build := ""
	for n := 1; n <= attempts; n++ {
		raw, err := gh("api", "--paginate", fmt.Sprintf("repos/%s/actions/runs/%s/attempts/%d/jobs?per_page=100", f.repo, runID, n))
		if err != nil {
			return nil, "", fmt.Errorf("job check: attempt %d: %w", n, err)
		}
		pages, err := decodeAll[ghJobsPage](raw)
		if err != nil {
			return nil, "", fmt.Errorf("job check: attempt %d: %w", n, err)
		}
		var jobs []ghJob
		for _, p := range pages {
			jobs = append(jobs, p.Jobs...)
		}
		if total, err := strconv.Atoi(string(pages[0].TotalCount)); err != nil || total != len(jobs) || total == 0 {
			return nil, "", fmt.Errorf("job check: attempt %d: the job list is incomplete (%d jobs, total_count %q); refusing", n, len(jobs), printable(string(pages[0].TotalCount)))
		}
		ranNow := map[string]string{}
		for _, j := range jobs {
			jid, err := apiID("job", j.ID)
			if err != nil {
				return nil, "", fmt.Errorf("job check: %w", err)
			}
			ran := j.RunnerName != "" || (j.RunnerID != nil && *j.RunnerID != "0" && *j.RunnerID != "")
			// Any runner field set (a group included) gets the full runner
			// check, so only a job with none of them can be vouched for
			// (review 93 L-1).
			if ran || j.RunnerGroupID != nil || j.RunnerGroupName != "" {
				if j.RunnerGroupName != hostedGroup || j.RunnerGroupID == nil || *j.RunnerGroupID != "0" {
					gid := "null"
					if j.RunnerGroupID != nil {
						gid = string(*j.RunnerGroupID)
					}
					return nil, "", fmt.Errorf("runner check: attempt %d job %q ran on runner %q in group %q (id %s), not a GitHub-hosted runner; %s",
						n, printable(j.Name), printable(j.RunnerName), printable(j.RunnerGroupName), printable(gid), refuseHint)
				}
			}
			if ran && j.Conclusion == "success" {
				ranNow[jid] = j.Name
			} else if !ran && j.Conclusion == "success" {
				if name, ok := ranOK[jid]; !ok || name != j.Name {
					return nil, "", fmt.Errorf("runner check: attempt %d job %q (%s) succeeded but reports no runner, and no earlier attempt ran it under that name; refusing", n, printable(j.Name), jid)
				}
			}
			if n == attempts && (j.Status != "completed" || j.Conclusion != "success") {
				return nil, "", fmt.Errorf("job check: job %q of attempt %d is %q/%q, want completed/success", printable(j.Name), n, printable(j.Status), printable(j.Conclusion))
			}
			if j.Name == "sums" && ran && j.Conclusion == "success" && !seenSums[jid] {
				seenSums[jid] = true
				sums = append(sums, jid)
			}
			if build == "" && strings.HasPrefix(j.Name, "build (") {
				build = jid
			}
		}
		for jid, name := range ranNow {
			ranOK[jid] = name
		}
	}
	if len(sums) == 0 {
		return nil, "", errors.New("job check: no successful sums job in any attempt; refusing")
	}
	return sums, build, nil
}

// logDigest returns the one release-sums-sha256 value a sums job logged.
// Lines carry a timestamp prefix; the log may start with a UTF-8 BOM and use
// CRLF. The script text the runner echoes shows `$d` unexpanded and does not
// match.
func logDigest(log []byte) (string, error) {
	s := strings.TrimPrefix(string(log), "\ufeff")
	found := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		line = logStampRe.ReplaceAllString(strings.TrimSuffix(line, "\r"), "")
		if m := digestLineRe.FindStringSubmatch(line); m != nil {
			found[m[1]] = true
		}
	}
	switch len(found) {
	case 0:
		return "", errors.New("no release-sums-sha256 line in the log")
	case 1:
		for d := range found {
			return d, nil
		}
	}
	return "", fmt.Errorf("%d different release-sums-sha256 values in one log", len(found))
}

// fetchDraft finds the one draft release for the tag, requires exactly the
// six archives and SHA256SUMS on it, downloads them by asset id and checks
// them against a digest the run logged. Nothing is written here.
func (f fetcher) fetchDraft(logged map[string]bool) (string, map[string][]byte, string, error) {
	raw, err := gh("api", "--paginate", "repos/"+f.repo+"/releases?per_page=100")
	if err != nil {
		return "", nil, "", fmt.Errorf("release check: listing releases: %w", err)
	}
	pages, err := decodeAll[[]ghRelease](raw)
	if err != nil {
		return "", nil, "", fmt.Errorf("release check: %w", err)
	}
	var match []ghRelease
	for _, p := range pages {
		for _, r := range p {
			if r.TagName == f.tag {
				match = append(match, r)
			}
		}
	}
	if len(match) != 1 {
		return "", nil, "", fmt.Errorf("release check: want exactly one release named %s, found %d; %s", f.tag, len(match), refuseHint)
	}
	rel := match[0]
	relID, err := apiID("release", rel.ID)
	if err != nil {
		return "", nil, "", fmt.Errorf("release check: %w", err)
	}
	if !rel.Draft {
		return "", nil, "", fmt.Errorf("release check: release %s (%s) is not a draft; %s", f.tag, relID, refuseHint)
	}
	assets := map[string]string{}
	sizes := map[string]int64{}
	for _, a := range rel.Assets {
		aid, err := apiID("asset", a.ID)
		if err != nil {
			return "", nil, "", fmt.Errorf("release check: %w", err)
		}
		if _, dup := assets[a.Name]; dup {
			return "", nil, "", fmt.Errorf("release check: the draft has two assets named %q", printable(a.Name))
		}
		if a.Size == nil {
			return "", nil, "", fmt.Errorf("release check: API returned no size for asset %q; refusing", printable(a.Name))
		}
		size, err := strconv.ParseInt(string(*a.Size), 10, 64)
		if err != nil || size < 0 {
			return "", nil, "", fmt.Errorf("release check: API returned a malformed size %q for asset %q; refusing", printable(string(*a.Size)), printable(a.Name))
		}
		assets[a.Name] = aid
		sizes[a.Name] = size
	}
	sumsID, ok := assets["SHA256SUMS"]
	if !ok {
		return "", nil, "", errors.New("release check: the draft has no SHA256SUMS")
	}
	sums, err := f.asset("SHA256SUMS", sumsID, sizes["SHA256SUMS"], maxSumsBytes)
	if err != nil {
		return "", nil, "", fmt.Errorf("release check: SHA256SUMS: %w", err)
	}
	version, err := checkSums(sums)
	if err != nil {
		return "", nil, "", fmt.Errorf("release check: the draft's SHA256SUMS: %w", err)
	}
	if !f.dry && "v"+version != f.tag {
		return "", nil, "", fmt.Errorf("release check: the draft's SHA256SUMS names version %s, not %s", version, f.tag)
	}
	want := sumsEntries(sums)
	for name := range assets {
		if _, ok := want[name]; !ok && name != "SHA256SUMS" {
			return "", nil, "", fmt.Errorf("release check: the draft holds %q, which is not one of the six archives or SHA256SUMS; %s", printable(name), refuseHint)
		}
	}
	for name := range want {
		if _, ok := assets[name]; !ok {
			return "", nil, "", fmt.Errorf("release check: the draft has no %s", name)
		}
	}
	digest := sha256Of(sums)
	if !logged[digest] {
		var l []string
		for d := range logged {
			l = append(l, d)
		}
		sort.Strings(l)
		return "", nil, "", fmt.Errorf("digest check: the draft's SHA256SUMS has SHA-256 %s, but the run logged %s; %s", digest, strings.Join(l, ", "), refuseHint)
	}
	files := map[string][]byte{"SHA256SUMS": sums}
	for name, sum := range want {
		b, err := f.asset(name, assets[name], sizes[name], maxArchiveBytes)
		if err != nil {
			return "", nil, "", fmt.Errorf("release check: %s: %w", name, err)
		}
		if got := sha256Of(b); got != sum {
			return "", nil, "", fmt.Errorf("archive check: draft asset %s has SHA-256 %s, SHA256SUMS says %s; %s", name, got, sum, refuseHint)
		}
		files[name] = b
	}
	return relID, files, digest, nil
}

// asset downloads one draft asset. It refuses the asset unread when its
// listed size is over limit, stops the download if it grows past limit, and
// refuses a download whose length is not the listed size (review 93 L-2).
func (f fetcher) asset(name, id string, size, limit int64) ([]byte, error) {
	if size > limit {
		return nil, fmt.Errorf("draft asset %s is %d bytes, over the %d-byte cap; %s", name, size, limit, refuseHint)
	}
	b, err := ghCapped(limit, "api", "--allow-escape-sequences", "-H", "Accept: application/octet-stream", "repos/"+f.repo+"/releases/assets/"+id)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) != size {
		return nil, fmt.Errorf("draft asset %s: downloaded %d bytes, the release lists %d; %s", name, len(b), size, refuseHint)
	}
	return b, nil
}

// checkTagRef checks the tag on GitHub still resolves (peeling an annotated
// tag) to COMMIT. The local clone is the reference; this catches a tag moved
// after its run, before publishing re-points the release to it.
func (f fetcher) checkTagRef() error {
	raw, err := gh("api", "repos/"+f.repo+"/git/ref/tags/"+f.tag)
	if err != nil {
		return fmt.Errorf("tag check: %w", err)
	}
	ref, err := decodeOne[ghGitObject](raw)
	if err != nil {
		return fmt.Errorf("tag check: %w", err)
	}
	for range 5 {
		if !sha40Re.MatchString(ref.Object.SHA) {
			return fmt.Errorf("tag check: API returned a malformed sha %q; refusing", printable(ref.Object.SHA))
		}
		switch ref.Object.Type {
		case "commit":
			if ref.Object.SHA != f.commit {
				return fmt.Errorf("tag check: %s on GitHub points to %s, not COMMIT %s: it was moved; %s", f.tag, ref.Object.SHA, f.commit, refuseHint)
			}
			return nil
		case "tag":
			raw, err := gh("api", "repos/"+f.repo+"/git/tags/"+ref.Object.SHA)
			if err != nil {
				return fmt.Errorf("tag check: %w", err)
			}
			if ref, err = decodeOne[ghGitObject](raw); err != nil {
				return fmt.Errorf("tag check: %w", err)
			}
		default:
			return fmt.Errorf("tag check: %s on GitHub points to a %q object; refusing", f.tag, printable(ref.Object.Type))
		}
	}
	return fmt.Errorf("tag check: %s on GitHub is nested too deep; refusing", f.tag)
}
