# 85 — R55-F22 security review (Windows local hardening)

Commit `aef96a0` on branch `p4/r55-f22` (base `eb283bf`). Reviewer: Opus worker R55-F22sec.
Scope: R55-090, 093, 094, 095 and the daemon-DSN part of 096 (`Docs/review/55-code-review/99-report.md`).

## Verdict

**Approve with Low follow-ups.** No Critical/High/Medium findings. The PATH lookups of
`powershell.exe` and `schtasks.exe` are gone, and so are the DSN truncation and the newline
injection in the service definitions. The rotation fix has two confirmed side effects. Both are
Low and fixable inside this ticket (F1, F2). Whether install should warn or refuse is up to the
owner (F3).

## Findings

### F1 · Low · confirmed-test — a failed rotation deletes the previous log generation
`internal/logfile/logfile.go:90-97` (`moveAside`). It removes `<path>.1` *before* the rename. On
Windows, when a viewer holds the log open, the rename fails and the old `.1` is already gone. The
owner ends up with no earlier log, which is the moment they most likely wanted it (they were
reading the log). Proof: `internal/logfile/rotate_sec_test.go` (`.1` missing after the failed
rotation).
**Fix:** call `os.Rename(path, path+".1")` first. On Windows, Go's `os.Rename` uses
`MOVEFILE_REPLACE_EXISTING`, so it replaces the target. Only on an "exists" failure, remove `.1`
and retry.

### F2 · Low · confirmed-test — while rotation keeps failing, the log is unbounded and every write re-tries the rotation
`logfile.go:62-66`. After a failed rotation `size` stays above `limit`, so every later write runs
close + remove + rename + open again. The test with a 64-byte limit and a viewer open grew the file
to 3206 bytes (100 writes, 100 failed rotations). This doesn't spin (one attempt per write), and
the original handle is never lost: the reopen is the same path in append mode. But the package
promises the log stays "about twice the limit", and a viewer left open on a chatty daemon breaks
that and costs four syscalls per log line. Only the user or an admin can hold the file (0600 in the
private home), so this is self-inflicted, not an attack.
**Fix:** after a failure, back off. Retry only once `size` passes the next multiple of `limit`, or
after N seconds. Optionally add a hard cap (e.g. 4×limit): truncate in place, which works under
`FILE_SHARE_WRITE`.
Related, not new (Info): if `w.f.Close()` fails (`:77`), or `moveAside` succeeds but `open()`
fails (`:87`), the writer still dies for the process lifetime. The `w.f == nil` test at `:63`
does not cover the first case: the closed handle stays in `w.f` and every write returns
`ErrClosed`-like errors.

### F3 · Low (owner decision) — install only warns when the daemon binary is writable by others
`cmd/agentnetd/install.go:126-133`. The verdict is the same as D24 L11: the same
`device.CheckProgramOwner`, with the same program/parent/ancestor roles. `deps.exe` is already
`EvalSymlinks`-resolved and is the exact path baked into the service. The consequence differs,
though. For scope programs L11 *refuses*. Here a non-admin local user who can swap the binary
gets code execution as the owner, with the owner's keys, **at every logon, automatically**. That
is a larger exposure than the owner launching it by hand, and the stderr warning is printed once
and is easy to miss.

