# 86: R55-F25 security review

Reviewer: R55-F25sec-Opus · model claude-opus-5-5 · 2026-10-01 · worktree `AgentNet-wt/r55-f25`,
branch `p4/r55-f25`, ticket commit `7a5d686` on `main` `eb283bf`. This is a report only; no code
or test was changed. Read against `Docs/review/55-code-review/99-report.md` (R55-085, 097, 098,
099, 145, 161, 162), `Docs/protocol/device.md` and owner decisions D13 and D24.

## Verdict

**PASS with one Medium finding (an owner decision). Nothing found lets a helper start a new
command after a scope clear, unlink or expiry beyond what was already possible before this
commit.** The fail-closed sweep, the stale-mark clear, the set lock and the WaitGroups are
correct and free of deadlocks as far as reading shows. M1 is a behaviour change the owner must
accept or reshape before merge: the `#!/usr/bin/env` refusal breaks `npm`, `npx`, `yarn` and
`pnpm` on Linux and macOS, including scopes that are already approved. The Low findings are
hardening and doc gaps.

## Checked (no finding)

- **Sweep fails closed** (`internal/daemon/device_run.go:206-227`). Any `take` error that comes
  while the daemon is up stops whichever command is running, through `cur.stop(errRunRevoked)`.
  Every error return in `take` (:356-416) happens before the commit, so a failed check cannot
  half-apply. When the daemon is stopping (`ctx.Err() != nil`), `runCtx` is derived from that
  same ctx, so the run is stopped anyway.
- **The kill reaches the process tree.** Cancelling `runCtx` makes `device.Run` kill the group
  (`internal/device/runner_unix.go`: `Setpgid` plus `kill(-pgid)`; Windows uses a job object).
  This path is unchanged by the commit.
- **Stale running mark** (:365-375). `take(start=true)` is called only from `loop`, which runs
  one job at a time. `cur` is cleared by `execute`'s defer before `loop` takes again, so when
  `!r.executing()` holds there, the run really is over. A stale mark is left only by a failed
  `finish`; it never skips a revocation check, because there is no command left to stop.
- **`finish(context.WithoutCancel(ctx))`** (:617). The finish still runs when the daemon stops
  right after a submitted result. It does not run when the daemon stopped before the result,
  because the `ctx.Err()` return at :588 comes first.
- **Set lock** (`internal/daemon/device_scope.go:76-133, 264-338`). `scope_set` holds the
  controller's lock from `takeLocked` through `put`. Every `take` caller runs **after** its own
  commit and outside any transaction:
  - `device_scope_clear` :366;
  - `device_unlink` (`device.go:565`);
  - `onUnlinked`, which runs in the mail Kind's `After` hook (`device.go:235-239`) after the
    commit.

  The only callback reached while the lock is held is `Reject` → `OnReject` → `drop`, which
  takes `s.mu`, not the set lock. So the set lock and the single SQLite connection never wait
  on each other. The `n` counting releases the entry correctly, and `defer lock()()` releases
  it on every return path.
- **Shutdown ordering.**
  - `kick` and `wait` (:177-199) do their `Add` under `kickMu` before `kicksClosed` is set, so
    `Wait` cannot race an `Add`.
  - `daemon.go:589` stops the loop first, then waits for the sweeps. The store's close was
    deferred earlier, so it runs later.
  - `startMail` stop (`mail.go:209-217`) waits in this order: receiver, then jobs (rotation can
    still fire its hook), then closes pushes, then waits for them. `PushAll` goes through
    `ob.Submit`, a DB write, so it is bounded. No lock is held across a wait.
- **Shebang walk** (`internal/device/perm.go:61-142`).
  - **Re-checked at every start:** `CheckTarget` calls `CheckProgramOwner` (`runner.go:204`).
  - **Check-to-exec gap (TOCTOU):** the script and every interpreter must be changeable only
    by this user or an admin, so nobody else can swap them between the check and the exec.
    That is the same model as for the program.
  - **Symlinked interpreters:** followed and checked hop by hop by `ownerWalk.check`.
  - **Interpreter arguments** (`#!/bin/sh -x`): only `fields[0]` is the interpreter, as the
    kernel does.
  - **Leading blanks and tabs:** skipped, as the kernel does.
  - **Relative interpreter:** refused. The kernel would resolve it against the working
    directory, which is the repo.
  - **Non-regular file:** never opened.
  - **Nesting:** bounded at 4.
  - **Script without `#!`:** `execve` returns `ENOEXEC`, and Go does not fall back to `sh`, so
    the run is "could not start".
  - **Windows:** unaffected; it returns before reading, and only `.exe` or `.com` passes
    `scope.go`.
