# 56: R55-F1 spec — relay budget fairness

Author: SPEC-F1 (Opus, `claude-opus-5-5`), 2026-09-29. Ticket R55-F1 (High) from
[55-code-review/99-report.md](55-code-review/99-report.md) §4. Findings: R55-001 (C01-02),
R55-002 (C01-01), R55-034 (C01-03), R55-035 (C01-04), R55-144 (C01-06). Owner context: D43
(4.1p is deployed now; **F1 must land before the relay URL is shared beyond known testers**).
Status: adversarial review done (review 56a, at the end; fixes made in place); awaiting owner approval (HANDOFF rule 3).

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
- (Review 56a) Two recipients behind one NAT address that both receive a backlog take turns
  at 4.1p: one prefix's outbound share (6 MiB) is one connection's maximum.
- (Review 56a) A maximum-size mail from a link under ~35 KiB/s is never delivered, and the
  daemon only logs it (`relay_disconnect`); it does not tell the user.

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
| OD-R55F1-7 | IPv6 /48s are cheap (many-prefix residual) | (a) keep /48, document; (b) add a per-/32 share; (c) 4.1p: no IPv6 service (drop AAAA **and** firewall v6 443) until (b) | **(a) now, (c) for 4.1p (it has IPv6 today); (b) before public launch** |
| OD-R55F1-8 | Charge bytes at read (R55-035) | (a) every frame, before parsing; (b) only invalid frames, after parsing; (c) leave in backlog | **(a)**, frames ≤ 1 KiB (acks) charged but never refused |
| OD-R55F1-9 (56a) | When is an outbound/ephemeral holder "not keeping up" | (a) flat 2 s; (b) `2 s + held ÷ 512 KiB/s`; (c) `2 s + held ÷ 128 KiB/s` | **(b)** |

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
   - Advance the clock 30 s (past `evictStale` for a 1 MiB holding: 2 s + 1 MiB ÷ 512 KiB/s).
   - Assert: an honest mail between two reading peers gets no `queued` and is delivered.
     One sink (the stalest in the heaviest prefix) is closed 1013. `Inflight()` ≤ 1 MiB.
   - Also assert: without advancing the clock (no stale holder), the honest mail takes the
     queue path (fallback). After the clock is then advanced 30 s, the drain's next recheck
     evicts a stale sink and the mail is delivered.
10. **Outbound prefix share.** Non-reading sinks in one prefix never hold more than
    `share(B, 6 MiB)` of the outbound budget. Past it, mail to them is queued.
11. **Ephemeral eviction and control frames.**
    - With the ephemeral budget full of stale presence (clock advanced past `evictStale`,
      e.g. 5 s), a presence envelope between two
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

### New tests from the adversarial review (56a)

17. **Cross-prefix read eviction ignores age.**
    - Prefix H holds the most of a full read budget with frames that all started **after**
      an honest frame R in a lighter prefix (R started first and is still growing).
    - R's next growth evicts H's oldest frame and R completes and is delivered (today's
      draft rule would have closed R).
18. **Eviction does not double-uncharge.** Internal test.
    - Evict a connection that at that moment holds a drain reservation, frames in its
      outbound buffer, presence, and an unfinished read frame; then let its drain `defer`,
      its `done`, its write loop and its `release` all run.
    - `Inflight()`, `Reading()` and `EphemeralInflight()` return exactly to their values
      before the connection existed, and are never negative (sampled throughout).
    - Its frame that completes after the eviction is not delivered to its recipient.
19. **An honest drain that keeps up is not evicted.**
    - Fake clock. The outbound budget is full; the heaviest prefix is one honest recipient
      draining a backlog (4 MiB buffered + reservation), whose oldest frame has waited 5 s.
    - A direct send from another prefix is not allowed to evict it (5 s < 2 s + 6 MiB ÷
      512 KiB/s); the send takes the queue path.
    - After the clock passes 14 s, the same send evicts it.
20. **Eviction never blocks the requester.**
    - The evicted peer never answers the close. The requester's charge (a direct send)
      completes in under 100 ms; the evicted TCP connection is gone within 1.5 s.
    - Same for the frame deadline: the read charge of the timed-out frame is 0 right after
      the timer fires, not after a close handshake.
21. **Small frames pass a spent byte bucket.**
    - A key spends its 32 MiB / min byte bucket. Its next `ack` (≤ 1 KiB) is processed (the
      queued envelope is deleted); its next 2 KiB invalid frame gets `rate_limited` unparsed.

### Merge gate

HANDOFF rule 5 as usual, plus `GOOS=linux` lint. Run the four converted reviewer tests with
`-count=3` (they use timing). The code ticket must also:

- delete the four `zz_review55_C01-*` files from `internal/relay` if they are still present
  in its worktree (the four C01 reviewer tests live only as `.txt` copies in
  `Docs/review/55-code-review/tests/`; the converted tests are new files, not edits of them);
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

## Adversarial review (review 56a)

Reviewer: ADV-F1 (Opus, `claude-opus-5-5`), 2026-09-29. Read: this spec and commit `fcf8f92`,
99-report R55-001/002/009/010/011/034/035/144 and §5/§6 D1, verify C01-01/C01-02, T10-02,
HANDOFF D37/D38/D43, `internal/relay/{relay,conn,limits,queue,ephemeral}.go`,
`deploy/early/`, `Docs/ops/early-relay-deploy.md`, and the daemon's `relayclient` reconnect
path. Attacker model: an internet stranger with free keys and a few /24s or /48s. Severities
use the rubric in [55-code-review/01-rubric.md](55-code-review/01-rubric.md). Every fix below is
already made in place (relay-hosted.md unless noted).

**Verdict.** The four rules hold up: F1 closes R55-002 at any cost and turns R55-001 into a
bandwidth-priced nuisance. No design-level Critical or High remains. Three Medium spec defects
would have let the fix be turned against honest users, or would have broken the memory bound in
the implementation. All three are fixed below. With these changes I recommend approval.

