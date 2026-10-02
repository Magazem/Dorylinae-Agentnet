# 97: R55-F31 security review (audit and status gaps, the post-commit audit rule)

Branch `p4/r55-f31`, commit `bdb1e22`, worktree `AgentNet-wt/r55-f31`. Spec:
[84](84-r55-f31-spec.md) with review 84b and the coder's implementation notes. Owner decisions:
D64 (S actions use `AppendTx`) and D68 (OD-F31-1 (b): removals always commit).

Method: I read every changed production file against the main checkout. No git commands
were used. I compared the files by hand, so changes that main gained after the branch point
(F6b, F26, F30) were left out. I traced every `AppendTx` / `AppendTxSoft` / `RunSoft` call
and its caller, and ran the targeted tests (see "Tests run").

## Verdict

**Approve after one Medium fix (M1).** The class rule holds:
- Every S action writes its row with `AppendTx` in the action's own transaction. That covers
  approve, create, grant create/auto/issue, `pair.complete`, `peer.verify`, `ws.release`,
  `decision.create`/`sign_in`, `team.roster_apply`, and `team.invite_issued` (which has no
  transaction and is compensated by `Cancel`).
- I found no path where an S action commits without its row, or where an S row commits
  without its action.
- Removals always commit (D68) when the row fails with a statement error. When SQLite loses
  the whole transaction, they also commit, but only where `RunSoft` owns the transaction.

M1 is an audit-integrity bug: an S row names the wrong approval. It does not open access.
The Low items can be done in the same PR or as a follow-up.

## Findings

### M1 (Medium): from round 2 on, the `ws.release` row names the wrong approval

`internal/daemon/session.go:600`: `Perform` reads the approval id with
`SELECT id FROM approvals WHERE kind = 'release' AND subject = ? AND state = 'approved'`
and no `ORDER BY`. A session can be released more than once:
1. `ReleaseInTx` moves the session `quarantined → awaiting_result` (`transitions.go:399-410`).
2. `RequestChanges` from `awaiting_result` starts a new round (`transitions.go:217-218`).
3. A new quarantined result needs a new release approval with the same subject (`sid`).

Decided rows are kept for 30 days. So from round 2 on, several `approved` rows match. SQLite
returns the first one it finds, either through the `approvals_state (state, expires)` index or
in rowid order, and that is the oldest approval. The row is S and it is the evidence that
links the release to the human's approval, so it records the wrong approval. The old code
captured `view.ID`. No test covers a second release.

**Fix:** make the read deterministic and specific: `... AND state = 'approved' ORDER BY rowid
DESC LIMIT 1`. This works because `Create` allows one pending approval per subject, so the
newest approved row is the one this `Confirm` just marked. A sturdier option is
`AND decided = ?`, using the `decided` value `Confirm` wrote in this transaction, passed down
through the context or the `Action`. Add a two-round e2e test that asserts
`ws.release.approval` equals the second approval's id.

### L1 (Low): deviation 2 is acceptable for now, but three removals the daemon owns should use `RunSoft` now

Deviation 2: an S- row in a transaction that `RunSoft` does not own fails the operation on a
transaction-killing error (`SQLITE_FULL`/`IOERR`/`NOMEM`), instead of retrying without the
row. Under that error the removal does not happen, so access stays.

That is fail-open, but only in a narrow window. A planted or broken-chain row is a
*statement* error, and the savepoint handles it. Only a lost transaction triggers this
behaviour.

The hook and mail-apply sites are acceptable for now, with a backlog item:
- the session-close `RevokeGrants` hook (`daemon.go:574`);
- the `grant.revoke` and `device.unlink` mail kinds (`grant_kinds.go:198`, `device.go:274`);
- B's `decision.refuse` (`debate/decision.go:152`).

Every one of their callers propagates the error. I checked all five `RevokeGrants` callers and
`chainRemovedTx`, so there is no partial write in an autocommitting dead transaction. A mail
that fails is not acked and is redelivered. An IPC close returns the error to the user.

