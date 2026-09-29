# 60b: Security re-review of the R55-F8 fix round (commit 4287f7a)

Reviewer: SEC-F8b · model claude-opus-5-5 · 2026-09-29
Scope: `git show 4287f7a` (the review 60 follow-ups), read against review 60 (26cabf6) and the
original fix 357c9f4. The files are `internal/ipc/{ipc,transport_windows,transport_unix,peercred_other}.go`,
`internal/keystore/keychain.go`, `internal/mailbox/mailbox.go`, `internal/paths/endpoint_unix.go`,
`internal/daemon/daemon.go`, `Docs/protocol/ipc.md`, and the new or extended tests.
Method: read-only in the worktree. Git use was read-only (`git show`, `git log`). The mutation
checks ran in a copy under `%TEMP%\secf8b-mut` (made with robocopy `/XJ`, no reparse points).
No links were created.

## Summary

Findings: Critical 0 · High 0 · Medium 0 · Low 3 · Info 2.

**Verdict: approve.** Review 60's F1 (Medium) is closed, and mutation tests prove it. F3–F6
are closed. F2 is closed for the common Windows upgrade. The three Lows are edges of the
same upgrade story:
- on Unix, a daemon from a release before the lock still gets its DB migrated by a losing
  second daemon (F8b-01);
- on Windows, the legacy-pipe probe misses an elevated, busy or differently spelled old daemon
  (F8b-02);
- the "already running" message then contradicts `agentnetd stop` (F8b-03).

None of them leaks anything or crosses a trust boundary. They can land as follow-ups or as a
release note ("stop the old agentnetd before upgrading") rather than block the merge.

## 1. Are review 60's findings closed?

| R60 | Status | Evidence |
|---|---|---|
| F1 Medium: mailbox keys not re-keyed | **Closed** | `mailbox.go:122` uses `KeychainForEntry("mailbox-", home, "-"+keyID)`. The legacy account is `accountFor("mailbox-", dir as spelled)+suffix`, the same bytes as the old `mailbox-<acctHex>-<id>` (old `acctHex` = the hash of `Paths.Dir`, which is `filepath.Abs`). `TestKeychainLegacyAccountFallback` proves it: a legacy entry is found, not rotated, copied to the canonical account, and removed from both accounts by the sweep. Mutations M1–M3 are caught. |
| F2 Low: two daemons on Windows across the upgrade | **Closed, with residual edges** | `LockInstance` (`transport_windows.go:75`) holds `LockFileEx` on `agentnetd.lock` from before `store.Open` (`daemon.go:273`→`:278`). It also probes the pre-R55-088 pipe name. `legacyEndpoint` = `sha256(dir)[:8]` over `Paths.Dir` = `filepath.Abs` matches the old `paths.endpoint(abs)` (checked at 357c9f4^). Residual edges: F8b-02, F8b-03. No release note was added (grep of `Docs/` finds none). |
| F3 Low: macOS case spellings | **Closed** (macOS CI passes; whether the test ran rather than skipped is not visible in the log) | `onDiskCase` in `endpoint_unix.go`. `TestCanonicalFoldsCase` is the proof and runs only on a case-insensitive volume. Locally (Windows) it exercises the Windows resolver, not `onDiskCase`. Unicode residual: F8b-05. |
| F4 Low: a busy squatter reported as "already running" | **Closed** | The `default:` branch (`transport_windows.go:63`) no longer wraps `ErrAlreadyRunning`, so `main.go` prints "pipe is in use but did not answer, owner unknown". This is safe because `LockInstance` already caught our own daemon. It has no test (M8 survives, F8b-04). |
| F5 Info: other Unixes fail open | **Closed** | `peercred_other.go` keeps `errPeerCredUnsupported`, and `checkPeer` (`transport_unix.go:151`) now refuses on any `perr`. `GOOS=freebsd`/`openbsd go vet ./internal/ipc` is clean. |
| F6 Info: test gaps | **Closed** | `TestDialAccessDeniedIsForeign` added (M7 caught), the elevation comment added to `TestOwnPipeOwnerIsUser`, and the mailbox test added. |

## 2. Did the fix round add anything new?

**KeychainForEntry: fallback, copy and delete**
- **Lost key?** No.
  - `Get` falls back only on `ErrNotFound` from the canonical account. `ErrUnavailable`
    (keychain locked) is returned as is, so `usable` does not rotate on it.
  - The copy is best effort, and the legacy entry stays until `Delete`.
- **Duplicate key?** Yes, by design: the legacy entry stays in the same user's keychain until
  `Delete` removes both accounts. This crosses no trust boundary.
