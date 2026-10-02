# Review 86b: R55-F25 focused re-check (D71 env shebang, L1, L2, L5)

Branch `p4/r55-f25`, worktree `AgentNet-wt/r55-f25`. Read-only code check. No new tests were
added and no local test runs were made. CI on draft PR #45 is green on all three OSes, plus
race and lint.

## Verdict

**Changes needed. All findings are small (2 Low-Medium, 3 Low).** The D71 design holds:

- The check and the exec get the same env slice.
- PATH cannot be injected by a peer or agent.
- Honest setups pass.
- L2 and L5 are correct.

Where the check is weaker, the resolution follows Go's view of the PATH search and the shebang
line, not what the kernel and `env`'s `execvp` actually do. Four small mismatches leave a gap. A
fifth item is the L1 trade-off, which the owner should accept knowingly.

## Checked (no finding)

- **Same env at check and at exec.** `device_run.go:590-606` builds `spec.Env` once.
  `CheckTarget(spec.Path, spec.Dir, spec.Env)` and `run(runCtx, spec)` both use that slice.
  The set-time check (`scope.go:294`) uses `MinimalEnv(env, nil)` from the same daemon
  environment.
- **No PATH injection.** A scope's `env` holds only names (`scope.go:277-290`). The values
  come from the daemon's own environment (`MinimalEnv`, `runner.go:42-59`). `PATH` is first in
  `BaseEnv`, so a scope that lists `PATH` is deduplicated away. `DORYLINAE_*` is refused. The
  scope itself is set only by the obeying device's human approval. A peer or agent controls
  neither the names nor the values. `envValue` taking the last value matches `os/exec`, and
  `MinimalEnv` never emits a name twice anyway.
- **Empty or relative PATH entries.** `filepath.SplitList` keeps `""`. `IsAbs("")` is false,
  so an empty entry (meaning cwd) or a relative entry searched before X is refused (`perm.go:142`).
  An empty `PATH=""` makes `env` search cwd, while the check refuses with "not found": it fails
  closed. An unset PATH makes `env` use its default list, while the check refuses with "no PATH":
  also fails closed.
- **X containing `/`.** An absolute X is checked as it is. A relative X with a slash is refused
  (`perm.go:249`). Both match `execvp`, which skips the PATH search when the name contains a
  slash.
- **Windows** returns before any shebang parsing (`perm.go:84`).
- **Honest setups.** `classifyUnix` (`perm_unix.go:45`) accepts owner = root or the daemon's
  user, with no other-write and no untrusted group-write. So `~/.local/bin`, user-owned
  Homebrew (`/opt/homebrew`, Intel `/usr/local/bin`), root-owned `/usr/local/bin` and nvm
  directories all pass, consistent with D24 L11. A dir searched before X is refused only when
  it is owned by another non-root user or writable by others. Missing PATH dirs are skipped.
- **L2** (`device_run.go:572-579`). `cur` is set before the synchronous `sweep`. The sweep's
  `take(false)` validates `st.Running`, which is this job, and stops it through `cur` if no
  longer allowed. `run` starts only when `runCtx.Err() == nil`. A revocation committing after
  that sweep goes through `kick → sweep → stopSession` against `cur`. No window is left.
- **L5** `stop_reason` (`device_run.go:611-624`) is correct.
- **TOCTOU in general.** Between check and exec, every checked file and dir can be changed
  only by the user or root, so the re-check at each start is sufficient. The exception is
  dirs that are not checked: see M1 and L1 below.

## Findings

### M1 · Low-Medium · the PATH search stops where `execvp` would continue
`internal/device/perm.go:141-155`

- **Scenario:** `lookPath` picks the first regular file with any `x` bit and ignores later
  PATH entries. `env` (glibc, BSD and macOS `execvp`) tries `execve` on that file and, on
  `EACCES`, `ENOENT` or `ENOTDIR`, **moves on to the next PATH entry**. Two cases trigger it:
  - The first file is executable by the owner only and owned by root, or sits on a `noexec`
    mount. Then `execve` returns `EACCES`.
  - The first file is an ELF whose loader is missing. Then `execve` returns `ENOENT`.

  The check passes on that file, and `env` then runs X from a later, unchecked dir. Example:
  `PATH=/usr/local/bin:/srv/shared/bin`, with `/usr/local/bin/node` owned by root, mode 0744.
  An attacker's `/srv/shared/bin/node` is what runs.
