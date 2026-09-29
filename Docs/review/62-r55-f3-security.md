# 62: R55-F3 security review (PR #12, bind the signed SHA256SUMS to the CI run)

Reviewer: SEC-F3 · model claude-opus-5-5 · elapsed 30 min · branch `p4/r55-f3` (PR #12, draft)

Scope: spec `Docs/review/57-r55-f3-spec.md` (with review 57a and A1–A10),
`Docs/review/55-code-review/verify/C15-01.md`, and the PR diff (`gh pr diff 12`, 19 files).
Severity follows `Docs/review/55-code-review/01-rubric.md` §2. This was a read-only review:
the only file written in the worktree is this report. I used scratch copies only under `%TEMP%`.

## Summary

**0 Critical, 0 High, 0 Medium, 2 Low, 3 Info. Verdict: approve.**

The binding holds against T1–T3. `fetch` accepts a draft only if all of these are true:

- the run is a `push` run of `.github/workflows/release.yml`;
- its `head_branch` is the tag and its `head_sha` equals the **local** commit;
- it completed with `success`;
- every job that got a runner, in every attempt, ran in group `GitHub Actions` with id 0;
- a `sums` job logged a digest, and exactly one such value appears in that log;
- the draft is the only release with this tag and holds exactly the seven assets;
- `SHA256SUMS` matches the logged digest and every archive matches its line.

`sign` then checks one in-memory buffer against a digest pasted by hand, and checks the
archive set strictly.

A leaked token (T1) cannot write logs. A workflow PAT (T2) cannot produce a push run at
the owner's commit that runs another workflow file. An account takeover (T3) meets the
local-commit comparison and the hosted-runner check. Neither Low finding lets an attacker
get the owner to sign a `SHA256SUMS` that no honest build produced. Both are denial of
service or defence in depth.

## Questions

**1. Can T1–T3 still get the owner to sign SUMS no honest build produced? No.**

- **Run and attempts.**
  - `findRun` lists every `event=push` run of release.yml, following all pages, and
    filters on the client (`tools/releasesign/fetch.go:291-330`).
  - A `gh` error mid-pagination is a refusal: `gh` exits non-zero and that is an error
    (`:61`).
  - `total_count` must equal the number of runs collected (`:304`); see F3S-04.
  - `checkRun` requires `push`, the release.yml path, the tag, the local commit and
    `completed`/`success` (`:349-371`).
  - `checkJobs` walks attempts 1..`run_attempt`, capped at 100 and required to be decimal
    (`:378-436`).
- **Hosted runners.**
  - Any job with a runner name or a non-zero runner id must report group name
    `GitHub Actions` **and** group id 0 (`:407-416`).
  - A successful job with no runner is refused (`:417-418`).
  - Every job of the latest attempt must be `completed`/`success` (`:420`).
  - Nothing is conditional in `release.yml`, so no legitimate job is ever `skipped`.
- **Event, workflow path and ref.**
  - A re-run keeps its run's `head_sha` and workflow file.
  - A `workflow_dispatch` run is refused outside `-dry-run`.
  - A branch named `vX.Y.Z` does not trigger release.yml, which runs on tags only
    (`release.yml:33-35`).
  - T3 can register a self-hosted runner, and the runner check covers that.
  - I found no `vars.*`, `secrets.*` (other than `github.token` in `draft`) or
    `pull_request_target` in any workflow. So T3 cannot steer the honest build through
    repository variables.
- **Tag against commit.**
  - `-commit` must be 40 hex from the owner's clone (`:191`), and fetch never asks
    GitHub for it.
  - The GitHub ref is checked as well, following annotated tags up to 5 levels
    (`:560-592`).
  - It uses the exact-match endpoint `git/ref/…`, and `decodeOne` would refuse an array.
- **Draft asset set.**
  - There must be exactly one release with `tag_name == tag`, and it must be a draft
    (`:474-492`).
  - Duplicate asset names are refused, and so is any asset outside the six names in
    `SHA256SUMS` plus `SHA256SUMS` itself; each of the seven must be present (`:493-529`).
  - Downloads go by asset id, after `apiID` validation.
  - The `SHA256SUMS` digest is checked before any archive is downloaded, and each archive
    must match its line (`:530-549`).
  - Written names come only from validated `SHA256SUMS` entries, so there is no path
    traversal (`:268-277`).
- **Crafted API responses and `gh` quirks.**
  - Every id, SHA and digest is validated by regex before use.
  - `decodeAll` accepts both concatenated pages and a merged array, so either
    `--paginate` form parses.
  - `--allow-escape-sequences` exists in gh 2.101.0 (`gh api --help`). Without it the
    binary asset bytes could be altered, and the result would still be a refusal.
  - Text from the API is passed through `printable` before it reaches the terminal.
  - `exec.CommandContext` takes an argv list, and Go's LookPath refuses a `gh` in the
    current directory.
- **Re-run with a changed workflow.** This is impossible: a re-run uses the workflow at
  `head_sha`. A new run at another commit gives `head_sha ≠ COMMIT`.
- **Digest logged early.**
  - `release.yml:233-237` logs the digest, then writes the summary and the output.
  - The only earlier steps are the pinned checkout, setup-go and download-artifact
    (`:209-220`). All of them are T4 (O-180, out of scope by design).
  - No repository code runs before the digest line. `TestReleaseLogsSumsDigestFirst`
    locks this in.

**2. sign.**

- `cmdSign` reads `SHA256SUMS` once (`main.go:370`), then runs `checkSums` → digest →
  `checkArchives` → signs the same `sums` buffer. There is no TOCTOU on the list.
- `-expect-sha256` and `-archives` are required, and each is named in its error.
  `-expect-sha256` must be exactly `^[0-9a-f]{64}$`.
- `checkArchives` (`main.go:419-461`) refuses:
  - any `agentnet_*` entry that `SHA256SUMS` does not name;
  - anything that is not a regular file (symlinks and junctions included);
  - a hash mismatch;
  - a missing archive.
- Archives are re-read from disk after the check. That is the owner's own folder, and a
  local attacker is out of scope.

**3. Workflows.**

- Every `uses:` is pinned to 40 hex with a version comment, enforced by
  `TestWorkflowsPinned`.
- Permissions:
  - `permissions: contents: read` at the top level of ci.yml, phase2 and phase3;
  - release.yml was already read-only and has `contents: write` only on `draft`;
  - `sensitive-paths.yml` keeps `pull-requests: write`, which was already the case.
- Every checkout has `persist-credentials: false`, and every setup-go in release.yml has
  `cache: false` (both tested).
- **Injection:** in release.yml every `${{ }}` inside a step goes through `env:`. The
  in-`run:` expressions in ci.yml (`runner.os`, `matrix.ext`) are runner and matrix
  constants, not attacker input, and were already there.
- Tag names are validated by `meta` before they are used. The notes heredoc expands only
  `$SHA`.
- **govulncheck:**
  - blocking in release.yml `test` (`:110-111`); `draft` needs `test`;
  - `continue-on-error` in ci.yml, and skipped on PRs (F3S-05);
  - `@v1.8.0` exists on the module proxy, and sum.golang.org verifies it.

**4. install.sh.**

- Signature verification is unchanged; only the banner and the `claim` comment were
  edited.
- Staging:
  - Both `.new` files are written before either binary is replaced.
  - A staging failure removes both `.new` files and reports "nothing was installed".
  - A failure on the first `mv` also says "nothing was installed", which is correct.
  - A failure on the second `mv` calls `fail` and reports "partially installed: agentnet
    replaced, agentnetd not".
- `tests/install/cases.sh` test 18 covers the partial case, and install-sh is green on
  PR #12.
- There is a side issue that was already in the code: if `$install_dir/agentnetd` is a
  *writable directory*, `mv -f` moves the binary into it and reports success. This is
  not a regression (Notes).

**5. Tests.**

- **A1–A6 fail on main's code.** I rebuilt main's `main.go` in `%TEMP%\f3main` by
  reverse-applying the PR hunk, removed `fetch.go`, and added a 2-symbol compile stub.
  - Every A1–A6 test fails there: `TestSign{RefusesUnboundSums,RefusesDigestMismatch,
    RefusesBadExpectFormat,Archives,BoundHappyPath}` and all `TestFetch*`.
  - All 32 `TestFetchRefuses` subtests fail too.
- **A7 would fail on main by construction.** main has `@v7` tags in phase2 and phase3
  and no `cache: false` in `test`, `sums` or `install-sh`.
- **Gaps:** see F3S-03.

**6. The two deviations.**

- **`runner_group_id == 0` on top of the group name.**
  - This is sound: it is stricter than the spec, and GitHub hands out non-zero ids to
    custom groups.
  - If GitHub-hosted jobs ever report another id, every release is refused. That fails
    closed and is visible, and the A9 dry run must record the value (V2).
- **SUMS version must equal the tag.**
  - This is sound and cannot break a legitimate release: `meta` already requires
    `v$version == tag` (`release.yml:74-80`), and `sums` requires the `SHA256SUMS`
    version to equal `$VERSION` (`:239-240`).
  - The check is skipped in `-dry-run`, as it must be.

## Findings

### F3S-01 · Low · confirmed-read
- **Where:** `tools/releasesign/fetch.go:508,541,553-555` (`asset`), `:55-64` (`gh`)
- **What goes wrong:** every asset and job log is read fully into memory with no size
  cap. The archive's hash is only compared after the whole download.
- **Scenario:** T1 (a `contents: write` token) replaces one archive asset on the draft
  with a multi-GB blob of the same name. The honest `SHA256SUMS` still matches, so
  `fetch` downloads the blob into a `bytes.Buffer` and exhausts memory, or hits the
  10-minute timeout, on the owner's machine. The result is a refusal or a crash, never a
  signature. This is denial of service only, and T1 can deny the release anyway by
  deleting assets.
- **Spec:** 57 §5.2 step 5 (no bound stated)
- **Fix direction:** cap each download (for example 200 MiB per archive, 64 KiB for
  `SHA256SUMS`, a few MiB per log), or use `-i` or the release JSON `size` to refuse
  oversized assets before downloading them.
- **Related:** none

### F3S-02 · Low · confirmed-read
- **Where:** `tools/releasesign/fetch.go:407-418` (`ran`), spec 57 §8 V5
- **What goes wrong:** a job counts as "ran" only if it reports a runner name or id. A
  successful job with neither is refused. How GitHub lists a job that is *reused* in a
  "Re-run failed jobs" attempt is not yet known (V5). If reused jobs appear in attempt N
  with `success` and null runner fields, then every legitimate partial re-run is refused.
- **Scenario:** in a real release `homebrew` flakes. The owner clicks "Re-run failed
  jobs". Attempt 2 lists the reused `build`/`sums` jobs without runner data, and `fetch`
  refuses with "succeeded but reports no runner". This fails closed and is an
  availability problem only. It burns a patch version, which is option (b) of
  OD-R55F3-7 in effect.
- **Spec:** 57 §5.2 step 3, OD-R55F3-7 (a), A9 (settle V5)
- **Fix direction:** A9 must record attempt-2 job JSON for a partial re-run. If reused
  jobs lack runner data, accept them only when the same job id is present, with runner
  data, in an earlier attempt.
- **Related:** OD-R55F3-7, V5

### F3S-03 · Info · confirmed-test
- **Where:** `tools/releasesign/fetch_test.go:361-452`
- **What goes wrong:** four fail-closed branches have no test. Each one can be deleted
  without any test failing:
  1. a successful job with no runner (`fetch.go:417`);
  2. the `SHA256SUMS` version ≠ tag check (`:516`);
  3. the job-list `total_count` mismatch (`:399`);
  4. an attempt with zero jobs.
- **Scenario:** a later refactor drops check 2 or check 3 without any test noticing. None
  of them is the primary binding; the digest and commit checks are, and those are tested.
- **Spec:** 57 §7 A6
- **Fix direction:** add four `TestFetchRefuses` cases.
- **Related:** none

### F3S-04 · Info · confirmed-read
- **Where:** `tools/releasesign/fetch.go:304`
- **What goes wrong:** the run list's completeness check uses only page 1's
  `total_count`. Suppose a new push run is created between page requests and there are
  more than 100 runs. The pages then shift: one run is listed twice, the oldest is
  dropped, and the count still matches.
- **Scenario:** the dropped run could be the only other-commit run for the tag, so the
  "tag was moved" message would not appear. It has no signing impact: the candidate must
  still be at the local commit, and step 6 checks the current GitHub ref independently.
- **Spec:** 57 §5.2 step 1
- **Fix direction:** also refuse on duplicate run ids, or on a `total_count` that differs
  between pages.
- **Related:** none

### F3S-05 · Info · confirmed-read
- **Where:** `.github/workflows/ci.yml:74-85`, `.github/workflows/release.yml:110-111`
- **What goes wrong:** the ci.yml govulncheck job is skipped on PRs (`gh pr checks 12`:
  `govulncheck skipping`). So the *blocking* release step is first exercised on push to
  main, or on the A9 dry run.
- **Scenario:** a reachable advisory already present at merge time makes the first
  release's `test` job fail. That is the intended blocking behaviour, but it would be
  discovered late.
- **Spec:** OD-R55F3-4 (c)
- **Fix direction:** read the ci.yml govulncheck result on the post-merge push before
  the A9 dry run.
- **Related:** R55-194

## Assumptions and contracts

- GitHub-hosted jobs report `runner_group_name == "GitHub Actions"` and
  `runner_group_id == 0`, and no self-hosted runner can (V2). **Unchecked**: this is to
  be settled on the A9 dry run.
- `attempts/N/jobs` lists or omits reused jobs in a way the code handles (V5).
  **Unchecked** (F3S-02).
- Job logs and step summaries cannot be edited, only deleted (V3). **Unchecked**
  (docs-only fact, spec 57 §8).
- A draft's assets download by id with `Accept: application/octet-stream` and
  `gh api --allow-escape-sequences` yields the raw bytes (V4). **Partly checked**: the
  flag exists in gh 2.101.0; the download itself is for A9.
- `checkSums` guarantees the `hash␠␠name` layout that `sumsEntries` slices at 64/66
  (`main.go:405-412`). **Checked**: `sumsEntries` is only called after `checkSums`
  (`fetch.go:512-519`, `main.go:374-385,419-420`).

## Checked and fine

- R55-003 (C15-01) is closed on the tool side. `sign` without both bindings exits with
  rc 2 and names the missing flag; a mismatched digest exits with rc 1 and writes no
  files (A1, A2).
- The draft job re-checks the `sums` output with a hex check before comparing
  (`release.yml:312-320`).
- The notes carry no commands (`release.yml:343-346`, `TestDraftNotesCarryNoCommands`).
- `-out` must not exist (Lstat, then `Mkdir`), and nothing is written on any refusal
  (`runFetch` asserts it).
- Dry-run and release tag patterns are mutually exclusive (`fetch.go:198-206`).
- `release.yml`'s `sums` job id `sums` is unnamed, so its API name is exactly `sums`.
  Build jobs start with `build (`.
- R55-032, 110, 132, 133 and 195 are all addressed as spec §5.4 describes.

## Leads for other chunks

- `scripts/install.sh:325` (unchanged behaviour): `mv -f` onto an existing writable
  directory named `agentnet` or `agentnetd` moves the binary inside it and reports
  success.

## Notes

- The fake `gh` design is good. It uses the test binary, alone on PATH, and answers
  exact argv keys, so an unexpected call fails as a 404. It records every argv.

## Commands run

- `gh pr diff 12 --name-only`, `gh pr diff 12`: 19 files, 2454 diff lines.
- `gh pr checks 12`, twice. At the second check: 7 pass (flag-sensitive-paths, test
  macOS, unix service install ×2, install-sh, lint), 3 pending (test ubuntu, test
  windows, race), and govulncheck skipped.
- `go test ./tools/releasesign ./tools/cilint -count=1` on the branch: ok, ok.
  `go vet` on both: ok.
- The same releasesign tests against main's `main.go` (reverse-patched in
  `%TEMP%\f3main`, `fetch.go` removed, stub for `releaseWorkflow`/`sha256Of`): the A1–A6
  tests FAIL as required. `-run 'TestFetchRefuses$' -v`: 0 subtests pass.
- `gh api --help`: `--allow-escape-sequences` is present in gh 2.101.0.
- `go list -m -json golang.org/x/vuln@v1.8.0`: exists (2026-09-08).
