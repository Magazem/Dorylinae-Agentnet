# 87b: R55-F21 security re-review

Reviewer: R55-F21rereview-Opus · model claude-opus-5-5 · 2026-10-02 · worktree
`AgentNet-wt/r55-f21`, branch `p4/r55-f21`, fix commit `bf5f262` (review 87 fixes) on top of
`2423b7e`. Report only. I added two proof-test files (listed at the end). The real OS keychain
was never touched; every test uses the go-keyring mock.

## Verdict

**Changes requested: one Medium and one Low-Medium finding remain.** M2, L1, L3 and the
webhook API change are fixed. The fix for M1 covers card deletion but not card replacement.
One security-relevant caller still uses plain `Load()`, and today that call is the only thing
that blocks the substitution end to end. L2 and L4 are fixed as designed; one residual each is
noted below.

| # | Severity | Title |
|---|---|---|
| N1 | Medium | M1 is still open when the card is **replaced**: the card is file-writable "trusted data" |
| N2 | Low–Medium | The Noise signer still uses plain `Load()`: a stray key file blocks start-up, and the call hides N1 |
| N3 | Low | "No service" in this process does not mean no secret: Delete can still drop a keychain mailbox key (M2 residual) |
| N4 | Low | The `SharedDirError` fix text does not remove explicit ACEs and uses `%USERNAME%` |
| I1–I3 | Info | see below |

## Findings

### N1 (Medium): a file-only writer substitutes the identity by replacing the card

`internal/identity/identity.go:117-125`. With a card present, the key is chosen by
`LoadMatching(SeedMatches(card.PublicKey))`. The card is `agent-card.json`, which is
**self-signed** (`agentcard.Verify` checks only that it is signed by its own `public_key`) and
sits in the same config dir as `identity.key`. The attacker in M1 is "same user, file access,
no keychain access". That attacker can write a new card just as easily as it can delete one.

- **Scenario, keychain reachable** (proved by
  `internal/identity/zz_review87b_f21_test.go`):
  1. The real key is in the keychain.
  2. The attacker writes its own seed to `identity.key` (0600) and an `agent-card.json`
     signed by that seed.
  3. `LoadOrCreate` returns the attacker's identity from `file`. The keychain answered and
     holds the different, real key, but it is ignored as "a copy that does not match".

  End to end, the daemon still refuses to start today. That is only because
  `newSessions` → `relayclient.NewKeystoreSigner` calls plain `Load()`, which returns
  `ErrConflict` (N2). Once N2 is fixed by switching that caller to the matching load, this
  substitution goes live.
- **Scenario, keychain locked or no service at start-up** (proved end to end by
  `internal/daemon/zz_review87b_f21_test.go` `TestReview87bReplacedCardLockedKeychainEndToEnd`):
  1. The same replaced card and planted file.
  2. The keychain mock returns "locked".
  3. `LoadOrCreate` succeeds as the attacker.
  4. `newSessions` succeeds too: plain `Load` returns the only readable copy.

  The daemon then signs Noise bindings, relay challenges and mailbox announcements with the
  planted key. This locked-keychain variant also exists on `main` (its `Load` fell through to
  the file when the keychain was unavailable), so it is not a regression. But
  `Docs/protocol/agent-card.md` §Key storage now claims protection that does not hold.
- **The same weakness in the other anchors.** The mailbox row `pub` (`mailbox_keys_own`) and
  `secret_sha256` (`notify.webhook`) are both in `dorylinae.db`, in the same file-writable
  directory. The impact there is low:
  - mailbox: a writer of the DB can already read the stored mail;
  - webhook: a planted secret the receiver does not know only breaks delivery.

  Identity is the case that matters.
