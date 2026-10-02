# Review 94: R55-F17b security review (review 77b residuals: IPC, approval, pairing, panic-safe locks)

Branch `p4/r55-f17b`, HEAD `fdaa111`, diff `git diff main...HEAD` (13 files). Background:
`Docs/review/77-r55-f17-security.md`, `Docs/review/77b-r55-f17-rereview.md`. Reviewer:
R55-F17bsec-Opus. No code was changed.

Tests run on Windows (go1.27.0):

- `go test -count=1 ./internal/ipc ./internal/approval ./internal/peers ./internal/session ./internal/mailbox`
  passes.
- `go test -count=15 -cpu=1,4 -run 'Evict|KeepsRecent|PanicLog' ./internal/ipc` passes (30 runs per test).
- `go test -count=5 -cpu=1,4 -run 'Panic|Refund'` on approval, session and peers passes.

**The race detector was not run.** This machine has no C compiler, so `-race` fails to build. CI
must provide that run. macOS was not run.

## Verdict

**Mergeable after M1 is fixed.** The IPC eviction, the approval `Create` cleanup and the lock
refactors are correct. I found no slot leak, no double count, no cap bypass, no lost or double
unlock, and no behaviour change. The pairing-start refund (77b R3) does what it claims, but it
removes the only rate bound on a start loop that a local agent can drive at no cost. That loop
writes unbounded audit rows and grows `m.sessions` without bound for an hour. This is **M1**, and
the fix is small. Everything else is Low or Info.

| Severity | ID | Summary |
|---|---|---|
| Medium | M1 | Refund makes a used-code or relay-down start loop unbounded: audit rows, plus `m.sessions` growth with an O(n) scan under `m.mu` |
| Low | L1 | `TestServeKeepsRecentConnectionsAtTheCap` depends on 32 round trips finishing within `evictMinIdle` |
| Low | L2 | Eviction gives adversarial clients little protection. It only helps against accidental keep-alive leaks, and the doc should say so |
| Info | I1 | The `maxConns` comment at `ipc.go:36` is stale |
| Info | I2 | The `ipc.md:67` wording "with the count of panics left out" is unclear |
| Info | I3 | A panic inside `window.Start` itself can leave a window without a handle. This predates the branch |

## 1. Eviction (`internal/ipc/ipc.go:182-300`): passed

- **Only connections that have been answered and are idle can be evicted.** `setIdle(false)` runs
  under `t.mu` before `dispatch`, and `setIdle(true)` runs only after `Encode` returns. A connection
  running a handler cannot be chosen, and neither can one writing its answer. That covers `wait`,
  debate wait and approval wait, which are all in-flight. So are silent and first-request
  connections, whose `idle` flag stays false, and `refuseBusy` connections, which never call
  `setIdle`. `evictIdlest` (`ipc.go:266`) also requires `now - idleSince >= 1 s`.
- **An evicted request never runs.** Suppose the client's next line is read just as the connection
  is evicted. Then `setIdle(false)` (`ipc.go:287`) sees `evicted` under the same mutex and returns
  before `dispatch`. If the close comes first, the read fails. Choosing a connection and marking it
  busy are serialised by `t.mu`, so no request is half-admitted. The client gets EOF or a reset and
  never a result, so a non-idempotent call is not executed without the client knowing.
- **Slots are counted correctly.** The evicted goroutine skips `<-free` when `st.evicted` is set
  and hands its token to the new connection. If shutdown begins at the same moment
  (`t.closing`), the new connection is closed and releases that one token through `<-free`. That is
  still one token, released once. One connection can end on its own (a read error) between `serve`
  returning and its `delete`, and still be chosen. It is then marked evicted and does not release,
  and the new connection inherits the token. This is still consistent.
- **The cap is not bypassed.** Running goroutines can briefly reach `maxConns` plus the number of
  evicted connections that have not exited yet. An evicted goroutine never dispatches, though, so at
  most `maxConns` handlers run at once. All of them are covered by `wg`.
- **Clients in this repository are not affected.** `ipc.Call` (`ipc.go:482`) sends one request per
  connection. The two raw `Dial` users, `cmd/agentnet/log.go:307` and
  `internal/daemon/stop.go:42`, close the connection at once. No shell or Python harness keeps a
  connection open.
- **The panic log map is bounded.** `dispatch` refuses unknown methods before `callHandler`, so the
  `s.panics` keys are limited to registered methods.

## 2. Approval `Create` cleanup (`internal/approval/store.go:130-247`): passed

- **No deadlock.** The cleanup defer is registered before the later `release = s.lock(); defer
  release()` pairs, so those run first (LIFO). `lock()`'s `release` is idempotent
  (`store.go:18-27`). A panic under `s.mu`, for example in `ExecContext`, therefore unlocks before
  `dropReserved` locks again.