- **Race between two processes?** Only the daemon touches mailbox keys, under `k.mu`
  (`MailboxKey` :263, `Rotate` :290), and `LockInstance` makes it one daemon per home.
  - Identity keys are never deleted. `agentnet doctor`'s `Get` may copy concurrently, but the
    value is the same, so the copy is idempotent.
  - Webhook deletes go through the daemon.
  - No copy-after-delete resurrection is reachable.
- **Residual:** a key stored under spelling A is not found by a daemon now started with a
  different non-canonical spelling B. That needs the spelling to change across the upgrade,
  and it is not rated.

**LockInstance**
- **Start paths.**
  - `agentnetd run`, the scheduled task, launchd and systemd all go through
    `daemon.RunWithOptions`, the only daemon entry (`main.go:114`). It takes the lock before
    `store.Open`.
  - `agentnetd install` opens the DB in `recordAudit` (`install.go:180`) without the lock. This
    is benign: `store.apply` runs each migration in `BEGIN IMMEDIATE` and re-reads the version
    (`store.go:584`), and install is the same binary.
  - That leaves one site of R55-102 unguarded, by choice.
- **Release on exit or crash.**
  - Windows: `LockFileEx` locks are released when the handle closes or the process dies. The
    OS may release them slightly late, so a restart right after a crash can see "already
    running" once.
  - Unix: `flock` is released when the process exits, and the descriptor is close-on-exec.
  - Go opens files with `O_CLOEXEC` and non-inheritable on Windows, so a scope's child process
    never keeps the lock.
- **Permissions.**
  - Unix: `0600`, `O_NOFOLLOW` and an owner check (shared `lockFile`).
  - Windows: the `0600` is ignored, so the file inherits the config dir's ACL, and there is no
    owner check. In a shared, other-writable `DORYLINAE_HOME` another user could pre-create
    and hold the file. That only denies service, and it needs the owner's own
    misconfiguration. Not rated.
- **Fail-open?** No. An open or lock error aborts the start on both OSes.
- **Gap on Unix:** it does not look for a daemon from before the lock (F8b-01).

**Legacy-pipe probe (Windows)**
- It uses the owner-checked `Dial`, so a squatter on the old name is ignored, not trusted, and
  nothing is written to it.
- It costs at most `dialTimeout` (1 s), and only when the old pipe exists but is busy.
- It misses some old daemons (F8b-02).

**`onDiskCase` (macOS and Linux)**
- **Symlink or TOCTOU?** None. It runs on the `EvalSymlinks` result and only compares names
  from `ReadDir`. It never opens or follows the result, and the output is used only for hashing.
  - A component swapped between `EvalSymlinks` and `ReadDir` needs write access to the user's
    own ancestors.
  - On a case-sensitive volume the exact name always matches first, so `p` is unchanged.
  - An unreadable parent (e.g. `/home` at 0711) keeps the name as given.
- **Performance.** The cost is one `ReadDir` per ancestor, on every `Canonical` call. It is not
  a DoS, but it now runs on every `keystoreFor` (F8b-05).
- **NFD versus NFC.** Not folded (F8b-05).

**The new error message** (`transport_windows.go:65`) names only the local pipe and the
underlying error: no content and no secret. See F8b-03 for the legacy-daemon case.

## 3. Do the tests prove the fixes?

**Local run (Windows, not elevated).** `go test ./internal/ipc ./internal/paths
./internal/keystore ./internal/mailbox -count=1` passes (ok ×4). With `-v`, the new tests
`TestLockInstanceOneHolder`, `TestLockInstanceSeesLegacyPipe`, `TestDialAccessDeniedIsForeign`,
`TestCanonicalFoldsCase` and `TestKeychainLegacyAccountFallback` pass, and none is skipped.

**Mutations**, each in the temp copy:

| # | Mutation | Result |
|---|---|---|
| M1 | mailbox uses the canonical account only | **caught**, `keychain_test.go:63` |
| M2 | `Delete` skips the legacy account | **caught**, "private key survived the sweep" |
| M3 | `Get` does not copy to the canonical account | **caught**, `:80` |
| M4 | no `LockFileEx` | **caught**, `lock_test.go:22` |
| M5 | no legacy probe | **caught**, `squat_windows_test.go:145` |
| M6 | wrong legacy formula (lower-cased) | **survives**: the test builds the old pipe with `legacyEndpoint` itself (F8b-04) |
| M7 | no `ACCESS_DENIED` branch | **caught**, `squat_windows_test.go:120` |
| M8 | F4 reverted (busy → `ErrAlreadyRunning`) | **survives** (F8b-04) |
| M9 | `daemon.go` without `LockInstance` | **survives**: no daemon test (F8b-04) |

