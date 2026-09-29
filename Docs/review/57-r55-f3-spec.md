# 57: R55-F3 spec: bind the signed SHA256SUMS to the CI run

Author: SPEC-F3 (Opus, `claude-opus-5-5`), 2026-09-29. Branch `p4/r55-f3spec`, based on
`4613ec0`. Ticket R55-F3 (High) from `Docs/review/55-code-review/99-report.md` §4 and §6 D4.
Findings: R55-003 (C15-01), 032 (C15-02), 110 (C15-04), 111 (C15-03), 132 (T6c-12),
133 (T6c-13), 194 (C15-05), 195 (C15-06). Status: **spec, not yet reviewed or approved**.
An Opus adversarial review follows, then the owner approves (HANDOFF rule 3).

Docs changed on this branch: `Docs/ops/release-signing.md` (the target runbook),
`Docs/review/49-phase4-tickets.md` (§install "What it protects against", O-180),
`Docs/ops/owner-next-steps.md` (§1 step 6), and this file. No code.

## 1. Owner summary

- **The flaw (R55-003, High).** You sign whatever `SHA256SUMS` is attached to the draft
  release when you download it. Anyone who can write release assets can change a draft
  (a leaked token, or someone who took over your GitHub account). If they swap the
  archives and `SHA256SUMS` before you sign, you sign their files. Every `curl | sh` and
  Homebrew install would then accept them. That is exactly what D36 / OD-P4-19 (ii) was
  meant to stop, so this reopens the *runbook* under HANDOFF rule 9 (a concrete security
  flaw). The D36 choices stay: plain Ed25519, an offline key, and a CI draft.
- **The fix (recommended, OD-R55F3-1 (b)).** The tag's own CI run writes the SHA-256 of
  `SHA256SUMS` into its log and job summary. It does this with runner tools, before any
  repository code runs in that job. Nobody can edit a run's log afterwards, not even you.
  A new `releasesign fetch` finds that run and checks it was built from **the commit you
  tagged on your own machine**. It reads the digest from the log and downloads the draft.
  `releasesign sign` then **refuses** unless the file's digest equals that value
  (`-expect-sha256`). It also refuses unless every archive in the folder matches its line.
  Your extra work per release: one command, then pasting one digest.
- **What it does not cover.** It does not catch a CI run that was itself compromised
  while running (a poisoned action or runner). That is O-180, and it was never covered.
  Only a local rebuild on your machine closes it (OD-R55F3-1 (d)). It is recommended as a
  follow-up before the public launch, the same timing as D41's outside review.
- **Pipeline hygiene in the same ticket:**
  - no Go cache in any release job (032);
  - the last three workflows pinned by SHA (032);
  - explicit read-only permissions and no stored token in ci.yml and the harness
    workflows (110);
  - Dependabot for Go modules plus govulncheck (194, OD-R55F3-4);
  - install.sh's stale "PLACEHOLDER" banner reworded (195);
  - a correct message when an install stops half way (133);
  - the "Next: agentnet setup" pointers, since that command does not exist yet (132,
    OD-R55F3-3).
- **Already done:** R55-111 (C15-03, the placeholder fixture broke install-sh) was fixed on
  `main` in f501b6c and 5e264f0. Both came after the freeze commit 6cc26a7. I checked the
  CI job: `install-sh` passed on `main` in run 36579528567 (commit 4613ec0).
  `tests/install/run.sh:89-106` now builds the placeholder copy by rewriting both key lines.
  **Mark R55-111 done.**
- **Decisions for you:** 6 ODs in §9. They pick the binding, the rebuild timing, the
  `setup` pointers, the vulnerability checks, immutable releases, and whether to add
  Sigstore attestations.

## 2. Facts checked for this spec (2026-09-29)