Three sites own their transaction, and nothing stops them from using `RunSoft` today:
- `device_unlink`: `internal/daemon/device.go:520-551`;
- `device_scope_clear`: `internal/daemon/device_scope.go:350-363`;
- `team.Store.Leave` and `OwnerRemoved`: `internal/team/store.go:478,556`.

**Fix:** wrap these three in `audit.RunSoft` (reset any captured outputs, such as `sub`, in
the closure). This is what the spec says ("What S- really guarantees"), so it needs **no
owner OD**.

For the remaining sites, record a backlog item: let the transaction owner retry when it sees
`*audit.TxLostError`. In practice that is `worksession` close/mirror and the mail receiver's
apply. Fixing those also brings them back to the spec, so it is not an OD either.

### L2 (Low): a re-pair reports `paired_at` as the time of the re-pair

`internal/peers/pairing.go:807` builds the returned `Peer` with `PairedAt: at`, the time of
this pairing. The branch removed `stored()` and `parsePairedAt`, which returned the first
`paired_at` kept in the row (R55-118). The trust read-back was kept (`stored`), but the date
was dropped. So `pair_status` and the `CompletionInfo` peer show a wrong pairing date after a
re-pair. The database is right; the report is not.

**Fix:** in `AddTrustedAudited` (`peers/store.go:134`), read `trust, paired_at` in the
transaction, return both, and parse `paired_at` in `store()` as before.

### L3 (Low): reading the audit detail can fail a removal

`internal/daemon/grant.go:1080`: `auditGrantRevokes` re-reads `peer` for every revoked id.
If that read fails, it returns the error and the session close or peer removal rolls back.
An audit-only read should never stop an S- removal. The read is unlikely to fail, because
the row was just updated in the same transaction.

**Fix:** have `RevokeForSessionTx` and `RevokeForPeerTx` return `(id, peer)` pairs (for a
peer removal the peer is `key` already). Alternatively, on a read error, log it and write
the row with an empty `peer`.

### Info

- **I1. Savepoints (`audit.go:169-195`) are correct for SQLite.**
  - `SAVEPOINT` / `ROLLBACK TO` / `RELEASE` nest inside the deferred transaction.
  - The name is reused only after `RELEASE`.
  - When the transaction is gone, `ROLLBACK TO` fails with "no such savepoint", and that
    becomes `TxLostError`.
  - `noRows` is keyed by `*sql.Tx` and deleted after `fn`, so nested hooks (`OnRemovedTx`)
    write nothing on the retry.

  The `RunSoft` retry is safe:
  - Every closure does database work only and resets its outputs (`mailID`, `written`; the
    `recordBadCode` counts are re-read).
  - The first transaction was fully rolled back, so the retry cannot double the outbox mail
    or the attempt count.
  - The only non-database effect is the idempotent async `runner.kick()`
    (`device.go:308`).
- **I2. Approval lockout holds, and the F17b overlap holds.**
  - `recordBadCode` (`store.go:540-592`) keeps the attempts count, the daily count and the
    three S- rows in one transaction. Every read goes through `tx`.
  - A failure still fails closed in memory (review 89 F1, `store.go:336-354`).
  - `dropAllLocked` still runs under `s.mu` before release.
  - `Create` changed only in `insertApproval` and `windowUnavailable`; the lock and limit
    checks are untouched.
  - Order change: `approval.locked` is now written before the per-entry
    `approval.reject {reason: locked}` rows. This is harmless.
- **I3. Window check (R55-125): no leak, no blocking.**
  - Windows stderr (`window_windows.go:233-244`) is read up to 4 KiB, lower-cased and
    scanned for a boolean. It is never stored or logged. The rest is drained, and
    `stderrDone` is awaited before `cmd.Wait` (as `StderrPipe` requires).
  - `fix` comes only from fixed strings (`checkWindow` on each OS and
    `FixBlockedByPolicy`).
  - `WindowStatus` never waits. At most one refresh runs at a time, with a 1 s context. A
    `Check` that ignored its context could hold at most one goroutine.
  - Detection gap: the CLM text must appear in the first 4 KiB, and the process must exit
    before `Create` stops waiting for the window. Otherwise the error falls back to the
    generic "could not be shown". This is acceptable. As the notes say, it has not been
    tested on a real Constrained Language Mode host.
