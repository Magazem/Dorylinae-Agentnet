# 87: R55-F21 security review

Reviewer: R55-F21sec-Opus · model claude-opus-5-5 · 2026-10-01 · worktree `AgentNet-wt/r55-f21`,
branch `p4/r55-f21`, ticket commit `2423b7e` on `main` `2a50862`. This is a report only. I added
three proof tests and one probe (listed at the end). I read them against
`Docs/review/55-code-review/99-report.md` (R55-092, 091, 155, 089, ticket R55-F21),
`Docs/protocol/agent-card.md` §Key storage and `Docs/protocol/mail.md`. The real OS keychain
was never touched: every test uses the go-keyring mock or the in-package fake keychains.

## Verdict

**Changes requested: two Medium findings, both regressions against `main` introduced by this
ticket.** Fix them before merge, or record an owner decision accepting them.

- **M1.** "Newer (less preferred) wins" lets anything that can write files in the config dir,
  but cannot touch the keychain, outrank the keychain copy. For the identity key this becomes
  a silent identity substitution (proved).
- **M2.** `Store.Delete` now reports success while a locked keychain still holds the secret.
  The mailbox then marks the key deleted and never retries, which breaks the mail.md promise
  that the private key is deleted at `not_after + 7 d` (proved). Before this ticket the delete
  failed and the hourly job retried it.

The rest of the ticket does what it claims:

- the private-from-creation temp file on Windows;
- `File.Get` refusing a non-private key file;
- the directory fsync;
- the outage-aware identity start-up;
- the Windows DACL logic. It cannot lock the owner out and does not follow junctions (probe).
  One gap (L1) remains.

## Findings

### M1 (Medium): a file-only writer outranks the keychain; identity substitution

`internal/keystore/keystore.go:44-62` (`Load`: on conflict the later backend wins, `:52`).
Also `internal/identity/identity.go:129` (key without card → card re-created for that key).

- **Answer to the brief's question.** The file backend's privacy check *is* applied before
  that copy can win. `File.Get` → `checkOwnerOnly` refuses a key file that is not private:
  - Unix: any group/other mode bit;
  - Windows: an owner other than self/SYSTEM/Administrators, or an allow ACE for anyone else
    (`perm_windows.go:70`, `paths.CheckPrivate`).
  So **another local user cannot plant a winning key**: the planted file is owned by them, and
  the directory is 0700 or private. **The same user can.** That includes any process with file
  access only, for example a coding-agent harness sandboxed to the file system, or an app
  that macOS keychain item ACLs would otherwise keep out.
- **Scenario (proved by `internal/identity/zz_review87_f21_test.go`).**
  1. The identity key is in the keychain.
  2. The attacker writes a 0600 / owner-only `identity.key` holding its own seed.
     - If the card stays, start-up fails with "stored private key does not match": a denial of
       service.
  3. The attacker also deletes `agent-card.json`.
  4. `LoadOrCreate` takes the file key, re-creates and signs a card for it, and runs as an
     identity whose private key the attacker knows. The audit shows only
     `identity.create {key_generated:false, key_backend:"file"}`. The real key stays,
     orphaned, in the keychain. Before this ticket the keychain copy won, and the card was
     re-created for the real key.
- **Side effect: legacy state after upgrade (webhook).** The pre-F21 `Save` never deleted
  other copies. Take this sequence: rotation during an outage leaves S1 in the file; a later
  rotation with the keychain up writes S2 to the keychain and leaves S1 in the file. After the
  upgrade, `Load` returns the **stale** S1. The "less preferred = newer" invariant holds only
  for states written by the new `Save`.
- **Fix direction.** Do not settle a conflict by backend order. Have `Load` report a conflict
  (`ErrConflict` with both values, or a caller-supplied `verify func([]byte) bool`) and let the
  caller choose against data it already authenticates:
  - identity: the copy whose public key matches the card. With no card and two different
    copies, fail and do not adopt either;
  - mailbox: the row's `pub`;
  - webhook: a hash of the secret kept in settings.

  A marker file naming "the last backend saved" does not help, because the same attacker can
  write it.

### M2 (Medium): delete during a keychain lock reports success; a mailbox key is never deleted

`internal/keystore/keychain.go:122` (every Delete failure becomes `ErrUnavailable`) together
with `keystore.go:129` (`Store.Delete` skips `ErrUnavailable`). Caller:
`internal/mailbox/mailbox.go:480` (`deleteKey` marks the row `deleted` once `ks.Delete()` is nil).