| Fact | How checked | Matters for |
|---|---|---|
| Repo is **public** | `gh repo view --json visibility` → `PUBLIC` | attestations (public Rekor) are available, OD-R55F3-6 |
| Default `GITHUB_TOKEN` permission is **read** | `gh api repos/…/actions/permissions/workflow` → `default_workflow_permissions: read` | R55-110 is not exploitable today; the explicit block guards against a later settings change |
| `release.yml` `test`, `sums`, `install-sh` run `setup-go` with its default cache (on); `build` has `cache: false` | `release.yml:100-102,203-205,236-238,118-121` | R55-032 |
| `phase2-harness.yml:21-22,35`, `phase3-harness.yml:23-24,37`, `sensitive-paths.yml:18` use tags (`@v7`, `@v9`) | grep | R55-032 |
| `ci.yml` has no top-level `permissions:` and no `persist-credentials: false` | `ci.yml:1-8`, grep | R55-110 |
| `dependabot.yml` covers `github-actions` only | file | R55-194 |
| `go.mod` says `go 1.27`, with no `toolchain` line | `go.mod:3` | option (d) needs the exact Go patch release |
| The Windows zip keeps each file's mtime (`zip -X` drops only extra attributes) | `release.yml:183` | option (d) compares archive *members*, not archive bytes |
| No `//go:embed` in `cmd/` or `internal/`; this PC has `core.autocrlf=true` | grep, `git config` | option (d): CRLF checkouts cannot change embedded data |
| R55-111 fixed after the freeze | `git merge-base --is-ancestor`; run 36579528567 `install-sh: success` | §1 |

## 3. Threat model (the D36 goal)

Actors, weakest to strongest:

