# Review 100: R55-F28 security review (clock-step robustness)

Branch `p4/r55-f28` (commit 0e98fcc), worktree `AgentNet-wt/r55-f28`. Scope: R55-103 (mailbox
keys), R55-053 (`mail_seen` prune basis), R55-151 (one daemon clock). Inputs: plan
`Docs/review/98-r55-f28-plan.md`, theme T12, owner decision D75. The reviewer used no git:
changed files were compared one by one against the main checkout (4b8da40).

## Verdict

**Approve after a fix round.** The `mail_seen` change (R55-053) holds up against peer and
relay manipulation, and the dry run, the real prune and the R55-F13 caps agree. The daemon
clock (R55-151) cannot weaken any production deadline. One Medium finding: a forward clock step
still deletes the **predecessor** key, which offline or slow peers still seal to (a probe
confirmed it). The rest are Low or Info.

## Medium

### M1. A forward step still deletes the key before the current one (R55-103 only partly fixed)

`internal/mailbox/mailbox.go:471-481` (`expired`). It measures the successor's age on the
stepped clock. With the normal three live keys (K_{n-2}, K_{n-1}, K_n, where K_n is 2 days old
in true time), one `Rotate` run 30 days ahead finds K_n "older than 7 days". So `expired(K_{n-1})`
is true and K_{n-1} is deleted, along with K_{n-2}. Only K_n and the new key survive.

- **Probe (temporary test, removed afterwards):** three keys on a steady clock, true time
  2 days after K_n, one run at +30 days, then the correction. Result: `ids[0]` gone, **`ids[1]`
  gone**, `ids[2]` live.
- **Why it matters:** peers seal to K_{n-1} until they accept K_n's `keys` mail. A peer that
  was offline, or whose outbox already held mail, keeps sealing to K_{n-1} until its
  `not_after`, and that mail lives up to 7 days in the queue. All of it becomes impossible to
  open: the R55-103 failure, one generation back. The new spec sentence in
  `Docs/protocol/mail.md:67-70` ("it cannot delete the key peers seal to") overstates the fix.
  K_{n-2} also goes up to 5 days before `not_after + 7 d` in true time.
- **Fix (preferred):** measure key ages by deletion from a basis anchored to peers, the same
  way as D75. Use `min(now, newest mail_seen.received_at)`, which `mail.SeenCutoff` already
  computes. Pass it to `Keys` as a func, so the keys package does not import retention.
  Deleting by age then needs `not_after + 7 d ≤ basis` and a newer key with
  `created + 7 d ≤ basis`. A forward step does not move the basis, and an idle daemon keeps
  keys longer. `MaxLive = 3` still bounds storage, and in steady state the cap alone already
  deletes the 21-day key when the next one is created.
- **Fix (alternative, no new input):** in-process, skip deletion by age in a run where the wall
  clock moved more than `mail.MaxSkew` beyond the monotonic clock since the previous run.
  Keep the `MaxLive` cap. This covers steps while running, not a boot with a bad RTC.
- **Test to add:** the probe above (predecessor 2 days old at the step must survive), and
  correct `mail.md` §Lifecycle.

## Low

### L1. The MaxLive cap assumes "oldest by `created`", which breaks after a backward step

`internal/mailbox/mailbox.go:508-514` deletes `keep[0]` while more than 3 keys are live, and
`keep` follows `live()`'s `ORDER BY created` (line 165). After a backward step,
`createLocked` makes a current key dated in the past (e.g. now − 20 d), so in a later
`rotate()` → `sweepLocked` the **current** key sorts first. The path is safe today only
because `createLocked` appends the new row last (line 457) and the steady state is exactly 3
live keys. If a deletion fails (keychain locked or timed out, `keep = append(keep, r)` at
line 505), the next run sees 4 live keys and deletes the just-announced current key. Peers
then seal to a key with no private half until the next hourly run.

- **Fix:** never delete the current (non-retired) row in the cap loop. Alternatively, order
  the cap by generation (`retired` time, then `rowid`) rather than `created`.
- **Probe result for the plan's own scenario** (backward step of 20 d with 3 live keys): one
  key, the 14-day-old `ids[0]`, **is deleted** by the cap. That is harmless (its successor
  has been current for 7 d), but the plan's "nothing is deleted" and
  `TestReview55T12_01BackwardClockStep` check only `ids[1:]`. Assert all three, or document
  the cap deletion.

### L2. "One daemon clock" is incomplete (test consistency only, no production impact)

With `Options.Now` set, these still read the wall clock. That is fine in production (nil →
`time.Now`), but a skewed test can see two clocks:

- `internal/daemon/fetch_client.go:477`: `capability.Verify(... Now: time.Now())` checks a
  held grant's expiry. This is a security deadline next to `capStore.Now = clock`. Use
  `clockNow(c.caps.Now)` or pass the clock in. Line 274 (`now` for the fetch result) is the
  same case. The call timers at 432 and 568 are durations and are correct to keep.