- **R55-162** is not part of this commit. Current code already carries the role in
  `op.Outcome` (`device.go:186-190, 213`); no `activatedRoles` map remains (grep). This is
  bookkeeping only: make sure the ticket records where 162 was closed.

## Findings

### M1 · Medium · owner decision: the `env` refusal breaks common toolchains, including already-approved scopes
`internal/device/perm.go:138` (via `CheckTarget`, `runner.go:204`).
- **Scenario:** on Debian/Ubuntu (NodeSource or official tarball) and macOS Homebrew, `npm` is
  a symlink to `npm-cli.js`, whose first line is `#!/usr/bin/env node`. The same holds for
  `npx`, `yarn`, `pnpm` and most `node_modules/.bin` tools. So:
  1. A scope with `argv[0]=npm` is now refused at `device_scope_set`.
  2. A scope approved **before** the upgrade fails at its next run as `"<name>: could not
     start"`, with only a daemon log line saying why.
  3. The workaround users will reach for, `argv = ["/usr/bin/node", ".../npm-cli.js",
     "test"]`, passes. But `argv[1]` is never ownership-checked, so the script ends up
     **less** protected than before this commit.
- **Fix direction:** the owner picks one.
  - (a) Accept `env`: resolve the named program through the PATH the run will get
    (`MinimalEnv`). Check the resolved file, and check every PATH directory before it for
    writability, at set time and at every start.
  - (b) Keep the refusal: add a D-entry, a release note, and a run-time error the controller
    sees, not just "could not start".

  Either way, note in §Program ownership that only the `argv[0]` chain is checked.

### L1 · Low · regression: an execute-only program is refused
`internal/device/perm.go:106`.
- **Scenario:** `scriptInterpreter` opens every regular `argv[0]` for reading. A binary with
  mode `0711`/`0111` (hardened systems, some setuid tools) is executable but not readable, so
  the open fails with `EACCES` and `CheckProgramOwner` refuses a program that worked before.
- **Fix direction:** treat a permission error on the open as "not a script". The kernel can
  exec an unreadable binary, while an unreadable script fails in its interpreter anyway.

### L2 · Low · pre-existing, not a regression: a command can start just after its revocation committed
`internal/daemon/device_run.go:549-587, 640`.
- **Scenario:**
  1. `loop`'s `take(true)` commits `Running=J`.
  2. A scope clear, unlink or expiry commits before `execute` sets `r.cur` (`ws.Get` sits in
     between). The kick sweep's `stopRunning` finds `cur == nil` and does nothing.
  3. `execute` then starts `watch` in a goroutine and **concurrently** runs `CheckTarget` and
     `run`.
  4. The process can start before the watch's first sweep kills it, a few milliseconds later.
     That is enough for a short side effect such as a deploy script's first line.

  This commit neither causes nor closes this window; the watch comment at :627 knows about it.
- **Fix direction:** after setting `r.cur` and before `CheckTarget`, run one synchronous
  check. Either call `r.sweep(ctx)` directly or re-run `valid` in a read transaction, and
  return as revoked if it fails. Then start `watch`.

### L3 · Low · doc vs code: the offer watermark now mixes two clocks
`internal/daemon/device.go:532`; `Docs/protocol/device.md:82-85`.
- **Doc gap:** the spec says the watermark is "the `at` of the last `device.unlink`
  **received**". A local unlink now writes this device's own `now`, and nothing in the docs
  says so.
- **Clock skew:** `offer.at` is the peer's clock, so the local watermark only holds up to the
  skew between the two devices.
  - **Peer clock behind by s:** a re-link within s after a local unlink has its fresh offer
    ignored. The link is then active on the peer only, until the user links again.
  - **Peer clock ahead:** an offer made before the unlink but dated after it still passes,
    which is exactly what R55-161 tries to block.
