# Review 100b: R55-F28 independent re-check (clock-step robustness, fix round)

Branch `p4/r55-f28` (head dbe18da), worktree `AgentNet-wt/r55-f28`. Inputs: plan 98, review 100
(including "Fixes applied"), owner decision D75. I used no git: I diffed the changed files
against the main checkout (`AgentNet`, 7ef24ff) with `diff -u --strip-trailing-cr`.

## Verdict

**Changes requested (small: the spec and the plan must be corrected; a code fix is
optional).** M1, L1 and L2 are closed for the scenarios in review 100, and the `mail_seen`
basis cannot be moved by a peer or a relay beyond what D75 already accepts. One case is still
open, and the spec misstates it: a clock step that **lasts** 14 days or more of daemon runtime
(forward, or backward by more than 10 min) lets the `MaxLive` cap delete the last key that
peers accepted (N1). A probe confirmed it.

## N1 (Low): a long-lasting step lets the cap delete the last key that peers accepted

`internal/mailbox/mailbox.go:488-502` (`capVictim`) and `:542-552` (cap loop).

`capVictim`'s "future-dated first" rule only recognises a key made on a stepped clock while
`now` is the true time. While the clock is still stepped, the keys the node makes look normal
to it. Peers refuse every one of them: a forward step fails check 5, and a backward step
gives an announcement whose `not_after` has already passed for them. So they keep sealing to
K_n, the last key they accepted. Each rotation of the stepped clock (every 7 days) adds one
key, and the cap then deletes "the oldest retired" key:

- Forward step: rotations 2 and 3 delete K_{n-1}, then **K_n**.
- Backward step: the true keys look future-dated, so the future-first rule picks them
  (oldest first). After two more stepped rotations it again reaches **K_n**.

**Probe** (temporary `internal/mailbox/zz_probe_f28chk_test.go`, removed after the run; file
backend; three steady keys, then 2 days later a step that keeps running hourly):

| Step | Runtime while stepped | ids[0] | ids[1] | ids[2] = K_n (true age at the end) |
|---|---|---|---|---|
| +30 d | 6 d | deleted | live | live |
| +30 d | 13 d | deleted | deleted (age 22 d, fine) | live |
| +30 d | 14 d | deleted | deleted | **deleted (16 d)** |
| −20 d | 6 d | deleted | live | live |
| −20 d | 14 d | deleted | deleted | **deleted (16 d)** |

**Impact.** K_n is deleted at a true age of 14 to 21 days: after its `not_after` (14 d), but
before `not_after + 7 d`. Mail that peers sealed to K_n before its `not_after`, and that is
still in the queue (7 d TTL), becomes a key miss after the correction. In that state the node
is already cut off: peers refuse its announcements and its mail (step 11), and from K_n's
`not_after` on they have no valid key for it at all. So the extra loss is small, and the live
key count stays at 3.

**The spec overstates the fix.** `Docs/protocol/mail.md:90-91` says that the only key the cap
deletes early is "the oldest of three … whose successor has been current for at least 7
days". In the cases above that successor was current only on the local clock: no peer ever
accepted it. The same applies to plan 98 lines 37-41.

**Fix (minimum).** Correct `mail.md` §Lifecycle (cap paragraph) and plan 98 to state the
residual: "A step that lasts while the daemon runs for 14 days or more can let the limit
delete the last key that peers accepted, after its `not_after`, at a true age of 14 to 21
days." Adding a regression test that pins the documented behaviour is optional.

**Fix (preferred, small).** Let the cap also spare **the newest retired key whose `created`
is at most the newest `mail_seen` stamp + 10 min**, which is the last key that peers can have
accepted. Use the raw `MAX(received_at)`, not `SeenBasis`, because a backward step pulls `now`
below it. Allow one extra live key for that (a hard bound of `MaxLive + 1` = 4).

- Forward and backward steps: K_n is kept, and the cap deletes the stepped keys first.
- Idle daemon: the key spared is the one before the last mail. The others still go at 21
  days, and the spared key is deleted by age once mail arrives.
- Test: the probe's 14-day rows must keep ids[2], and the live keys must stay at most 4.

## Item checks

### (1) M1: closed for a step during which the clock does not make two more keys

- The age rule (`expired`, `mailbox.go:470-480`) is measured at `mail.SeenBasis`
  (`receiver.go:533-557`), which is `min(now, newest stamp)`. A forward step therefore ages
  neither K_n nor K_{n-1}, and with no `mail_seen` row nothing is deleted by age.
- If the basis query fails, nothing is deleted by age either (`mailbox.go:526-530`), and the
  error is returned.