- `internal/daemon/mail.go:127`: `mail.Receiver` and `Opener` get no `Now`. Step 11, the
  receive age limit and `seenStamp` run on the wall clock while the kinds' stores run on
  `clock`.
- `internal/daemon/daemon.go:364`: `mailbox.New(..., nil)`, and `presence.Receiver` /
  `presence.Sender`.
- The `Options.Now` comment at `daemon.go:188-194` says it is the Now of "the stores the IPC
  handlers and mail kinds call". Either wire the receiver, opener, mailbox keys and fetch
  client to `clock`, or name them in the comment as excluded.

No security deadline can be left on a nil clock: `clockNow(nil)` is `time.Now()`, the approval
store gets `opts.ApprovalNow` → `clock` → `time.Now`, and the device store gets the same
chain.

## Info

- **I1. Peer or relay manipulation of `SeenCutoff`: none found.** The stamp is
  `min(now, created + 10 min)` (`receiver.go:502`), and step 11 (`open.go:126`) gives
  `created ≤ now + 10 min`. So every stamp is ≤ the local receive time and within 10 min of
  the signed `created`.
  - A peer cannot push the newest stamp past the receiver's clock, so it cannot force an
    early prune on a correct clock.
  - It cannot lower `MAX(received_at)`, so it cannot stop pruning; at most it adds rows
    inside the receive window, as before.
  - A relay cannot sign.
  - A pruned row has `created < basis − 35 d + 10 min ≤ now − 35 d + 10 min`, so a replay
    fails `MaxAge` (30 d) at any later clock that is not more than about 5 days behind.
  - The documented residual (a step of the victim's clock **and** a peer signing near the
    stepped time) needs a step of more than 5 days (`keys`) or more than 21 days (other
    kinds) to open a replay window. That is accepted under D75; no action.
- **I2. Legacy and future rows.** Rows stamped before F28 (including any stamped during an
  old forward step) can make `MAX` later than now. The basis then falls back to `now`, the
  old behaviour, so nothing gets worse. An unparsable `MAX` also falls back to `now`.
  Bad-body rows: `TrimSuffix(badBodyMark)` is right, and the string order holds.
- **I3. Dry run vs real run, R55-F13 caps.** `makePlan` (`retention.go:409`), `DryRun` (591)
  and `PruneSeenTx` (`receiver.go`) all call `SeenCutoff` on the same snapshot. The
  `mail_seen` prune cannot delete the newest row (cutoff ≤ newest − 35 d), so the recomputed
  cutoff after deletion is the same. Mail arriving between the approval and the run can only
  move the cutoff forward, and `PruneTxWithin` still caps at the approved counts. The
  35-day `MinOlderThan` and `MaxRows` / `MaxContentBytes` are unchanged. With an empty table
  the cutoff is `""`: it prunes nothing in `mail_seen`, and inbox rows without a seen row are
  handled exactly as before.
- **I4. Spec nit.** `Docs/protocol/mail.md:366` still says a pruned replay's `created` is
  "older than `now − 30 d + 10 min`". It is now `now − 35 d + 10 min` (still true, but loose).
- **I5. `mail_seen` stamp vs `mail_inbox.received_at`.** These now differ (signed time vs
  receipt time). A `mail_seen` row for mail that arrived late goes up to 14 d (30 d for
  `keys`) earlier than before. Replay safety holds per I1, and inbox rows are still removed
  only after their seen row. No action; the spec covers it.

## Tests and hygiene

- **Names.** Rename the review-probe files:
  - `internal/mailbox/zz_review55_T12-01_test.go` → `internal/mailbox/clockstep_test.go`
  - `internal/mail/zz_review55_T12-02_test.go` → `internal/mail/seen_clockstep_test.go`
  - The test functions likewise, e.g. `TestReview55T12_01ForwardClockStep` →
    `TestRotateForwardClockStepKeepsSealedKey`, and `TestReview55T12_02*` →
    `TestSeenPrune*`. Keep a `// review 55 R55-103` / `R55-053` comment for traceability.
- **Injected clocks.** All new tests drive injected clocks; there is no `time.Sleep` in the
  four new files. `daemon_clock_test.go` uses the harness's `harnessWait` polling (existing
  pattern) and skews `Options.Now` by 1 h. That is fine.
- **gofmt.** `internal/daemon/outbox_harness_test.go:192-194` is not gofmt-clean with CRLF
  stripped: the alignment of the new `Now` field against `OnStoresReady` is off. Every other
  changed `.go` file is clean.
