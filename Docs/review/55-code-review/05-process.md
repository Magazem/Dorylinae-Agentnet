# 55 / 05: Process: dispatch, verification, consolidation

Owner's terms: **Opus reviewers only**, about **6 in parallel**, **small chunks**, each
reviewed with a **fresh context**, thorough rather than cheap, reviewers **report only**
(no fixes), and a **Critical goes to the Orchestrator at once**. Everything targets commit
**`6cc26a7`**.

## 1. Phases

| Phase | Units | Input | Output | Parallel |
|---|---|---|---|---|
| P1 Chunks | C01–C31 ([03-chunks.md](03-chunks.md)) | 01, 02, own chunk section | `chunks/Cnn.md` | ≤ 6 |
| P2 Themes | T1–T5, T6a, T6b, T6c, T7–T13 (15 passes, [04-themes.md](04-themes.md)) | 01, 02, own theme section, **all** chunk reports | `themes/Tn.md` | ≤ 6 |
| P3 Verify | every Critical and High, plus every Medium with confidence `suspected` | 01, the one finding, its chunk/theme section | `verify/<finding-id>.md` | ≤ 6 |
| P4 Consolidate | one consolidator (Opus) | everything above | `99-report.md` | 1 |
| P5 Owner | the owner reads 99 and decides which tickets to run | 99 | decisions in HANDOFF §3 | — |

- P1 goes in id order (the order is by risk), so the riskiest chunks finish first and
  their Criticals surface early.
- P3 starts as soon as a report with a High/Critical lands; it does not wait for P1 to end.
  A Critical is verified **immediately** (§5).
- P2 starts only when all 31 chunk reports exist, because themes resolve the chunks'
  "Assumptions and contracts".
- Rough size: 31 + 15 + (verifications, typically 10–30) + 1 ≈ 60–80 Opus runs. A chunk
  takes a reviewer roughly 30–90 minutes. At 6 in parallel, P1 is about 6 waves.

## 2. Fresh context, consistency

