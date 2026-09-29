# 56: R55-F1 spec — relay budget fairness

Author: SPEC-F1 (Opus, `claude-opus-5-5`), 2026-09-29. Ticket R55-F1 (High) from
[55-code-review/99-report.md](55-code-review/99-report.md) §4. Findings: R55-001 (C01-02),
R55-002 (C01-01), R55-034 (C01-03), R55-035 (C01-04), R55-144 (C01-06). Owner context: D43
(4.1p is deployed now; **F1 must land before the relay URL is shared beyond known testers**).
Status: draft for the Opus adversarial review, then owner approval (HANDOFF rule 3).

Spec changes (all docs, no code):

- `Docs/protocol/relay-hosted.md`: status note; §2 table rows; new subsection **Memory budgets
  and fairness (R55-F1)**; two new residuals in "What the limits do not stop"; acceptance
  summary; new section **Open decisions (R55-F1)** at the end.
- `Docs/protocol/presence.md` §Relay: ephemeral envelopes are charged to the new ephemeral
  budget.
- `Docs/protocol/envelope.md` §Forwarding: bytes charged at read (new step 0); 1013 load
  shedding is not an "error on a single envelope".

## 1. Summary for the owner

**The problem.** The hosted relay limits its memory, but on a first-come basis. A stranger
with free keys can take the whole budget and keep it:

- R55-001: ~50 half-sent frames from one home network block every other daemon, indefinitely,
  after a one-time upload of ~40 MiB.
- R55-002: presence messages to the attacker's own slow readers use up the mail budget, so
  all mail between online peers is queued and not delivered.

**The fix, in four rules:**

1. **A deadline per frame.** A message must arrive within 30 s of its first byte. A half-sent
   frame cannot be held forever.
2. **A share per network.** One /24 (or IPv6 /48) can hold at most 1/8 of each budget. At the
   4.1p settings that is 6 MiB of 48 MiB. One network can no longer fill it.
3. **A separate budget for presence and control replies** (6 MiB at 4.1p). Presence can never
   touch the mail budget. When the presence budget is full, presence is dropped, as the spec
   already allows.
4. **Evict the heaviest, not the newcomer.** When a budget is full, the relay closes the
   biggest, oldest holder in the network that holds the most. Before, it closed whoever asked
   next. An attacker's connections are the heaviest and oldest, so the attacker's connections
   are the ones closed. Honest traffic keeps flowing. Nothing waits, so the deadlock that R52
   H1 worried about cannot come back.

Three small fixes come with it:

- Frames are charged for the memory they really use (R55-034).
- Every frame's bytes count towards the per-minute byte limits before the frame is parsed
  (R55-035).
- Budget release is deferred, so it also runs after a panic (R55-144).

**After the fix** (rough, at the 4.1p flags):

- **R55-001:** one network holds at most 6 MiB. To fill the budget an attacker needs at least
  8 networks and must re-upload ~13 Mbit/s without stopping. Even then, honest frames evict
  the attacker's frames rather than being refused.
- **R55-002:** closed whatever the attacker spends, because presence no longer shares the
  mail budget.
- **Mail variant.** Writing this spec turned up the same attack using mail to the attacker's
  own slow readers. It works from one network today (found by reading the code, not tested).
  The same rules close it.
- **Memory:** ≈ 255 MiB in total, under `GOMEMLIMIT=400MiB`. Before, presence alone could pin
  ≈ 0.5 GiB.

**What remains:**

- An attacker with many networks (≈ 48, which is realistic with IPv6) can still make large
  honest messages retry.
- An attacker who can send faster than a victim downloads can get that victim's connection
  closed while the budget is full. The victim reconnects and gets its mail again.

**Cost to honest users:**

- A daemon on a very slow uplink (under ~35 KiB/s) cannot send a maximum-size (1 MiB) message.
- Under attack, an evicted daemon reconnects. Mail that was on its way is resent by the
  sender's outbox. It is delayed, not lost.

**4.1p unit:** keep `--max-conns 2000 --max-inflight 48MiB` and `GOMEMLIMIT=400MiB`; add
`--max-inflight-ephemeral 6MiB --frame-read-timeout 30s` (both equal the defaults but should be
explicit). The code ticket updates `deploy/early/agentnet-relay.service` and
`Docs/ops/early-relay-deploy.md` (its "about 96 MiB at most" line).

## 2. Open decisions