- **Fix direction:**
  - Use `unix.Access(p, unix.X_OK)` (or `faccessat` with `AT_EACCESS`) for "executable".
    It also reports `noexec` mounts as `EACCES` on Linux.
  - Keep checking PATH dirs after the match too. The simplest form: every existing PATH entry
    must pass the dir check whenever the script uses `env`. Honest PATH dirs pass, as
    established above.
  - Add a Unix test using mode 0744 under a non-root owner, or a 0600 file in the first dir.

### M2 · Low-Medium · the shebang is split with Go's rules, not the kernel's
`internal/device/perm.go:223-241`

- **Scenario:**
  - On Linux, the kernel passes everything after the interpreter as **one** argument. So
    `#!/usr/bin/env node --flag` makes `env` call `execvp("node --flag")`. The check, however,
    looks up `node`. A user's script with this (broken) line normally fails. But if
    `/srv/shared/bin/node --flag` exists in a writable PATH dir (later than, or with no,
    `node` match), `env` runs the attacker's file while the check passed on `/usr/bin/node`.
    macOS splits on whitespace instead, so the check is right there.
  - `strings.Fields` also splits on `\r`, `\v`, `\f`, NBSP and U+0085. The kernel splits on
    space and tab only. A CRLF line `#!/usr/bin/python3\r` is checked as `/usr/bin/python3`,
    but the kernel opens `/usr/bin/python3\r`. That fails closed today, but it is the same
    class of mismatch.
- **Fix direction:**
  - Split on `' '` and `'\t'` only, and strip one trailing `\r` the way the kernel would not
    (i.e. refuse a `\r` in the line).
  - For `env` without `-S`, on Linux (`runtime.GOOS == "linux"`), require exactly one word
    after `env`, or treat the whole trimmed rest as the name. Otherwise refuse.

### L1 · Low · a PATH dir missing at check time can appear before exec
`internal/device/perm.go:150`

- **Scenario:** a PATH entry before X that does not exist is skipped. If its parent is writable
  by others, for example `PATH=/tmp/tools/bin:/usr/bin` or a stale entry under a shared dir,
  an attacker can create `bin/X` between `CheckTarget` and `Run` and win the race. If the
  attacker creates it before the check, it is refused, so only the race window is open.
- **Fix direction:** for a missing entry, check its nearest existing ancestor (`check(anc, 1)`).
  Alternatively, refuse a missing entry whose nearest existing ancestor is not trusted.

### L2 · Low · L1 fix: the interpreter of an unreadable script is no longer checked
`internal/device/perm.go:194-196`

- **Scenario:** take a script owned by root, mode 0711 (unreadable by the daemon user), whose
  first line is `#!/srv/shared/py`. The kernel still reads the `#!` line and **execs** the
  interpreter, with the script path as its argument. The interpreter then fails to open the
  script, but by then the attacker's binary is already running as the daemon user. Before L1
  this case was refused; now it passes. "An unreadable script fails in its interpreter" is
  true, but only after the interpreter has run.
- **Fix direction:** accept `EACCES` only for files that are not scripts in practice. On
  Linux, refuse `EACCES` unless the file is setuid or setgid, which covers `sudo`-style 4111
  tools. Otherwise, refuse with a clear reason. Or accept this as a documented residual risk
  (owner call). The risk is narrow: an administrator-installed, execute-only script pointing
  at a writable interpreter.

### L3 · Info · launcher interpreters other than `env`
`#!/usr/bin/nice python3`, `nohup`, `timeout`, `stdbuf` and similar (macOS splits the
arguments) find their program on PATH with no check. The same goes for
`#!/usr/bin/env env python3` on macOS. This predates D71 and is outside its letter. Note it in
the device doc as a known residual.

## Files created

- `Docs/review/86b-r55-f25-check.md` (this file). No temp dirs, no subst, no tests.
