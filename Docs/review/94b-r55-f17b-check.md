# Review 94b: independent re-check of the R55-F17b fix round

Branch `p4/r55-f17b`, HEAD `0749044`. The fix diff is `git diff f05bc60..HEAD` and the whole ticket
is `git diff main...HEAD`. Background: `Docs/review/94-r55-f17b-security.md`. Reviewer:
R55-F17bchk-Opus. No code was changed. Only read-only git was used.

## Verdict

**Approve.** M1 is closed, every remaining start-failure path is bounded by the bucket, and the
`evictMinIdle` override is race-free and flake-safe. I found no regression in the original F17b
changes. There is one Low documentation finding (D1), which can be fixed in this PR or later. It is
not a blocker.

| Severity | ID | Summary |
|---|---|---|
| Low | D1 | `Docs/cli/pair.md:109` says every `pairing_id` can be polled; a `code_used` refusal's id cannot |
| Info | N1 | A `team_invite` refused because the relay is down still spends the approval id. This predates the branch and is unchanged |
| Info | N2 | The race detector was not run: there is no C compiler here (`CGO_ENABLED=0`, no gcc). CI must cover it |

## Tests run (Windows, local)

- `go vet` on `./internal/peers ./internal/ipc ./internal/daemon ./cmd/agentnet`: clean.
- `go test -count=1` on `./internal/peers ./internal/ipc ./internal/approval ./internal/session
  ./internal/mailbox`: pass.
- `go test -count=1 -run Pair ./cmd/agentnet` (5 tests) and `-run 'Pair|Team' ./internal/daemon`:
  pass.
- `go test -count=10 -cpu=1,4 -run 'Evict|KeepsRecent|StartRefused|FailedSendKeeps|RateLimited'`
  on ipc and peers: pass (20 runs of each test).

## 1. M1 is closed

I checked each start path in `internal/peers/pairing.go`:

- **`beginIssuer` (`:540`), `beginRedeemer` (`:573`), `beginRedeemerV1` (`:608`)** all call
  `precheck` (`:403`) first. With the relay down it returns `relayclient.ErrNotConnected` before
  `ownMaterial`, `newSession`, `takeStartLocked` or `m.audit`. So there is no session, no token,
  no audit row and no send.
- **Used code.** `beginRedeemer` returns `ErrCodeUsed` (`:581-583`) after `Store.CodeUsed` and
  before `newSession`. `CodeUsed` (`store.go:203`) prunes, then runs a `SELECT`. It does not grow
  any state, and it behaves exactly as it did before the branch.
- **`StartTagged` / `team_invite` (`daemon/team.go:308`)** and **`RedeemTagged` / `team_join`
  (`daemon/team.go:324`)** go through the same `begin*` functions, so there is no separate path.
  `pair_new`, `pair_redeem` and `team_*` all reach the Manager through these functions.
- **Remaining free refusals** are `ErrBadCode`, `ErrNeedV1`, `ErrNoRelay`, relay-down, a used code,
  `ownMaterial` errors and `ErrTooMany(Starts)`. None of them writes an audit row or registers a
  session, and none sends anything. Before the branch, refusals of this kind were already free and
  unbounded. That is acceptable because they leave no state behind.
- **No refund remains.** `grep refundStart` finds nothing. `failSend` (`:628`) now only finishes the
  session, after a token was taken and `pair.start` was written. That is 2 rows per token, so the
  bucket bounds it (10 per minute).
- **Behaviour change for honest users: none.** Before the branch, `relayclient.write`
  (`relayclient.go:228-233`) already returned `ErrNotConnected` at once when `conn == nil`, and
  `Connected()` (`:195`) reads the same `c.conn` under `c.mu`. Users get the same
  `relay_unavailable` IPC error (`daemon/pairing.go:87`), only without the audit rows.
- **A concurrent used-code race** is two redemptions of one fresh code that both pass `CodeUsed`.
  Both take a token and a session, and the later `MarkCodeUsed` (`:953`) fails the second one. The
  bucket bounds this, and the case predates the branch.

**The `RedeemTagged` fresh `pairing_id` (`usedCodeStatus`, `:369`)**:

- **CLI `pair <code>`** (`cmd/agentnet/pair.go:128-140`) prints a failed status and exits
  `exitError`. It never polls a failed id.
- **CLI `team join`** (`cmd/agentnet/team.go:395-414`) behaves the same way. It prints the `--status`
  hint only for `pending`.
- **`JoinTag.Completed`** (`internal/team/invite.go:52`) acts only on `StateComplete`. Before the
  branch it received a failed completion and returned. Now it is not called at all, so the effect
  is the same.
