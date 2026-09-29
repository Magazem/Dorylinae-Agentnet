# 60: Security review of R55-F8 (commit 357c9f4)

Reviewer: SEC-F8 · model claude-opus-5-5 · 2026-09-29
Scope: R55-008 / C16-01 (Windows pipe squatting), R55-088 / C16-05 (canonical config dir),
R55-197 (foreign-owner test), and the R5 Unix-listen lead (`chunks/C16-leads-R5.md` item 3).
Change: `internal/ipc/transport_{windows,unix}.go`, `peercred_{linux,darwin,other}.go`,
`internal/paths/{paths,endpoint_windows,endpoint_unix}.go`, `internal/keystore/keychain.go`,
`internal/identity/identity.go`, `internal/daemon/daemon.go`, `Docs/protocol/ipc.md`, and the new
tests `squat_windows_test.go`, `peer_unix_test.go`, `paths/canonical_test.go`,
`keystore_test.go`, plus the helper `testutil/spelling.go`.
Method: read-only. I took the diff by comparing the worktree with the main checkout, ignoring
CR, and read the commit's file list and blobs through `gh api`. I ran no git commands. The
mutation and reproduction checks ran in temporary copies outside the repo.

**Verdict: changes needed.** The Windows and Unix owner checks are sound and close R55-008.
However, R55-088's re-keying misses the mailbox keys (F1, Medium): after the upgrade, any
install whose config dir spelling is not canonical loses the keychain entries for its mailbox
private keys, and those entries are never deleted afterwards. The remaining findings are Low or
Info.

## 1. Windows

- **Is every foreign pipe refused before a byte is sent?** Yes. `Dial`
  (`transport_windows.go:66`) opens the pipe, then `checkOwner` (:90) reads the owner with
  `GetSecurityInfo(handle, OWNER_SECURITY_INFORMATION)` and closes the handle on any mismatch,
  missing owner or read error. `ipc.Call` writes only after `Dial` returns. A pipe that denies
  the current user access (`ERROR_ACCESS_DENIED`) is also mapped to `ErrForeignOwner`.
  - The `Listen` probe (:52) goes through the same `Dial`.
  - `runningPID` in `cmd/agentnetd/main.go` and `StopWait` also use `Dial`.
  - `log.go:307` calls `ipc.Dial` directly.
  - I found no other dial site (grep for `winio.`, `DialPipe`, `"unix"`).
- **Where does the owner SID come from?** From the handle, not the name. The client opens the
  pipe with `GENERIC_READ|GENERIC_WRITE`, which includes `READ_CONTROL`. All instances of a
  pipe share one security descriptor, set by the first creator.
- **Is there a TOCTOU window?** No. The check reads the object that the connected handle refers
  to, and nothing is re-opened by name afterwards.
