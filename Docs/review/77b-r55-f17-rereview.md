# Review 77b: R55-F17 security re-review (fixes for review 77)

Branch `p4/r55-f17`, HEAD `306d2aa` (review-77 fix commit, rebased on `main` with F9 and F23).
First review: `Docs/review/77-r55-f17-security.md`. Reviewer: R55-F17rereview-Opus. No production
code was changed. One proof test file was added: `internal/ipc/f17rereview_proof_test.go`.

Targeted tests pass: `go test ./internal/approval ./internal/peers ./internal/ipc` and
`go test ./internal/daemon -run 'Approv|Pair|IPC|Busy|Panic'`. The two new proof tests pass, which
means the residual behaviour they describe is present. The race detector was not run.

## Verdict

**Mergeable.** All three Mediums from review 77 are fixed or reduced to Low, and L1 to L3 are
fixed. Nothing new is Medium or higher. What remains is three Low findings and some Info notes; they
suit follow-up tickets.

| Review 77 | Status |
|---|---|
| M1 IPC lockout | Reduced to **Low (R1)**: clients now fail fast and visibly, but a keep-alive squatter still holds all 64 slots |
| M2 panic wedges lock | **Fixed** for `approval.Store` and for logging; a non-lock leak remains (**R2, Low**); other stores are a follow-up (see "Follow-up scope") |
| M3 KDF burn | **Fixed**; the new start bucket can be drained by a local agent (**R3, Low**, same as the old `maxPending` DoS) |
| L1 secret copy | Fixed: the copy is made only in `startKDFLocked` (`pairing.go:418`) and wiped by the goroutine's `defer clear` on every path |
| L2 unwritten expiry | Fixed: `s.unwritten` hides the approval in List and reports it expired in Show and unknownOrExpired; retried 12 × 5 s; one audit row, because `writeExpired` audits only when `RowsAffected > 0` |
| L3 accept logging | Fixed: logged on the first failure, then at most once a minute, plus a "recovered" line |

## Checks that passed

- **The accept loop never blocks** (`ipc.go:155-196`). It chooses a slot, else a busy slot, else
  a plain close, and it never waits. `free` is released on every path: in the goroutine's defer, or
  on the `closing` branch. A Windows pipe instance is therefore re-armed as soon as `Accept` returns.
  `TestListenerAnswersBusyAtTheCap` shows the busy line reaches a real named-pipe client on Windows.
- **The 5 s first-request deadline** (`ipc.go:229`) does not break any legitimate client. Every
  caller either uses `ipc.Call`, which writes the request right after Dial, or dials and closes at
  once:
  - `cmd/agentnet/log.go:307` `openLogSource` probe;
  - `internal/daemon/stop.go:42` stop poll.

  No long-lived or delayed-first-request client exists. The deadline also bounds a slow 1 MiB first
  line, which is generous even on a slow pipe.
- **The busy path is not an amplifier.** At most 16 goroutines run `refuseBusy`, each for at most
  2 s, with a 4 KiB reader that grows to at most `maxLine`. That is bounded memory and cheap CPU.
- **`approval.Store.lock()`/release** (`store.go:18`). Each `lock()` returns its own closure with
  its own `held` flag. `release = s.lock(); defer release()` binds the new closure at the moment of
  the defer, so early release plus deferred release never double-unlocks. The flag is touched only
  by the goroutine that holds the lock, so there is no data race.
  - I checked every method in the store:
    - with `lock()`: Close, Create (3 sections), dropReserved, confirm, expireNow, OpenWindow,
      reopen, startWatch, onWindowAnswer, List;
    - with `Lock`+`defer`: ResolveTag, takePending, stillPending, windowState, setUnwritten,
      isClosed, isUnwritten.
  - None calls another method that takes `s.mu` while holding it. `unknownOrExpired`, `Show` and
    `windowState` are always reached after `release()`.
  - The comment at `store.go:457` ("it is released and reacquired around the (rare) DB write") is
    stale: `checkExpiryLocked` never unlocks. It is harmless, but see I3.
- **`confirm`'s deferred `tx.Rollback`.** On a panic it runs before `release`, because it was
  deferred later. After `Commit`, and after the explicit rollbacks, it is a no-op
  (`sql.ErrTxDone`). No path leaves the one SQLite connection in a transaction.
  `TestPanicInPerformLeavesStoreUsable` covers this.
- **Create's `runOnReject(swept)`** is now deferred first. It therefore runs last, after every
  release, and it captures `swept` by reference. Same semantics as before.
- **The panic log** (`ipc.go:305-323`) records the method, `panicSummary`, and the stack.
  `panicSummary` gives a `runtime.Error`'s own text (index, nil map, type names) or only the value's
  type. The params are never logged (see I1 for the stack-argument caveat).