Full text with options in [relay-hosted.md §Open decisions](../protocol/relay-hosted.md#open-decisions-r55-f1).

| OD | Question | Options | Recommended |
|---|---|---|---|
| OD-R55F1-1 | **Reopen R52 H1** ("close the reader, never wait") | (a) evict the heaviest prefix's oldest holder, deadline as backstop; (b) keep closing the newcomer, add shares + deadline only; (c) wait up to 1 s, then close | **(a)** |
| OD-R55F1-2 | Frame read deadline | (a) fixed 30 s; (b) 10 s + 1 s per 32 KiB; (c) fixed 60 s | **(a)** |
| OD-R55F1-3 | Prefix share of each budget | (a) 1/8 with floors; (b) 1/4; (c) 1/16 | **(a)** |
| OD-R55F1-4 | Control reply that does not fit the ephemeral budget | (a) drop (`ready` exempt); (b) close that connection 1013; (c) unconditional, bounded per connection (≈ 125 MiB worst case) | **(a)** |
| OD-R55F1-5 | Ephemeral budget size | (a) new flag, `max(max-inflight/8, 1 MiB)`; (b) fixed 16 MiB; (c) carve 1/8 out of `--max-inflight` | **(a)** |
| OD-R55F1-6 | Mail in an evicted connection's buffer | (a) accept, sender outbox resends; (b) re-queue before closing; (c) no outbound eviction | **(a)** |
| OD-R55F1-7 | IPv6 /48s are cheap (many-prefix residual) | (a) keep /48, document; (b) add a per-/32 share; (c) 4.1p: no AAAA record until (b) | **(a) now, (c) for 4.1p if it has IPv6; (b) before public launch** |
| OD-R55F1-8 | Charge bytes at read (R55-035) | (a) every frame, before parsing; (b) only invalid frames, after parsing; (c) leave in backlog | **(a)** |

## 3. Acceptance tests

The code ticket (Opus, D26; Opus security review before merge, rule 4) must pass these.
Reviewer tests are in `Docs/review/55-code-review/tests/` (with a `.path` file each). They
currently **pass when the defect exists**. Each is made permanent in `internal/relay` with its
assertion inverted, as described below. New `export_test.go` accessors: `EphemeralInflight()`,
and per-prefix `ReadingFor(prefix)` / `InflightFor(prefix)`. Tests that need eviction staleness
advance the relay's fake clock (`limitEnv.clock`). The deadline uses a real timer, so tests set
`FrameReadTimeout` short.

### Converted reviewer tests

1. **C01-01** (`zz_review55_C01-01_test.go` → `TestLimitPresenceFloodKeepsMailDirect`).
   - Same setup: `MaxInflight` 512 KiB, `EphemeralPerMinute` 1<<20, two reading victims, four
     non-reading sinks, one presence sender.
   - Add `MaxInflightEphemeral` 128 KiB.
   - Replace the "until `Inflight() > budget`" loop: send a fixed 4 × 40 presence frames of
     ~5.5 KiB.
   - Assert: `Inflight()` (outbound) stays 0 throughout; `EphemeralInflight()` ≤ 128 KiB.
   - Then the honest 200-byte mail from va to vb gets **no** `queued` reply within 1 s, and
     vb reads it within 2 s.
2. **C01-01v** (`zz_review55_C01-01v_test.go` → `TestLimitPresenceFloodDefaultRate`).
   - Same as 1, with the default 600/min limit and the default ephemeral budget. Same
     assertions.
3. **C01-02** (`zz_review55_C01-02_test.go` → `TestLimitUnfinishedFramesEvictHeaviest`).
   - `MaxInflight` 64 KiB. Attackers in separate /24s each start a frame of ≥ 8 KiB and
     never finish it, until the read budget is full.
   - `other` now reads.
   - Assert: the victim's 200-byte mail is delivered to `other`, and the victim is **not**
     closed.
   - Exactly one attacker connection (the oldest) is closed 1013.
   - A second honest mail is also delivered; drop the old "reconnect and closed again" step.
4. **C01-02v** (`zz_review55_C01-02v_test.go` → `TestLimitUnfinishedFramesPrefixShare`).
   - `MaxInflight` 48 MiB, all attackers in 10.1.0.0/24. Tolerate write errors on attacker
     connections (they are evicted or closed).
   - Assert: `ReadingFor("10.1.0.0/24")` never exceeds 6 MiB (sampled after each attacker
     write), and `Reading()` ≤ 6 MiB + the victim's frame.
   - Then the victim's 200-byte mail is delivered and the victim is not closed.

### New tests

5. **Frame deadline.** `FrameReadTimeout` 300 ms.
   - A key starts a frame and never finishes it. It is closed 1013 within 1 s, and
     `Reading()` returns to 0.
   - The log has `limit=frame_read_timeout`.
   - A second key that writes a 256 KiB frame in fragments over 200 ms is delivered.
6. **Read eviction takes the heaviest prefix, oldest first.**
   - Prefix A holds 3 unfinished ~1 MiB frames and prefix B one; the budget is full.
   - A small frame from prefix C evicts A's **oldest** frame (1013). B's frame then completes
     and is delivered.
   - `limit=evict_read prefix=A` is logged.
7. **The requester's own prefix pays.**
   - Prefix A is at its share. A's next growth evicts A's oldest holder, not a holder in
     another prefix.
   - If A's only holder is the requester, the requester is closed 1013 (fallback).
8. **Existing `TestLimitUnfinishedFramesShareReadBudget` (R-4.0 H1) is updated.** Two keys in
   different prefixes each start a ~0.9 MiB frame against a 1.5 MiB budget. The **first**
   (older, equally heavy) frame is now evicted, and the second completes. This is the
   intended change of OD-R55F1-1.
9. **Outbound eviction (mail variant).**
   - `MaxInflight` 1 MiB. Sinks in 8 prefixes that never read are sent 64 KiB mail until the
     outbound budget is full.
   - Advance the clock 3 s.
   - Assert: an honest mail between two reading peers gets no `queued` and is delivered.
     One sink (the stalest in the heaviest prefix) is closed 1013. `Inflight()` ≤ 1 MiB.
   - Also assert: without advancing the clock (no stale holder), the honest mail takes the
     queue path (fallback). After the clock is then advanced 3 s, the drain's next recheck
     evicts a stale sink and the mail is delivered.
10. **Outbound prefix share.** Non-reading sinks in one prefix never hold more than
    `share(B, 6 MiB)` of the outbound budget. Past it, mail to them is queued.
11. **Ephemeral eviction and control frames.**
    - With the ephemeral budget full of stale presence, a presence envelope between two
      reading peers evicts one stale sink and is delivered.
    - A **new** connection still gets `ready`.
    - A `queued` reply that cannot fit (no stale holder) is dropped and counted in the log
      (OD-R55F1-4 (a)).
12. **Charge what is pinned (R55-034).** Internal test.
    - A 4097-byte envelope forwarded to a non-reading recipient raises `Inflight()` by
      exactly 4097.
    - The frame in the recipient's `out` has `cap == len` (or, if `cap` is charged instead,
      `Inflight()` equals the summed `cap`).