- **Can a squatter set the owner to the victim's SID?** Only with `SeRestorePrivilege`. With
  `SeTakeOwnershipPrivilege` it can only take ownership for itself or an owner-flagged group.
  Standard users hold neither privilege, and neither do LocalService or NetworkService.
  Administrators, LocalSystem and backup operators can do it, but they are outside the threat
  model (they can already read the victim's keys).
- **Can the squatter impersonate the client?** No. go-winio dials with
  `SECURITY_SQOS_PRESENT|SECURITY_ANONYMOUS` (`pipe.go:218`, `PipeImpLevelAnonymous`), and the
  client never writes, so `ImpersonateNamedPipeClient` cannot run either.
- **Elevated and admin tokens.** `Listen` now sets `O:<user SID>` explicitly (:40), so an
  elevated daemon's pipe is owned by the user rather than BUILTIN\Administrators. Clients
  compare against `TokenUser`, which is the same whether or not the token is elevated. Result:
  an elevated daemon with a plain CLI works, and so does the reverse.
  - Integrity labels are unchanged (no label for medium-or-higher creators).
  - `TestOwnPipeOwnerIsUser` proves the explicit owner only when it runs elevated. Locally
    (not elevated) it still passes with `O:` removed. GitHub's `windows-latest` runner is
    elevated, so CI does cover it.
- **Services and LocalSystem.** The SID is part of the pipe name, so a daemon running as
  LocalSystem and a user's CLI never share a pipe. This is no regression: the old DACL already
  granted access to the creating SID only. There is no service deployment on Windows; the
  scheduled task runs as the user.
- **Is the pipe still created exclusively?** Yes. go-winio creates the first instance with
  `FILE_CREATE` (`pipe.go:380`), the NtCreateNamedPipeFile equivalent of
  `FILE_FLAG_FIRST_PIPE_INSTANCE`. The DACL grants only the owner `FILE_CREATE_PIPE_INSTANCE`,
  so no one else can add an instance to our pipe.
- **Residual risk.** The SID is not a secret: SIDs can be looked up by name. A squatter can
  therefore still keep the victim's daemon from starting (a denial of service). The difference
  is that the victim now sees "held by another user" instead of leaking content. See F4.

## 2. The new pipe id and compatibility

- **Old daemon, new CLI.** Unix is unaffected: the endpoint is still `<dir>/agentnetd.sock`.
  `listenLocked` still dials the socket before trusting the lock, so a daemon from before the
  lock file is detected.
- **On Windows the id changes.** A new CLI reports "not running" to an old daemon, and a new
  `agentnetd` starts on the new name next to the old one. Nothing else enforces a single daemon
  per home on Windows: there is no lock file and SQLite does not lock exclusively. See F2.
- **Harness scripts and docs.** No script, test or doc outside `ipc.md` hard-codes the pipe
  name (grep for `\\.\pipe`, `dorylinae-<id>`). `ipc.md` §Endpoint is updated. `doctor`
  prints `p.Endpoint` from `paths`, so it follows the change.

## 3. Unix

- **Peer uid check.** `checkPeer` reads the uid with `SO_PEERCRED` on Linux and Android (the
  `_linux.go` suffix covers Android) and with `LOCAL_PEERCRED`/`xucred` on darwin. Both return
  the peer's effective uid at `listen()`, which is compared with `os.Geteuid()`. Nothing is sent
  before the check.
- **Other operating systems.** `peercred_other.go` fails open (`errPeerCredUnsupported` →
  accept). No release target is affected: release.yml builds darwin, linux and windows only.
  See F5.
- **The flock lock file.** It is opened with `O_NOFOLLOW` and mode `0600`, with an owner check
  and `LOCK_EX|LOCK_NB`. An `EWOULDBLOCK` result maps to `ErrAlreadyRunning`. The descriptor is
  close-on-exec (Go's default), so child processes do not inherit the lock.
  `lockedListener.Close` closes (and so unlinks) the socket before releasing the lock.
  `TestUnixConcurrentListenOneWins` covers the R5 race.
- **Stale-socket removal.** It uses `Lstat`, then an owner check, then `Remove`.
  - A symlink is judged by the link's own owner, and `Remove` unlinks the link, never its
    target.
  - Racing between `Lstat` and `Remove` requires write access to the config dir, which
    `Ensure` keeps at `0700`. A dir owned by another user fails that `Chmod`.
  - Safe.

## 4. `paths.Canonical`

- **Nonexistent tail.** `ERROR_FILE_NOT_FOUND` and `ERROR_PATH_NOT_FOUND` both match
  `fs.ErrNotExist` (Go's `Errno.Is`). The walk goes up to the longest existing prefix and
  appends the tail as given. Windows lower-cases the whole path for the pipe name, so the case
  of the tail does not matter.
- **Junctions, subst drives, 8.3 names, mapped drives.**
  - `GetFinalPathNameByHandle` (`FILE_NAME_NORMALIZED|VOLUME_NAME_DOS`) resolves junctions,
    symlinks and 8.3 names, and returns the on-disk case.
  - A `subst` drive is reported on its underlying volume.
  - UNC paths come back as `\\?\UNC\…`, which is rewritten to `\\…`.
  - Tests cover case, the missing tail and 8.3 names (the CI `%TEMP%`).
- **Errors other than "not found".** On ACCESS_DENIED, ELOOP or ENOTDIR, `Canonical` returns
  the absolute path unresolved (`paths.go:81`). One home could then get two ids if the error
  comes and goes. That is unlikely for one's own config dir (Info, not rated).
- **Can two users or homes share an id?** The SID in the hash separates users. On Windows,
  lower-casing merges `X\Home` and `X\home` inside a case-sensitive directory. The effect is
  that the second daemon reports "already running". There is no security impact.
- **Can one home get two ids?** On macOS, `EvalSymlinks` keeps the given case on
  case-insensitive APFS, so two `DORYLINAE_HOME` spellings that differ only in case still give
  two keychain accounts. See F3.

## 5. Keystore fallback

- **Identity and webhook keys.**
  - `Get` falls back to the legacy account and copies the secret to the canonical one on a
    best-effort basis.
  - `Delete` removes both accounts and joins the errors.
  - Neither leaks a key or loses one, and a deleted key cannot come back.
  - The legacy hash is of `Paths.Dir` as spelled, which is exactly what `AccountFor`/`webhook-`
    hashed before (verified).
  - The legacy copy stays in the same user's keychain until `Delete`. It crosses no trust
    boundary, so it is not a leak.
- **Mailbox keys are not covered.** `internal/mailbox/mailbox.go:88` builds
  `mailbox-<acctHex>-<keyID>` from `AccountFor`, which is now canonical, with a plain
  `NewKeychain` and no legacy account. See F1.
- **Consistency with F7.** F7 compares forbidden resources by file identity (volume and file
  ID, device and inode). `Canonical` is path-based: it is fine for naming, but it does not
  unify bind mounts or two mounts of one tree. F7 should reuse `resolveExisting` or
  `GetFinalPathNameByHandle` where it needs a path, rather than add a second canonicaliser.
  Nothing here conflicts with F7.

## 6. `internal/daemon/daemon.go` line endings

The change is semantic only. The commit's patch (`gh api …/commits/357c9f4`) touches
daemon.go with +1/−3: it drops the `strings` import and replaces the `webhook-` account with
`KeychainFor("webhook-", dir)`. The committed blobs of `daemon.go`, `transport_windows.go`,
`endpoint_windows.go` and `peercred_other.go` contain 0 CR bytes. There is no whole-file churn.

## 7. Do the tests prove the fix?

- **Local runs.** `go test ./internal/ipc ./internal/paths ./internal/keystore
  ./internal/identity ./internal/mailbox -count=1` passes: ok ×5 on Windows, not elevated.
- **Mutations** (each in a temp copy):
  - `checkOwner` → `return nil`: `TestPipeSquatRefused` fails ("already in use, want
    ErrForeignOwner").
  - Skipping `owner.Equals`: `TestPipeSquatRefused` fails.
  - `Canonical` without resolution: `TestCanonicalOneNamePerDir` and
    `TestAccountSameForTwoSpellings` fail.
  - Dropping `O:` from the SD: `TestOwnPipeOwnerIsUser` still passes when not elevated. It
    needs an elevated runner, as noted in §1.
- **What the squat test uses.** It plays "another user" by expecting LocalSystem, with an
  Everyone:GA squatter. It asserts that `Listen` reports "held by another user" and that both
  the `Listen` probe and `Call` sent zero bytes. The ACCESS_DENIED branch of `Dial` has no
  test (Info).
- **`peer_unix_test.go`** runs in CI only.
  - PR #10, at the time of writing: `test (ubuntu-latest)` passed (4m10s) and
    `test (macos-latest)` passed (5m27s).
  - `test (windows-latest)` and `race` were still pending.
  - The job logs are not available until the run finishes, so I could not confirm that the
    `TestUnix*` tests ran rather than skipped.
- **Still open.** The two-account manual check (D7) remains the only proof of the cross-user
  leg.

## Findings

### F1 · Medium · Mailbox private keys are not re-keyed: orphaned on upgrade, never deleted
- **Where:** `internal/mailbox/mailbox.go:88` (`acctHex` from `AccountFor`, now canonical) and
  :123 (`NewKeychain`, no legacy account).
- **What:** before 357c9f4, mailbox keys lived at `mailbox-<hash(dir as spelled)>-<id>`; now they
  are looked up under `mailbox-<hash(Canonical(dir))>-<id>`. This hits every install whose
  spelling is not canonical and which stores keys in the keychain:
  - Linux `~/.config` symlinked by a dotfile manager, or `/home` on a symlink;
  - macOS `DORYLINAE_HOME` under `/var` or `/tmp`;
  - Windows `%APPDATA%` with a different case, a redirected folder, DFS, or a junction.

  For those installs:
  - `usable` sees `ErrNotFound` and silently rotates to a new key.
  - `MailboxKey` misses every old key id, so mail in flight is rejected with `key_miss` until
    key-miss recovery.
  - `deleteKey` and the sweep delete only the new account. The old private keys stay in the
    keychain for good, which defeats forward secrecy through rotation (`mail.md:26`).
- **Reproduced** (temp copy, `keyring.MockInit`): a key stored as
  `mailbox-4053391bd69e8c54-k1` under the upper-case spelling. `New(spelled,…).keystoreFor("k1").Load()`
  returns `keystore: secret not found` (new `acctHex` `b4da9ba2cc0ea4c4`).
- **Fix:** give the mailbox keychain the same legacy fallback: a `KeychainFor` variant with a
  suffix, or keep a legacy `acctHex`, so that `Get` copies and `Delete` removes both. Add a
  mailbox test in the style of `TestKeychainLegacyAccountFallback`. The file backend is
  unaffected.

### F2 · Low · Windows upgrade can run two daemons on one home
- **Where:** `internal/paths/endpoint_windows.go:22` (new id); Windows has no lock file
  (unlike `transport_unix.go:43`).
- **What:** the pipe name is the only single-instance guard on Windows, and this release changes
  it. `agentnet stop` from the new CLI reports "not running" to an old daemon, and a new
  `agentnetd run` starts beside it on the same DB and identity. That needs the new binaries to
  land while the old daemon runs, for example unpacked to a new directory, because an in-place
  overwrite fails on a running `.exe`.
- **Fix:** release notes or `install.md` should say "stop agentnetd with the old CLI before
  upgrading (Windows)". Better, take a `LockFileEx` lock on `<dir>\agentnetd.lock`, as on Unix,
  so the guard no longer depends on the name.

### F3 · Low · macOS: two case spellings of one home still give two keychain accounts
- **Where:** `internal/paths/endpoint_unix.go` (`resolveExisting` = `EvalSymlinks`, no case
  normalisation).
- **What:** R55-088 is fixed for symlinks, but not for case on case-insensitive APFS or HFS+.
  `ErrKeyLost` then remains possible with `DORYLINAE_HOME` spelled differently in two shells.
  It needs a user-chosen odd spelling. The default dir is stable.
- **Fix:** on darwin, resolve the real case (`F_GETPATH` on an open fd) or document the limit.

### F4 · Low · A busy squatter is reported as "already running"; the DoS remains
- **Where:** `transport_windows.go:57` (`default:` branch).
- **What:** if the squatter keeps its only instance busy, the `Listen` probe times out, and the
  user is told their own daemon is running. The owner check still prevents any leak, since no
  byte is sent. Separately, a squatter can always keep the daemon from starting, because the SID
  and path are public. That residual risk is inherent to fixed names and needs an owner
  decision if it matters.
- **Fix (optional):** when the probe fails for another reason than `ErrForeignOwner`, say "pipe
  in use, owner unknown" instead of "already running".

### F5 · Info · Peer-uid check fails open on other Unixes
- **Where:** `internal/ipc/peercred_other.go`, `transport_unix.go:143`.
- **What:** on FreeBSD, OpenBSD and other Unixes, any socket is accepted. No release target is
  affected. FreeBSD has `LOCAL_PEERCRED` in x/sys/unix and could share the darwin file.
  Otherwise, fail closed and let `DORYLINAE_HOME` users opt out.

### F6 · Info · Test gaps
- `Dial`'s ACCESS_DENIED → `ErrForeignOwner` branch has no test.
- `TestOwnPipeOwnerIsUser` is meaningful only when elevated. A comment saying so would stop a
  future reader from trusting a local pass.
- No mailbox test covers the account change (F1).
