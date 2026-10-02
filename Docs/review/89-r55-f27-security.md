# 89: R55-F27 security review (fail closed on rare errors)

Branch `p4/r55-f27`, commit `0f81383`. Scope: R55-080, R55-081, R55-104 and R55-147 in
`Docs/review/55-code-review/99-report.md`. This review is read-only. No code or tests were added.

## Verdict

**Approve with one Low fix recommended (F1).** All four changes fail closed as the ticket
intends. No High or Medium findings.

The one gap: when the wrong-code transaction fails, the bad guess is not counted, and the
approval stays confirmable. F1 explains the cases where this matters.

Targeted tests:

- `go test ./internal/approval/` passes.
- `go test ./internal/daemon/ -run 'Grant|Request|Approv|UnverifiedPeer|BadCode|Recheck'` passes
  except `TestHelperRunsInScopeRequests`. That test fails on this machine's ACL on `C:\`
  (`writable_by_others: "C:\" can be changed by Authenticated Users`). The failing code is in
  `device_run`, which this ticket does not touch.
- `-run 'FailClosed|Policy|Orphan|D5'` passes, including `TestGrantKindSessionReadErrorIsNotOrphan`
  and `TestRecheckPolicyTx`.

## Checks

| Question | Result |
|---|---|
| D5 treats a missing peer row as unverified | Yes. `request.go:460` returns `ok=false`, and `request.go:177` refuses the request on a non-loopback relay. A read error is returned, never treated as "not relay". |
| Other `"", nil`-style reads on the trust, grant and approval paths | None left. These sites remain and are all correct: `peerTrustTx` (`grant.go:956`) returns `ok=false` and its callers refuse. `peerFacts` (`approval_summary.go:35`) only displays text. `isPairedCtx` treats not-found as not paired. `CheckSensitiveGrant` treats no debate as allowed, which is correct. `MergeMailboxKeysTx` ignores a key for a peer that is gone. The fetch-server and fetch-client `SessionOpen` callbacks (`daemon/fetch.go:29`, `fetch_client.go:480`) report a DB error as `unknown_session`. That is a refusal, so it fails closed, but the reason is wrong (I1). |
| Grant `sessionOpen` errors are retryable, not orphaned | Yes (`grant_kinds.go:64`). Only `ErrUnknownSession` leads to an orphan. Any other error rolls back the dedupe transaction and the mail is not acked (`mail/receiver.go:335-345`). |
| Can a peer cause an endless retry loop or a state leak? | No. A peer cannot trigger a DB error through the session id: it is a plain lookup by key. The resend rate is limited by the sender's outbox backoff and expiry (`outbox.go:387,472`). Failures are logged once per (from, id) (`logFailure`). Nothing is written on the error path because the whole transaction rolls back. One persistent case exists: a stored session row whose `result` no longer decodes in `toView`/`decodeStoredResult`. Every grant mail for that session then stays unacked until it expires (I2). Before this change those grants were acked as orphans and lost, so retrying is no worse. |
| Does the policy recheck cover every way a policy can change? | Yes. There is no `UPDATE grant_policies` anywhere. The only writes are `PolicyInsertTx` (random `p-<32 hex>` id), `PolicyDelete`, `PolicyDeleteForPeerTx` (peer removed) and the expiry prune. So: **removed** → `ErrNoRows` → `bad_state`. **Expired** → the `until` check. **Peer removed** → the policies are deleted with the peer, and `recheckIssuanceTx` also catches it. **Scope, path, public or max TTL changed** → impossible, because rows are never edited. A NULL `until` from before 2.2c fails the parse and refuses. `RFC3339Nano` parses the store format `...05.000Z`. |
| Refusing on a removed policy instead of falling back to approval | Acceptable, and the right choice. The window is milliseconds, the user can re-run `grant_create`, and the re-run takes the approval path. A fallback inside the error path would duplicate the summary and pending-insert code for no security gain. The refusal is not audited, though, unlike the debate refusal nearby (I3). |
| Wrong-code transaction: 3 attempts and the daily limit under concurrency | Correct. `confirm` holds `s.mu` across the whole `recordBadCode` transaction (`store.go:312`). The window and terminal paths both go through `confirm`, and the daemon has a single SQLite connection. So two wrong codes are serialised, and each one reads the counts committed by the previous one. When the lock is reached, it is decided from the committed count, and `dropAllLocked` runs under `s.mu` before release. A failed write can never unlock: the window is only appended to, and a rolled-back transaction leaves the earlier entries in place. |
| Does the `settingsDB` refactor let other settings writes escape a transaction? | No. `settingsDB` is private and is used only for `approval.wrong_codes`. The other settings writers (`notify`, `presence`, `device`, `device_run`) are unchanged. `Settings.recordWrongCode` (`settings.go:97`) no longer has any caller outside tests (I4). |