- **T1: a leaked `contents: write` token** (a PAT, or a job's `GITHUB_TOKEN`). It can edit
  or replace a draft's assets and notes. It cannot write another run's logs, artifacts or
  job summaries. It cannot push workflow files: GITHUB_TOKEN never can, and a PAT needs
  the `workflow` scope.
- **T2: a leaked PAT with `repo` + `workflow` + `actions:write`.** In addition to T1, it
  can push branches and workflow changes, dispatch and re-run workflows, and delete runs
  and logs. The `v*` tag ruleset stops it from pushing a release tag unless the token is
  an admin's.
- **T3: GitHub account takeover** (session or admin PAT). It can do everything above. It
  can also bypass the tag ruleset (Repository admin bypass, D42), move or re-push a tag,
  push to `main`, change repository settings (rulesets, environments, runners), and approve
  the `release` environment.
- **T4: a compromised CI step:** a poisoned cache, a retargeted action, a malicious
  toolchain download, or a compromised hosted runner. The code runs *inside* the honest
  run, so anything it prints or uploads looks like CI's own output.

What the owner has that no attacker controls:

- the offline key;
- the **local git clone**, where the owner created the tag. `git fetch` refuses to move an
  existing local tag without `--force`, so a tag moved on GitHub does not silently move
  locally;
- a digest that the tag's CI run wrote before any repository code ran in the job. The
  run's log cannot be edited, only deleted. GitHub itself is assumed honest; the attacker
  is a user of it.

## 4. Options compared

Each option is judged by whether the owner can still be made to sign bytes CI did not
build from the owner's commit.

| | (a) Download the run's artifact | (b) Digest in the run's log and summary, enforced by `sign -expect-sha256` | (c) GitHub artifact attestation (Sigstore provenance), `gh attestation verify` | (d) Local reproducible rebuild |
|---|---|---|---|---|
| How | `gh run download <run> -n release-files`, then sign that file | `sums` logs `release-sums-sha256: <hex>`; `fetch` reads it from the run it verified; `sign` refuses a mismatch | `sums` runs `actions/attest-build-provenance` over `SHA256SUMS` + archives; owner verifies with `--signer-workflow`, `--source-ref`, `--source-digest`, `--deny-self-hosted-runners` | Owner rebuilds the 18 binaries at the tag with the pinned Go, and compares every archive member byte for byte |
| T1 leaked token | **closed**: a token cannot write a run's artifact | **closed**: a token cannot write a run's log | **closed**: it cannot mint a certificate for the workflow identity | **closed** |
| T2 workflow PAT | closed **only if** the run is checked: push event, `release.yml` at `refs/tags/vX.Y.Z`, head SHA = local commit. Otherwise the attacker dispatches a look-alike run from their own branch | same condition as (a); `fetch` enforces it | closed; the check is in the certificate (`--source-ref`, `--source-digest`) | closed |
| T3 account takeover | closed if the head SHA is compared with the **local** commit, not GitHub's view of the tag, **and** every job ran on a GitHub-hosted runner (the attacker can register a self-hosted runner) | same; `fetch` checks each job's `runner_group_name` (to be confirmed at implementation, §8 V2) | closed with `--deny-self-hosted-runners`; relies on the certificate, not on the jobs API | closed; does not depend on GitHub at all |
| T4 compromised CI step | **open** | **open**; narrowed: the digest is written before repository code runs in `sums`, and the `build` jobs already have no cache | **open**; a step inside the run can attest what it likes | **closed**: the only one that is |
| Owner cost per release | one command; the artifact **expires after 7 days** (`retention-days: 7`) | one command (`fetch`) and one paste; logs are kept 90 days | install/upgrade `gh`; one command | 18 cross-builds (a few minutes) on a clean clone; false alarms if the build stops being reproducible |
| Engineering | runbook only, plus `sign -archives` | small: one log line, `fetch`, two `sign` flags | a new third-party action, `id-token: write` + `attestations: write` on an unattended job, and gh version drift | medium: a `toolchain` pin, a rebuild tool, and a CI job that proves reproducibility across OSes |
| Also useful to users | no | no | **yes**: anyone can check provenance | no |

Notes:

- (a) and (b) bind the same thing. (b) keeps a fixed value on record for 90 days, and a
  tool can enforce it, which is what the acceptance test asks. (a) is still useful:
  `fetch` could download the artifact instead of the draft, but the draft is what gets
  published, so (b) checks the draft against the digest.
- Nothing that trusts "what the CI run produced" closes T4. The docs already say so
  honestly (install.sh:22-23, `Docs/cli/install.md:48-49`). Spec 49 §install did not; it is
  fixed on this branch (O-180).
- T3 can also push a malicious commit that the owner then tags without noticing. That is a
  code-review problem, not a release-binding one. It is out of scope and named in the
  runbook.

**Recommendation:** (b) now, tool-enforced, before the first release. (d) as a follow-up
ticket before the public launch. (c) not now. See OD-R55F3-1, -2 and -6.

## 5. Design (recommended option (b))

### 5.1 `release.yml`

1. **`sums` job**, in the step that writes `SHA256SUMS`, before `go run` (the first
   repository code in the job):

   ```sh
   (cd dist && sha256sum agentnet_* | LC_ALL=C sort -k2 > SHA256SUMS)
   d=$(sha256sum dist/SHA256SUMS | cut -d' ' -f1)
   echo "release-sums-sha256: $d"
   { echo "### SHA256SUMS"; echo; echo "release-sums-sha256: \`$d\`"; } >> "$GITHUB_STEP_SUMMARY"
   echo "sums_sha256=$d" >> "$GITHUB_OUTPUT"
   ```

   - The job gets an output `sums_sha256`.
   - `cat dist/SHA256SUMS` stays in the log.
   - The script text in the log shows `$d` unexpanded, so only the expanded line matches
     `^release-sums-sha256: [0-9a-f]{64}$`.
2. **`draft` job:** after the download, recompute the digest and refuse if it differs from
   `needs.sums.outputs.sums_sha256`. This catches a mix-up between artifacts. It adds no
   protection against T1-T3.
3. **Draft notes:** remove the five owner steps (`release.yml:313-318`). Notes can be
   edited by T1, so they must not carry commands to copy. New text: "Built from $SHA.
   Unsigned. Sign only by following Docs/ops/release-signing.md. Never copy a digest or
   command from these notes."
4. **`cache: false`** on every `actions/setup-go` in `release.yml` (`test`, `sums`,
   `install-sh`; `build` has it already). This closes R55-032 and O-185.
5. The existing header comment (`release.yml:1-26`) gains one line. It says the owner
   signs only a `SHA256SUMS` whose digest this run logged (runbook step 2).

### 5.2 `tools/releasesign`

It stays standard-library only and imports no internal package.

- **`sign` gains two required flags:**
  - `-expect-sha256 HEX`: 64 lowercase hex. `sign` refuses (rc 1, no files written) unless
    `sha256(SHA256SUMS)` equals it. If the flag is missing, it is a usage error (rc 2).
  - `-archives DIR`: `sign` refuses unless DIR holds exactly the six archives named in
    `SHA256SUMS` and each one hashes to its line. Extra `agentnet_*` files are refused.
    This ties the signature to the files that will be published, not only to the list.
  - The order is `checkSums` (format) → digest → archives → sign. The error names which
    check failed.
- **`verify` gains an optional `-archives DIR`**, with the same archive check. The runbook
  uses it after publishing.
- **New `fetch` subcommand.** It is the only subcommand that uses the network. It shells
  out to `gh`, which is already a runbook prerequisite. The key never goes near it.

  ```
  releasesign fetch -repo OWNER/REPO -tag vX.Y.Z -commit SHA40 -out DIR
  ```

  - `-commit` is required: 40 hex. The runbook gets it from the local clone
    (`git rev-parse vX.Y.Z^{commit}`). `fetch` never asks GitHub which commit the tag
    points to.
  - `-repo` defaults to `Magazem/Dorylinae-Agentnet`.
  - `-dry-run` accepts a `workflow_dispatch` run and a `dry-run-<id>` name instead of a tag
    push. It prints a banner that the result may only be signed with a TEST key. Real
    releases never use it.

  Steps, each fail-closed with a message naming the check:
  1. `gh api repos/R/actions/workflows/release.yml/runs?event=push&branch=vX.Y.Z` (§8 V1).
     Among the runs, the **candidates** are those with `head_sha == -commit`. Refuse if
     there is not exactly one. Refuse, and list the others, if any other push-event run
     exists for this tag with a different `head_sha`: it means the tag was moved on
     GitHub (T3).
  2. On the candidate run:
     - `event == "push"`;
     - `path == ".github/workflows/release.yml"`;
     - `head_branch == vX.Y.Z`;
     - `conclusion == "success"`, meaning `draft` has finished.
  3. `gh api repos/R/actions/runs/ID/attempts/N/jobs` for the run's **latest attempt**:
     - every job is `completed`/`success`;
     - every job has `runner_group_name == "GitHub Actions"` (hosted; §8 V2);
     - there is exactly one job named `sums`.
  4. `gh api repos/R/actions/jobs/JOB/logs` for `sums`. After stripping the leading
     timestamp, exactly one distinct value matches `^release-sums-sha256: ([0-9a-f]{64})$`.
     Zero lines, or two different values, are refused.
  5. `gh release download vX.Y.Z -R R -D DIR -p 'agentnet_*' -p SHA256SUMS`. Then
     `sha256(DIR/SHA256SUMS)` must equal the logged digest, and the archive check from
     `sign -archives` must pass.
  6. Print the run URL, the commit, the Go version line from the `build` log (information
     only, for option (d)) and `expected sha256: HEX`. Write nothing else.

  `fetch` runs `gh` with an argv list, never through a shell. Every value from the API is
  validated before use:
  - IDs are digits;
  - SHAs are 40 hex;
  - the digest is 64 hex.

  If the key file is on the same machine this does not matter, because `fetch` never
  reads it.
- **Why the owner pastes the digest into `sign`** instead of `sign` reading `fetch`'s
  output: `sign` stays offline and network-free, so it can run on a separate machine. The
  paste is also a deliberate human step. The acceptance test is written against
  `-expect-sha256`.

### 5.3 Runbook

The new process is in `Docs/ops/release-signing.md` §Every release on this branch. In
short:

1. Tag locally and note the commit.
2. `fetch`.
3. Wait for the run, approve `draft`, then read the run page yourself: the commit and the
   summary digest.
4. `sign -expect-sha256 … -archives …`.
5. Upload the signatures.
6. Publish.
7. `verify -archives` on the published release.
8. Homebrew from the fetched `SHA256SUMS`.

### 5.4 Pipeline hygiene (the other findings)

| Finding | Change | Where |
|---|---|---|
| R55-032 (C15-02) | Pin `actions/checkout`, `actions/setup-go`, `actions/upload-artifact` and `actions/github-script` by full SHA with a `# vX.Y.Z` comment, using the same SHAs as ci.yml where the action is the same. Dependabot keeps them current. Set `cache: false` in all release.yml jobs (§5.1 item 4). | `phase2-harness.yml`, `phase3-harness.yml`, `sensitive-paths.yml`, `release.yml` |
| R55-110 (C15-04) | Top-level `permissions: contents: read` in `ci.yml`, `phase2-harness.yml` and `phase3-harness.yml`. `persist-credentials: false` on every `actions/checkout` in every workflow. `sensitive-paths.yml` keeps its own narrow block. The repo default is already `read` (§2); this keeps it read if the setting ever changes. | the four workflows |
| R55-111 (C15-03) | **Done** (f501b6c, 5e264f0; §1). No change. | — |
| R55-132 (T6c-12) | Per OD-R55F3-3 (rec. (a)): until 4.9a ships, `install.sh:323`, the Homebrew caveat `packaging/homebrew/agentnet.rb:43` (the template `render.sh` fills), `Docs/cli/install.md:23` and `doctor.go:257,309` stop naming `agentnet setup`. They name what exists: `agentnetd install`, then `agentnet doctor`. 4.9a puts `setup` back. | those files |
| R55-133 (T6c-13) | install.sh: before the replace loop, stage both binaries as `.new` files. A failure while staging is "nothing was installed". A failure in the `mv` loop prints "partially installed: <which> replaced, <which> not; rerun the installer", not "nothing was installed". `die` gets a variant without the "nothing was installed" line. New case in `tests/install/cases.sh`: a read-only second target reports "partially installed". | `scripts/install.sh:67-71,311-315`, `tests/install/cases.sh` |
| R55-194 (C15-05) | Per OD-R55F3-4 (rec. (c)): add a `gomod` ecosystem to `.github/dependabot.yml` (weekly). Add `go run golang.org/x/vuln/cmd/govulncheck@<pinned version> ./...` as a **blocking** step in release.yml `test`, and as a non-blocking job in ci.yml (push to `main` + weekly schedule). | `.github/dependabot.yml`, `release.yml`, `ci.yml` |
| R55-195 (C15-06) | Replace the "PLACEHOLDER: … NOT BEEN GENERATED YET" banner with a neutral one: the key below is the release key made by `releasesign keygen` and written by `embed`; the script refuses to install when the lines hold `REPLACE_WITH_RELEASE_*`. Also make `embed` rewrite the banner (optional, same ticket). | `scripts/install.sh:36-47` (+ `tools/releasesign` `embed`) |
| Claim text | install.sh:18-23 and `Docs/cli/install.md:44-49`: "after the owner signed them" becomes "before or after the owner signs them (the owner signs only the SHA256SUMS the tag's CI run logged)". The last sentence stays: a compromised build is not caught. Change this **with the code**, not before it. | code ticket |

## 6. Files the implementation ticket changes

- `tools/releasesign/main.go`: `sign` flags, `verify -archives`, `fetch`.
- `tools/releasesign/main_test.go`: new tests (§7). The review test `zz_review55_C15-01`
  is removed after it is converted.
- `.github/workflows/release.yml`, `ci.yml`, `phase2-harness.yml`, `phase3-harness.yml`,
  `sensitive-paths.yml`.
- `.github/dependabot.yml`.
- `scripts/install.sh`, `tests/install/cases.sh`, `packaging/homebrew/agentnet.rb` (the
  template `render.sh` fills), `cmd/agentnet/doctor.go`.
- `Docs/cli/install.md`.
- New `tools/cilint/cilint_test.go` (a test-only package, §7 A7).
- Model: Opus. Security review: yes (supply chain), before merge.

## 7. Acceptance tests

The C15-01 review test (`Docs/review/55-code-review/tests/zz_review55_C15-01_test.go.txt`,
target path `tools/releasesign/zz_review55_C15-01_test.go`) is converted into permanent
tests in `tools/releasesign/main_test.go`. They reuse its fixture: a well-formed
`SHA256SUMS` of hashes unrelated to any build.

| # | Test | Asserts |
|---|---|---|
| A1 | `TestSignRefusesUnboundSums` (the inverted C15-01) | `sign -key K -archives D SUMS` **without** `-expect-sha256` → rc 2 (usage), no `.sig`/`.minisig` written |
| A2 | `TestSignRefusesDigestMismatch` | the C15-01 fixture with `-expect-sha256` = the digest of a *different* well-formed SUMS → rc 1, stderr names the expected and actual digests, no files written |
| A3 | `TestSignRefusesBadExpectFormat` | `-expect-sha256` that is uppercase, 63 hex, or has a prefix/suffix → rc 2 |
| A4 | `TestSignArchives` | real archives in a temp dir: all good → signs; one flipped byte → rc 1 naming the archive; one missing → rc 1; one extra `agentnet_*` file → rc 1; no files written on any refusal |
| A5 | `TestSignBoundHappyPath` | correct digest + archives → rc 0; the signature verifies with `verify -install-sh` (test-embedded copy) and `verify -archives` |
| A6 | `TestFetch*` with a fake `gh` (a helper binary built in the test and put first on `PATH`; it serves canned JSON/logs) | refuses: no run; two runs with `head_sha == commit`; a push run for the tag with another `head_sha` (tag moved); `event: workflow_dispatch`; `path` ≠ `release.yml`; `conclusion` ≠ success; a job with `runner_group_name` ≠ "GitHub Actions"; no digest line; two different digest lines; draft SUMS digest ≠ logged; a draft archive ≠ its line; a non-hex API value. Accepts the good case and prints `expected sha256: <hex>`. Checks `gh` is invoked with an argv list (no shell metacharacters reach a shell) |
| A7 | `tools/cilint`: `TestWorkflowsPinned`, `TestReleaseNoCache`, `TestWorkflowPermissions` | every `uses:` in `.github/workflows/*.yml` is `owner/repo[/path]@<40 hex>`; every `actions/setup-go` step in `release.yml` has `cache: false`; every workflow has a top-level `permissions:`; every `actions/checkout` has `persist-credentials: false`; `release.yml`'s `sums` step prints `release-sums-sha256:` before its first `go ` command |
| A8 | install-sh job (existing, `tests/install/run.sh`) | still green (R55-111 stays fixed); the new partial-install case (R55-133) passes; the placeholder case still refuses |
| A9 | Dry run on `main` after merge (owner-approved, as D40) | `sums` log and summary carry `release-sums-sha256:`; `draft` recomputes it; `releasesign fetch` works against a dry-run draft. Needs a `-dry-run` switch that accepts `event: workflow_dispatch` and the `dry-run-<id>` name, used only with a TEST key |
| A10 | Runbook check (reviewer, by reading) | `Docs/ops/release-signing.md` step 2 names the run's logged digest and `fetch`; the notes in `release.yml` carry no commands to copy |

These are review 55's acceptance line (§4.1 R55-F3): "invert C15-01; install-sh passes;
`cache: false` in every job; SHA-pinned (lint or grep test); runbook step 2 names the
artifact or digest". They map to A1/A2, A8, A7 and A10.

## 8. To verify during implementation

These are facts about GitHub that I could not confirm offline. Test each against the real
API on the dry run (A9). If one is false, stop and report; do not work around it.

- **V1:** the runs endpoint's `branch=` filter matches a tag's `head_branch` for
  push-tag runs. Fallback: list by `head_sha=`.
- **V2:** the jobs endpoint's `runner_group_name` is `"GitHub Actions"` for hosted runners,
  and cannot be set to that by a self-hosted runner's owner. If this cannot be
  established, **(c)'s `--deny-self-hosted-runners` becomes the T3 control** (OD-R55F3-6
  switches to (a)).
