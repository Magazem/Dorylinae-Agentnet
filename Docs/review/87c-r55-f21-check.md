# 87c: R55-F21 focused check (fix round 3)

Reviewer: R55-F21check-Opus · model claude-opus-5-5 · 2026-10-02 · worktree
`AgentNet-wt/r55-f21`, branch `p4/r55-f21`, commit `7678665`. Report only. I added one probe
test file (listed at the end). The real OS keychain was never touched; every test uses the
go-keyring mock, and the Doctor tests ran with `DORYLINAE_KEYSTORE=file`.

## Verdict

**Approve.** N1, N2, N3 and N4 from 87b are fixed as briefed, or within the documented and
accepted limit. No Medium or higher finding remains. Two Low findings and three Info notes
follow. None of them blocks merge.

| # | Severity | Title |
|---|---|---|
| C1 | Info (accepted) | The marker residual is real, bounded and documented |
| C2 | Low | Rows with `key_backend` NULL (from before migration 26, or imported by `importLegacy`) still use the old Delete semantics |
| C3 | Low | The `MaxLive` sweep stops at the first failed delete, so the 3-live-key cap can be exceeded |
| C4 | Info | Error-ignoring `ks.Delete()` rollbacks can leave orphaned mailbox secrets (not new in this round) |
| C5 | Info | N4 fix text: no lock-out, but it does not use `/T` and it drops the SYSTEM and Administrators entries |

## Check results

**(1) Identity substitution.** `identity.go` `loadKey`:

- With the keychain readable and holding the real key, a replaced card plus a planted file
  key gives `ErrConflict`, with or without the marker. Probe
  `TestReview87cMarkerDeletedKeychainReachable` passes.
- With the keychain locked or showing no service, the marker present gives `errKeychainUnread`
  (covered by existing tests).
- With the marker deleted and the keychain locked, the planted identity runs. Probe
  `TestReview87cResidualMarkerDeletedLocked` logs `RESIDUAL: runs as planted=true ... from file`.
  This is the documented limit (`Docs/protocol/agent-card.md` §Key storage, "Accepted
  limit").

**A stray file key is never used.** `chosen` must match the card. A file key that differs
from a card-matching keychain key is never returned. With the keychain locked or showing no
service, no copy matches, so `LoadKey` fails (`ErrUnavailable`), even with the marker deleted.
Probe `TestReview87cStrayFileKeyNeverUsed` passes.

**(2) Single read path.** I grepped for `.Load()`, `LoadMatching`, `.Read()` and
`NewKeyFromSeed` (non-test code). Every identity secret read goes through `identity.LoadKey`
or `LoadOrCreate`:

- daemon `identity_key.go:71`, which `idKey.Sign` and `idKey.Priv` use for the relay
  challenge, Noise sessions (`daemon.go:372`), mailbox announcements, the outbox, the receiver
  and grants;
- doctor (`doctor.go:354`).

`relayclient.NewKeystoreSigner` is gone. The one remaining plain `ks.Load()`
(`doctor.go:356`) runs only when there is no card. It is a read-only report: the seed is
cleared and nothing is signed. OK.

**(3) Migration 26 and `DeleteSaved`.**

- `deleteKey` passes `r.backend`. A keychain row is not marked deleted while the process sees
  no service, and is retried hourly. Time-based deletion holds: `MailboxKey` refuses any key
  past `not_after+7d` whether or not the row is marked deleted, and once the keychain is
  reachable the delete goes through (`noservice_test.go`).
- All seven rewind sites drop `key_backend` before replaying: `store_test.go` ×2,
  `migration19/22/24_test.go`, `review79_migration23_test.go` and `audit/chain_test.go`. The
  store tests pass.

**(4) N4.** The SID comes from the process token (`private_windows.go:45`). The daemon runs as
the user (schtasks), so it is the same principal who types the commands. `/reset` followed by
`/inheritance:r /grant:r *SID:(OI)(CI)F` leaves full control for the user, inherited to
children. The owner also keeps implicit `WRITE_DAC`, so no lock-out is possible. The test
touches only `testutil.TempDir` (`os.MkdirTemp`) and passed through real `cmd` and PowerShell.

## Findings

### C1 (Info, accepted): marker residual

`identity.go:323`. Scenario: the keychain is locked or unreachable; a process that can write
files deletes `identity.keychain`, replaces the card and plants a matching `identity.key`. The
daemon then runs as the planted key.

Why it is bounded:

- It lasts only for one process (the key is cached in `identityKey`).
- The next start with a readable keychain refuses with `ErrConflict`.
- Peers pin the real public key, so the planted identity cannot impersonate this agent to
  them. The gain is local only.

It cannot be closed without trusted state outside the config dir. Acceptable as documented.

### C2 (Low): NULL `key_backend` keeps the 87b N3 behaviour

`mailbox.go:500`, `keystore.go:225`. For pre-26 rows, `r.backend == ""` becomes `Delete()`,
which skips `ErrNoService`. A pre-upgrade keychain key swept by a daemon that sees no D-Bus is
then marked deleted while its secret stays in the user's keychain, and it is never retried.
The window is up to 21 days after the upgrade.

The same applies to rows from `importLegacy` (`mailbox.go:574`). Those rows are inserted
without `key_backend`, although `k.load` at `:563` knows the backend.

**Fix:**

- Record the backend in `importLegacy`; `load` already returns it, but the value is discarded.
- Optionally treat NULL as `"keychain"` when the mode is auto and a keychain backend exists.
  This is conservative, at the cost of hourly retries on hosts that never had a keychain.
- Otherwise document it as a residual.

### C3 (Low): one undeletable row blocks the `MaxLive` cap

`mailbox.go:478-482`. If `deleteKey(keep[0])` fails (a keychain row while there is no service),
the loop `break`s. Newer, file-backed keys over the cap are then not deleted early, and more
than 3 keys stay able to decrypt. Each is still deleted at its own `not_after+7d`, so this
does not affect the time-based deletion rule.

**Fix:** skip the failed row and continue with the next one; do not stop the loop.

### C4 (Info, not new in this round): rollback deletes ignore errors

`mailbox.go:420-437` and `:560`. These are `_ = ks.Delete()` calls, and no row records the
secret.

- When `createLocked` fails after `Save`, a delete that fails because the keychain is locked
  leaves an orphaned secret that nothing will ever sweep. It is never used, because no row
  points to it.
- The legacy expiry path removes `current.json` even if the delete fails.

**Fix (later):** use `DeleteSaved(backend)`, and keep `current.json` when the delete fails.

### C5 (Info): N4 fix text

`paths.go` `FixCommands`. The commands do not use `/T`, so explicit (non-inherited) entries on
files already in the dir remain. Bypass-traverse means such a file stays readable by its full
path. Key files are protected by their own `OwnerOnly` check, so this is acceptable.

`/inheritance:r` also drops SYSTEM and Administrators, which `CheckPrivate` would accept. That
is harmless to the user. A sentence could mention it.

## Tests run

All pass:

- `go test ./internal/{keystore,paths,identity,mailbox,relayclient,store}`
- `./internal/daemon -run 'Identity|Signer|StrayKey|ReplacedCard|Sessions|MailKey'`: 6 passed
- `./cmd/agentnet -run Doctor` (with `DORYLINAE_KEYSTORE=file`)
- `TestSharedDirFixCommandsWork/{cmd,powershell}`

## Files created

- `internal/identity/zz_review87c_f21_test.go`: three probes, all mock keychain
- `Docs/review/87c-r55-f21-check.md`: this file
