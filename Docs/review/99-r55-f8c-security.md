# Review 99 — R55-F8c security review (fixes for 60b F8b-01..05)

- **Scope:** branch `p4/r55-f8c` at 367876d, 15 files, in `AgentNet-wt/r55-f8c`. Inputs:
  `60-r55-f8-security.md` and `60b-r55-f8-rereview.md`.
- **Method:** the worker rules forbid every git command, so the diff was taken with
  `diff -ru --strip-trailing-cr` against the clean `main` checkout (4b8da40). It shows the same
  15 files. `HANDOFF.md` also differs, but only because main is newer.
- **Reviewer:** R55-F8csec-Opus.

## Verdict

**Approve. No blocking findings.** F8b-01, -02, -03 and -05 are closed, and F8b-04 is closed for
the three gaps it named. The change creates no new local denial of service, no path to a fake
daemon and no leak. The keychain account is the same as before for every existing user. Four Info
findings follow. None of them needs to block the merge.

| 60b finding | Status | Evidence |
|---|---|---|
| F8b-01 Unix pre-lock daemon | **Closed** | `transport_unix.go:56-66` dials the socket after the flock and before `store.Open` (`daemon.go:273`→`:278`). Test: `TestLockInstanceSeesPreLockDaemon`. |
| F8b-02 Windows legacy probe misses | **Closed** (the spelling case is documented) | `probeLegacy` (`transport_windows.go:137-151`) refuses on every result except `ErrNotRunning`. The spelling case is in `known-limitations.md`. |
| F8b-03 misleading message | **Closed** | `main.go:124-126` prints the cause when no pid answers. The Windows foreign and busy refusals carry the hint themselves. Test: `TestAlreadyRunningWithoutAnswerGivesCause`. |
| F8b-04 unpinned fixes | **Closed** | Literal vector: I recomputed `sha256("C:\Users\Alice\AppData\Roaming\dorylinae")[:8]` = `be041a3b321a2fa8` independently, and it matches. Busy-pipe test: `TestListenBusyPipeOwnerUnknown`. Daemon test: `TestRunStopsBeforeStoreWhenLocked`. |
| F8b-05 accounts per call, NFC/NFD | **Closed** | `KeychainEntriesFor` runs once in `mailbox.New` (`mailbox.go:97`). `entryName` compares names after NFC (`endpoint_unix.go:55,60`). |

## Adversarial analysis

### (1) Local attacker against the new dial or probe

- **Unix, reachability.** The new dial targets `<dir>/agentnetd.sock`, and the dir is `0700`
  and owner-checked by `Ensure`. Only the owner, or root, can place a socket there.
  - `listenLocked` already dialled the very same path before this change
    (`transport_unix.go:93-98`), so no new way to deny service exists.
  - The only change is timing. The refusal now happens before `store.Open`, which is strictly
    better.
- **Unix, nothing sent or trusted.** `Dial` runs `checkPeer` before anything is written, and
  `LockInstance` closes the connection straight away (`:59-62`). A fake daemon receives only a
  connect.
- **Unix, `ErrForeignOwner`.** It is handled the same way as `Listen`: the flock is released and
  the error is wrapped `listen on <sock>: …` (`:63-65`, compare `:96-97`). `main.go` prints it
  and exits 1, as before.
  - One difference: a *stale* socket file owned by another user is still caught only by
    `Listen`'s `Lstat` check (`:102-103`), which runs after `store.Open`. That is unchanged from
    before and is not a security issue, since the database is ours.
- **Windows, new denial of service?** A squatter of another user on the old name now refuses
  the start, where before it was ignored. This gives the attacker no new capability:
  - The new name, `sha256(SID‖lower(canonical dir))`, is just as computable, because SIDs are
    public (60 §1). A squatter on the new name already refuses the start in `Listen`.
  - The attacker still has to squat a name before the victim starts. The second name only
    gives them a choice, not more reach.
  - No accidental collision exists between users: the legacy name hashes the absolute dir, and
    `Ensure` refuses a dir that another user owns.
- **Windows, leak or fake daemon.** None. `Dial` checks the owner before it sends anything, and
  the probe never writes.

### (2) Windows legacy probe

- **Lock release.** All three refusal paths go through `_ = f.Close()` at
  `transport_windows.go:124-126`. Closing the handle releases `LockFileEx`. The `elevated` and
  `busy` subtests re-take the lock to prove it.
- **Messages.** Each one is accurate:
  - Success says "an older agentnetd serves pipe X", which is true.
  - Foreign says "in use, by an older agentnetd started elevated or by another user", and lists
    both real causes. A newer daemon cannot be the cause: newer daemons set `O:<user SID>`, so
    they answer as ours.
  - Busy or other errors say "in use but did not answer, so an older agentnetd may be running".