- **V3:** a run's job logs cannot be edited through the API, only deleted.
- **V4:** `gh release download` finds a draft by its tag name for the owner. The current
  runbook already relies on this; confirm it.

## 9. Owner decisions

**OD-R55F3-1: what binds the signed `SHA256SUMS` to the CI run.**
- (a) Download the run's `release-files` artifact and sign that.
- (b) The run logs the digest; `fetch` checks the run (commit, event, workflow, hosted
  runners); `sign -expect-sha256` enforces the digest.
- (c) Sigstore attestations checked with `gh attestation verify`.
- (d) Local reproducible rebuild only.

**Recommendation: (b).** It closes T1-T3 at small cost and the tool enforces it. (a)
expires after 7 days and adds nothing over (b). (c) adds an action and write-scoped OIDC
permissions for the same binding (see OD-6). (d) alone is the strongest but blocks the
first release on reproducibility work (OD-2).

**OD-R55F3-2: the local rebuild (option (d), the only control against T4 / O-180).**
- (a) Never; keep O-180 as a documented limit.
- (b) A follow-up ticket R55-F3b before the **public launch**, optional per release until
  then.
- (c) Required before the first release.

**Recommendation: (b).** It matches D41's timing for the outside review. F3b contents:
- a `toolchain go1.27.N` pin in `go.mod`;
- `releasesign rebuild -tag … -archives DIR`, which builds in a fresh clone with the same
  flags and compares each archive **member** (not the archive bytes; the zip keeps
  mtimes) and requires exactly the expected member set;