- `expired` can never delete the current key:
  - It needs `not_after + 7 d ≤ basis ≤ now`, so the key is at least 21 days old. `rotate`
    would already have made a new current key (`need` at 7 days, `mailbox.go:382`) before
    `sweepLocked` runs.
  - The key that `createLocked` makes has `not_after = now + 14 d`, so `expired` is false for
    it.
- The cap order is correct for a single step and its correction, with any number of hourly
  runs within 7 days:
  - It deletes K_{n-2} (whose successor was accepted by peers), then the future-dated key at
    the correction.
  - A failed delete leaves the victim in `keep` and the loop breaks, so the next run retries
    the same victim. It never deletes a newer key instead.
- Restarts: the only in-memory state is the private-key cache, so a restart changes nothing.
- No sequence I tried leaves the node without a usable current key:
  - The cap never picks a key that is not retired (`mailbox.go:491`).
  - `usable` replaces a future-dated current key at once (`:257`).
- Growth stays bounded at 3, except when deletes keep failing (review 87b N3). That limit
  predates F28 and is unchanged.
- The documented residual (a step that makes one key lets the cap delete K_{n-2} at a true
  age of 14 to 21 days) is acceptable. The successor really was accepted, so only mail from
  a peer that missed two `keys` mails (both outboxed and retried) can be lost. The case that
  is not acceptable as written is N1.

### (2) Peer influence: none beyond D75

Only a paired peer whose mail passes steps 1 to 11 can add a `mail_seen` row (`open.go`, and
`store` at `receiver.go:313-323`).

- The stamp is `min(now, created + 10 min)`, and step 11 gives `created ≤ now + 10 min`. So
  the newest stamp can reach the receiver's own `now`, but never pass it. On a correct clock,
  a peer can at most make the basis equal `now`, which is the behaviour before F28.
- Nobody can pin the basis back:
  - `MAX` only grows, and the prune never removes the newest row.
  - A replayed old mail is stamped at its old `created`, below `MAX`.
  - A relay cannot sign, and holding mail back only stalls the basis. That is the safe
    direction: keys are kept longer, and the cap still bounds them.
- Residual, under D75: a malicious paired peer that signs `created` near the victim's
  **stepped** clock moves the basis forward during the step, which brings M1 back for that
  window.
- Info: rows stamped in the future before F28 (review 100 I2) keep the basis at `now` until
  true time passes them. A later forward step that stays below that stamp is then aged on
  the wall clock again. This is bounded by the old step and self-limiting; no action.

### (3) L1 and L2: closed

- **L1.** `capVictim` skips non-retired keys. `TestCapNeverDeletesCurrentKey` uses the mock
  keyring and a locked backend, and it fails if the cap goes back to `keep[0]`.
- **L2.**
  - `fetch_client.go:478-481` checks token expiry with `clockNow(c.caps.Now)`.
  - `daemon.go:902-904` wires `rcv.Now` and `rcv.Opener.Now`.
  - The `Options.Now` comment (`daemon.go:188-197`) names what stays on `time.Now`.
- **The backlog is reasonable.** The mailbox keys, the outbox and presence sign a `created`
  that peers check against their own clocks, so a skewed test clock there would make peers
  refuse the node. That needs a design choice, not a wiring change. There is no production
  impact, because a nil `Now` is `time.Now` everywhere.

### (4) Spec vs code

The following match the code:

- `mail.md` §Lifecycle:
  - the future-dated current key: lines 62-64 vs `usable`;
  - the age rule and the basis, and nothing deleted by age without mail: 65-68 vs `expired`
    and `sweepLocked`;
  - the cap order and the protected current key: 85-88 vs `capVictim`.
- §Dedupe:
  - the stamp and the cutoff: 372-376 vs `seenStamp` and `SeenCutoff`;
  - the `received_at` column comment (571).
- `retention.md:88`, against `retention.go:409, 591` and `PruneSeenTx`.

Mismatches:

- **Lines 90-91: see N1.**
- Nit: the spec does not say that a failed basis query skips deletion by age. Optional.
- Nit: line 94 ("Decrypting uses any live key") does not mention the step that
  `MailboxKey` (`mailbox.go:318`) already applies: it still refuses a key past
  `not_after + 7 d` by the **wall** clock. During a forward step, mail sealed to K_{n-1} or
  K_n is therefore a key miss, even though the key was kept. It opens again after the
  correction. This predates F28 and is the documented behaviour on a correct clock; Info
  only.

### (5) Test quality: good

- Every new test drives an injected clock. There is no `time.Sleep` in
  `internal/mailbox/clockstep_test.go`, `internal/mail/seen_clockstep_test.go`,
  `internal/retention/clockstep_test.go` or `internal/daemon/daemon_clock_test.go`.
