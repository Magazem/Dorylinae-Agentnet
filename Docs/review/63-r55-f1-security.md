# 63: R55-F1 security review (relay budget fairness, PR #13)

Reviewer: SEC-F1 (Opus, `claude-opus-5-5`), 2026-09-29. Branch `p4/r55-f1`, head `dc27574`.
Read-only review; the only file written is this one.

Read: [56-r55-f1-spec.md](56-r55-f1-spec.md) (incl. review 56a, tests 1–21, OD-1…9 approved as
recommended, D45); [relay-hosted.md](../protocol/relay-hosted.md) §2 "Memory budgets and
fairness"; `verify/C01-01.md`, `verify/C01-02.md`; the full PR diff (`gh pr diff 13`:
`internal/relay/{budget,conn,relay,limits,ephemeral}.go`, the tests, `cmd/relay`,
`deploy/early/agentnet-relay.service`, `Docs/ops/early-relay-deploy.md`); `deploy/early/setup.sh`
and `Caddyfile` for the IPv6 question. Attacker model: an internet stranger with free keys
and a few /24s or /48s. Severities follow [55-code-review/01-rubric.md](55-code-review/01-rubric.md)
at the 4.1p sizing.

## Verdict: **changes needed**

The design is implemented faithfully, and the ledger is sound. One ledger per connection
under `bmu`, gated by `dead`, with `min(n, hold)` clamping, gives exactly-once uncharge on every
path I traced. The four converted C01 tests fail on main for the right reason, and R55-001 and
R55-002 are closed.

Merge is blocked by:

- **CI is red.** Test 11 fails on ubuntu and macOS, and is flaky on Windows (S-3).
- **The OD-R55F1-7 (c) ops change for 4.1p is missing** (S-2).
- **One eviction rule reopens review 56a A1** through the ephemeral budget (S-1).

## Findings

| # | Sev | Finding |
|---|---|---|
| S-1 | Medium | Ephemeral eviction bypasses the `evictMinRate` protection for honest heavy receivers |
| S-2 | Medium | OD-R55F1-7 (c) not done: the 4.1p runbook and `setup.sh` still serve the relay on IPv6 |
| S-3 | Low (merge blocker) | Test 11 depends on kernel socket buffers: fails on Linux/macOS CI, 2 of 3 runs locally |
| S-4 | Low | Every refused charge scans all holders under the global ledger lock, including each 50 ms drain recheck |
| S-5 | Low | `frameAllowed` lets small frames drive the byte buckets into unbounded debt |
| S-6 | Info | Test 19 and test 11 assertions weaker than they look |
| S-7 | Info | Runbook still sizes the VM at 512 MB in two places |
| S-8 | Info | Kernel socket buffers are outside every budget (pre-existing) |
| S-9 | Info | In-process TLS: the "1 s" hard close can take up to 6 s |

### S-1 (Medium): ephemeral eviction ignores what is queued ahead

`ledger.eligible` (`internal/relay/budget.go:356-361`) compares the age of `c.queue[0]` with
`evictStale(c.hold[k])`. The problem is how the two sides are measured:

- **The age.** `c.queue[0]` is the oldest frame of *any* budget. Presence and mail share one
  FIFO (`c.out`).
- **The threshold.** `evictStale(c.hold[k])` uses only the bytes held in budget *k*.

For the ephemeral budget this makes the threshold about 2 s: a holder has at most 32 × 8 KiB of
presence, so `hold[kindEphemeral]` is ≤ 256 KiB. The age, though, is set by the mail queued
ahead.

**Why an honest drainer is eligible.** Take an honest daemon draining a 4 MiB backlog at
1–4 Mbit/s. Its oldest frame is several seconds to ~30 s old, so it passes the 2 s threshold.
It is not eligible under the outbound rule: that rule gives it 14 s, which is exactly the A1
protection (OD-9 (b)).

**The attack.** The attacker knows the victim's key (the precondition of every
victim-targeted attack in §2):

1. It sends presence to the victim. `routeEphemeral` forwards to any online key. With 500 KiB
   mail frames the victim's `out` has room, so the victim's prefix holds ~150–250 KiB of
   ephemeral bytes.
2. It fills the 6 MiB ephemeral budget from prefixes that each hold less than that. This takes
   about 25–40 prefixes: cheap on IPv6 (S-2), and comparable to the accepted many-prefix
   residual.
3. The next ephemeral charge picks the victim's prefix as the heaviest and evicts the victim.
   The victim reconnects, re-drains from cursor 0 (T10-02), and is evicted again.

