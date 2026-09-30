# Review 70: R55-F12 security review (poison mail)

Branch `p4/r55-f12`, commit `87dc2ef` (on `main` `eadf189`). Scope: review 55 R55-017, 058, 021,
067, 070 and 065 (`Docs/review/55-code-review/99-report.md` §4, §4.1, ticket R55-F12).
Reviewer: R55-F12sec-Opus. This is a read-only review: no code was changed, and no test was added.

## Verdict

**Changes needed (small).** The core fix is sound. That covers the dedupe on `mail_seen` OR
`mail_inbox`, the `op.Outcome` hand-off, the once-per-id logging, the close-list rules, the
B-side closed-is-final rule and the canonical grant token. One Medium regression remains: R55-070
now drops, on A, a legitimate constraint from B that arrives before B's accept (M1). Two Low items
follow; neither blocks the merge.

## Findings

### M1 · Medium: A drops a B constraint that overtakes the accept
- **Where:** `internal/debate/constraint.go:189-195` (the new `!constraintPhase(r.phase)` branch);
  proved by `internal/debate/close_list_test.go:83` `TestConstraintWhileInvitedIgnored` (case
  `{a, b.self}`).
- **Scenario:**
  1. B accepts, which moves B to `positions` (`start.go` `OpenedTx`). B's human then approves a
     constraint, and B sends `debate.constraint`.
  2. debate.md §Applying peer entries (review 43 M3) says "mail can overtake mail". If the
     constraint reaches A before the `request.accept` and slot-1 entry, A is still `invited`.
  3. A ignores it with reason `state` and acks it. B never re-sends an acked mail.
  4. The constraint is lost for good on A. A's close does not list it, so B marks it `late` as if
     it had arrived after the close.
- **Effect:** The Decisions still agree, because A is authoritative for the set, so both sides sign
  the same record. What goes wrong:
  - A human-approved constraint silently leaves the signed record.
  - B's view shows it active until the close, while A never had it.
  - Before this change, A stored it.
  - This is the exact overtake that review 43 M3 made A tolerate for slot 1.
- **Fix direction:** Mirror review 43 M3.
  - On A (`r.role == RoleInitiator`) in `invited`, apply a B constraint as usual when A's `out`
    request row is `pending` or `deferred`. Alternatively, hold it until the accept opens the
    debate.
  - Ignore with `state` only on B (`invited` means B has not accepted, so no A constraint can be
    legitimate), and on A when the request is gone, cancelled or declined.
  - `maxStoredConstraints` (20) already bounds a misbehaving peer, which was R55-070's concern.
  - Update debate.md §Human constraints (the new sentence at line 367-370) to match.

### L1 · Low: only the first error per (from, id) is ever logged
- **Where:** `internal/mail/receiver.go:191-211`.
- **Scenario:**
  1. The first delivery fails with a transient error, such as `SQLITE_BUSY` or context cancelled
     at shutdown. It is logged.
  2. Every later resend fails with a different, persistent error, such as an Apply bug or a
     constraint violation. None of these are logged.
  3. The operator sees only the misleading first cause, for up to 14 days of resends, unless
     4096 other failing pairs reset the set first.
- **Not a flood or hiding vector:** Only non-rejection errors enter the set, and the key includes
  `from`, so one peer cannot evict or mask another peer's entry. Reset-when-full only causes
  re-logging, never less logging. Growth is bounded at 4096 keys. The receiver is
  single-consumer (`internal/daemon/mail.go:177-186`), and `failMu` guards the map anyway.
- **Fix direction:** Log again when the error text or class changes, or log again after a time
  window (for example once per hour per pair with a repeat count). R55-058's own fix direction
  ("after N identical failures, mark the mail bad-body") was not implemented. A poison mail still
  retries unacked for 14 days, now with one log line. Record this as deferred or accepted in
  HANDOFF.

### L2 · Low: spec and code both say "no body content", but the log carries the raw `err`
- **Where:** `internal/mail/receiver.go:209-210`; `Docs/protocol/mail.md` new §Dedupe paragraph.
- **What I checked:**
  - Plain (non-bad-body) Apply errors in `request`, `worksession`, `debate`, `team` and
    `daemon/grant_kinds.go` wrap DB errors and ids that already passed validation. I found no
    path that formats body text into a plain error.
  - Bad-body errors, which do quote body values (for example `badBody("%s", verr.Error())`),
    take the `storeBad` path and are never logged.
  - Today's claim therefore holds.
- **Risk:** It rests on convention only. A future `fmt.Errorf("…%s", bodyField)` without
  `ErrBadBody` would leak peer text into the log.
- **Fix direction:** Pick one:
  - Add a comment on `Kind.Apply` stating the contract.
  - Log `errors.Unwrap` chains only down to a fixed category.
  - Add a test that feeds a marker string through each kind's plain-error paths.

## Checks that passed