- `daemon_clock_test.go` uses the existing `harnessWait` polling with a 1 h skew, so it has no
  timing margin to flake on.
- Keystores: mailbox tests use `"file"`, or `"auto"` with `keyring.MockInit()` (deferred
  reset), the same pattern as `outage_test.go`. The real keychain is never reached.
  `TestCapNeverDeletesCurrentKey` skips if the keys land in the file backend. That cannot
  happen under the mock, so the skip is only a guard.
- The old `zz_review55_*` files are gone at dbe18da.
- The steady-schedule test checks every hour to day 21, both with mail and idle.
- Missing: a test for N1, whichever way it is resolved.

## Runs (targeted, this branch)

- `go test -count=1 ./internal/mail ./internal/mailbox ./internal/retention`: all ok.
- `go test -count=1 ./internal/daemon -run 'Clock|Request|Prune|Rotation|KeyMiss|Mail'
  -skip TestHelperRunsInScopeRequests`: ok. The skipped test is host-only and also fails on
  main (review 100).
- The probe file was created and deleted inside the worktree. Nothing else was written except
  this file.

## Fixes applied

Applied by the re-checker (task 01a10105), with no git used. I backed up the changed files
to `%TEMP%\f28n1bak` first.

- **N1 (the preferred fix).**
  - `mail.SeenNewest` (`internal/mail/receiver.go`) returns the raw `MAX(received_at)`, not
    capped at `now`. `SeenBasis` now calls it, and its behaviour is unchanged.
  - `spareOf` (`internal/mailbox/mailbox.go`) picks the newest retired key whose `created` is
    at most that stamp + 10 min: the last key peers can have accepted.
  - In `sweepLocked`'s cap loop, when the normal victim (`capVictim`: future-dated first, then
    the oldest retired, never the current key) is the spared key:
    - with 4 keys live, the loop stops;
    - with more than 4, it deletes the next candidate (`capVictim(..., skip)`).
  - So at most `MaxLive + 1` keys stay live. With no mail, or if the query fails (the error
    is returned), nothing is spared and the cap works as before.
  - Behaviour by case:
    - Steady clock with mail: the victim is never the spared key, so the schedule is
      unchanged.
    - Long step: K_n is kept.
    - Idle daemon: the last key created before the newest mail is kept until mail arrives.
      Every later key still goes at 21 days.
- **Comments.** The package doc, `MaxLive`, `Rotate` and `sweepLocked` now name the spare.
- **Spec.** `Docs/protocol/mail.md` §Lifecycle:
  - The old sentence "the oldest of three … whose successor has been current for at least 7
    days" is replaced by two bullets.
  - The first states what is protected: the spared key, the one-key overflow, when the spared
    key is deleted, and the idle case.
  - The second states the remaining case: a step that creates a key can delete another key
    early, at a true age of 14 to 21 days. That key's successor was accepted and has been
    current for at least 7 days.
- **Plan 98.** An amendment note for N1 is added.
- **Tests** (`internal/mailbox/clockstep_test.go`):
  - `TestRotateLongClockStepKeepsAcceptedKey` covers +30 d and −20 d steps, each running
    hourly for 14 and for 21 days. It checks that:
    - live keys stay at most 4 after every run;
    - K_n survives the step;
    - K_n is live after the correction while its mail can still be queued;
    - the current announcement is accepted;
    - K_n is deleted by age when mail arrives 7 days later, with at most 3 keys live.
  - `TestCapSparesLastAcceptedKeyWhenIdle` covers 50 idle days after one mail. It checks that:
    - live keys stay at most 4 every hour;
    - every key except the spared one is deleted at exactly 21 days;
    - the spared key goes at the first new mail.
  - Mutation checks (the code was restored after each):
    - with no spare, both tests fail (K_n deleted);
    - with an unbounded spare, both fail (5 live keys).
- **Checks.**
  - `go build ./...` and `go vet ./internal/... ./cmd/...` with GOOS=windows, linux and
    darwin: ok.
  - golangci-lint v2.13.2, `run ./...` on 3 OSes: only the CRLF "properly formatted" lines.
  - CRLF-aware gofmt (`tr -d '\r' | gofmt -l`) on the 3 changed `.go` files: clean.
  - Every changed file is still CRLF.
  - `go test -count=1 ./internal/mail ./internal/mailbox ./internal/retention`: ok. The
    mailbox tests use the file backend or `keyring.MockInit` only.
  - `go test -count=1 ./internal/daemon -run 'Clock|Rotation|KeyMiss|Mail'`: ok.
