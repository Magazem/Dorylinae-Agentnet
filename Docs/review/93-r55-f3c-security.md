# 93: R55-F3c security review (release fetch size caps and the re-run edge case)

Reviewer: R55-F3csec-Opus · model claude-opus-5-5 · branch `p4/r55-f3c` at `ec4744d`

Scope: `git diff main...HEAD` (`tools/releasesign/fetch.go`, `fetch_test.go`,
`Docs/ops/release-signing.md`), against review 62 F3S-01 and F3S-02 and spec 57 §5.2 and
§8 V5. Severity follows `Docs/review/55-code-review/01-rubric.md` §2.

`go test ./tools/... -count=1` and `go vet ./tools/releasesign/` pass. I ran four extra
cases as a scratch test file through the real fake-`gh` subprocess harness, then deleted
the file. The worktree is clean apart from this report. I made no calls to the real `gh`.

## Summary

**0 Critical, 0 High, 0 Medium, 2 Low, 4 Info. Verdict: approve.** The Low items are
hardening that can ship in this PR or in a follow-up.

Both review-62 findings are fixed, and neither fix opens a way to get a `SHA256SUMS`
signed that no honest hosted build produced. Every `gh` output now has a cap, and going
over a cap always means refusal. The reused-job relaxation applies only to job ids that
an **earlier attempt** recorded as succeeding on a hosted runner, and every attempt's
runners are still checked.

## Q1: size caps

- **Every `gh` call goes through the cap.** `gh()` is just `ghCapped(maxAPIBytes, …)`
  (`fetch.go:66`). Logs use 16 MiB (`:274`, `:295`). Assets use 64 KiB or 200 MiB
  (`:564`, `:597`, `:611-616`). The tag check calls `gh()` (`:622`, `:641`). No call
  uses `exec` directly.
- **Going over the cap fails closed.** `capWriter.Write` (`:76-83`) refuses the whole
  chunk that would cross the limit and keeps none of it. It sets `over`, cancels the
  context (which kills `gh`) and returns an error. `ghCapped` checks `over` **before**
  it looks at `err` (`:94-96`), so a truncated buffer is never returned, even if `gh`
  exited 0 first. `Run()` waits for the copy goroutine, so reading `over` afterwards is
  race-free.
- **A truncated stream** fails at three levels:
  - `gh` exits non-zero on a broken body, which is an error (`:97-99`);
  - a short `SHA256SUMS` fails the logged digest check (`:586-594`);
  - a short archive fails its line check (`:601-603`).
- **The listed size is required and parsed strictly** (`:550-556`): it must be present
  and non-null, and must be a non-negative integer. Anything else is refused. Its only
  use is the early check `size > limit` (`:612`).
- **The listed size is not compared with the bytes actually read.** This is L-2 below.
  It is not a bypass: the cap and the hashes still bind the content.
- **Nothing reaches the disk before verification.** All bytes stay in memory.
  `os.Mkdir(out)` and `writeOut` run only after `fetchDraft` and `checkTagRef` succeed
  (`:302-314`). `runFetch` checks that a refused fetch leaves no `-out`.
- **Peak memory:** at most one oversized download, under 200 MiB of data (plus the
  `bytes.Buffer` growth slack), on top of the archives already verified. Archives are
  fetched in turn, and the first hash mismatch stops the fetch.

## Q2: reused-job rule

The rule (`fetch.go:424`, `:442-477`): a job id goes into `ranOK` only if it **ran**
(`runner_name`, or a non-zero `runner_id`), passed the hosted-group check, and
succeeded. `ranOK` is updated after each attempt finishes. So a job cannot vouch for
itself within one attempt, and an id that failed or was cancelled earlier is never
vouched for (this is tested). A later attempt may list a successful job with no runner
data only if its id is in `ranOK`.

What an attacker can and cannot do:

- **Job metadata.** An attacker with `actions: write` (T1/T3) can start re-runs, but
  cannot edit job metadata, attempt numbers or job ids. GitHub sets those.
- **Any non-hosted job refuses the run.** Every attempt from 1 to `run_attempt` is still
  checked (`:426`). Any job that ran on a non-hosted runner in any attempt refuses the
  whole run (`:449-457`). A self-hosted build in attempt 1 cannot be "reused" into a
  clean-looking attempt 2.
- **Attempt numbers.** `run_attempt` is bounded to 1..100 and must be a decimal
  (`:418-421`). Each attempt's job list must be complete (`total_count`, `:439-441`).
  A run that is re-run after `fetch` read it would show `in_progress` (refused in
  `checkRun`). Its new `sums` digest would also be missing from `logged`. Both points
  are unchanged by this PR.
- **The residual risk is an inconsistent API view.** The relaxation trusts "same job
  id = same execution". To abuse it, GitHub would have to do two things:
  1. re-execute a job under its old id in a new attempt; and
  2. report that new execution with null runner fields, for example after an ephemeral
     self-hosted runner was deleted.

  Both are unconfirmed (V5 / A9), and both are GitHub behaviour rather than attacker
  input. L-1 below makes the rule hold even if they happen.
- **`sums` source.** A no-runner `sums` job never becomes a digest source (`:467` needs
  `ran`). Its digest comes from the earlier attempt that really ran it.

## Findings

### L-1 · Low · confirmed-test — a no-runner job is accepted on its id alone; its other runner fields and its name are ignored
- **Where:** `tools/releasesign/fetch.go:448`, `:461-463`
- **What happens:** `ran` reads only `runner_name` and `runner_id`. In the no-runner
  branch, `runner_group_id` and `runner_group_name` are never checked, and neither is
  the job name.
  - A scratch test listed every reused job in attempt 2 with
    `runner_group_id: 7, runner_group_name: "evil"`: `fetch` exited 0.
  - The same test with every reused job renamed `renamed-<name>`: also rc 0.