- a CI job that cross-builds `linux/amd64` on Windows and macOS runners and compares, so
  reproducibility is proven before the owner depends on it.

**OD-R55F3-3: the `agentnet setup` pointers (R55-132).**
- (a) Point to the commands that exist (`agentnetd install`, `agentnet doctor`) until 4.9a
  lands.
- (b) Block the first release on 4.9a.
- (c) Leave them.

**Recommendation: (a).** A tester's first command should work.

**OD-R55F3-4: vulnerability checks (R55-194).**
- (a) Dependabot `gomod` only.
- (b) `govulncheck` only.
- (c) Both: govulncheck **blocking** in release.yml `test`, non-blocking weekly and on
  pushes to `main` in ci.yml.

**Recommendation: (c).** Dependabot raises PRs; govulncheck stops a release that links a
known reachable vulnerability.

**OD-R55F3-5: turn on GitHub "immutable releases" for the repository.** It locks a
release's assets and tag once published; drafts stay editable, so it does not replace
this ticket.
- (a) Yes, a settings change by the owner (HANDOFF rule 7: repo settings are the owner's).
- (b) No.

**Recommendation: (a).** It is free. After publishing, T1/T2 can no longer swap assets
(a denial of service today) or re-point the tag. T3 can turn it off, which is why this is
an addition to the fix, not the fix.

**OD-R55F3-6: also publish Sigstore build-provenance attestations (option (c)).**
- (a) Yes, in addition to (b): `sums` attests `SHA256SUMS` and the six archives, and
  `fetch` also runs `gh attestation verify` with `--deny-self-hosted-runners`.
- (b) Not now; revisit with F3b.

**Recommendation: (b), unless V2 fails, then (a).** (b) already binds the run for the
owner. Attestations mainly help third parties, and cost a new action and `id-token` /
`attestations: write` on an unattended job.