**Cross-builds.** `GOOS=darwin|linux|freebsd go vet` over ipc, paths, keystore, mailbox and
daemon is clean. openbsd fails only in `internal/relay/diskfree_unix.go` (`Statfs_t` fields),
which predates this change and is not a release target.

**CI** (PR #10, run 36605545002, head 4287f7a), at the time of writing:
- passed: lint, `test (ubuntu-latest)`, `test (macos-latest)` (4m55s), install-sh,
  flag-sensitive-paths, and both unix service installs;
- pending: `test (windows-latest)` and `race`.

`test (macos-latest)` is the only real proof of `onDiskCase`, and `test (windows-latest)` the
only elevated proof of `TestOwnPipeOwnerIsUser`. CI runs `go test ./...` without `-v`, so the
macOS log shows only `ok internal/paths`. It cannot show whether `TestCanonicalFoldsCase` ran
or skipped. The GitHub macOS runner volume is case-insensitive APFS by default, so it
presumably ran.

## Findings

### F8b-01 · Low · confirmed-read
- **Where:** `internal/ipc/transport_unix.go:45` (`LockInstance`), with `internal/daemon/daemon.go:273-289`;
  `Docs/protocol/ipc.md` §Endpoint.
- **What goes wrong:** on Unix, `LockInstance` only takes the flock. A daemon from a released
  build (no lock file of any kind; the `.sock.lock` is new in 357c9f4) holds nothing. The new
  daemon therefore passes the lock, runs `store.Open`, which migrates the DB, and only then
  meets the old socket in `Listen` (`listenLocked` → `ErrAlreadyRunning`). ipc.md now claims
  that "a second daemon for the same home stops before it migrates anything" on every OS.
- **Scenario:** a user on Linux or macOS upgrades the binary in place while the old service
  daemon runs (Unix allows replacing a running executable), then runs `agentnetd run` in a
  terminal. The new binary applies its migrations under the running old daemon and exits with
  "already running". The old daemon keeps serving on a schema newer than it knows. Actor: none
  (the owner's own upgrade). Impact: R55-102 / C28-03 is not closed on Unix for this upgrade.
- **Spec:** `Docs/protocol/ipc.md` §Endpoint (the new sentence above).
- **Fix direction:** mirror Windows. After the flock, `Dial(filepath.Join(dir,"agentnetd.sock"))`
  and return `ErrAlreadyRunning` on success. Add a unix test like `TestLockInstanceSeesLegacyPipe`.
- **Related:** R55-102 (C28-03), R60-F2.

### F8b-02 · Low · confirmed-read
- **Where:** `internal/ipc/transport_windows.go:89-94` (legacy probe).
- **What goes wrong:** only a successful `Dial` counts as an old daemon. The probe misses three
  cases, and each starts a second daemon on the same DB, which is the scenario R60-F2 is about:
  - The old daemon runs elevated. Pre-357c9f4 pipes had no `O:`, so their owner is
    BUILTIN\Administrators, and `Dial` returns `ErrForeignOwner`.
  - All the old daemon's pipe instances are busy for 1 s, so `Dial` times out.
  - The old daemon was started with another spelling of the dir. The legacy name hashes the
    spelling, so no probe can find it.
- **Scenario:** an old `agentnetd` was started from an admin terminal. After an unpack-to-new-dir
  upgrade, the new `agentnetd run` takes the new lock, the probe returns `ErrForeignOwner`, and
  the new daemon migrates and serves the same home next to the old one. Actor: none (owner's
  upgrade).
- **Spec:** `Docs/protocol/ipc.md` §Endpoint, "a daemon from before the lock that answers on the
  old pipe name counts as running too".
- **Fix direction:** treat any probe result other than `ErrNotRunning` as "possibly running":
  refuse with an explicit message. A squatter on the old name gains no more denial of service
  than on the new one. Add the release-note line from R60-F2 for the spelling case.
- **Related:** R60-F2, F8b-03.

### F8b-03 · Low · confirmed-read
- **Where:** `cmd/agentnetd/main.go:115-121`, `cmd/agentnetd/stop.go:45-46`.
- **What goes wrong:** when `LockInstance` refuses because of the legacy pipe, `main.go` drops
  the error text ("an older agentnetd serves pipe …"). It prints "agentnetd is already running
  for <dir>", with no pid, because `runningPID` dials the new endpoint. Then `agentnetd stop`
  and every `agentnet` command from the new binaries report "not running", since they dial the
  new name.
- **Scenario:** a Windows user upgrades while the old daemon runs and starts the new daemon. They
  are told it is already running, but `stop` says it is not. Nothing tells them to stop it with
  the old binary.
- **Spec:** none (rubric: "an error message that misleads").
- **Fix direction:** when `runningPID` returns 0, print `err` itself, or add "stop the older
  agentnetd with its own CLI".
- **Related:** F8b-02.

### F8b-04 · Info · confirmed-test
- **Where:** `internal/ipc/squat_windows_test.go:124`, `internal/daemon` (no test),
  `internal/ipc` (no F4 test).
- **What goes wrong:** three fixes are not pinned by any test:
  - The legacy pipe formula is checked only against itself. M6 (lower-casing the dir) passes.
    Pin it with a literal vector: the old name for a fixed `dir`, computed from the
    pre-R55-088 `paths.endpoint`.
  - F4's message: M8 passes.
  - `RunWithOptions` taking the lock before `store.Open`: M9 passes, and no daemon test exists.
- **Fix direction:** add the vector, a squatter-busy test, and a daemon test that holds
  `LockInstance` and asserts that a second `RunWithOptions` returns `ErrAlreadyRunning`
  without creating or migrating the DB.

### F8b-05 · Info · suspected
- **Where:** `internal/paths/endpoint_unix.go` (`entryName`), `internal/mailbox/mailbox.go:122`.
- **What goes wrong:**
  - `strings.EqualFold` does simple case folding only, with no Unicode normalisation. On
    HFS+ (NFD on disk), or on APFS (which keeps the creation form but compares
    normalisation-insensitively), a `DORYLINAE_HOME` typed in NFC for an NFD component misses
    every entry. It keeps the given bytes, so it still gets its own keychain account (the R60-F3
    residual for non-ASCII names). That needs the owner's own unusual spelling.
  - `Canonical` (`EvalSymlinks` plus one `ReadDir` per ancestor) now runs on every
    `keystoreFor`, which means once per inbound mail in `MailboxKey`. Before, `acctHex` was
    computed once in `New`. The cost is bounded and dwarfed by the keychain call, so there is
    no DoS. But a transient resolution error (`Canonical` falls back to the abs path) can now
    flip the account mid-run.
- **Fix direction:** compute the `KeychainForEntry` base account once in `New`. Optionally
  compare names with `norm.NFC` (x/text) on darwin, or document the limit.

## Assumptions and contracts
- `legacyEndpoint(p.Dir)` equals the pre-R55-088 pipe name only because `Paths.Dir` is
  `filepath.Abs(dir)` both then and now (`paths.go:42`, 357c9f4^ `paths.go:42`). **Checked.**
- The mailbox legacy account needs `accountFor(prefix, Paths.Dir)` to equal the old
  `AccountFor(configDir)` hash (`keychain.go:35`, old `mailbox.go:88`). **Checked.**
- The daemon is the only writer of mailbox keychain entries (`mailbox.New` is called only at
  `daemon.go:332`). **Checked.**
- The migration atomicity that makes `install.go:180` safe without the lock is in
  `store.go:584-602`. **Checked.**

## Checked and fine
- The Windows `LockInstance` fails closed on open and lock errors and maps only
  `ERROR_LOCK_VIOLATION` to `ErrAlreadyRunning` (`transport_windows.go:79-88`).
- The Unix `lockFile` refactor keeps `O_NOFOLLOW`, `0600`, the owner check and `LOCK_NB`
  (`transport_unix.go:51-68`).
- `lockEndpoint` still guards the socket, so two concurrent new daemons are serialised twice.
  That is harmless.
- The legacy probe uses the owner-checked `Dial`, so a squatter on the old name receives no
  byte.
- `peercred_other.go` fails closed, and the constant has moved without duplication; the
  freebsd and darwin vets are clean.
- `ipc.md` documents the other-Unix refusal and the Windows instance lock accurately, apart
  from F8b-01.

## Commands run
- `git log --oneline -5`, `git show --stat 4287f7a`, `git show 4287f7a`,
  `git show 357c9f4^:internal/paths/paths.go`, `git show 357c9f4^:internal/ipc/transport_unix.go`:
  read-only.
- `go test ./internal/ipc ./internal/paths ./internal/keystore ./internal/mailbox -count=1`: ok ×4.
- The same with `-v -run 'TestLockInstance|TestDialAccessDenied|TestCanonicalFoldsCase|TestKeychainLegacyAccountFallback|TestOwnPipe'`: all PASS.
- M1–M9 in `%TEMP%\secf8b-mut` (`go test <pkg> -run <test> -count=1`): results in §3.
- `GOOS={darwin,linux,freebsd,openbsd} go vet ./internal/{ipc,paths,keystore,mailbox,daemon}`:
  clean, except openbsd's relay, which predates this change.
- `gh pr checks 10`, `gh run view 36605545002`: status as in §3.
