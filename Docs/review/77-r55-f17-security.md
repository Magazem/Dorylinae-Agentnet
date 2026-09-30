# Review 77: R55-F17 security review (daemon availability vs a local agent)

Branch `p4/r55-f17`, commit `bc95b13` (on `main` `fa691d6`). Scope: review 55 R55-030, 031, 083,
143 and 146 (`Docs/review/55-code-review/99-report.md`, ticket R55-F17). Specs: pairing.md §Local
timers, ipc.md §Framing. Reviewer: R55-F17sec-Opus. No production code was changed. One test file
was added to prove M1 and M2: `internal/ipc/f17sec_proof_test.go`.

Targeted tests pass: `go test ./internal/approval ./internal/peers ./internal/ipc` and
`./internal/daemon -run 'Approv|Pair|IPC'`. The two proof tests pass, which means the behaviour
they describe is present. The race detector was not run; it runs in CI only.

## Verdict

**Mergeable, with follow-ups.** None of the findings is High.

- **R55-030:** the approval deadlock is really gone, and no new lock-order inversion was
  introduced.
- **R55-146:** OnReject on an UPDATE failure is safe. It cannot approve anything and cannot fire
  twice.
- **R55-031:** the KDF bound caps memory at 4 × 64 MiB. It does not leak slots or goroutines.

The new IPC limits bring two Medium trade-offs:

- **M1:** a local client can now lock the CLI out with 64 connections.
- **M2:** a recovered panic can leave the daemon silently wedged instead of crashed.

A third Medium, M3, is a residual in R55-031: a caller that submits random codes over IPC can keep
all 4 KDF slots busy.

## Checks that passed

- **Approval lock order.** After the fix, every path takes the locks in the order
  `s.mu` → the single SQLite connection:
  - `Create`, `confirm` and `sweepExpiredLocked` (which calls `notifier.Remove` under `s.mu`).
  - `List` closes its cursor before it calls `windowState`
    (`internal/approval/store.go:978-991`).
  - `RejectSubjects` closes its rows before it takes `s.mu` (`store.go:713-727`).
  - `Show` uses `QueryRow`, which is closed before `windowState`.
  - `ExpireStale` never takes `s.mu`.

  Handlers that call `Create` (`grant.go:775`, `device.go:468`, `device_scope.go:284`,
  `debate_constrain.go:119`) do so outside any open transaction. `Perform` runs only on `confirm`'s
  own tx.

  `confirm` with a Background ctx (from `onWindowAnswer` and `expireNow`) can still wait forever on
  the connection. That happens only if some holder of the connection waits on `s.mu`, and I found
  no such path.
- **expireNow when the UPDATE fails** (`store.go:579-588`).
  - The entry is removed under `s.mu` first. After that, `confirm`, `Reject`, `RejectSubjects` and
    sweeps all see "not in memory", so the approval cannot be approved and only this path runs
    OnReject.
  - At the next start, `ExpireStale` sets the row to `expired` and audits it (`approval.go:336-366`).
  - A stray old timer after a `Perform` error takes the same path, so nothing changes there.
- **KDF.**
  - At most `maxDerivations` = 4 derivations run at once (`pairing.go:412`).
  - Waiting goroutines are bounded by 16 sessions × (1 + 3 reissues) = 64. Each one leaves on
    either a free slot or `s.done`.
  - The slot is released on every path: `!live()` → release, and after `deriveKFunc` → release.
  - The code that superseded `s.kd` is race-safe:
    - `kd.k` is written and read under `m.mu`.
    - `close(kd.ready)` runs after the unlock, so the write happens before any reader sees the
      channel closed.
    - An old derivation that finishes after `reissue` hits `s.kd != kd` and clears its K.
    - Every waiter also selects on `s.done`, so a `kd.ready` that is never closed (the send failed)
      does not hang anyone.

  Honest pairing is **not starved by the KDF queue.** The channel's waiting senders are served
  FIFO, and queued derivations of ended sessions drop out. The worst-case delay is about 4 rounds
  of Argon2id (t=3, 64 MiB, 1 thread), well inside `RelayWait` (30 s), which `onPeerRedeemer`
  arms. Blocking honest pairing through `maxPending` = 16 is still possible, but that predates this
  change and is out of scope.