- **Exit code.** The foreign and busy refusals do not wrap `ErrAlreadyRunning`, so they exit 1
  and not 3. Nothing outside `main.go` keys on 3, so this is acceptable. It is also consistent
  with the F4 decision that "unknown" is not "already running".
- **Assumption, not verified.** The hint assumes that the *old* CLI, run elevated, can stop an
  elevated old daemon. That depends on the pre-357c9f4 `Dial`, which I could not read without
  git. In the worst case the user ends that daemon with Task Manager instead.

### (3) NFC/NFD and case folding

- **Existing mappings are unchanged.**
  - `entryName` still returns the exact name as soon as it is listed (`endpoint_unix.go:58-59`).
    On a case- and normalisation-sensitive volume, `EvalSymlinks` succeeding means every
    component is listed exactly, so the output is byte-identical to before.
  - The fallback branch now matches a superset of what it matched before. It returns a
    different entry only if an earlier sibling is equal under case+NFC folding but not under
    plain `EqualFold`. APFS and HFS+ forbid two such siblings in one directory.
- **Upgrades keep their keys.**
  - Released builds used the raw absolute dir as the account. `KeychainEntries.legacy` is still
    `accountFor(prefix, dir)` (`keychain.go:58-59`), so that fallback is intact.
  - A user whose canonical form changes (only an NFC-typed name over an NFD entry) also keeps
    access through the legacy fallback, because the previous canonical value was those same
    raw bytes.
  - The strings `KeychainEntriesFor(...).Entry(s)` produces are identical to the old
    `KeychainForEntry`: account+suffix, and legacy+suffix only when the two differ. I checked
    this by reading the code. The test is `TestKeychainEntriesSuffix`, which ran rather than
    skipped on Windows.
- **Exploitable aliasing?** Not found.
  - Go's `EqualFold` uses simple folding (for example, U+212A KELVIN folds to `k`), which can
    differ from the filesystem's own folding. That only matters when the exact name is not
    listed *and* an attacker created a fold-equal sibling in an ancestor directory, such as a
    home under `/tmp` or `/Users/Shared`.
  - The result would only change which account name is used inside the victim's *own*
    keychain. No cross-user access or secret exposure follows.
  - This existed before the change with `EqualFold`, and NFC adds no case that APFS or HFS+
    allows.
  - Endpoints on Unix do not use `Canonical`, so sockets are unaffected.

### (4) `golang.org/x/text` v0.42.0

- **Same version as before.** It moves from indirect to direct at the same v0.42.0. `go.sum`
  already pinned it (`go.sum:57-58`).
- **No new code shipped.** `x/text/unicode/norm` was already compiled in through
  `x/net/idna`; `GOOS=darwin go list -deps ./cmd/agentnetd` shows it both via vendor and
  directly.
- **govulncheck.** `govulncheck@v1.8.0` (the CI version) over ipc, paths, keystore, mailbox and
  cmd/agentnetd finds 0 reachable vulns.
  - Its only module-level hit is GO-2026-5932 in `x/crypto` v0.57.0, which is unrelated and not
    called.
  - The historical x/text advisories (GO-2020-0015, GO-2021-0113, GO-2022-1059) are all fixed
    long before v0.42. Dependabot already covers gomod.

### (5) Test quality and flake risk

- **`TestLockInstanceSeesPreLockDaemon` (unix).** Deterministic, with one caveat:
  - The connect completes into the backlog without `Accept`, and `checkPeer` needs no reply.
  - After `SetUnlinkOnClose(false)` plus `Close`, a connect gives `ECONNREFUSED`, which maps to
    `ErrNotRunning`, on both Linux and darwin.
  - The temp path (`MkdirTemp("", "dn")`) stays well under the 104-byte `sun_path`.
  - It hard-codes `socketName`, so it shares the drift blind spot in F8c-02.
- **`TestEntryNameFoldsNormalisation`.** A pure listing test, independent of the volume's
  insensitivity. The NFC and NFD constants differ in bytes: I checked with `od`, and they are
  `Caf\303\251` and `Cafe\314\201`. No flake risk.
  - It does not prove `Canonical` end to end on macOS, only `entryName`. Acceptable, since the
    resolver only composes `entryName`.
- **Windows tests.** Deterministic. Each `busy` case costs one 1 s dial timeout. The `elevated`
  case uses a DACL for SYSTEM only, which gives `ACCESS_DENIED` and so `ErrForeignOwner`,
  elevated or not.

## Findings

### F8c-01 · Info · confirmed-read: Unix `LockInstance` fails open on unexpected dial errors
- **Where:** `internal/ipc/transport_unix.go:57-67`; the earlier behaviour is at
  `transport_unix.go:93-107`.