**Outcome hand-off (R55-017 leak):**
- Every former `pending*` `sync.Map` is gone from production code (grep for `pending[A-Z]` and
  `sync.Map` finds only relay code).
- Every consumer reads its own concrete type with a checked assertion, so a nil or foreign
  `Outcome` is a no-op with no panic: `grant_kinds.go` `*grantOutcome` and `*grantRevokeOutcome`,
  `device.go` `string`, debate `afters`, request `*applyOutcome`, `*mirrorOutcome` and
  `*cancelOutcome`, team `*rosterOutcome`, `*joinOutcome` and `*leaveOutcome`, worksession
  `*resultOutcome`, `*stateOutcome` and `*cancelOutcome`.
- `Kind.Apply` and `Kind.After` are called only from `mail/receiver.go:180,251`, on a fresh `op`
  from `Opener.Open` (`open.go:227`).
- No Apply passes `op` to another package's Apply. Cross-package hooks take `(peer, request)` and
  return closures (`sessionAfter`, `debateAfter`, `helperAfter`), so two kinds cannot overwrite
  one `op.Outcome`.
- After runs only when `store` returns `seenNew` with a nil error, which means after a successful
  commit. It does not run for the bad-body (`seenBad`, `seenDupBad`) or duplicate paths, or for
  any rolled-back error. Debate kinds set `Outcome` only in a `defer` when `err == nil`, and
  `device.go` resets it at the start of Apply.
- There is no shared mutable state, so this is race-free.

**Dedupe (R55-017):**
- The key is `(from_key, id)` in both tables (`mail_inbox` primary key, `store.go:65-73`).
  `from_key` is the verified sender, so peer X cannot suppress Y's mail.
- Ids are `crypto/rand` 128-bit (`mail/mail.go:78`), so an honest sender never reuses one.
- The inbox-hit path returns `seenDup`: it rolls back, runs no After, writes no `mail.in` audit,
  and acks under `ids`, the same as a `mail_seen` duplicate.
- No production code deletes `mail_inbox` rows, which matches the new mail.md wording (prune both
  together, D12).
- Non-inbox kinds (`team.*`, `keys`) still dedupe on `mail_seen` only, as the spec text says, and
  they are idempotent on re-apply.

**Close list (R55-021):**
- A builds its list with `ORDER BY id` (`store.go:428`, BINARY collation). This is the same order
  as Go's `<=` on `c-<hex>` ids.
- A's active count stays at or below 10:
  - `ConstraintPreconditionTx` is checked at confirm.
  - A received constraint beyond 10 is stored `excess`.
- An honest A therefore never trips B's new bad-body rule.
- `TooLargeError` → `refuseOnB("size")` sends the wire `refused: "mismatch"` (`decision.go:295`),
  which A's `applySign` accepts. Both sides end refused and broken, with no A/B split.
- `size` exists only as an audit reason, which matches decision.md.

**Worksession mirror (R55-067):**
- Closed-is-final is checked after the `seq` dedupe and before any write. It stops the
  experience-PK poison and the O-131 reopen.
- The only B-local close is the Phase 1 fallback (`phase1.go:59-67`), which runs only when A
  cannot send `ws.state`. Ignoring a later state there is correct.
- The `ws.ignored` reason `closed` matches work-session.md step 3.

**Grant token (R55-065):**
- The holder now uses `agentcard.CanonicalValue` over the received generic token. The issuer uses
  the same function (`capability.Canonical`, `token.go:129-170`), and so does `Verify`'s size
  check.
- `TestGrantKindCanonicalToken` covers a `&<>`-heavy token under 2048 bytes.

**Spec edits:** mail.md, decision.md and work-session.md match the code. The debate.md text
matches the code, but see M1 for the rule itself.

## Tests run (targeted)

- `go vet` on mail, debate, request, worksession, team and daemon: clean.
- `go test ./internal/mail/ ./internal/debate/ ./internal/request/ ./internal/worksession/
  ./internal/team/`: all ok.
- `go test ./internal/daemon/ -run 'TestAuditInventory|TestGrant' -v`: `TestAuditInventory`,
  `TestAuditInventoryIsComplete` and every `TestGrant*` pass (including the new
  `TestGrantKindCanonicalToken`).
  - `TestAuditInventoryDevices` fails only on the known local C:\ ACL (`writable_by_others:
    "C:\\"`). This is not a regression.
- I did not run the race detector (it runs in CI only). The race analysis above is from reading
  the code.

**Note:** The report's acceptance tests (`internal/mail/zz_review55_T9-01_test.go`, the C24-01
test, `internal/worksession/zz_review55_C22-01_test.go`) are not in the tree, so they could not be
"inverted". Their scenarios are covered by the new `reused_id_test.go`, `close_list_test.go` and
`mirror_closed_test.go`.

## Files created

- `Docs/review/70-r55-f12-security.md` (this file). No other files were created or changed.