- **KDF only at a valid `pair_peer`** (`pairing.go:837`, `:900`). It starts only after
  `verifyPeer` accepts the card and mailbox announcement:
  - F23's strict card parsing runs first, so a bad card derives nothing.
  - On the issuer, it also needs `s.st.Code != ""`.
  - `kd.started` makes it at most once per kd.
  - A random code gets the relay's unknown-lookup error. F9's `HandleError` → `finish` choke point
    ends that session with no derivation.

  To burn a slot, an attacker needs a lookup the relay knows, which means a real code. Over IPC,
  that costs one token of the start bucket per session. Redeeming the daemon's own code is refused
  in `verifyPeer` ("the peer has our own key"). A malicious relay can start at most one derivation
  per pending session, and pending sessions are bounded by the bucket and by `maxPending`.

  Timing still works:
  - The redeemer derives under `RelayWait`.
  - The issuer derives in parallel and checks tag_R under `ConfirmWait` (60 s).
  - Honest derivations can no longer be queued behind attacker derivations started by random codes.
- **`reissue`** now creates an unstarted kd. The superseded kd's `k` is cleared, and a queued
  goroutine can only exist for a started kd. It then drops out through `!live()` (review 77's I1
  item still applies there).
- **The bucket** (`pairing.go:458`, `:503`) is taken under `m.mu`, after the `maxPending` check, for
  all four entry points: `pair --new`, `pair <code>`/`--v1`, `team_invite` and `team_join`. The
  last two go through `pairError` (`daemon/team.go:157`, `:169`), which maps `ErrTooManyStarts` to
  `too_many_pairings` with its own message.
  - A backward clock jump adds no tokens.
  - A forward jump gives at most a full burst, which is harmless.

## Findings

### R1 · Low (residual M1): a keep-alive squatter still holds all 64 slots; past 16 busy answers the CLI sees a bare EOF
- **Where:** `internal/ipc/ipc.go:183-196` (slot choice), `:229` (the deadline covers only the first
  request), `:251` (`refuseBusy`). Proved by `TestF17ReProofKeepaliveHoldsSlots` and
  `TestF17ReProofBusyPathSaturates`.
- **Scenario:**
  1. A same-user local agent that is limited to IPC opens 64 connections and sends `ping` on each
     one at once.
  2. It then sends one request per connection every 29 s. The 5 s deadline applies only before the
     first request, so the slots stay held indefinitely.
  3. The CLI, other agents' approval creation, `approval_*` and `agentnet stop` all get `busy`.
     That is fast and clearly worded, which is a real improvement over the hang.
  4. If the agent also keeps 16 silent connections in the busy path, re-opened every 2 s, further
     clients are closed with no reply. `ipc.Call` then reports
     `ipc: read response: unexpected EOF`, which reads like a daemon crash.
- **Why Low now:**
  - It needs a hostile local agent that is confined to IPC; an unconfined one can kill the daemon
    anyway.
  - The failure is immediate and, in the common case, diagnosable. It needs constant activity.
  - Approval windows answer through their own process handle, not IPC, so decisions already open
    still work.