- **Runs (targeted only, on this branch):**
  - `go test ./internal/mail ./internal/mailbox ./internal/retention`: all ok. Mailbox
    tests use the file backend or `keyring.MockInit`; the real keychain was not touched.
  - `go test ./internal/daemon -run 'Clock|Request|Prune'`: one failure,
    `TestHelperRunsInScopeRequests` (`writable_by_others: "C:\\"`). It **fails identically
    on main**, so it comes from this host, not F28. With it skipped, the rest is ok.

## Fixes applied

Applied by the reviewer (task 01a100f2), with no git used.

- **M1.** `mail.SeenBasis` (`internal/mail/receiver.go`) returns `min(now, newest
  mail_seen.received_at)`, or not-ok on an empty table. `SeenCutoff` now uses it.
  `sweepLocked` (`internal/mailbox/mailbox.go`) measures `expired` at that basis. With no
  `mail_seen` row, or if the basis query fails (the error is returned), nothing is deleted by
  age, and `MaxLive` bounds the keys. On the 7-day rotation the cap deletes each key at 21
  days, as the age rule would.
- **M1 follow-on (the cap).** Measured alone, the age basis was not enough. The correction
  run adds a 4th live key, and "oldest by created" then deleted K_{n-1}. `capVictim` now
  deletes a retired key created after `now + MaxSkew` first (every peer refused it), then
  the oldest retired key.
- **L1.** `capVictim` never picks the current (non-retired) key.
- **Spec.** `Docs/protocol/mail.md` §Lifecycle now:
  - states the basis;
  - states that a forward step ages neither the current key nor the one before it;
  - states the idle-daemon rule;
  - states the cap order and that the current key is protected;
  - states the residual: a forward step that creates a key can delete the oldest of three
    keys through the cap. That key's successor has been current for at least 7 days.

  The nit at the old line 366 is fixed (`now − 35 d + 10 min`, step 11 at most 30 d).
- **Plan.** `Docs/review/98-r55-f28-plan.md`:
  - the backward-step claim is corrected (the cap deletes the oldest retired key);
  - an amendment note for M1 and L1 is added;
  - the test file names are updated.
- **L2.**
  - `fetch_client.go` `fetch` checks the token expiry with `clockNow(c.caps.Now)`. The
    call's deadline stays monotonic. I withdraw the review's line-274 point: that `now`
    only times the fetch.
  - `daemon.go` sets `rcv.Now` and `rcv.Opener.Now` to `opts.Now` (nil means
    `time.Now`).
  - The `Options.Now` comment now lists what still uses `time.Now`.
  - **Backlog:** the mailbox keys (`mailbox.New(..., nil)`), the outbox and presence. They
    sign times (`created`) that peers check against their own clocks, so a skewed test clock
    there would make peers refuse the node. Wiring them needs a design choice, not a quick
    change.
- **Tests.**
  - `internal/mailbox/clockstep_test.go` replaces `zz_review55_T12-01_test.go`. It holds:
    - the forward step (the sealed key kept; the future-dated key is the one the cap deletes;
      the first key goes at `not_after + 7 d` once mail arrives);
    - **forward step keeps the predecessor** (M1);
    - the backward step, now checking all ids (ids[0] deleted by the cap, ids[1..2] live, no
      key churn while stepped);
    - **cap never deletes the current key** (L1, mock keyring plus a locked keychain);
    - **age deletion needs mail** (after downtime, with mail vs idle);
    - the steady 21-day schedule, with mail and idle.
  - Mutation check: reverting the basis fails the M1 tests, and reverting the cap to
    `keep[0]` fails the L1 and forward tests.
  - `internal/mail/seen_clockstep_test.go` replaces `zz_review55_T12-02_test.go` (functions
    renamed `TestSeen*`, plus `SeenBasis` cases).
  - `export_test.go` gains `ReceivedMailAt`. The four existing keychain and cache tests
    record mail before the deleting run; outage and noservice record it before the locked
    run, so the deletion is really attempted.
  - **The old files are left in place for `git rm`.** The `mail` package does not compile
    until `internal/mail/zz_review55_T12-02_test.go` is removed (duplicate helpers).
    `internal/mailbox/zz_review55_T12-01_test.go` is superseded, and its forward test now
    fails by design.
- **gofmt.** `outbox_harness_test.go` is fixed. Every changed `.go` file passes
  `tr -d '\r' | gofmt -l` with no output. The files edited here are CRLF.
- **Runs** (with the two old files moved aside temporarily):
  - `go build` and `go vet ./internal/... ./cmd/...` on windows, linux and darwin: ok.
  - golangci-lint v2.13.2 on 3 OSes: only the CRLF "File is not properly formatted" lines.
  - `go test` on mail, mailbox (file backend and mock keyring only) and retention: ok.
  - daemon `-run 'Clock|Request|Prune'` (skipping the host-only `TestHelperRunsInScopeRequests`):
    ok.
  - daemon `-run 'Outbox|Rotation|KeyMiss|Forged|Mail'`: ok.