This is the A1 loop that OD-9 (b) was approved to prevent. The spec text ("the oldest frame
waiting in H's outbound buffer … `(bytes H holds in X)`") permits this reading, so this is a
spec gap as well as a code finding.

**Fix.** Measure staleness against everything queued ahead of the oldest frame: use
`evictStale(c.hold[kindOutbound] + c.hold[kindEphemeral])` (the reservation is already in
outbound) for both kinds, which is about one line. Add a test: a drainer holding 4 MiB of mail
plus some presence is not evicted by an ephemeral charge 5 s later. Add one sentence to
relay-hosted.md step 2.

### S-2 (Medium): IPv6 is still served on 4.1p (OD-R55F1-7 (c))

D45 approved OD-7 as "(a) now, (c) for 4.1p". Per the spec, the code ticket updates
`Docs/ops/early-relay-deploy.md` and `deploy/early/`, and the owner applies the change on the
VM. The PR changes neither for IPv6:

- The runbook still says "public IPv4 and IPv6 both on" (step 1.4).
- It still publishes an `AAAA` record for `<prefix>::1` (step 2).
- `setup.sh` has `ufw allow 443/tcp`, which opens 443 on v4 **and** v6.

So 4.1p keeps the cheap-/48 exposure the shares assume away: 8 prefixes to fill a budget, and
≈ 8–48 prefixes for the many-prefix residual.

The deployed VM has IPv6 today. Until the steps below are done, the URL must not be shared
beyond known testers (D43), whatever the code state.

**Advice for 4.1p.** Do all of the following. Each alone is incomplete.

1. **DNS.** Delete the `AAAA` record. Change runbook step 1.4 to "public IPv4 only (untick
   IPv6)" for new VMs, and drop the AAAA row and "shows both" from step 2.
2. **Host firewall (v6 443).** In `setup.sh`, replace `ufw allow 443/tcp` with
   `ufw allow proto tcp from 0.0.0.0/0 to any port 443`. On re-runs, also run
   `ufw delete allow 443/tcp`, as the script already does for SSH.
   - With the default `IPV6=yes` in `/etc/default/ufw`, "deny incoming" then drops v6 443.
   - **Do not** set `IPV6=no`: ufw then stops managing ip6tables, and v6 is left *open*.
3. **Hetzner Cloud Firewall.** If the owner enables it, give the 443 rule the source
   `0.0.0.0/0` only, not `::/0`. On the existing VM, the primary IPv6 can instead be
   unassigned in the console (with the VM powered off).
4. **Verify from outside.** `curl -6 -m 5 https://relay.dorylinae.net/healthz` must fail, and so
   must `curl -m 5 -k https://[<prefix>::1]/healthz`. `curl -4` must still give 200.
5. **Optional belt:** Caddy `bind tcp4/0.0.0.0`. A plain `0.0.0.0` in Go listens dual-stack,
   so it does not help. Not needed if step 2 is done.

Add a line to the runbook's owner-actions list and to HANDOFF: "(b) per-/32 share before
public launch".

### S-3 (Low, blocks merge): test 11 is not reliable

`TestLimitEphemeralEvictionAndControlFrames`:

- **CI ubuntu and macOS** fail at `fairness_test.go:626` ("timed out waiting for the sinks to
  hold presence"). The race job was still pending when I finished.
- **Locally on Windows** it failed 2 of 3 runs with `-count=3`: at `:644` (pres-1 not
  delivered) and at `:647` (no `evict_ephemeral` line).

**Cause.** The four "never read" sinks still have TCP receive windows, and the relay's
socket send buffers are open too. On Linux/macOS loopback those buffers absorb the whole
880 KiB flood, so the relay's ephemeral budget never holds more than 32 KiB. On Windows the
write loops keep emptying `out` after the top-up (`ChargeEphemeral(eph - …)`). The budget then
has room without eviction (`:647`), or holds nothing evictable (`:644`).

**Fix.** Use the pattern that tests 9/10/20 already use: keep sending presence until
`EphemeralFor` settles, as `fillOutbound`/`settle` do. Top up only after settling, and assert
the eviction via the log line **and** a sink's `Connected` becoming false. This is a test
defect, not a code defect: the eviction path works when the budget is really full (see the
`-count=3` runs of tests 9, 19 and 20).

### S-4 (Low): the eviction scan under `ledger.mu`

Every refused charge calls `pick`, which walks every prefix and every holder of the budget
(`p.oldest`, then `pay.holders`) under the relay-wide `ledger.mu`. It does this even when no
holder can be eligible, which is the steady state of a sustained fill.

Measured (benchmark in my temp dir, not committed): **20 µs per `pick`** with 2000 holders in
250 prefixes.

The refused charges are frequent under a fill:

- Each waiting queue drain rechecks every 50 ms (`reserve`, `spaceRecheck`), and each recheck
  is a refused charge.
- Refused presence and direct sends add more.

So an attacker with its own queued mail and ~1000 connected keys (16 prefixes at 64 keys)
holds the global ledger lock ~0.4 s per second. Every frame push and write-completion on the
relay contends for it. This degrades the relay; it does not deny service.

**Fix, any of:**

- Remember the earliest time any holder can become eligible and skip `pick` before then.
- Let a drain's rechecks call `evictFor` at most once per `evictStaleBase`.
- Keep the per-prefix `oldest` incrementally.

### S-5 (Low): byte-bucket debt from small frames

`bucketSet.spend` (`internal/relay/limits.go`) subtracts without a floor. Frames ≤ 1 KiB are
unlimited in count: acks are outside the 60/min control limit, and invalid small frames are
only answered. So a host behind a shared prefix can flood small frames and push the
**prefix's** 64 MiB/min bucket arbitrarily far negative.

Every neighbour's > 1 KiB frames (mail, presence) are then refused for the length of the debt,
not only for the length of the flood. For example, one minute at 10 MiB/s locks the prefix
out for ≈ 8 minutes after it stops.

"Charged but never refused" (A7) does not need debt. **Fix:** clamp at 0
(`tokens = max(0, tokens-cost)`).

### S-6 (Info): test assertions

- **Test 19** checks `e.s.Connected(r.key)` right after h3 closes. An evicted connection
  leaves `s.conns` only when `serve` returns, up to ~1 s after eviction, so an eviction
  moments earlier passes unnoticed. It also asserts `expectQueued` for a send to h3, which is
  queued anyway: h3 is draining. The real requester is h3's drain recheck, which is fine but
  should be said. **Fix:** assert on the ledger (`InflightFor("10.1.0.0") > 0`) or a
  `gone`-style accessor, and keep h3 alive long enough for several rechecks.
- **Test 11's** `logged("max_inflight_ephemeral", "relay=all")` is also satisfied by the
  earlier presence drops. The dropped `queued` reply is proven only by `noFrameWithin`.

### S-7 (Info): VM sizing drift in the runbook

The PR adds "the VM needs about 1 GB of RAM". Two places still size it at 512 MB:

- Step 1.3: "Review 52 needs at least 512 MB of RAM".
- The `GOMEMLIMIT` row: "about 80 % of a 512 MB VM".

Align them. The unit's comment has already dropped "512 MB-class".

### S-8 (Info): kernel socket buffers are outside every budget

This is what makes S-3 fail on Linux. A non-reading peer absorbs its TCP window plus the
sender's socket buffer before `out` grows:

- On 4.1p that is the relay→Caddy loopback buffer plus Caddy→client, each autotuned up to
  `tcp_wmem` max (4 MiB by default).
- This is kernel memory, capped globally by `tcp_mem`, so it is not an OOM of the relay. It
  is RAM the ≈ 255 MiB figure does not count.
- It is pre-existing and not F1's to fix. A note under "Memory bound after R55-F1", and
  optionally `net.ipv4.tcp_wmem` capped in `setup.sh`, would make the 1 GB sizing honest.

### S-9 (Info): hard close with in-process TLS

`closeBounded` closes `c.raw` after 1 s. With `--tls-cert`/`--acme-domain`, `raw` is a
`*tls.Conn`. If no `Write` is in flight at that moment (for example, `ws.Close` is waiting to
read the peer's close reply), `tls.Conn.Close` first sends `close_notify` under a 5 s write
deadline. So the bound is 1 s + ≤ 5 s there.

There is no leak and no panic:

- The timer is stopped by `defer`.
- A double close only returns an error.
- `raw` is published before `c` enters the ledger or `s.conns`, via `bmu`/`ledger.mu`/`s.mu`.

4.1p is unaffected: Caddy terminates TLS, so `raw` is plain TCP.

## Checked and found sound

**Ledger, exactly-once uncharge.** Every charge and uncharge goes through the connection's
`bmu` and is skipped once `dead` is set. This covers `charge`, `popWritten`, `unreserve`,
`sendReserved`, `doneRead` and `releaseAll`.

- `dieLocked` sets `dead`, uncharges all three budgets, zeroes the private counters and empties
  `out`, all under `bmu`.
- `unchargeLocked` clamps to `c.hold[k]`, so no pool or prefix can go negative even on a
  logic slip.
- Frame-push order equals `c.queue` order, because every push holds `bmu`, so `popWritten` pops
  the right entry.
- Test 18 exercises every late return. The `handleFrame` `gone()` check drops a frame finished
  after eviction. The residual window is a frame whose route has started when the eviction
  lands: it is delivered, and its bytes are dst's charge, so there is no ledger effect.

**Lock order and races.** The order is `conn.mu` / `ephMu` → `bmu` → `ledger.mu`, and it holds.

- `evictFor` runs with no `bmu` held, and takes only H's `bmu`.
- No path takes a lock while holding `bmu`, except `ledger.mu`. So R holding `R.mu` in
  `direct` and taking `H.bmu` cannot deadlock.
- `pick` excludes R.
- The frame timer's self-eviction only contends briefly with `chargeRead`.
- The timer firing between EOF and `t.Stop()` evicts a finished frame, and `handleFrame` drops
  it. This is harmless.
- `onRoute` is set only from `export_test.go`.

**The requester never waits.** Eviction closes in a goroutine with a 1 s bound and then
`raw.Close` (test 20: < 100 ms for the requester, gone within 1.5 s). The frame deadline
uncharges at expiry, not after the handshake.

**Budget exhaustion as a stranger.**

- **Many small keys:** shares are per prefix, capped at 64 keys per prefix.
- **Mixed frame types:** at most 6 + 6 + 0.75 MiB per prefix.
- **Slowloris under 30 s:** it re-pays 6 MiB per prefix per 30 s. The attacker's 7th frame in a
  prefix evicts its own oldest (test 4), and cross-prefix honest growth evicts the heaviest
  (tests 6 and 17).
- **Reconnect churn:** `ready` is forced but one per connection. Upgrades are 30/min per
  prefix and reconnects 20/min per key.
- **Eviction ping-pong:** a refilled attacker prefix pays its own share first (`over`). An
  attacker then gets honest frames evicted only through the many-prefix residual, which is
  documented.

Outside S-1 I found no new way to make an honest heavy user the target.

**Memory bound.** The ≈ 255 MiB Go-heap figure holds at the 4.1p flags:

- The read charge is `cap(buf)`.
- Outbound and ephemeral frames are `exact()`-trimmed (test 12).
- Forced charges are only `ready`.
- Evicted buffers live for ≤ 1 s.

The bound is subject to S-8 (kernel buffers) and S-9 (6 s with in-process TLS).

**Honest behaviour.**

- **Slow uplink:** 30 s ⇒ ~35 KiB/s for a 1 MiB frame, as documented.
- **CGNAT:** documented to take turns at 6 MiB. The debt of S-5 is the one new way a neighbour
  hurts others.
- **Relay restart:** budgets are in memory and start at 0. A reconnect surge with more than
  8 large backlogs can evict drainers slower than 512 KiB/s. Each still acks what it received
  before the eviction, so progress is kept. Acceptable (Info, no finding).

## The tests

**Converted C01 tests fail on main.** I rebuilt main in my temp dir: the worktree copy with
the PR diff reverse-applied. I stubbed `EphemeralInflight` (0) and `ReadingFor` (relay-wide),
and in test 3 waited on `Reading()`. All four fail for the right reason:

| Test | Result on main |
|---|---|
| 1 | "presence reached the outbound (mail) budget: 421736 bytes" |
| 1v | Same as test 1 |
| 3 | The victim's mail is never delivered: the victim is closed through `errReadBudget` |
| 4 | "after attacker 5 the prefix holds 6782976 bytes, share 6291456" |

On the branch, `go test ./internal/relay ./cmd/relay` passes. `-count=3` on the F1 tests passes
except test 11 (S-3).

**Deviations.**

| Test | Deviation | Assessment |
|---|---|---|
| 3 | Seven /24s filling a 7-frame budget exactly | As the spec describes. It is stronger than the spec, because it also checks that the other six keep their frames. Acceptable |
| 9 | Split in two; part 2 runs at 16 MiB | The justification is correct: the 2 MiB drain reservation exceeds a 1 MiB budget, and step 1 then never lets a sink's prefix pay. Acceptable |
| 11 | Order changed | Acceptable in principle, but the test is unreliable and its log check is ambiguous (S-3, S-6). **Not acceptable until fixed** |
| 19 | Requester is h3's drain, not a direct send | Acceptable, but the "not evicted" assertion needs the S-6 fix to prove anything |
| 20 | Sink never reads, so it never answers the close | This is the right way to model an unanswered close. It also proves the hard close, because `Connected` turns false within 1.5 s. Acceptable |

None of the acceptable deviations weakens the proof.

## Flags, unit and runbook

- **Flags.** `--max-inflight-ephemeral` (0 means `max(max-inflight/8, 1 MiB)`) and
  `--frame-read-timeout` (validated > 0) match OD-2 and OD-5, and so does the start line.
- **Unit.** `agentnet-relay.service` adds both flags explicitly, and its comment now says
  102 MiB / 255 MiB / Caddy / 1 GB, which is consistent with the spec.
- **Runbook.** It has the new rows and the memory paragraph, but misses OD-7 (c) (S-2) and has
  the sizing drift (S-7).

## Required before merge

1. **S-3.** Fix test 11, and get CI green, including the race job.
2. **S-1.** Change the staleness rule, with a test and the one-sentence spec change.
3. **S-2.** Make the runbook and `setup.sh` IPv4-only for 443, and record the owner action on
   the VM.

S-4, S-5 and S-6 are recommended in the same PR, since they are small. S-7 is a doc edit.
S-8 and S-9 are notes.