- **IPC transactions on panic.** Every handler tx on the IPC path uses a deferred Rollback:
  `grant.go:608` and `:842`, `device.go:500`, and the other 43 of the 48 `BeginTx` sites. A
  recovered panic therefore does not hold the connection with an open tx.
- **Accept retry does not spin.**
  - The delay grows 5 ms → 1 s and resets after a success (`ipc.go:142`).
  - Cancelling ctx exits the loop.
  - go-winio v0.6.2 maps a closed listener to `net.ErrClosed` (`pipe.go:107`).
  - The Windows close race that returns `ERROR_OPERATION_ABORTED` happens only during a ctx
    cancel, so Serve returns.

## Findings

### M1 · Medium: 64 connections lock the CLI and the other agents out of IPC
- **Where:** `internal/ipc/ipc.go:126-133`, with `idleTimeout` at `:29` and `:183`. Proved by
  `internal/ipc/f17sec_proof_test.go` `TestF17SecProofSilentConnsLockOut`.
- **Scenario:**
  1. A same-user local agent opens 64 connections and sends nothing. That holds every slot for
     30 s.
  2. If it sends one cheap request (for example `pair_status` with any id) on each connection every
     29 s, it holds the slots indefinitely.
  3. While the slots are held:
     - On Windows, Serve is not in `Accept`, so no pipe instance is listening. `Dial` gets
       PIPE_BUSY and gives up after `dialTimeout` = 1 s. `agentnet` then reports the daemon as
       unreachable, and `agentnet stop` fails too.
     - On Unix, `connect` lands in the backlog and the CLI hangs until its own deadline.
  4. Approval creation by other agents (grant or device requests) is refused. `approval_list`,
     `approval_open` and `approval_reject` from the CLI are refused.
- **Before the fix:** there was no cap. Exhausting file descriptors took about ulimit (1024 or
  more) connections, and then the daemon died. Now 64 connections suffice, and the lockout is quiet
  and lasts as long as the agent wants.
- **Caveat:** this matters only for agents limited to IPC. An unconfined same-user process can kill
  the daemon anyway.
- **Fix direction (pick one or more):**
  - When at the cap, keep accepting, then answer one line `{"error":{"code":"busy"}}` and close.
    This keeps a Windows pipe instance listening and makes the CLI fail fast with a clear message.
  - Evict the connection that has been idle longest when a new client arrives at the cap.
  - Give a connection that has not yet sent a request a short first-request timeout (2–5 s).
  - Cap how long one connection may live, or how many requests it may carry.
  - Raise the cap (256), still well below RLIMIT_NOFILE.

### M2 · Medium: a recovered panic can leave a lock held, silently wedging the daemon
- **Where:** `internal/ipc/ipc.go:237-244`, where `callHandler` recovers the panic without logging
  it. Proved by `TestF17SecProofPanicLeavesLockHeld`.
- **Scenario:**
  1. `approval.Store` unlocks `s.mu` explicitly (not with `defer`) at about 30 sites.
     `sweepExpiredLocked` runs `handle.Kill()` and `notifier.Remove()` while holding `s.mu`.
     `peers.Manager` works the same way.
  2. A panic in a region that holds the lock (a nil map or slice error in window or notifier code)
     is now recovered. The mutex stays locked.
  3. Every later call to `approval_list`, `approval_*`, or `Create` on the grant or device paths
     blocks forever inside `dispatch`. The read deadline does not cover a handler that is still
     running, so each blocked call holds a connection slot.
  4. After 64 such calls, IPC is completely dead (see M1). Because the panic value is thrown away
     and nothing is logged, the cause cannot be diagnosed.
- **Before the fix:** the daemon crashed and a supervisor or the user restarted it cleanly.
- **Fix direction:**
  - Log the panic value and `debug.Stack()` at Error level, with the method name and a per-method
    rate limit if log volume is the concern. "No log" is the wrong trade here.
  - Consider making the daemon exit (for example by re-panicking) after writing the log for
    packages known to unlock by hand, or at least for `approval`.
  - Longer term: give each handler call a deadline so that a wedged handler releases its slot.

### M3 · Medium: random-code `pair_redeem` calls keep all 4 KDF slots busy (residual R55-031)
- **Where:** `internal/peers/pairing.go:529-536` (`beginRedeemer` starts the KDF as soon as
  `pair_redeem` has been sent) and `:405-431`.