- **A finalised entry is never removed.** `dropReserved` (`store.go:267`) now deletes only
  `reserved` entries. `settled = true` is set before the final `release()`, right after
  `reserved = false` and the TTL timer. A panic after that point (audit `Append`, `startWatch`,
  `windowState`) leaves the row, the entry and the window together and consistent. The `id` is fresh
  and random, so the cleanup cannot touch another caller's approval. If a lockout or `Close` has
  already removed the entry, the cleanup does nothing.
- **No window is killed wrongly.** `handle` is this call's own window, and it is killed only while
  the call is not settled. Before settlement the user cannot confirm anything: the entry is
  `reserved` with no MAC and no row. So no approval can be granted without a record. The defer
  calls `notifier.Remove` with `WithoutCancel`, which is correct.
- **Each explicit error path** sets `settled` after its own cleanup, so cleanup never runs twice.

## 3. Lock refactors: passed

- `session.go`: `sealCurrent`, `prepareApp`, `withLock` and `send` behave exactly as before. The
  error from `sealLocked` is returned, `ok == false` with a `nil` error matches the old "already
  dialing, return nil" path, and the queue-full and handshake errors are unchanged. `sendMu` and
  `m.mu` are taken in the same order as before (`sendMu`, then `mu`). `send` still runs without
  `m.mu` held.
- `fetch_client.go`: `admit`, `forget` and `lookup` are line-for-line moves under a deferred
  unlock. `wireLocked` still runs under `c.mu`.
- `mailbox.go`: `announce` and `rotate` still run `hook` outside `k.mu`. The run conditions are
  unchanged: `Announcement` runs the hook only on success, and `Rotate` runs it when
  `hook != nil && ann != nil`, as before.
- I found no lost unlock, no double unlock and no new lock ordering.

## M1 (Medium): the refund lets a local agent drive pairing starts without limit

`internal/peers/pairing.go:577` (used code) and `:613` (`failSend`), with `refundStart` at `:476`.

Every refunded start still does three things:

1. It calls `newSession`, which registers a session in `m.sessions`. That session is kept for
   `keepFinished = 1 h` (`pairing.go:77`).
2. It writes a `pair.start` audit row (`:546`, `:574`, `:599`).
3. It writes a `pair.fail` row through `finish` → `end` (`:1143`).

For an issuer, `finish` also calls `SendControl` to cancel the entry.

Before this branch, the bucket bounded all of this at 10 per minute. Now nothing does:

- **The used-code path needs no relay at all.** A local agent marks one code used, or reuses one it
  already redeemed, and then loops `pair <code>`. Each call costs one DB read on the IPC path.
- **Relay-down works the same way** for as long as the relay is down. An agent can also wait for
  the relay to be down.

Effects:

- **Audit rows grow without bound.** That is two hash-chained rows per call, which inflates the DB
  and buries real events.
- **`m.sessions` holds up to an hour of finished sessions.** `newSession` scans all of them under
  `m.mu` on every start (`:502-509`). The cost per start is therefore O(n), the loop is O(n²)
  overall, and the contention on `m.mu` slows every pairing operation.

The 16-pending cap does not help, because these sessions finish at once.

**Fix (recommended):** fail fast before any state exists, and drop the refunds.

1. In `beginRedeemer`, check `used` before `newSession`. Return a typed error (for example
   `ErrCodeUsed`, mapped to the existing `code_used` IPC code) with no session, no token and no
   audit row. The check is local and deterministic, so the relay never needs to see it.
2. Extend `precheck` (`:377`): if the relay is not connected, return
   `relayclient.ErrNotConnected` before `newSession`. Again there is no session, no token and no
   audit row. `peers.Sender` (`pairing.go:130`) has no `Connected()` method yet. Add it, as
   `session.Sender` already has (`session.go:110`, implemented by relayclient at
   `relayclient.go:195`), or use an optional interface assertion.
3. Remove `refundStart` from `failSend`. What remains is a send that fails after the connectivity
   check passed. That window is short, and it is right for those starts to keep their token,
   because their `pair.start` rows exist.

With this fix, audit rows and sessions stay bounded by the bucket (10 per minute), and honest users
still do not lose tokens to the relay being down or to a used code. Update the `pairing.md:395-399`
sentence to match: "is refused before it starts and does not count".

The other options are weaker:

- **Refunding without the audit row** loses the audit trail and still grows `m.sessions`.
- **Capping the audit rows** leaves the memory and CPU growth in place.
- **Refunding only on relay-down** still lets the used-code loop through.

**Test:** in `start_refund_test.go`, after the loops, assert that `len(m.sessions)` and the audit
row count did not grow with the number of failed starts. Change the used-code expectation to an
error, or keep the `code_used` failure shape if the CLI depends on it: `pair_status` then needs a
synthetic, unregistered status.

## L1 (Low): flake risk in `TestServeKeepsRecentConnectionsAtTheCap`

`internal/ipc/evict_test.go:64`. The test expects `busy`. If the 32 non-silent round trips, plus
the scheduling after the first one, take ≥ 1 s, client 32 qualifies for eviction and the 65th
client is served instead. This is unlikely on in-memory pipes, but the race detector on a loaded
Windows or macOS runner slows the code by 5-10×.

