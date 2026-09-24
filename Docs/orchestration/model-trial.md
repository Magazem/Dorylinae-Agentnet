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
| 2.3a fetch server | L | ~60 (one restart when idle at start) | 0 | load flakes only | pending | — |
| 2.4 quarantine | S | ~60 | 0 | load flakes only | pending | — |
| 2.D1 device link | M | ~60 | 0 | load flakes only | pending | — |

## Opus 5.5 tickets

| Ticket | Kind | Minutes | Follow-ups | Gate failures | Review C/H/M | CI after merge | Model id |
|---|---|---|---|---|---|---|---|

## Findings

(Filled in as tickets complete.)