- **Fix direction:** update §Link flow step 3 to cover the local unlink, its local clock and
  the skew caveat. Optionally record `now − IntentTTL`-style slack, or compare against the
  local *receive* time of the offer.

### L4 · Low · doc gap: §Program ownership does not describe the `#!` walk
`Docs/protocol/device.md:140-180`. The commit touches no doc. Missing:
- the interpreter chain (at most 4) is checked like the program, Unix only;
- relative and `env` interpreters are refused (see M1);
- the check reads 256 bytes and splits on blanks and tabs;
- interpreter arguments, `argv[1..]` and `binfmt_misc` handlers are **not** checked.

### L5 · Low: a fail-closed stop looks like a revocation and can hit a run just validated
`internal/daemon/device_run.go:209-215, 221-227`.
- **Scenario 1:** a transient DB error such as `SQLITE_BUSY` past the busy timeout kills a
  legitimate run. The controller gets a `ws.cancel` with no reason, which is identical to a
  revocation, and the audit says `cancelled: true`.
- **Scenario 2:** `sweepMu` does not serialise against `loop`'s `take(true)`. A sweep whose
  `take` failed for run A can call `stopCurrent` after A ended and B started, killing B, which
  `take` had just validated.

Both are safe (they fail closed) but confusing.
- **Fix direction:** audit a distinct cause (for example `check_failed`). In `stopCurrent`,
  stop only the session that was running when the failed `take` began.

### Info
- **I1 · CRLF:** `#!/bin/sh\r\n` is accepted as `/bin/sh` (`strings.Fields`), but the kernel
  execs `/bin/sh\r` and fails. Fail-safe: the run is "could not start".
- **I2 · Long lines:** a `#!` line longer than 256 bytes is truncated (`perm.go:127-130`). On
  Linux the kernel refuses a truncated interpreter (fail-safe). macOS reads 512 bytes, so a
  user-owned script with an interpreter path of 255–510 bytes would be checked as a truncated
  prefix. This needs the user's own unusual script, so it is not a practical bypass.
- **I3 · `env` match:** the check matches only `path.Base(interp) == "env"`.
  `#!/bin/busybox env node` and other PATH-searching launchers pass. This is acceptable
  because the script is the user's own; record it with M1.
- **I4 · R55-145 residual:** `daemon.go:660-664` (no relay) starts `rot.Run` without awaiting
  it, so it can outlive the store at shutdown. It is outside the ticket's two named sites.
- **I5 · Set racing a clear (pre-existing):** a `scope_set` that loses the set lock to a
  clear's `take` leaves its new approval pending.
  - For an unlink, `Precondition` refuses it at approval (`device_scope.go:283`).
  - For a clear, the human approves a new scope explicitly, which is acceptable.
- **I6 · Early-return `finish`:** the revoked path (:592) and the session-closed path (:552)
  still use the cancellable `ctx`. This is harmless: restart recovery reports a revoked run
  through `ws.cancel` and handles an already-reported one through `BadStateError`.

## Tests run

- **`go test ./internal/daemon -run 'Helper|Device|Scope' -count=1`: ok (79.5 s).** Run with
  the HANDOFF §5 subst trick: `Q:` pointed at `%TEMP%\r55f25sec` with a tightened ACL, and
  TEMP, TMP and LOCALAPPDATA pointed at it. The subst was removed afterwards.
- **`go test ./internal/device -count=1`:** everything passes except the known
  `TestCheckProgramOwnerWindows` (C:\ ACL on this PC). `TestParseShebang` passes.
- **Not run here:** the Unix-only `perm_shebang_unix_test.go` and the race job. CI is
  authoritative for both.

## Files created

- `Docs/review/86-r55-f25-security.md` (this file).
- `C:\Users\yazan\AppData\Local\Temp\r55f25sec\` (plus `tmp`, `lad`): the scratch dir for the
  subst. It was left in place and not recursively deleted, per hard rule 14, because Go test
  temp trees may hold links.