- **I4. `SetErrorLog`** logs `action` and `err.Error()` only:
  - The errors come from the driver, chain or marshal code: "read head", "chain broken: row
    N", trigger text, json type errors. None carries detail.
  - It is the only `audit_error` emitter left (I grepped for others).
  - It is process-wide: the last daemon to run `daemon.Run` owns it, and it is never reset
    at exit. That is fine in production, where there is one daemon per process. In tests it
    is safe only because no daemon test uses `t.Parallel`. Add a comment there so that
    nobody adds `t.Parallel` to a fault test.
- **I5. Sweep (I2) and deviation 1 are acceptable.**
  - `fetch.go:607` now takes `sig` from `parseToken` (`agentcard.ParseStrict`, exact
    names).
  - The remaining `json.Unmarshal(pt, &r)` (`fetch.go:250`) decodes the Noise-authenticated
    `fetch.req` envelope. `Token` stays `json.RawMessage` and is verified strictly.
    `type`/`req` are checked, and `op`/`path` are validated later.
  - There is no verify-then-decode differential, because no second parser reads these
    bytes. Optional later: `ParseStrict` for uniformity (case-folded and duplicate keys).
- **I6. R55-124 and R55-200 are complete.**
  - Revoke rows are written for session close (both sides, `audit_inventory_e2e_test.go:189-190`)
    and for peer removal, including `grant.policy_remove`.
  - The no-content scan covers every node's log, the relay log, `outbox.error` and the
    webhook queue (body and error), with a negative control. It runs in all three e2e
    no-content tests.
- **I7. Test quality.**
  - `TestAuditClassification` exempts any function *named* `audit`, `record`, `Append`, …
    (`audit_classification_test.go:136-139`), even when it writes a constant action with
    the wrong kind. Key `wrapperDefs` by `(file, func)`.
  - The `actionExtra` `Append` allowances for `peer.remove` and `team.invite_issued` admit
    any new site that uses `Append` for them. Pin them to their file.
  - The sweep test matches text and is easy to evade. It is acceptable as a tripwire.
  - Flake risk is low:
    - the TEMP-trigger cases assert that the trigger exists, and a recycled connection
      fails loudly (S- cases wait for the log line);
    - no daemon test runs in parallel;
    - the fake `Check` locks.

## Tests run (Windows, no `-race`: there is no C compiler on this host)

- `go test ./internal/{audit,approval,debate,team,peers,worksession,capability,notify}`: all
  **ok**.
- `go test ./internal/daemon -run 'Audit|Inventory|Grant|Approval|Status'`: everything passes
  except two tests:
  - `TestAuditInventoryDevices`;
  - `TestPhase2AuditHasNoContent`.

  Both fail the same way, with `device_scope_set: bad_scope: … "C:\\" can be changed by
  Authenticated Users`. That is this host's ACL, not this branch: the same two tests fail
  identically on the `main` checkout. As a result, the new R55-200 scan in the phase-2 test
  and the device inventory rows were not exercised here. CI on all three OSes, with `-race`,
  must show them green before merge.

## Fixes applied

The fixes are in the r55-f31 worktree. I made no git writes and kept the files' CRLF line
endings.

- **M1.**
  - Change (`internal/daemon/session.go:600`): the `ws.release` lookup ends with
    `ORDER BY rowid DESC LIMIT 1`.
  - Why this is the right row: the newest approved release approval of the session is the
    current one. An older pending approval fails the `seq` precondition once a newer one has
    released.
  - New test: `internal/daemon/release_audit_round_test.go`
    (`TestReleaseRowNamesEachRoundsApproval`) runs two rounds. Each `ws.release` row must
    name its own round's approval.
  - I checked the test: it fails with the old query (round 2's row named round 1's approval)
    and passes with the fix.