## Findings

### F1 · Low · A failed wrong-code transaction leaves the guess uncounted and the approval confirmable

`internal/approval/store.go:312-316` and `:484-516`.

**Scenario.** After this change, the attempts update and the daily-count write either commit together or not at all. `TestBadCodeCountsBothOrNeither` asserts the "neither" case: after a failed write, the approval is still `pending` with `MaxAttempts` left. `confirm` then returns the error and leaves the entry in `s.pending`. Every later wrong guess hits the same failure, so for as long as the failure lasts there is no 3-attempt cap and no daily cap. The approval stays confirmable until its TTL.

Before this change, the attempts update committed on its own. A failing daily-count write therefore still let the 3-attempt cap hold. The new coupling adds a way to lose that cap: an unreadable `approval.wrong_codes` value. `json.Unmarshal` fails at `settings.go:48` on every call, so every wrong code fails. Busy or full-disk errors are no worse than before, because they also broke the attempts update.

**Reach.** Only someone who can damage the local DB can set this up. The guess also still has to arrive through the approval window or the terminal. That is why this is Low.

**Fix.**

- On any `recordBadCode` error, fail closed in memory. Either:
  - drop the entry from `s.pending`, kill its handle and `runOnReject` (the row is fixed by the expiry sweep or `rejectRow`, best effort), or
  - keep an in-memory `attempts` count on `entry` and refuse once it reaches `MaxAttempts`, whatever the DB says.
- Optionally, treat an undecodable wrong-codes value as locked, not as an error.
- Change the test so that after a failed write the code is no longer accepted.

This matches the spirit of F31's OD-F31-1 (b): narrowing actions must not fail open.

### I1 · Info · The fetch `SessionOpen` callbacks report a DB error as `unknown_session`

`internal/daemon/fetch.go:29-38` and `internal/daemon/fetch_client.go:480-489`. Both refuse the fetch, so they fail closed. The peer or user just sees a misleading reason. Out of scope; only worth noting for consistency with R55-081.

### I2 · Info · An undecodable stored result blocks grant delivery for that session

`worksession.GetTx` → `toView` → `decodeStoredResult`. If a stored `result` no longer decodes, every grant mail for that session is retried until the sender's outbox expires. This can happen after `DecodeResult` is tightened in a later version. `sessionOpen` only needs id, peer, role, state and kind. A narrower column read would decouple it from the result.

### I3 · Info · The policy-recheck refusal is not audited

`grant.go:654`: `recheckPolicyTx` returns `bad_state` with no `grant.refused` row. The debate refusal three lines below writes one with `"policy": match.ID`. Consider writing the same row with reason `policy_ended`. F31/D64 will decide whether refusals need a row.

### I4 · Info · Cleanups

- `Settings.recordWrongCode` (`settings.go:97`) has no caller outside tests.
- `trustOfTx` (`request.go:460`) duplicates `peerTrustTx` (`grant.go:956`) and could call it.
- `recheckPolicyTx` re-checks only `until` and relies on "policies are never edited". Re-scanning the row and calling `policyCovers` would hold even if editing is added later, at almost no cost.
- `recordBadCode` writes `decided` from `s.now()`, not from the `now` it is passed. This is harmless.

### I5 · Info · On a loopback relay a removed peer's request is not refused at D5

`request.go:167` returns before the trust read when `!nonLoopbackRelay`. D5 is defined for non-loopback relays only, so this is to spec. A peer removed between the mail layer's paired check and apply is not stopped here on loopback. Whether `peers remove` cleans up such a row is outside F27.

## Overlap with the F31 spec (`Docs/review/84-r55-f31-spec.md`, D64)

- **Rows #4 and #5 (`approval.bad_code`, `approval.reject` attempts):** F31 plans to put `AppendTxSoft` into F27's transaction in `recordBadCode`. Write it after `recordWrongCodeIn` and before `Commit`. Under OD-F31-1 (b), a failure must stay in a savepoint, so an audit failure never rolls back the wrong-code count. Otherwise F1 gets worse: a broken audit chain would also make wrong codes uncounted.
- **F7 in the F31 spec:** `recordBadCode` now reads attempts through the transaction. But `s.kindSubject` (`store.go:~517`) still uses `s.db` after the commit. If F31 moves the audit row into the transaction, `kind` and `subject` must be read through `tx`, or the single connection hangs.
- **Row #7 (`approval.locked`):** the lock is now decided inside `recordBadCode`'s transaction. F31's "in the tx that writes the lock" means that transaction, not `lockAll`, which runs after the commit.
- **F1's fix** touches the same `confirm` error branch F31 edits. One coder should do both.

## Files created

- `Docs/review/89-r55-f27-security.md` (this file). No other files were created or changed.