- **Scenario:**
  1. A local agent sends `pair_redeem` over IPC with random, well-formed v2 codes, in parallel
     across several connections.
  2. Each call sends to the relay and immediately takes a free slot.
  3. The relay answers "unknown lookup" within one round trip, and the session ends. Queued
     derivations are dropped, but the ones that already started run to completion.
- **Effect:**
  - The agent can keep 4 cores and 256 MiB busy indefinitely.
  - Each call writes a `pair.start` and a `pair.fail` audit row.
  - Honest pairing still progresses (see "Checks that passed"), so this costs CPU and memory; it
    does not deny pairing.
- **Fix direction:**
  - Start the derivation only once `pair_peer` arrives, on both sides: K is not needed before a
    peer tag exists. A lookup that does not exist then costs no Argon2id at all. The extra latency,
    about one derivation, fits in `RelayWait`, which `onPeerRedeemer` already arms.
  - Add a daemon-side rate limit on pairing starts per minute, like the approvals' `MaxPerHour`.

### L1 · Low: the secret copy is never wiped when the send fails
- **Where:** `internal/peers/pairing.go:399` copies the secret. When the send fails, `startKDF` is
  never called: `beginIssuer` at `:496-499`, `beginRedeemer` at `:533-536`, and `reissue` at
  `:642-646`.
- **Scenario:** the `pair_new` or `pair_redeem` send fails, for example because the relay dropped
  mid-send. The copy made by `prepareKDFLocked` stays reachable from the unused closure until
  garbage collection, and it is never cleared. This contradicts the doc comment on
  `prepareKDFLocked`, which says the copy is "wiped once Argon2id is done or skipped". `end()`
  wipes `s.secret`, but not this copy.
- **Fix direction:** copy the secret inside `startKDF` under `m.mu`, and skip if the session ended.
  Alternatively, return a `discard` function and call it on the send-failure paths.

### L2 · Low: the approval row stays pending in the DB for the rest of the run after the UPDATE fails
- **Where:** `internal/approval/store.go:579-588`.
- **Scenario:** the `expired` UPDATE fails, for example with SQLITE_BUSY while the CLI's read-only
  store holds a lock. For the rest of the run:
  - `approval_list` and `Show` report the approval as `pending`, with the window `closed`.
  - `approval_open` and `approval_reject` return `unknown`, because `unknownOrExpired` sees
    `pending`.
  - No `approval.reject`/`expired` audit row exists until `ExpireStale` runs at the next start.
  - This is safe, since nothing can approve it, but the approval shows as pending when it is not,
    and the audit trail has a gap.
- **Fix direction:** retry the UPDATE once after a short delay. Or keep a small in-memory set of
  ids whose write failed, filter them out of `List`, report them as expired, and audit the expiry
  anyway (the detail can note that the write failed).

### L3 · Low: when Accept keeps failing, Serve retries forever and logs nothing
- **Where:** `internal/ipc/ipc.go:136-148`.
- **Scenario:** the listener breaks in a way that is not `ErrClosed` (for example a Windows pipe
  create failing every time). Serve retries once per second forever. The daemon looks alive, but
  IPC is dead, and there is no log line to show why.
- **Fix direction:** log the first failure and every Nth consecutive one. Consider giving up
  (returning the error) after, say, 60 s of consecutive failures.

### I1 · Info
- **Superseded derivations still queue for a slot.** After a `reissue`, the derivation that was
  replaced keeps waiting for a slot until it gets one, and only then sees `!live()`. With 16
  sessions × 3 reissues, up to 48 of these can wait ahead of an honest derivation, each for one
  brief slot hand-off. A per-kd cancel channel, closed when the kd is replaced, would drop them at
  once.
- **Close does not end pending sessions.** `Manager.Close` stops the timers only, so sessions stay
  pending. Derivations queued at shutdown can still run until the process exits.
- **Unreachable branch in expireNow.** When `RowsAffected` = 0 (`store.go:590`), expireNow returns
  without running OnReject. I found no reachable path to that branch, because every other
  state-changing path removes the in-memory entry first. It predates this change.

## Files created
- `Docs/review/77-r55-f17-security.md` (this report)
- `internal/ipc/f17sec_proof_test.go` (proof tests for M1 and M2; delete them or turn them into
  regression tests once fixed)
