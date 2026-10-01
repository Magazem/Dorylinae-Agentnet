# 81b: R55-F13 focused check (D62 caps and prune approval binding)

Reviewer: R55-F13check-Opus · model claude-opus-5-5 · 2026-10-01 · worktree `AgentNet-wt/r55-f13`,
branch `p4/r55-f13`, HEAD `43a24b6` (the review-81 fix). This is a report only; two proof
tests were added (listed at the end).

## Verdict

**Changes needed: one Medium (M1) and one Low (L1).** Neither is a regression from
`43a24b6`: both are in F13's prune code. M1 makes the "counts are an upper bound / nothing newer
is removed" argument of retention.md §Approval false, which is what L1 of review 81 now relies on.
The D62 cap change itself is sound.

## (1) D62 introducer caps: no finding

- **The introduction time is local.** `peers.Store.Introduce` (`internal/peers/store.go:389`)
  writes `paired_at` only on the first INSERT, using `now` from `team/kinds.go:63` (`s.now()`),
  never a time from the roster. A later roster, a replayed roster (deduped anyway) or a relay
  delay can only leave it unchanged or make it later, which is more restrictive. `AddTrusted`
  keeps `paired_at` and clears `introduced_by`, so the key leaves the introducer caps (it
  becomes a direct pairing, as before).
- **A GC'd key that is re-added is fresh.** `GCIntroduced`/`removeTx` delete the `peers` row, so
  a re-introduction INSERTs a new `paired_at = now`. Its old rows keep their old
  `introduced_at` and stop counting, and its new rows count. Churn is capped:
  `TestIntroducerCapsFreshKeyChurn` covers GC and re-add, and the 7-day roll-off.
- **Hostile established members can't lock others out.** Their rows carry an old
  `introduced_at` and are excluded from both counts (`receive.go:487`, the `introduced_at >= ?`
  in both queries). Only a fresh sender is checked against the introducer total.
  `TestIntroducerCapsSpareEstablishedMembers` shows this.
- **Formats.** `introducerOf` (`receive.go:465`) re-renders `paired_at` (RFC3339) with
  `storeTime`, so the column, `freshSince` and the SQL compare as same-width strings.
- **Migration 24.** It adds the new column and extends the two indexes with `introduced_at`
  ahead of `state`/`received_at`, so the queries still use them. `migration24_test.go` rewinds
  both columns, `audit/chain_test.go` was updated, and the store and audit tests pass.
- **Residuals (Info; accepted by D62's wording):**
  - Fresh hostile keys can still starve an honest member introduced in the same 7-day window.
  - An owner can pre-age up to `team.MaxMembers` (32) keys per team for 7 days and then use
    per-key caps only.
  - A local clock that was behind at introduction time (and later corrected) makes a key look
    established early. Only the local operator controls this.

## (2) Prune approval binding

Checked, no finding:
- Nothing is removed without an approved id. The path is unchanged: `runPrune` →
  `pruneBatch` only with `auth.approved`.
- Every batch uses the approved `auth.cutoff`. `PruneTx` still refuses a cutoff under 35 d.
- Every `mail_inbox` delete keeps `received_at < cutoff`, so inbox rows that join late are
  still older than the approved cutoff.
- `countAll` (`retention.go:~340-405`) and `makePlan` have the same predicates, table by
  table: requests, sessions (dependents plus orphans), debates, grants (direction rules plus
  session grants), experience, inbox delete and inbox blank.

### M1 (Medium): the sender mirror finalises an `out` row without setting `updated`

- **Where:** `internal/request/mirror.go:320` (`applyMirror`). `setSQL` sets
  `state, state_seq, state_at` plus `extraSet`, and none of the five callers
  (`:97, :155, :178, :258, :291`) adds `updated`. Every other request update does set it
  (`lifecycle.go:189`, `cancel.go:94/161/270`, `session_hooks.go:192`).
- **Scenario (honest peers).**
  - A sends a request 40 d ago. B deferred it (up to 90 d is allowed) or never answered.
  - Today B sends `request.complete` with a result, or a decline or cancel. The row turns
    final with `updated` still 40 d old.
  - If no work session or debate blocks it (a Phase-1 complete, any decline or cancel), a
    35 d `prune` removes the row and the result received today.
  - Against approval binding, the same row can **join the set after the human approved**. It
    becomes final between count and batch, so the batches remove more requests than the
    summary showed, and they remove content that is newer than the cutoff. That breaks
    retention.md §Approval ("every change sets `updated` to now … an upper bound") and
    §Why 35 days ("created ≤ updated").
  - A peer can choose when that happens: it can send its final mail while a prune approval
    is pending.
- **Proof:** `internal/request/zz_sec81b_mirror_updated_test.go`
  (`TestSec81bMirrorLeavesUpdatedOld`) fails. After a complete just now, `updated` is still
  40 d old and `DryRun` at 35 d counts the row.
- **Fix direction:**
  - Add `updated = ?` (`storeTime(s.now())`) to `applyMirror`'s `setSQL`.
  - Keep the test as a regression test, inverted to pass.
  - Also consider bumping `updated` in `session_hooks.go:129`, which writes a result into a
    completed `out` row.
  - Optional hardening for the binding: have `pruneBatch` stop once the running totals reach
    the approved counts (for inbox, bound by the approved `MailInbox + InboxBlanked`).

### L1 (Low): a debate's experience record is not in the approved count but is removed in the same run

- **Where:** `retention.go:194-220` (`makePlan`) and `:386` (`ex` CTE).
  - Experience records are collected only for removed *work* sessions, plus orphans
    (`created < cutoff`, with no work session and no debate).
  - `debate/experience.go` writes records under the **debate** session, so those records are
    counted neither with the debate nor as orphans while the debate exists.
  - Once batch k removes the debate, the record becomes an orphan, and batch k+1 of the same
    approved run removes it.
- **Impact:** `experience_records` in the summary understates what is removed. The extra rows
  are older than the cutoff and belong to approved debates, so nothing newer is removed.
- **Proof:** `internal/retention/zz_sec81b_debateexp_test.go`
  (`TestSec81bDebateExperienceNotCounted`) fails: approved 0, removed 1.
- **Fix direction:** remove the debate's experience records with the debate, in both
  `makePlan` (`p.debates` loop) and `countAll` (`UNION … JOIN db`). Add a row for them to
  retention.md's "removed with the request" table. The dry-run tests would then catch this
  case.

## Tests run

All with `GOTMPDIR=%TEMP%\f13check`:
- `go test` passes for `./internal/request`, `./internal/store`, `./internal/audit`, and
  `./internal/retention` (excluding the new proof).
- `./internal/daemon -run 'Prune|Cap|Retention|AuditInventory$'` passes.
- `./cmd/agentnet -run Prune` passes.
- The two proof tests fail, as described above.

## Files created

- `Docs/review/81b-r55-f13-check.md` (this file)
- `internal/request/zz_sec81b_mirror_updated_test.go` (M1 proof, fails today)
- `internal/retention/zz_sec81b_debateexp_test.go` (L1 proof, fails today)
- Temp only: `%TEMP%\f13check\` (GOTMPDIR)