- **Scenario (proved by `internal/keystore/zz_review87_f21_test.go`).**
  1. A mailbox key is in the keychain.
  2. At `not_after + 7 d` the hourly job runs while the macOS login keychain is locked, or the
     Secret Service times out after 5 s.
  3. `Store.Delete` returns nil, the row gets `deleted`, and no later job revisits it.
  4. The X25519 private key stays in the keychain indefinitely. mail.md :26 and :53 make
     forward secrecy depend on that deletion.
- **Why the documented residual does not cover this.** The residual says the copy "stays there
  until the next Save overwrites or outranks it". A mailbox key has its own account per
  `key_id`, so no later Save ever writes to that account and the residual never ends. Before
  F21 the raw keychain error failed `deleteKey` and the job retried every hour. T3-02 (headless
  Linux, no Secret Service) was the reason for the change, but that case is "no service", not
  "locked".
- **Fix direction.**
  - Distinguish a *permanently absent* keychain from a *transient* failure. Absent means
    go-keyring's D-Bus connect error or "no secret service"; transient means locked or timed
    out. Map only the first to "skip on Delete".
  - Better still, record the backend `Save` returned in the mailbox row. Have `deleteKey`
    leave the row live (and retry) while that backend is unavailable.
  - The webhook secret after `--webhook off` lingers the same way. That is lower stakes, but
    the same fix covers it.

### L1 (Low): inherit-only ACEs on the config dir are ignored; new files inherit them

`internal/paths/private_windows.go:91` skips `INHERIT_ONLY_ACE` entries.

- **Effect.** For the directory's own access that is right. But such an entry decides what
  every file later created in the directory gets.
- **Scenario (proved by `internal/paths/zz_review87_f21_windows_test.go`
  `TestReview87InheritOnlyAceOnConfigDir`).**
  1. The directory carries `Users:(OI)(CI)(IO) GENERIC_READ`.
  2. `CheckPrivate(dir)` passes, so `Ensure` changes nothing and doctor says "owner-only".
  3. `dorylinae.db` created afterwards is readable by `S-1-5-32-545`.
- **What stays safe.** Key files carry their own protected DACL.
- **What is exposed.** The database, logs and anything else written with an inherited DACL.
- **Fix direction.** When checking a directory, treat an inherit-only ACE with OI or CI as
  access to future contents. Map `CREATOR OWNER` to the creator, i.e. the current user.

### L2 (Low): a host with no keychain service is treated as "locked" everywhere

Root: `keystore.go:54-66`, any `ErrUnavailable` makes the result "unavailable". On headless
Linux without a Secret Service, every `Load` of a secret the file does not hold now returns
`ErrUnavailable` rather than `ErrNotFound`.

- Identity: when `identity.key` is really lost and the card exists, the error says "unlock the
  keychain and start again" (`identity.go:135-142`). There is no keychain, and the recovery
  hint ("restore it or delete agent-card.json") is gone.
- Doctor: warns "keychain unavailable" instead of failing "key lost".
- Mailbox: `usable` returns true (`mailbox.go:252-254`), so a really lost key is kept and
  announced for up to 7 days, and mail sealed to it cannot be opened.

The documented residual is acceptable for a transient lock, but it is certain on such hosts.
Same fix as M2: distinguish "no service", or use the backend recorded at Save.

### L3 (Low): a broken leftover file copy now blocks a good keychain copy

`keystore.go:44-58` reads every backend, and a non-`NotFound`/`Unavailable` error aborts.

- **Scenario.** A leftover `identity.key` or `<key_id>.key` with mode 0644, or a widened DACL
  on Windows (now refused by R55-089), stops identity start-up or webhook signing
  (`no_secret`). This happens even when the keychain holds the right key. Before, the keychain
  hit returned early.
- **Assessment.** Failing closed is defensible: a key file others can read means the key may be
  exposed.
- **Fix direction.** The error should name the file and say that the keychain holds a copy, so
  deleting the file is the fix.

### L4 (Low): `Ensure` rewrites the DACL of any shared directory chosen as home

`private_windows.go:119-139`. If `DORYLINAE_HOME` points to an existing shared directory (a
team or project folder that grants Users or a group), `Ensure` silently replaces its DACL with
`D:P(A;OICI;FA;;;<self>)`. `SetNamedSecurityInfo` propagates this to every child with
inherited ACEs. Colleagues, SYSTEM-run services and Administrators lose access.

What does not happen:

- no data loss;
- no owner lockout (owner and self keep FA; admins can take ownership);
- children with explicit or protected DACLs are untouched;
- **the walk does not follow junctions**. The probe `TestReview87EnsurePropagationThroughJunction`
  found the junction target's DACL unchanged; only the junction's own DACL was rewritten.