13. **Bytes charged at read (R55-035).**
    - One key sends 40 invalid 1 MiB frames within one fake minute. At most 32 get
      `bad_envelope`; the rest get `rate_limited` with an empty `ref` and are not parsed
      (a parse counter or the log).
    - The key's valid mail in the same minute is refused `rate_limited` too (one bucket).
14. **Release on panic (R55-144).**
    - Internal test with a test-only route hook that panics: after the connection ends,
      `Inflight()`, `Reading()` and `EphemeralInflight()` are 0.
    - If a hook is not wanted in the code, the Opus review checks the `defer` by reading
      instead, and says so.
15. **Memory tests (C01-05).**
    - `TestLimitMaxInflightRelayWide` loses its 64 KiB "control frames are never refused"
      slack. The outbound and ephemeral budgets are checked separately and exactly.
    - The load test (`limits_load_test.go`) adds a presence flood to non-reading recipients
      and 64 unfinished frames. Its heap check stays under 2 × (sum of budgets) + idle cost.
16. **cmd/relay flags.**
    - `--max-inflight-ephemeral` and `--frame-read-timeout` parse with units.
    - They appear in `-h` with their defaults.
    - The start line prints both.

### Merge gate

HANDOFF rule 5 as usual, plus `GOOS=linux` lint. Run the four converted reviewer tests with
`-count=3` (they use timing). The code ticket must also:

- delete the four `zz_review55_C01-*` files from `internal/relay` if they are still present
  in its worktree;
- update `deploy/early/agentnet-relay.service` (flags and comment) and
  `Docs/ops/early-relay-deploy.md`.

## 4. Notes for the adversarial reviewer

Points I am least sure of:

- **Tie rules.** A tie between equal holdings evicts the older one. Check this cannot evict
  an honest frame that started first in a realistic mix.
- **Read-eviction guard.** Read eviction has no staleness guard; outbound and ephemeral
  eviction have a 2 s guard. I rely on the heaviest-prefix choice to protect honest readers.
  Probe with many prefixes.
- **Frame arrival vs. uncharge.** An evicted connection's uncharge happens at eviction, but
  its frame may still arrive completely before the close lands. The implementation must drop
  such a frame, not route it. The spec says "mark H"; check that it is precise enough.
- **Drain reservations.** A reservation counts as H's holding and as stale after 2 s. Check
  that an honest drain waiting behind a slow SQLite read is not evicted under normal load.
  Eviction happens only when a budget is full.
- **Mail variant.** The mail-to-own-slow-sinks variant is by reading only. A quick test before
  the code ticket would settle whether it also works at today's defaults.