- **Fix direction:**
  - Main: at the cap, evict the connection that has been idle between requests the longest. This
    defeats the keep-alive pattern cheaply, and a legitimate client never sits idle for long.
  - Or cap requests or lifetime per connection. All current clients use one request per
    connection.
  - In `ipc.Call`, map EOF-before-response to a hint ("daemon closed the connection; it may be
    overloaded").
  - Optionally, skip reading the request in `refuseBusy` unless bytes are already buffered, so each
    busy answer lasts milliseconds rather than up to 2 s.

### R2 · Low (residual M2): a panic between Create's reservation and its finalisation leaks a pending slot; 5 of them disable approvals until restart
- **Where:** `internal/approval/store.go:126` reserves (`reserved: true`); `:210` finalises. The
  sections in between run unlocked: `window.Start`/`Ready`, `newCode`, `notifier.Show`.
- **Scenario:**
  1. A panic in the window runner or the notifier during `Create` is recovered by `callHandler`.
     Unlike before, `s.mu` is now released.
  2. The reserved entry stays in `s.pending`. `sweepExpiredLocked` and `dropAllLocked` skip reserved
     entries, and no timer exists, so nothing ever removes it. Any window that was started stays
     open with no code.
  3. After `MaxPending` = 5 such panics, every `Create` returns `ErrLimit` until the daemon
     restarts. The grant, device and debate-constraint approvals are all refused.
- **Also:** a panic in `Perform` (`confirm`) leaves the entry pending with `timer = nil`
  (`store.go:359`) and `handle = nil`. The lazy sweep still expires it on the next Create or List,
  so this is only delayed; OnReject is late but still runs.
- **Fix direction:** in `Create`, set `finalised := false` and add
  `defer func(){ if !finalised { s.dropReserved(id); kill handle; notifier.Remove } }()`.
  In `confirm`, restore `entry.timer` in a defer when the function exits by panic.

### R3 · Low: the global pairing-start bucket can be drained by a local agent, blocking honest `pair`/`team invite`/`team join`
- **Where:** `internal/peers/pairing.go:458` (`takeStartLocked`), `:503`.
- **Scenario:**
  1. An IPC agent sends `pair_redeem` with a random well-formed v2 code about every 6 s.
  2. Each call takes a token; the relay's unknown-lookup error ends the session. No KDF runs, but
     `pair.start` and `pair.fail` audit rows are written.
  3. The bucket stays empty, so the user's `agentnet pair --new` and team invites and joins get
     `too_many_pairings`.
- **Also:** tokens are consumed by starts that fail locally too: `failSend` at `:539`, `:575` and
  `:591` when the relay is down, and a code that was already used. A user who retries about 10
  times while the relay is down is locked out for up to a minute.
- **Why Low:**
  - The pre-existing `maxPending` = 16 limit already allowed the same DoS: hold 16 `pair --new`
    sessions until the code TTL.
  - There is no caller identity to budget per client.
- **Fix direction:**
  - Refund the token when a start fails before any relay reply: `failSend`, or a code that was
    already used.
  - Optionally, give CLI-started pairings (`pair --new`) a small reserved allowance the redeem path
    cannot use.
  - Document the trade-off in pairing.md.

### I1 · Info: panic logging
- **No rate limit.** A caller that can trigger a panic deterministically gets one full stack per
  call, a few KiB each, which is unbounded log volume. The file logger's rotation is the only cap.
  Consider a per-method limit, for example 1 per minute with a suppressed count.
- **Stack arguments.** `debug.Stack()` prints each frame's argument words in hex. Params are passed
  as slice headers (pointer, length), so their content is not printed. A secret passed *by value*
  as an array would appear, but I found none on an IPC path. Acceptable.

### I2 · Info: Accept never gives up
L3's "give up after N" option was not taken. With the new logging this is fine, because the cause
is now visible.

### I3 · Info: stale comment
The doc comment on `checkExpiryLocked` (`store.go:457-458`) claims s.mu "is released and
reacquired around the DB write". The code does not do this. Correct the comment so no one relies
on it.

## Follow-up scope: manual unlocks elsewhere (M2 across the codebase)

`approval.Store` is now panic-safe with respect to its lock. For other stores, the question is
whether an **IPC handler** can panic while holding a manually unlocked mutex. Relay, timer and
worker goroutines do not recover, so a panic there crashes the daemon, which is the old and safe
behaviour. I found no concrete panic trigger in any of the following, but these handler-reachable
sections run non-trivial code under the lock:

| Where | IPC path | Work under the lock | Impact if wedged |
|---|---|---|---|
| `internal/session/session.go:406-452` `sendApp` (also `SendData` `:258`, `send` `:501`, `Ping` `:337-367`) | `ping`, and any handler that sends app data (debate, fetch, device) | `m.sessions[sid]` deref, `sealLocked` (Noise encrypt), `noise.NewHandshake`/`Write`, `randBytes`; **both `sendMu` and `mu` held** | The session worker blocks on `m.mu`: all inbound envelopes stop and the daemon is silently deaf. The panic is now logged, but messaging is dead until restart |
| `internal/peers/pairing.go` IPC entry points (`beginIssuer`/`beginRedeemer` lock sections, `Get` uses defer, `newSession` uses defer) | `pair_*`, `team_invite`/`join` | Trivial field copies only | Low; convert for hygiene |
| `internal/daemon/fetch_client.go` (`start` `:431`, `status` `:545`, `wait` `:414`) | `fetch_*` | Map and op bookkeeping | Fetch client wedged |
| `internal/daemon/device_scope.go` (`put`/`take`/`drop` `:77-92`), `device_run.go` (`stopRunning`, `stillRunning`) | `device_*` | Small maps | Device flows wedged |
| `internal/capability/fetch.go` (`admit` `:625`, `addServed` `:653`), `internal/mailbox/mailbox.go` (`Announcement` `:189`, `Rotate` `:289`), `internal/mail/keys.go` (`Note`/`Flush`) | Reached only indirectly from handlers | Mixed; `Rotate` does crypto and DB work under the lock | Mail and fetch wedged |

**Recommended ticket:** "R55-F17b: panic-safe locking on IPC-reachable paths".

1. Convert the rows above to `Lock()`+`defer Unlock()`, or to the `lock()`/release helper from
   `approval`. Start with `session.Manager.sendApp`/`SendData`/`send`/`Ping`, then
   `fetch_client`, `device_*` and `mailbox.Rotate`. Leave relay-side packages (`internal/relay/*`)
   out; they are not in the daemon.
2. Add a per-package test in the style of `TestPanicInPerformLeavesStoreUsable` for session.
3. Consider making `callHandler` treat a second panic of the same method within N minutes as fatal
   (log, then `os.Exit(2)`), so an unknown wedge becomes a clean crash and restart.

Estimated at about 60-90 mechanical edit sites; no protocol change.

## Files created
- `Docs/review/77b-r55-f17-rereview.md` (this report)
- `internal/ipc/f17rereview_proof_test.go` (proof tests for R1; delete them or turn them into
  regression tests once R1 is fixed)