- **L1.** Three daemon-owned removals now run in `audit.RunSoft`, so a lost transaction is
  retried once without the row:
  - `device_unlink` (`internal/daemon/device.go`);
  - `device_scope_clear` (`internal/daemon/device_scope.go`);
  - team `leave` and `OwnerRemoved` (`internal/team/store.go`). On the retry, the GC's
    `peer.remove` rows become no-ops through `noRows`.

  The hook and mail-apply sites are unchanged. **Backlog:**
  - The S- rows in transactions owned by others do not retry yet. These are the
    session-close `RevokeGrants` hook, the `grant.revoke` and `device.unlink` mail kinds,
    `grant.revoked_in`, B's `decision.refuse` and the roster GC. The owner of each
    transaction (worksession close/mirror, the mail receiver's apply) should retry on
    `*audit.TxLostError`. This follows the spec, so it needs no owner OD.
- **L2.**
  - Change (`internal/peers/store.go`): `AddTrustedAudited` now returns the stored `trust`
    and `paired_at`, read in the transaction.
  - Change (`internal/peers/pairing.go`): `store()` reports the first `paired_at`, using
    `parsePairedAt`, which is restored.
  - New test: `internal/peers/repair_paired_at_test.go`. It re-pairs one hour later, at a
    lower trust, on a fake clock, and expects the stored trust and the first `paired_at`.
    It fails without the fix.
  - Rebase note: main's F30 test `internal/peers/repair_trust_test.go`
    (`TestStoreReportsStoredTrustOnRepair`) asserts the same two facts, so it agrees with
    this change. But it calls `m.store(sc, card, trust, nil)`, and on this branch `m.store`
    takes two more arguments (`id, role`). On rebase it needs `, "", ""` (or a pairing id
    and role), or it can be dropped in favour of the new test.
- **L3.**
  - Change (`internal/daemon/grant.go`): `auditGrantRevokes` takes the peer when it is
    known, which is a peer removal, so nothing is read. For a session close it reads the
    peer. If that read fails, it reports the failure through the new
    `audit.ReportFailure(action, err)` (`internal/audit/audit.go`), which logs the action
    and the error, never the detail. It then writes the row with an empty `peer`. The
    removal is never rolled back.
  - New test: `internal/daemon/grant_revoke_audit_internal_test.go`.
- **Info, done.**
  - I7: `TestAuditClassification` keys `wrapperDefs` by `<dir>:<func>`. The fixed-action
    wrappers are no longer exempt, so their bodies are checked like any call site. The
    `actionExtra` kinds are pinned to one file each (`actionExtraFile`). I checked that
    removing one key makes the test fail.
  - I4: a comment in `audit_fault_test.go` forbids `t.Parallel`.
- **Info, backlog.**
  - I3: the CLM stderr detection (4 KiB, after the process exits).
  - I5: optional `ParseStrict` for the `fetch.req` envelope.
  - I7: the sweep test matches text only.

### Verification

| Check | Result |
|---|---|
| `go build ./...` | ok |
| `go vet ./...` on windows, linux and darwin | clean |
| gofmt on the changed files (after removing CR) | clean |
| `go test` audit, approval, peers, team | all ok |
| `go test ./internal/device` | fails (see below) |
| `go test ./internal/daemon -run 'Audit\|Inventory\|Grant\|Approval\|Session\|Device\|Team\|Pair\|Release'` | fails (see below) |

Every failure has the same host cause, `writable_by_others: "C:\\" can be changed by
Authenticated Users` (this machine's ACL; see "Tests run"):
- in `internal/device`: `TestCheckTarget` and `TestValidateScopeResolvesArgv0`;
- in the daemon run: `TestDeviceScopeSetConcurrentSupersedes`, `TestDeviceScopeSummary`,
  `TestAuditInventoryDevices`, `TestHelperRunSessionOwnedByRunner`,
  `TestPhase2AuditHasNoContent` and the three `TestHelper*Unlink*` tests.

Everything else in the daemon run passes, including:
- `TestAuditClassification` and `TestAuditFaultInjection`;
- the new M1 and L3 tests;
- `TestPeersRemoveRevokesGrants`;
- the `TestDeviceUnlink*` e2e tests, which exercise the new `device_unlink` `RunSoft` path.

`device_scope_clear` needs a scope set first, which this host refuses, so it was not
exercised here. CI on three OSes with `-race` must cover it.