The other two eviction tests are robust. The connections they expect to be evicted call
`setIdle(true)` before the sleep, so they always have the lowest `idleSeq`, however slow the rest
of the setup is.

**Fix:** make `evictMinIdle` a package `var` and set it to a minute in this test (restore it in
`t.Cleanup`; the test must not be parallel). Or record the time before the first round trip and
`t.Skip` if more than `evictMinIdle/2` has passed before the 65th client.

## L2 (Low): eviction does not stop a deliberate squatter

A hostile local client keeps its slots in two ways:

- it sends its next request within 1 s of each answer, or
- it parks every connection in a long-poll method (`wait`, debate wait), which can never be
  evicted.

So 77b R1 is solved for accidental keep-alive leaks, but not for an adversary. That matches the
existing "no caller identity" position in `pairing.md`. **Fix:** add one sentence to `ipc.md:57-60`
saying that eviction protects against connections left open by mistake and is not a per-client
quota.

## Info

- **I1:** `ipc.go:35-36` still says a client past `maxConns` is "answered CodeBusy and closed at
  once". It should mention eviction first.
- **I2:** `ipc.md:67`: "with the count of panics left out" reads as "the count is omitted". Write
  "with the number of panics not logged since the last line".
- **I3:** `store.go:150`: if `window.Start` panics after it has spawned the window process,
  `handle` is never assigned, and the cleanup cannot kill that window. No code has been made yet, so
  this is not exploitable. It predates the branch and needs no action on it.

## Fixes applied

Applied by R55-F17bsec-Opus in the worktree, uncommitted.

- **M1 (fixed).** `internal/peers/pairing.go`:
  - `Sender` gains `Connected() bool`, which `*relayclient.Client` already implements.
  - `precheck` returns `relayclient.ErrNotConnected` while the relay is down.
  - `beginRedeemer` returns the new `ErrCodeUsed` after its `CodeUsed` check and before
    `newSession`, so the start has no session, token or audit row, and sends nothing.
  - `RedeemTagged` turns `ErrCodeUsed` into the unchanged failed status (`code_used`) through
    `usedCodeStatus`, with a fresh `pairing_id` that is not registered. The CLI and `team_join`
    contracts stay as they were; team tags act only on completion.
  - `refundStart` is removed, with its calls in `failSend` and on the used-code path. A send that
    fails after precheck keeps its token, because it has a session and both audit rows. No refund
    remains anywhere.
  - Tests:
    - `internal/peers/start_refused_test.go` replaces `start_refund_test.go`.
      `TestPairingStartRefusedBeforeSession` makes 3 × burst starts with the relay down
      (issuer, v2 and v1) and 3 × burst used-code starts. It checks there are 0 sessions, 0 audit
      rows and a full bucket, that nothing was sent, and that the burst is then still available.
    - `TestPairingStartFailedSendKeepsToken` covers the remaining bounded path: 2 rows per start,
      and the bucket ends empty.
    - Test fakes now have `Connected()`. `failSender` takes `down`; by default it is connected, so
      the KDF tests still test failed sends.
    - `cmd/agentnet/pair_test.go` now expects no audit rows for the refused used-code redemption
      and for `TestPairRelayDown`.
  - Docs: `pairing.md` (§ start limits) and `ipc.md` (`pair_status`: the id of a used-code status
    is unknown).
- **L1 (fixed).** `evictMinIdle` is now a package `var` (`ipc.go`).
  `TestServeKeepsRecentConnectionsAtTheCap` sets it to **1 minute** and restores it in
  `t.Cleanup`. The brief said "shorten", but this test needs the threshold *longer* so that a slow
  runner cannot age the answered connections past it. The restore runs after `serveOn`'s cleanup
  has stopped `Serve`, and no ipc test is parallel.
- **L2 (fixed).** `ipc.md` § Framing now says eviction only reclaims connections left open by
  mistake and is not a per-client quota. It also names the two ways a client keeps its slots.
- **I1 (fixed).** The `maxConns` comment (`ipc.go:35-38`) now describes eviction first, then
  `busy`.
- **I2 (fixed).** `ipc.md` § Framing: "carries the number of the method's panics not logged since
  its previous line".
- **I3:** no change, as recommended.

Verification:

- `GOOS=windows|linux|darwin go vet ./internal/... ./cmd/...`: clean.
- `go test -count=1` on ipc, approval, peers, session and mailbox: pass.
- `go test -run 'Pair|Team|Invite|Join'` on `./internal/daemon` and `./cmd/agentnet`: pass.
- `-count=10 -cpu=1,4` for the eviction and panic-log tests, and `-count=5 -cpu=1,4` for the
  pairing-start and KDF tests: pass.
- gofmt is clean on LF copies of the changed files.
- The race detector was not run, because this machine has no C compiler; CI must cover it.