Default Windows ACLs make this common. The `C:\` root grants Authenticated Users inherited Modify
on new top-level folders, so `C:\tools\agentnet\agentnetd.exe` trips the check. The owner's own
machine trips it for the whole drive (HANDOFF, Phase 2 walkthrough). That is the argument
*for* warn: a refusal would block the owner's own install. "Warn" is defensible as the ticket
allows ("warn or refuse").

**Recommended direction:** refuse by default with an explicit `--allow-writable-program`
override, *or* keep warn but repeat it where it persists: an `agentnet doctor` check and/or the
install audit row. Minor: any `CheckProgramOwner` error is printed as "others may be able to
change", even when it is only "could not read the ACL" or "resolve the program". Only
`*device.WritableError` should get that wording.

### F4 · Low — `systemTool` falls back to a PATH lookup instead of failing closed
`internal/service/systemtool_windows.go:16-18`. When `GetSystemDirectory` fails it returns the bare
`schtasks.exe`. `notify.powershellPath` returns an error in the same situation. The failure is
practically impossible, and Go 1.19+ `LookPath` refuses CWD hits (`ErrDot`), so the only residual
is a PATH entry. **Fix:** return an error (make `systemTool` return `(string, error)`, or have
`ExecRunner.Run` fail) so both helpers behave the same.

### Info
- **I1 — bare names that remain (outside this Windows ticket).** Windows daemon: none. `notify`
  (toast, approval toast and removal, approval window) uses `powershellPath()`.
  `service.ExecRunner` and therefore `agentnet doctor`
  (`cmd/agentnet/doctor.go:123,546,552`, which uses `ExecRunner`) use `systemTool`. `idle` on
  Windows uses no exec. `capability.LookGit` (`internal/capability/git.go:68`) and scope `argv[0]`
  still resolve through PATH *once, by design*: the user's own git and programs, stored as absolute
  paths, and scope programs are ownership-checked. On Unix, bare `gdbus`/`notify-send`
  (`internal/notify/desktop_linux.go:28,31`), `systemctl`, `launchctl` and `xprintidle` still use
  PATH. `tools/releasesign` `gh` is a release tool, not the daemon.
- **I2 — the `relay` package still builds raw DSNs** (`internal/relay/queue.go:243`, `journal.go:98`,
  `backup.go:30`). These are the non-daemon part of R55-096 and remain open. `fileURI` can be reused.
- **I3 — GetSystemDirectory.** `x/sys/windows.GetSystemDirectory` handles the buffer length and
  errors. Release targets are amd64/arm64 only (`.github/workflows/release.yml:120`), so WOW64
  redirection doesn't apply. Even under a 386 build it would redirect to `SysWOW64`, which is equally
  TrustedInstaller-owned.
- **I4 — spec-char coverage.** Every interpolated, owner-chosen field is covered. Executable, home
  and relay are checked. The log path, launchd out-path and XML path derive from home. `env.User`
  (OS-supplied) is XML-escaped. Escaping per format:
  - systemd: `systemdQuote` doubles `\`, escapes `"`, doubles `%` and `$`. With newlines now refused
    (a trailing `\` cannot continue the line, because it is doubled), no extra ExecStart word or
    unit key can be injected.
  - launchd: each argv is its own `<string>`, run through `xml.EscapeText`.
  - schtasks: `windowsQuote` follows the CommandLineToArgvW rules (2n+1 backslashes before `"`,
    trailing run doubled). `%` is refused, because Task Scheduler expands it in
    Command/Arguments/WorkingDirectory. The values are XML-escaped.

  A home path with spaces or quotes stays one argument. C1 controls (U+0080–U+009F) and
  U+2028 are not refused but are harmless: systemd splits lines only on `\n`, and XML encodes them.
  A cost to accept: a relay URL with percent-encoding is now refused for schtasks.
- **I5 — doc-comment misplaced.** `internal/service/env.go:31`: the `// checkUnix validates …` line
  now sits above `checkSpecText`'s comment, so `checkUnix` has no doc comment and
  `checkSpecText`'s comment starts with the wrong name. This is cosmetic, but it may trip
  revive/godot in CI lint.
- **I6 — fileURI round-trip, confirmed on Windows** (`internal/store/fileuri_sec_test.go`). Open
  and OpenReadOnly land on exactly the given file for: unicode + `#%41&x=y` in the name; `\\?\C:\…`;
  `\\?\` paths over 260 characters; UNC `\\localhost\C$\…` (rendered `file:////localhost/…`, empty
  authority); forward-slash `C:/…`. modernc v1.59 hands the whole DSN to `sqlite3_open_v2` with
  `SQLITE_OPEN_URI` and splits its own query at the first literal `?`. Because `?` is
  `%3F`-escaped, a path cannot add parameters (`mode=`, `vfs=`, `_pragma=`), and `&`/`=` before the
  `?` are just path bytes. No path can redirect the open.

## Verification run
- `go vet` on the six touched packages for GOOS=linux, darwin and windows: clean. `golangci-lint`
  is not installed locally, so CI lint was not run (see I5).
- Targeted tests (Windows host): notify, service, store, cmd/agentnetd pass. logfile passes except
  the new proof test `TestRotateFailureSideEffects`, which fails by design to show F1 and F2.
  Cross-compiled the linux service tests and darwin notify tests.

## Files created by this review
- `Docs/review/85-r55-f22-security.md` (this report)
- `internal/logfile/rotate_sec_test.go`: Windows-only proof of F1 and F2. It **fails** until they
  are fixed. Drop it, or keep it as the acceptance test for the fix.
- `internal/store/fileuri_sec_test.go`: Windows-only fileURI path-form test (passes).