The default `%AppData%\…` directory is SYSTEM/Administrators/user, all trusted, so it is never
rewritten. The "DACL rewrite propagation" residual is acceptable for that default.

Fix direction:

- rewrite only a directory `Ensure` just created, or one holding nothing but Dorylinae files;
- otherwise refuse with the doctor's fix text;
- log the rewrite.

Unverified: a home on FAT/exFAT or an SMB/NAS share. `CheckPrivate` may report a null DACL or
a foreign owner, `SetNamedSecurityInfo` may fail, and the daemon would then refuse to start
where it started before.

### Info

- **I1.** `file.go:78-81`: if `syncDir` fails after a successful rename (Unix; only `EINVAL` is
  tolerated, not `ENOTSUP` or an `EIO` on FUSE/NFS), `WriteOwnerOnly` returns an error although
  the new key is in place.
  - Example: `RotateSecret` reports failure, yet `Load` now returns a secret the user never saw.
  - Fix direction: tolerate `ENOTSUP` too, or report the dir-sync failure as a warning.
- **I2.** `harmlessAccess` includes 0x20, which is `FILE_EXECUTE` on a file. Execute-only allows
  image mapping of a valid PE only, so a base64 key file cannot be read that way. Harmless; a
  comment would help.
- **I3.** A crash between create and rename leaves a private `.secret-*.tmp`. It is owner-only on
  both OSes and pre-existing; nothing cleans it up.

## Checked (no finding)

- **No world-readable key material and no temp leaks.**
  - Windows `createPrivateTemp` (`perm_windows.go:35-63`) passes `D:P(A;;FA;;;self)` in
    `SECURITY_ATTRIBUTES` to `CreateFile` with `CREATE_NEW` and share mode 0. The DACL is
    correct from the first instant, and no second handle can be opened; the test proves this.
    The protected DACL survives the rename.
  - Unix `CreateTemp` is 0600, and the explicit chmod is redundant (umask can only tighten).
  - On every error path the temp file is closed and removed (`file.go:63-68`).
- **No silent identity fork from an outage.**
  - Card present + keychain unavailable → error, nothing written (`identity.go:135-142`).
  - A non-private or unreadable file → error.
  - No card + unavailable → a new key goes to the file. When the keychain comes back, the file
    wins, which matches the new card; the old keychain key is orphaned, but no card refers to
    it.
  - No path generates a key while a card exists.
- **Windows `CheckPrivate`.**
  - Deny ACEs are ignored, which is safe because they only restrict.
  - Object ACEs are flagged and callback ACEs are treated as allow (conservative).
  - Generic rights fall outside `harmlessAccess`, so they are flagged (conservative).
  - A NULL or absent DACL is flagged.
  - `CREATOR OWNER` and `OWNER RIGHTS` map to the owner.
  - AppContainer SIDs are skipped correctly, since access also needs the user check.
  - An Administrators-owned file (elevated creation) is accepted, consistent with root on Unix
    and documented.
  - `secureDir` keeps the owner and grants self FA OICI, so the owner cannot be locked out. A
    directory owned by another user is refused, never rewritten.
- **`Save` copy removal.** A failure to remove a less-preferred copy errors, and the copy is
  then ranked below, consistent with `Load`. A failure to remove a more-preferred copy is
  ignored, and `Load` then ranks the new copy first. This is correct under the current
  `Load` rule; it changes if M1 is fixed.
- **Build tags.** `private_unix.go` (`!windows`) and `private_windows.go` (`windows`).
  `GOOS=linux` and `GOOS=darwin go vet` of keystore, paths, identity, mailbox and cmd/agentnet
  are clean.
- **Targeted tests** (`-skip Review87`) pass: keystore, paths, identity, mailbox, and
  `cmd/agentnet -run Doctor`.

## Residuals (implementer's list)

| Residual | Acceptable? |
|---|---|
| Keychain copy left at Delete when unavailable | Identity and webhook: yes. **Mailbox: no (M2).** |
| Lost mailbox key kept up to 7 days | Transient lock: yes. Host with no keychain service: no (L2). |
| DACL rewrite propagation | Yes for the default dir; no junction traversal (probe). Shared-home case: L4. |

## Files I created

- `internal/identity/zz_review87_f21_test.go`: M1 proof. Fails while the finding stands.
- `internal/keystore/zz_review87_f21_test.go`: M2 proof. Fails while the finding stands.
- `internal/paths/zz_review87_f21_windows_test.go`: L1 proof (fails while the finding stands)
  and the junction probe (passes: no finding). The junction and its target are both inside the
  test's own `TempDir`. The junction is removed non-recursively before the temp dir cleanup.
- `Docs/review/87-r55-f21-security.md`: this report.