- **IPC docs**: `ipc.md` records the unknown id (§ `pair_status`). The CLI docs do not (see D1).
- **Probing.** The check is a local hash lookup (`usedCodeHash`) in this daemon's own
  `pair_used_codes`. It reveals only whether this daemon redeemed the code in the last 24 h, which
  is the same `code_used` answer the caller got before the branch. Nothing is sent to the relay, so
  the check cannot probe the relay's live codes. A random `pairing_id` given to `pair_status`
  returns `unknown_pairing`, as any made-up id does.

## 2. Every remaining failure path is bounded

Once `newSession` has taken a token, every failure ends in `finish`: a send failure (`failSend`),
a relay error, a timeout, a KDF timeout, a `MarkCodeUsed` failure or replay, a confirm failure, or
`Close`. Each such start costs exactly one token, and its `pair.start` and `pair.fail` rows are
bounded by the bucket. `m.sessions` growth is bounded by the bucket times `keepFinished`, which is
at most about 10 + 600 entries per hour. `TestPairingStartFailedSendKeepsToken` asserts 2 rows per
start and an empty bucket.

## 3. `evictMinIdle` as a package variable

- **It is restored correctly.** `t.Cleanup` at `evict_test.go:69` is registered before `serveOn`'s
  cleanup, so it runs after it (LIFO). `serveOn` waits for `Serve` to return, and `Serve` runs
  `wg.Wait()` (`ipc.go:167`) over every connection goroutine. `evictIdlest`, the only reader
  (`ipc.go:275`), runs on the accept goroutine. So no reader is left when the variable is
  restored.
- **No parallelism race.** The `internal/ipc` package has no `t.Parallel`, and the earlier tests'
  servers were all stopped by their own cleanups. The race detector would therefore see a
  happens-before edge between the write and every read.
- **The tests are flake-safe.**
  - `KeepsRecent` now needs more than 60 s of setup to fail.
  - In `EvictsIdlestFirst` and `ListenerEvictsIdleSquatter`, the connections to be evicted go idle
    before a sleep of `evictMinIdle + 100–200 ms`. Eviction picks the lowest `idleSeq`, so the
    target is still right if a slow runner ages other connections.
  - One theoretical residual remains in `KeepsRecent`: the 32 silent connections have the 5 s
    `firstRequestTimeout`. If 32 in-memory round trips took more than 5 s, slots would free up and
    the test would fail. That is far beyond a race or macOS slowdown, so no action is needed.

## 4. Docs match the code

- **`pairing.md:395-401`** matches the code. The relay-down refusal maps to `relay_unavailable`,
  and the used-code refusal is a `code_used` failed status. Neither counts, creates a pairing or is
  audited. A send that fails after the check counts and writes 2 rows.
- **`pairing.md:342-343`** ("fail with `code_used` and send nothing") still holds.
- **`ipc.md`**: the eviction paragraph, the panic-log wording and the `pair_status` note all match
  `ipc.go` and `pairing.go`.

### D1 (Low)

- **`Docs/cli/pair.md:109`** says `pairing_id` is a "Stable id to poll with `--status`".
  (`Docs/cli/team.md:33` speaks only of a `pending` id, so it is correct.)
- **The mismatch:** the id of a `code_used` refusal is not registered, so `pair --status <id>`
  returns `unknown_pairing`. The CLI never prints that id as a polling hint, so no user flow breaks.
  Only the reference text is wrong.
- **Fix:** change `pair.md:109` to "Stable id to poll with `--status` (not for a `code_used`
  failure, which is refused before a pairing exists)".
- **Fixed** in the worktree (uncommitted). `Docs/cli/pair.md:109` now says that a `code_used`
  failure's id cannot be polled and that `--status` answers `unknown_pairing`.

## 5. Regressions from the original F17b changes: none found

- **The approval `Create` cleanup** (`approval/store.go:129-247`): every explicit return sets
  `settled`. The success path sets it before the final `release()`. `dropReserved` deletes only
  `reserved` entries. The defer runs after the inner `release` defers, and `release` is
  idempotent.
- **IPC eviction**: `setIdle` and `evictIdlest` are serialised by `t.mu`. An evicted connection
  never dispatches and never releases its slot, and the new connection inherits it. On the
  `closing` path the inherited token is released once.
- **The lock refactors** (session, mailbox, fetch_client) are covered by passing package tests.
  Review 94's line-by-line reading also stands. I spot-read session and approval and found no lost
  or double unlock.

### N1 (Info)

`daemon/team.go:303-309`: `gates.take` spends the invite approval before `StartTagged`. A relay-down
refusal therefore still costs the user a new approval. This is the same as before the branch,
because a failed send did the same, and the comment there documents it. No action is needed.