- **One unit per worker context.** Spawn a fresh Opus worker for each chunk, theme or
  verification (or clear the worker's context between units). A worker never reviews two
  chunks, and never verifies a finding from a chunk or theme it reviewed.
- **The brief is identical for everyone:** the task text in §3 plus [01-rubric.md](01-rubric.md)
  verbatim. Do not add per-worker hints beyond the chunk/theme section; if something must be
  said to everyone, add it to 01 first, and note the change (date, reason) at the end of 01.
- Workers run on their own read-only worktree of branch `review/plan` (or any branch frozen
  at `6cc26a7`), one worktree per worker, created and removed by the Orchestrator. Their
  only writes are their report file and optional `zz_review55_*_test.go` files.
- The Orchestrator copies each finished report into the collecting worktree under
  `Docs/review/55-code-review/` (paths in §6), so that P2–P4 read everything from one place.
- Machine load: reviewers run only targeted tests (rubric §6 rule 4), so 6 in parallel is
  fine. If IPC tests start timing out (HANDOFF §5), drop to 4.

## 3. Task text for each unit (the Orchestrator's template)

```
Task: code review 55, <unit id> (<title>).
Worktree: C:\Users\yazan\Documents\AgentNet-wt\<name> (frozen at 6cc26a7). Stay inside it.
Read first, fully: Docs/review/55-code-review/01-rubric.md (your brief; follow it exactly),
02-open-findings.md, and your section "<unit id>" in 03-chunks.md | 04-themes.md.
[Theme only: then read every report in Docs/review/55-code-review/chunks/.]
[Verify only: the finding to verify is <finding id> in <report path>; see 05-process.md §4.]
Write your report to Docs/review/55-code-review/<chunks|themes|verify>/<file>.md.
Report only: no fixes, no git except read-only, targeted tests only.
A Critical: message slot 01a0d959-dcbb-7e93-8535-9f1283d5aea1 at once, then continue.
Finish with a team_send_message to the same slot: model id, elapsed minutes, counts per
severity, report path, Critical ids (≤ 150 words).
```

## 4. Verification (P3)

**What gets verified:** every **Critical** and **High**, from chunks and themes, and every
**Medium** whose confidence is `suspected`. The consolidator may also send any finding
whose severity looks inconsistent with the rubric.

**Who:** a fresh Opus worker that did **not** write the finding and did not review its
chunk. It gets the rubric, the finding block (only that block, not the rest of the report,
so it is not anchored by the reviewer's narrative), and the chunk/theme section for context.

**Method:** the verifier's job is to **disprove** the finding. It must:
1. Restate the claim in one sentence and the precondition the scenario needs.
2. Trace the path independently from the entry point (not from the cited line backwards):
   find every check the scenario must get past, including checks in other packages, specs
   and owner decisions that make it intended behaviour.
3. Where feasible, confirm with a targeted test (`zz_review55_<id>_test.go`), or rerun the
   reviewer's test and check that it fails for the stated reason, not an unrelated one.
   If a `-race` run is needed, write the exact test and ask the Orchestrator for a CI run.
4. Re-rate with the rubric §2, independently of the original rating.

**Outcome** (exactly one):
- `confirmed` (severity unchanged),
- `confirmed-different-severity` (state the new level and which rubric criterion decides it),
- `false-positive` (state the check or decision that blocks it, with `file:line`),
- `duplicate` (of which finding or `O-nnn`),
- `inconclusive` (what would settle it: a CI race run, a real macOS/Linux box, an owner
  answer). The consolidator treats an inconclusive High as High until settled.

**File:** `verify/<finding-id>.md`:
```markdown
# Verify C07-03
Verifier: <name> · model <id> · elapsed <n> min · commit 6cc26a7
**Outcome:** confirmed | confirmed-different-severity (Medium) | false-positive | duplicate (O-054) | inconclusive
**Claim restated:** …
**Path traced:** entry point → … → the defect (file:line each)
**Checks that could block it:** each one, and why it does or does not
**Test:** file, command, result (or "none, because …")
**Severity reasoning:** rubric criterion quoted, actor, reachability
```

## 5. Criticals

1. The reviewer messages the Orchestrator at once (rubric §6 rule 5) and keeps working.
2. The Orchestrator dispatches a verifier for it immediately (ahead of the queue) and tells
   the owner as soon as it is `confirmed` (or `inconclusive`), with the scenario in plain words.
3. Whether to break the code freeze for an urgent fix is the **owner's** decision. The
   early private relay (4.1p) and any release are the exposure to weigh: a confirmed
   Critical in C01–C04 or C15 may justify pausing the relay or the release process first.
4. The review continues on `6cc26a7` either way; a fix lands later as a normal ticket with
   its own security review (HANDOFF rule 4).

## 6. Files and naming

All under `Docs/review/55-code-review/`:

| Path | Written by |
|---|---|
| `00-index.md` | CR-Index (Haiku) |
| `01-rubric.md`, `02-open-findings.md`, `03-chunks.md`, `04-themes.md`, `05-process.md` | CR-Plan (this plan) |
| `chunks/C01.md` … `chunks/C31.md` | chunk reviewers |
| `themes/T1.md` … `themes/T5.md`, `themes/T6a.md`, `themes/T6b.md`, `themes/T6c.md`, `themes/T7.md` … `themes/T13.md` | theme reviewers |
| `verify/C07-03.md`, `verify/T3-02.md`, … (one per verified finding id) | verifiers |
| `99-report.md` | the consolidator |

Finding ids: `Cnn-kk` / `Tn-kk` (two digits, per report, never renumbered). The consolidated
report gives each unique issue a new id `R55-nnn` and lists every original id as an alias.
Temporary tests: `zz_review55_<finding-id>_test.go`, never merged.

## 7. Consolidation (P4) and `99-report.md`

The consolidator is one fresh Opus worker. It reads all of 00–05, every chunk, theme and
verification report. It does not review code anew, except to settle a disagreement it cannot
settle from the reports (then it says so and cites what it read).

**Steps:**
1. **Coverage check.** Every chunk and theme has a report; every chunk question and theme
   method item is answered; every `unchecked` assumption was resolved by a theme. List any
   gap in "Not covered" (with the reason); do not silently skip it.
2. **Deduplicate.** One entry per **root cause**. The same defect reported by a chunk and a
   theme, or at two call sites of one helper, becomes one `R55-nnn` with every original id
   as an alias and every location listed. Different root causes with a similar symptom stay
   separate.
3. **Normalise severity** with the rubric §2 only: the verified rating wins over the
   reviewer's; the adjustments in §2 (off-by-default features, hosted relay sizing,
   owner's-own-mistake) are applied once, here, and written down per finding.
   `false-positive` findings are dropped from the ranked list but listed in an appendix with
   the verifier's reason, so they are not re-raised later.
4. **Open findings.** Produce a delta for `02-open-findings.md`: rows found fixed (with the
   evidence), rows escalated (by which R55 id), rows confirmed still open. The consolidator
   does not edit 02; the Orchestrator applies the delta.
5. **Rank** all remaining findings: Critical → Info; within a level, by reachability (relay /
   stranger > paired peer > local agent > other local user), then by blast radius.
6. **Fix plan: group into tickets.** A ticket fixes one root cause or a tight cluster in one
   area, so that it can get one focused security review. For each ticket:
   `R55-Fn · title · findings (R55 ids) · files · severity (max of its findings) · suggested
   model (D26/D28: Opus for security-critical/OS-level/crypto/peer-input parsing, Sonnet for
   well-specified feature fixes, Lite for routine) · needs a spec change? (→ spec first, HANDOFF
   rule 3) · needs an owner decision? · acceptance test (the failing test that must pass) ·
   dependencies / order`.
   Order: Criticals and Highs first; spec changes before the code that depends on them;
   tickets that touch the same files sequenced, not parallel.
7. **Owner decisions.** A short list of the questions only the owner can answer (reopening a
   D-decision, accepting a residual risk, the D33 revisit (O-177) with C25's facts, anything
   `inconclusive`).

**Structure of `99-report.md`:**
1. Header (consolidator, model, elapsed, commit `6cc26a7`, date).
2. Verdict in one paragraph, and counts per severity (before and after verification).
3. Coverage: units done, lines covered (69,135: 62,806 Go + 6,329 other), gaps.
4. Ranked findings (R55 ids), each in the rubric §4 format plus `Aliases`, `Verified`,
   `Severity reasoning`.
5. Fix plan: the ticket table, then per-ticket detail.
6. Owner decisions needed.
7. Delta for 02-open-findings.md.
8. Appendix A: false positives and duplicates, with reasons.
9. Appendix B: per-unit summaries (one line each, with the reviewer's model and minutes).

## 8. Done criteria

The review is done when: 31 chunk reports and 15 theme reports exist; every Critical/High
(and every `suspected` Medium) has a verification; `99-report.md` exists and passes its own
coverage check; the Orchestrator has updated HANDOFF §0 with the outcome and the owner has
the fix plan. No code changes happen as part of review 55 itself.