- **What happens:**
  - On Windows, `probeLegacy` now refuses on everything except `ErrNotRunning`. On Unix,
    `LockInstance` treats any dial error other than `ErrForeignOwner` as "nothing there".
    Examples are `EAGAIN` from a non-blocking connect when an old daemon's listen backlog is
    full on Linux, or a `peerUID` read failure.
  - `listenLocked` then makes the same call and goes on to `os.Remove` the live socket of the
    old daemon, so two daemons share one DB.
- **Reach:** only the owner can fill the backlog of a `0600` socket in a `0700` dir, so no
  attacker gains anything. Practically unreachable, and the `listenLocked` half is older than
  this change.
- **Fix:** in `LockInstance`, return `ErrAlreadyRunning`-wrapped "socket in use but did not
  answer" for any error that is not `ErrNotRunning`, mirroring `probeLegacy`. Optionally, have
  `listenLocked` remove the socket only after `errors.Is(err, ErrNotRunning)`. Failing closed is
  safe on Unix because nobody else can reach the path.

### F8c-02 · Info · confirmed-read: socket name duplicated in `ipc` and `paths`
- **Where:** `internal/ipc/transport_unix.go:43` (`socketName`), `internal/paths/endpoint_unix.go:16`,
  `internal/ipc/lock_unix_test.go:18`.
- **What happens:** `LockInstance` dials `dir/socketName`, while `Listen` uses `p.Endpoint`,
  which `paths` builds. If either constant changes, or `paths` ever moves the socket (for
  example to shorten `sun_path`, which `known-limitations.md:122` hints at), F8b-01 silently
  reopens. The unix test builds its socket from `socketName` too, so it would still pass. This
  is the same self-reference blind spot as 60b M6.
- **Fix:** have `LockInstance` take the endpoint, as `LockInstance(p.Dir, p.Endpoint)`, or export
  one constant from `paths` and use it in `ipc`. Alternatively, add a unix daemon test that
  listens on `p.Endpoint`, runs `daemon.Run`, and asserts `ErrAlreadyRunning` with no DB
  created.

### F8c-03 · Info · confirmed-read: the account is now fixed per run, including a fallback one
- **Where:** `internal/keystore/keychain.go:58-59`, `internal/mailbox/mailbox.go:97`.
- **What happens:** if `paths.Canonical` falls back to the absolute path at `mailbox.New`, the
  whole run uses the legacy-spelled account. That happens on an `EvalSymlinks` error other than
  not-exist on an existing dir.
  - Keys stored under the canonical account then read as `ErrNotFound`, `usable` reports them
    lost (`mailbox.go:261`), and the daemon rotates.
  - Before the change the same failure hit one call; now it lasts the whole run.
  - The identity keystore has always behaved this way.
- **Reach:** practically unreachable, since the daemon has just created and opened the dir.
  No security impact: it only affects availability of old mail.
- **Fix:** none needed. Optionally, log a warning when `Canonical` falls back.

### F8c-04 · Info · confirmed-read: the old-CLI hint for an elevated daemon is unverified
- **Where:** `internal/ipc/transport_windows.go:138`.
- **What happens:** the hint tells the user to run the old `agentnetd stop` from an elevated
  terminal. If the old `Dial` checked the pipe owner against the user SID, even an elevated old
  CLI would refuse an Administrators-owned pipe.
- **Fix:** check the pre-357c9f4 `transport_windows.go` with read-only git. If the old CLI
  cannot stop it, add "or end agentnetd.exe in Task Manager" to the hint and to
  `known-limitations.md`.

## Commands run
- `diff -rq` / `diff -u --strip-trailing-cr` main checkout vs worktree (no git).
- `go test ./internal/ipc ./internal/paths ./internal/keystore ./internal/mailbox -count=1`
  with `DORYLINAE_KEYSTORE=file`: ok ×4. The keystore tests use `keyring.MockInit`.
- `go test ./internal/daemon -run 'Lock|Instance' -count=1 -v`: PASS, including
  `TestRunStopsBeforeStoreWhenLocked`. `go test ./cmd/agentnetd -count=1`: ok.
- The new tests with `-v` all PASS, none skipped: `TestLegacyEndpointVector`,
  `TestListenBusyPipeOwnerUnknown`, `TestLockInstanceRefusesUnansweredLegacyPipe/{elevated,busy}`,
  `TestKeychainEntriesSuffix`, `TestAlreadyRunningWithoutAnswerGivesCause`.
- `GOOS={linux,darwin,freebsd} go vet` over ipc, paths, keystore, mailbox, daemon and
  cmd/agentnetd: clean. The unix tests themselves run only in CI.
- `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 [-show verbose] <pkgs>`: 0 reachable.
- `sha256sum` of the vector dir; `od -c` of the NFC/NFD test constants.
- Not done: mutation runs. The Windows subtests fail on removing either refusal branch by
  inspection, but this was not executed.