- **Answers to the brief.**
  - "Delete the card AND plant a matching file, keychain unavailable": blocked. The no-card
    branch refuses when a backend is `Unavailable` (`identity.go:149`).
  - Blocked only if the keychain classifies as `Unavailable`. When it classifies as
    `NoService` (`ErrNoService` is not counted at `identity.go:149`), the planted file is
    adopted. Example: the daemon is started from ssh, cron or a session without the D-Bus
    session bus, while the real key sits in the desktop keychain. With no card, that is a
    substitution, and the card is then re-created for the attacker's key.
  - "Every backend answered and agrees" is sound for a merely *locked* keychain: it lands in
    `Unavailable` and start-up refuses. It is not sound for a keychain this process cannot
    see (N3's classification).
- **Fix direction.**
  - When any backend *answered* with a key that differs from the card-matching one, refuse
    (a conflict) instead of silently ignoring it. That closes the reachable-keychain case.
    The error must say which copy matches the card. For the rare legitimate orphan (a key
    created into the file during an outage), it tells the user to delete the keychain entry,
    not the file.
  - Accept and document the locked/no-service residual: with the keychain unreadable, a
    file-only writer cannot be told apart from a legitimate file fallback. In the no-card
    branch, treat `NoService` like `Unavailable` when this home has ever stored to the
    keychain. Note that any marker recording that is also file-writable, so this only
    narrows the window.
  - Correct the agent-card.md wording ("could otherwise plant its own key and take over
    the identity") to state the residual.

### N2 (Low–Medium): plain `Load()` on the identity key at daemon start

`internal/relayclient/signer.go:43`, called from `internal/daemon/ping.go:52`
(`newSessions`, `daemon.go:372`). This is the one remaining plain-`Load()` caller on a
security secret. `grep` finds no others outside tests: identity, doctor, `identityKey`,
mailbox and webhook all use `LoadMatching` or `Read`.

- **Scenario** (proved by `TestReview87bStrayKeyFileBlocksSessions`):
  1. The card is present and the keychain holds the matching key.
  2. A stray `identity.key` holds another key.
  3. `LoadOrCreate` accepts the keychain copy, as documented in the agent-card.md lifecycle
     row "a stray identity.key is ignored".
  4. `newSessions` then fails: `noise: sign static key: … the backends hold different
     secrets (keychain, file); remove the stale copy`.

  Result: daemon start-up fails, contradicting the doc. The legitimate orphan state does the
  same (an outage-created file identity plus an old keychain key). There the message
  "remove the stale copy" names both backends without saying which one is stale. If the user
  deletes the file, which in that case holds the *current* key, the identity is lost.
- **Fix direction.** Have `NewKeystoreSigner` load with the card-matching check, or pass the
  daemon's `identityKey.Sign` to `noise.NewStatic` (it is already built a few lines earlier,
  `daemon.go:347`). **Fix together with N1**, otherwise N1 goes live in the reachable case.
  Make every `ErrConflict` message name the copy that matches.

### N3 (Low): "no keychain service" is judged per process, not per host (M2 / L2 residual)

`internal/keystore/keystore.go:233` (Delete skips `ErrNoService`) and `keychain.go:146-164`
(the `noServiceSigns` strings). Caller: `internal/mailbox/mailbox.go:494`.

- **Classification per OS** (checked against go-keyring v0.2.8 in the module cache):
  - **Linux:** `NoService` covers "couldn't determine address of session bus", the
    `dbus-launch` failure, and `ServiceUnknown` / "not provided by any .service files". A
    locked collection fails in `Unlock`/prompt handling, and a timeout returns the
    `withTimeout` error. Both are `Unavailable`, and so is `Spawn.ChildExited` or `NoReply`.
    Correct, and conservative for activation failures.
  - **macOS:** go-keyring runs `/usr/bin/security` and maps any output containing "could not
    be found" to `ErrNotFound`. That includes errSecNoSuchKeychain ("The specified keychain
    could not be found") and "A default keychain could not be found". So "no keychain" is
    already *not found* (a correct absence). errSecInteractionNotAllowed (locked or headless)
    comes back as a bare `exit status 36` → `Unavailable` → delete fails and is retried.
    Correct. No macOS string can hit `noServiceSigns`.
  - **Windows:** wincred `ERROR_NOT_FOUND` → not found. Everything else, including
    `ERROR_NO_SUCH_LOGON_SESSION`, → `Unavailable`, never `NoService`. That fails closed. The
    scheduled task uses `InteractiveToken` (`internal/service/schtasks.go:77`), so the
    credential manager is present in normal use.
  - **Can a misclassification silently drop a deletion?** Not for "locked" on any OS. It can
    when the *process* has no session bus or Secret Service but the same user's keychain does
    exist and holds the key.
- **Scenario (Linux).**
  1. Mailbox keys were created while the daemon ran in the desktop session (keychain).
  2. Later the daemon runs from ssh, cron or a systemd user unit without
     `DBUS_SESSION_BUS_ADDRESS`. Or gnome-keyring or kwallet is restarting and not
     D-Bus-activatable, which gives a transient `ServiceUnknown`.
  3. `usable` → `ErrNotFound` → the key is replaced at once (harmless: one rotation).
  4. At `not_after + 7 d`, `deleteKey` → `NoService` is skipped → the row is marked
     `deleted`, while the X25519 private key stays in the desktop keychain for good. That
     breaks the mail.md forward-secrecy promise, as M2 did, in a narrower case.
  - Identity with a card: `ErrKeyLost` and no replacement. But the hint "delete
    agent-card.json to start a new identity" may lead the user to destroy an identity whose
    key is merely out of reach of this process.
  - Identity without a card: see N1 (the planted file is adopted).
- **Fix direction.** Record the backend `Save` returned in the mailbox row (and, for the
  identity, in the audit detail that already exists). `deleteKey` accepts `NoService` as
  done only for a row whose key went to `file`. For a `keychain` row it keeps the row live
  and retries. In the identity `ErrKeyLost` text, when the keychain is `NoService`, mention
  that the key may be in a keychain this session cannot reach.

### N4 (Low): the `SharedDirError` fix text is incomplete

`internal/paths/paths.go:126` and the matching text in agent-card.md.

- **What the command misses.** `icacls <dir> /inheritance:r /grant:r "%USERNAME%:(OI)(CI)F"`
  removes inherited ACEs and resets only the current user's grant. An *explicit* allow ACE
  for Users or a group (common on team and OneDrive folders) stays. The daemon still
  refuses, and the user has no next step.
- **PowerShell.** `%USERNAME%` does not expand in PowerShell, the default Windows shell
  (it needs `$env:USERNAME`).
- **Fix direction.** Suggest `icacls <dir> /inheritance:r /grant:r "<DOMAIN\user>:(OI)(CI)F"`
  with the resolved account name filled in, followed by `/remove:g <each SID reported>`. Or
  just recommend a new `DORYLINAE_HOME`.

**L4 upgrade impact (brief question).**

- The default `%AppData%\…` dir is private (SYSTEM/Administrators/user), so it is never
  touched.
- `main` never secured the Windows dir; R55-089 is this ticket. A user on `main` with
  `DORYLINAE_HOME` pointing at an existing shared, non-empty folder will find that
  `agentnetd` and `agentnetd install` (`cmd/agentnetd/install.go:143`, `daemon.go:268`)
  **refuse to start** after the upgrade. `agentnet doctor` still runs and warns.

I consider that acceptable: it fails closed, it is explicit, nothing is rewritten, and the
dir really is readable by others. Two conditions: the release notes must mention it, and N4's
text must work. An empty pre-existing dir is still rewritten, which is fine.

### Info

- **I1.** `private_windows.go:96-98`: the comment says an inherit-only ACE "on a file has no
  effect at all", but the code also flags OI/CI inherit-only ACEs on files. That is harmless:
  it errs toward refusal, and key files carry a protected DACL with no such ACEs. Either align
  the comment or skip the check for non-directories.
- **I2.** Webhook config from before this change (no `secret_sha256`) works while the backends
  agree. It goes to `no_secret` only on differing copies, as documented. The hash is never
  backfilled. Consider writing it on the first successful load where every backend answered
  and agrees.
- **I3.** `Keychain.Set` errors (`keychain.go:104`) are not passed through `unavailable()`. So
  `Save`'s `skipped` and the `keychain_error` audit detail carry the raw error. That is
  cosmetic; `Save` treats every failure the same.

## Verified fixed (no finding)

- **M1 core** (`keystore.go:104-156`): backends are no longer ranked. `Load()` returns
  `ErrConflict` on differing copies. `LoadMatching` picks only a copy the check accepts.
  `Broken` does not hide a matching copy (L3), and its error names the backends that hold a
  copy.
- **Identity, no card** (`identity.go:144-162`): `Distinct` is refused; a copy plus
  `Unavailable`/`Broken` is refused. Reviewer 87's delete-card scenario now fails as intended
  (`TestPlantedKeyFileDoesNotReplaceIdentity`).
- **`identityKey.read`** and doctor use the card match. Mailbox `load`/`usable`/
  `MailboxKey`/`importLegacy` match on the row `pub`. Webhook `attempt` matches
  `SecretSHA256` with a constant-time compare.
- **Webhook API.** `RotateSecret` returns `(printable, hash, err)`. Its only callers,
  `daemon/notify.go:268` and `notify/webhook_test.go:67`, are updated. The hash is persisted
  through `SetWebhook`. `--webhook off` clears the config, URL/format edits keep the existing
  hash, and the queue rows carry no secret.
- **M2.** Only `ErrNotFound` and `ErrNoService` are skipped on `Delete` (`keystore.go:233`).
  Locked or timed-out deletes fail, `deleteKey` leaves the row live, and the hourly `Rotate`
  retries. The per-OS classification is above (N3).
- **L1.** Inherit-only ACEs with OI/CI are evaluated, and `CREATOR OWNER` inherit-only maps
  to the current user. `CREATOR GROUP` is not mapped and is flagged (conservative).
- **L2.** A host without a service → `ErrNotFound` → lost key reported; mailbox replaces the
  key at once. Residual: N3.
- **Targeted tests** (`-skip Review87b`) pass: keystore, paths, identity, mailbox, notify,
  `daemon -run 'Identity|Webhook|Notify'`, and `cmd/agentnet -run Doctor`.
  `GOOS=linux go vet` (keystore, paths, identity, mailbox) and `GOOS=darwin go vet`
  (keystore, paths) are clean.

## Files I created

- `internal/identity/zz_review87b_f21_test.go`: N1 proof (replaced card, keychain
  reachable). Fails while the finding stands.
- `internal/daemon/zz_review87b_f21_test.go`: N2 proof (`TestReview87bStrayKeyFileBlocksSessions`)
  and N1 end-to-end proof with a locked keychain
  (`TestReview87bReplacedCardLockedKeychainEndToEnd`). Both fail while their findings stand.
- `Docs/review/87b-r55-f21-rereview.md`: this report.