- **Scenario:** GitHub reports a job that was re-executed in a later attempt, on a
  self-hosted runner that has since been deleted, under its earlier id with
  `runner_name`/`runner_id` cleared but the group fields kept. `fetch` accepts it as
  "reused". This needs the unconfirmed API behaviour described in Q2, so it is
  defence in depth.
- **Fix:**
  1. Treat a job as having runner data if **any** runner field is set: `runner_name`, a
     non-zero `runner_id`, a non-null `runner_group_id`, or a non-empty
     `runner_group_name`. Run the hosted-group check on all such jobs.
  2. Store `ranOK[jid] = j.Name` and require the reused listing to have the same name.
  3. Optionally also store `started_at`/`completed_at` and require them to be equal. A
     reused job keeps the earlier attempt's timestamps; a re-executed one cannot.
  4. Add `TestFetchRefuses` cases for a reused job with a non-hosted group and for one
     whose name changed.

### L-2 · Low · confirmed-test — the listed asset size is not compared with the bytes downloaded
- **Where:** `tools/releasesign/fetch.go:597-604`, `:611-616`
- **What happens:** `size` is used only for the early `size > limit` check. A scratch
  test set every asset's listed `size` to 1, below the real lengths: `fetch` exited 0.
  The cap and the SHA-256 checks still bind the content, so this is not a bypass. But
  the brief's property "the size is checked against the bytes actually read" does not
  hold, and a broken or tampered API view goes unnoticed.
- **Fix:** in `asset`, after `ghCapped` returns, refuse when
  `int64(len(b)) != size`, with a message like `draft asset %s: downloaded %d bytes,
  the release lists %d; refusing`. Add a `TestFetchRefuses` case where the listed size
  differs from the content. The existing `TestFetchRefusesOutputOverCap` cases, which
  set `size: 1`, still hit the cap first because their content is over 64 bytes. Its
  comment "a lying size" stays correct.

### I-1 · Info · confirmed-read — `gh` stderr is not capped
- **Where:** `fetch.go:91-92`, `:98`
- **What happens:** stderr goes into an unbounded `bytes.Buffer` and then into the error
  message. What `gh` writes there is its own error text, not response bodies, so the
  risk is negligible.
- **Fix (optional):** use a second `capWriter`, for example of 64 KiB, that truncates
  instead of failing.

### I-2 · Info · confirmed-read — test gaps
- **Where:** `tools/releasesign/fetch_test.go:449-469`, `:527-591`
- **What is missing:**
  - the two L-1 cases;
  - the L-2 case;
  - a three-attempt chain: attempt 3 reuses a job that attempt 2 also listed without a
    runner. It works today because `ranOK` is never cleared, but no test pins this;
  - a size that is not an integer (for example the literal `1.5`, which `ParseInt`
    refuses). The current negative test only covers `-1`;
  - an asset exactly at its cap being accepted (boundary `>` vs `>=`). A scratch test
    with `maxArchiveBytes` set to the largest archive's length exited 0, which is
    correct but not pinned;
  - a build-job log over the cap still giving rc 0 with `build: unavailable`.

  The covered fail-closed paths are good: no size, negative size, `SHA256SUMS` and an
  archive listed over the cap, output over the cap with a lying listed size (for
  `SHA256SUMS`, an archive, a log and an API answer), a no-runner success in attempt 1,
  a no-runner job with an unknown id, and a no-runner job whose id failed earlier. The
  F3S-03 item 1 gap is closed.
- **Fix:** add the cases above.

### I-3 · Info · confirmed-read — the docs mention only the asset caps
- **Where:** `Docs/ops/release-signing.md:92-93`, `:146-147`
- **What happens:** the text is accurate for assets ("refused before or while it
  downloads" matches `asset`/`ghCapped`). The re-run sentence at `:86-88` is accurate,
  but it should say "succeeded on" to match the code. The docs do not say that a job
  log over 16 MiB or an API answer over 32 MiB also stops `fetch`. An owner who hits
  that limit would find no matching line under "If a check fails".
- **Fix:** add one clause, for example "a job log or API answer over `fetch`'s cap
  (16 MiB / 32 MiB) also stops it". If L-1 is done, change the re-run sentence to
  "…ran that same job, under the same name, successfully on a GitHub-hosted runner".

### I-4 · Info · plausible — the caps depend on V5 / A9 facts
- **Where:** `fetch.go:56-61`
- **What happens:** 32 MiB is about 4,500 run objects or about 3,000 releases in one
  paginated answer. That is ample today, but a busy repo will one day exceed it, and
  `fetch` will then refuse (fail closed, availability only). The reused-job rule's
  premise is still to be confirmed by A9.
- **Fix:** when A9 runs, record the attempt-2 job JSON (as review 62 F3S-02 asked) and
  check that reused jobs keep their ids. If they keep their runner data, the new branch
  is simply unused.

## Checked and fine

- `capWriter` never keeps a partial chunk, and the `over` flag takes precedence over the
  exit status.
- `exec.CommandContext` with `cancel()` kills `gh`. `gh api` starts no children, so
  `Wait` cannot block on an inherited pipe.
- `ranNow` is applied only after each attempt, and only for jobs that **succeeded on a
  checked runner**.
- A no-runner job is never a source of the `sums` digest.
- The latest attempt still requires every job to be `completed/success` (`:464-466`).
- Draft assets: duplicate names are refused, ids are validated, and there must be
  exactly seven assets (unchanged).
- Nothing is written on any refusal (the `runFetch` assertion, plus the scratch cases).
