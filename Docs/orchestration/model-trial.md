# Worker model trial: Opus 5.5 vs Sonnet for implementation

Owner request (2026-09-24): move more implementation work to Opus 5.5 workers, gradually, starting with
complex tasks and investigations, and measure whether it is faster or needs fewer rounds.

## Method

- Sonnet stays the default for well-specified tickets. Opus 5.5 gets the complex ones: investigations,
  security-heavy code, and code with hard concurrency or OS-specific parts.
- One or two Opus tickets at a time, never a whole wave.
- Every Opus worker states its exact model id in its report, so we know it is Opus 5.5.
- The same task template, gate and security review apply to both models, so the numbers compare.

## Metrics (per ticket)

| Metric | How it is measured |
|---|---|
| Minutes to report | Task creation (UUIDv7 time in the task id) to the worker's done report / my commit. Usage-limit pauses are marked, not counted. |
| Follow-up rounds | Times I had to send the work back (missing tests, gate failures, spec misses) before review. |
| Gate failures | Problems my own gate re-run found that the worker's report said were clean. |
| Review findings | Critical / High / Medium from the Opus security review. |
| CI failures after merge | Failures attributable to the ticket (race, per-OS lint, flakes). |

## Baseline: Sonnet tickets so far

| Ticket | Size | Minutes | Follow-ups | Gate failures | Review C/H/M | CI after merge |
|---|---|---|---|---|---|---|
| 2.2b tokens | S | 15 | 0 | 1 (gofmt) | 0/0/2 | 0 |
| 2.2a approval | M | 51 (incl. a usage-limit pause) | 0 | 1 (gofmt) | 0/0/4 | 1 (Linux lint) |
| 2.1a work sessions | L | 57 (incl. a usage-limit pause) | 0 | 0 | 1/1/6 | 1 (flaky test) |
| 2.2c grants | M | 49 | 1 (integration after rebase) | 0 | 0/1/5 | — |
| 2.1b session IPC | M | 95 | 2 (missing tests; D18 rework) | 0 | not reviewed | 2 (flaky tests) |
| 2.2d approval window | M | 44 | 1 (integration after rebase) | 0 | 0/1/9 | 3 (per-OS lint; test races) |
| 2.5 consult | M | overnight usage limit, not comparable | 0 | 0 | 0/0/1 | — |
| 2.3a fetch server | L | ~60 (one restart when idle at start) | 0 | 1 (load-sensitive timing bound) | 0/0/3 | — |
| 2.4 quarantine | S | ~60 | 0 | 0 | 0/2/0 | — |
| 2.D1 device link | M | ~60 | 0 | 0 | 0/0/1 | — |
| 2.3c fetch client (control, model claude-sonnet-5) | M | ~17 | 0 | 0 | 0/0/4 | — |

## Opus 5.5 tickets

| Ticket | Kind | Minutes | Follow-ups | Gate failures | Review C/H/M | CI after merge | Model id |
|---|---|---|---|---|---|---|---|
| INV-1 pairing flake | investigation + test fix | 24 | 0 | 0 | n/a (test-only) | — | claude-opus-5-5 |
| 2.3b git serving | M, security-heavy | ~16 | 0 | 0 | 0/0/2 | — | claude-opus-5-5 |

## Findings

- **Measurement caveat (important):** the earlier Sonnet "minutes" were measured from task creation to **my commit**, which includes my own processing delay and other waits. From 2.3b/2.3c on, the time is dispatch to the **worker's report** (the worker states it). On that fairer measure, **Sonnet 5 (2.3c) took ~17 min, the same as Opus 5.5 (2.3b, ~16 min)**, both with a clean first gate. So far speed is a tie; the comparison must come from review findings and follow-ups. Reviews: 2.3b (Opus) 0/0/2 vs 2.3c (Sonnet 5) 0/0/4. Sonnet's Mediums were robustness/UX (IPC wait, a limit counting finished fetches, terminal escapes, file mode); Opus's were subtle git semantics. One pair is not enough to call it.

- **2.3b (Opus 5.5):** ~16 min from dispatch to report for an M security ticket (Sonnet M tickets: 44–95 min). My gate passed on the first run. Tests cover every acceptance item plus a positive control (a porcelain commit must trigger the hostile hook, which proves the fixture is really hostile). It flagged its own residual risks. Review 37: 0/0/2, both subtle (git rev-parse resolving a missing branch to a same-named tag; the Windows git.exe launcher surviving the timeout kill).

- **INV-1 (Opus 5.5):** 24 min, no follow-ups, my gate found nothing. It reproduced the flake under synthetic load, proved the root cause (a 1.5 s test ConfirmWait vs Argon2id under load, not the bus), fixed three other tests with the same exposure, and reported an unrelated flake instead of guessing at it. No Sonnet baseline for investigations; qualitatively above the Sonnet reports so far (evidence-based, scoped).