| # | Sev | Issue | Change made |
|---|---|---|---|
| A1 | Medium | **The flat 2 s staleness rule evicts honest heavy receivers.** The oldest-frame age grows with what is buffered, so a daemon draining a 4 MiB backlog over any link under 2 MiB/s is "stale". With its 2 MiB reservation it holds a full 6 MiB share, which makes it the heaviest holder there is. An attacker that fills the budget cheaply (8 prefixes, then ~50 KiB/s to keep sinks alive) can hold each of its prefixes just under 6 MiB. The honest drainer is then evicted, reconnects, re-drains from cursor 0 (the T10-02 path) and is evicted again, in a loop for as long as the attack lasts. This contradicts "a reader that keeps up is never evicted". | Eligibility is now `2 s + held ÷ 512 KiB/s` (`evictMinRate`). The drain reservation's age never counts. New **OD-R55F1-9** with the attacker-cost trade-off. The mail-variant row and the residuals now give the honest price of the rule: ≈ 40 Mbit/s each way, sustained, to hold the budget with ineligible sinks. New test 19; tests 9 and 11 advance the clock past the new threshold. |
| A2 | Medium | **The read-eviction age rule can be gamed.** "H's frame started before R's" also applied when the paying prefix is another, strictly heavier prefix. An attacker could keep one prefix strictly heaviest (7 prefixes at 5.9 MiB, 1 at 6 MiB) and restart that prefix's frames often. No holder there is then older than a long honest frame, so every growth step of that frame while the budget is full takes the fallback, and the honest connection is closed. Cost: ~6 MiB re-uploaded per few seconds. It hits every honest frame that takes longer than that, which means slow uplinks and large mail. | Cross-prefix read eviction now has no age condition: the heavier prefix always pays. The age rule is kept only inside R's own prefix. New test 17. |
| A3 | Medium | **Double uncharge after eviction.** Today `drainStep` returns its reservation with a `defer` straight to the budget, and `readFrame`'s `done` uncharges from a local counter. Neither goes through `conn.dead`. "Mark H" did not say that these must move onto the connection. An implementation that follows the draft would let an eviction plus those returns drive the budget negative. `tryAdd` then admits more than the budget, and the memory bound breaks silently. | New bullet "One ledger per connection": every charge and uncharge (outbound, ephemeral, reservation, read) goes through one per-connection record under `bmu`, gated by `dead`. Step 3 marks H dead under that lock, and a frame that H completes after eviction is discarded, not routed. New test 18. |
| A4 | Low | **Eviction and deadline closes could block.** "Written with at most a 1 s timeout" did not say where. Done inline, it stalls the requester (R) for up to 1 s, which reintroduces waiting on another connection. For the deadline, a plain `ws.Close` from the timer waits up to 5 s + 5 s while the reader is blocked mid-frame, and the frame stays charged meanwhile. | Step 3: the close runs in its own goroutine, with a 1 s wait and then `CloseNow`. The frame-deadline expiry is handled as a self-eviction (uncharge at once, same close). New test 20. |
| A5 | Low | **The many-prefix residual understated how cheap a target a shared prefix is.** The cost is `B ÷ h`, where h is what the honest prefix holds. Against a busy office or CGNAT prefix at its 6 MiB share that is ≈ 8 prefixes, not ≈ 48. | Residual and "What the limits do not stop" corrected. |
| A6 | Low | **OD-R55F1-7 (c) did not match 4.1p as deployed.** The runbook turns IPv6 on and publishes an AAAA record, and Caddy listens on `*:443`. Dropping the AAAA record alone leaves the VM's guessable `<prefix>::1` reachable. | (c) now means dropping the AAAA record **and** blocking v6 443 in the Hetzner firewall (or binding Caddy to IPv4). The recommendation is now unconditional for 4.1p, as an ops change in the code ticket plus an owner action on the VM. |
| A7 | Low | **Charging bytes at read could drop acks.** One heavy sender behind a NAT spends the prefix's 64 MiB/min. Every neighbour's `ack` is then refused unparsed, so their queued mail is kept and redelivered (more T10-02 traffic). | Frames of at most 1 KiB are charged but never refused at step 0. Parsing them is cheap, and later per-frame limits still apply. OD-8's recommendation is amended. New test 21. |
| A8 | Low | **The per-prefix share had no empty-holder rule.** `tryAdd` always admits one charge into an empty budget, but the share check did not say the same. With test-sized budgets, a frame larger than a share could be refused forever. | Added: a prefix holding nothing of X may always take one charge. |
| A9 | Low | **NAT cost at 4.1p was not stated.** One prefix's outbound share (6 MiB) equals one connection's maximum, so two recipients behind one address that are both draining take turns. | Stated under Shares and in "Cost to honest users" (§1). It is a delay, not a loss, and only at the 48 MiB size. |
| A10 | Info | **The memory bound omitted three terms.** (1) An evicted connection's buffers stay reachable until its ≤ 1 s close. (2) 256 unauthenticated auth frames (1 MiB). (3) Caddy is outside `GOMEMLIMIT`: I estimate ≈ 0.1 MiB per proxied WebSocket, so ≈ 250 MiB at 2256 connections, unmeasured. The ≈ 255 MiB relay figure holds; the VM needs about 1 GB, which every current Hetzner type has. | Rows and notes added under "Memory bound after R55-F1". |
| A11 | Info | **"Reported by the daemon like any other 1013" was inaccurate.** `relayclient.Run` only logs `relay_disconnect` and backs off; the outbox retries until the mail expires. A maximum-size mail from a link under ~35 KiB/s is never delivered, and the user is not told. | Text corrected; also listed in §1 "Cost to honest users". Not a blocker, because the relay's 10 s `writeTimeout` already needs ~100 KiB/s to *deliver* such a frame. |
| A12 | Info | **Acceptance tests.** The 16 tests covered the rules but none of A1–A4 or A7. The four converted C01 tests do fail on today's code, for the right reason. Test 1/1v: `Inflight()` > 0 and the honest mail gets `queued`. Test 3: the victim is closed 1013 (today's `errReadBudget` path). Test 4: `Reading()` reaches 48 MiB from one /24. `EphemeralInflight`/`ReadingFor` do not exist yet, so the new versions also fail to compile until the accessors exist. Test 3's attackers sit in separate /24s at a 64 KiB budget, so it tests eviction, not shares; test 4 covers shares. | Tests 17–21 added; tests 9/11 clock advances fixed; a merge-gate note says the C01 originals exist only as `.txt`. |
| A13 | Info | **One ticket?** Yes: one coherent change to `internal/relay` plus `cmd/relay` flags, the unit and the runbook. It is large, though (the ledger, three budgets with per-prefix maps, eviction, the deadline, 21 tests). Suggested order: ledger + `defer` (R55-144) → R55-034 → ephemeral budget (closes R55-002) → deadline → shares + eviction → R55-035. If it must split, cut after the deadline. Alone, that half leaves R55-001 open to one prefix re-uploading 48 MiB every 30 s, so it must not be shared beyond known testers until the second half lands (D43). | None (advice to the Orchestrator). |

**Checked and found sound (no change):**
- Mixed frame types: each budget has its own share, so one prefix holds at most 6 + 6 + 0.75 MiB.
- Many small keys: shares are per prefix, and keys are capped at 64 per prefix.
- Slowloris just under the deadline: already priced at 48 MiB per 30 s.
- Control replies: an attacker can fill only its own prefix's ephemeral share.
- `ready` exemption: bounded by `--max-conns`.
- R52 H1: no path waits on another connection. This now holds by construction after A4.
- T10-02 / T6a-02: F1 adds at most one re-drain per eviction, within the key's 20 reconnects a minute, so no new amplification. Stated in "What eviction costs".

**OD recommendations.**
- OD-1 (a), OD-2 (a), OD-3 (a), OD-4 (a), OD-5 (a) and OD-6 (a) hold.
- OD-4 (a): a dropped `error` or `queued` only costs the daemon information it recovers by resending.
- OD-7: changed to (a) now + (c) for 4.1p, unconditionally.
- OD-8: (a) with the 1 KiB exception.
- New OD-9: recommended (b).
