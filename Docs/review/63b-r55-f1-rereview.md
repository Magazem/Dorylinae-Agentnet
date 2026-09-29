# 63b: R55-F1 security re-review of the fix round (PR #13, `d3462a6`)

Reviewer: SEC-F1b · model `claude-opus-5-5` · 2026-09-29 · branch `p4/r55-f1`, head `d3462a6`
(review 63 was written at `dc27574` and committed as `22b1c09`). This is a read-only review, and
this file is the only one it writes.

Read: [63-r55-f1-security.md](63-r55-f1-security.md), the fix-round diff (fetched with
`gh api repos/…/commits/d3462a6`; see "Commands run"), and the current `internal/relay/budget.go`
(`evictFor`, `pick`, `eligible`, `oldest`, the `c.queue` mutations), `conn.go` (`push`, `reserve`),
`relay.go` (`growRead`), `limits.go` (`frameAllowed`, `bucketSet`), tests 11 and 19, and
`settle`. Severities follow [55-code-review/01-rubric.md](55-code-review/01-rubric.md) at the
4.1p sizing.

## Verdict

**Changes needed. The only blocker is CI: the race job is red.**

On the code and docs side, every finding S-1 to S-8 is closed or mitigated. The fix round
adds no security defect, and the ledger's exactly-once uncharge is untouched.

The race job fails in tests 1 and 9 (`TestLimitPresenceFloodDefaultRate`,
`TestLimitOutboundEvictsStaleSink`), not in test 11. It is a test defect (N-4): a drain
reservation that starts late is counted by `watchMax`. The Linux, macOS and Windows test jobs
are green, and test 11 now passes on all three.

Fix N-4, get a green race run, and this can merge without another security review.

## Findings of review 63: status

| # | Sev (63) | Status | Evidence |
|---|---|---|---|
| S-1 | Medium | **Closed** | `budget.go:383`: `evictStale(c.hold[kindOutbound]+c.hold[kindEphemeral])` for both kinds. The honest-drainer case is tested at the ledger: `TestEphemeralEvictionCountsQueuedMail` (4 MiB of mail + 200 KiB of presence; an ephemeral charge 5 s later picks nobody, and at 11 s it picks the drainer). The arithmetic holds: the allowance is 2 s + 4.2 MiB ÷ 512 KiB/s ≈ 10.4 s. The spec sentence is in relay-hosted.md step 2 |
| S-2 | Medium | **Closed (code/docs)**; the owner action is still open | `setup.sh`: `ufw allow proto tcp from 0.0.0.0/0 to any port 443`, then `ufw delete allow 443/tcp`. The runbook: IPv4 only at step 1.4, no `AAAA`, three `curl -4/-6/[v6]` checks, the Cloud Firewall source `0.0.0.0/0`, and a new "Owner actions on the VM that exists" list (incl. "(b) per-/32 before a public launch"). See the IPv6 analysis below |
| S-3 | Low (blocker) | **Closed.** Test 11 passes on ubuntu, macOS and Windows CI and in the race job, and 5/5 locally on Windows | Test 11 now uses 4 KiB socket buffers at both ends (`newSmallSocketEnv`), fills adaptively until `settle` shows that the budget cannot take another frame, asserts that the sinks' prefix holds ≥ half the budget, and asserts the eviction by the log **and** by a sink's `Connected` going false |
| S-4 | Low | **Mitigated; residual Info (N-2)** | `pick` walks the holders (`oldest`) only on a tie in `used`; a drain's rechecks call `evictFor` at most every 250 ms (`conn.go:209`) |
| S-5 | Low | **Closed** | `limits.go` `spend`: `tokens = max(0, tokens-cost)`. The only caller is `frameAllowed`, and a frame > 1 KiB passes `has(size)` first, so clamping changes nothing for them. `TestByteBucketHasNoDebt` |
| S-6 | Info | **Closed** | Test 19 asserts `InflightFor("10.1.0.0") == held` after 1 s of h3's rechecks, and zero `evict_outbound` lines. Test 11 counts the drop line (`count` > `before`) and asserts zero `evict_ephemeral` lines before the clock advance. `count` matches `"limit="+name+" "`; that is sound, because `limits.go:210` always logs `subjectKind` and `suppressed_before` after `limit` |
| S-7 | Info | **Closed** | Runbook step 1.3 and the `GOMEMLIMIT` row now say 1 GB |
| S-8 | Info | **Closed (note)** | Runbook "Memory numbers" states that kernel socket buffers are outside the budgets and capped by `tcp_mem` |
| S-9 | Info | Not addressed (a note, not required) | — |

### S-1 and the attacker's side of the new rule

Counting both budgets also lengthens an **attacker** sink's allowance when the ephemeral budget
pays. A sink that also holds mail is protected for up to 2 s + (6 + 0.75) MiB ÷ 512 KiB/s ≈ 15.5 s,
where before it was ≈ 3.5 s. This is N-1 below. It does not reopen A1: the protection an honest
drainer needs is exactly this one.

### S-2: is "IPv4-only 443 via ufw, `IPV6=yes`" safe?

Yes.

- **What ufw does.** With `IPV6=yes`, ufw writes both iptables and ip6tables. `default deny
  incoming` then sets the ip6tables INPUT policy to drop. `allow proto tcp from 0.0.0.0/0 … 443`
  is an IPv4-only rule, so no `(v6)` twin is created. `ufw delete allow 443/tcp` removes the old
  rule's v4 **and** v6 halves. It runs after the new rule is added, so a re-run never closes v4
  443, and `|| true` covers a fresh VM.
- **Caddy.** Caddy still listens on `[::]:443` (the Caddyfile comment now says so). Behind the
  dropped v6 INPUT, that socket is unreachable from outside. The only exposure is if ufw itself
  is disabled, and then 8787, 9787 and 2019 are protected only by their loopback binds, so that
  case is no worse than before. `bind tcp4/0.0.0.0` stays an optional belt.
- **ACME.** With no `AAAA` record, Let's Encrypt validates over v4 only, so the certificate is
  unaffected.
- **The runbook** is clear and ordered: keep `IPV6=yes`, and why; the external checks; the
  existing-VM action list; the (b) gate before a public launch. The check
  "`443/tcp` without a `(v6)` line" is the right one. `22/tcp (v6)` stays present unless
  `SSH_ALLOW_FROM` is set, and the comment wording allows for that.

Until the owner has run the list on the deployed VM, D43 ("known testers only") still applies.
This is an owner action, not a code defect.

### S-4: can an attacker use the 250 ms window?

No.

- The first refusal of a drain evicts at once (`lastEvict` starts at zero).
- The drain still retries `tryReserve` every 50 ms, so it takes room freed by anyone.
- Eligibility is measured in seconds (2 s base), so delaying the drain's own eviction attempt by
  ≤ 250 ms costs an honest drain at most 250 ms. It does not let an attacker keep a holder
  ineligible, or make an honest one eligible.
- The window does not touch the ledger's exactly-once uncharge: `reserve` changes only when it
  calls `evictFor`.

## New findings

| # | Sev | Finding |
|---|---|---|
| N-4 | Low (merge blocker) | Tests 1 and 9 count a late drain reservation as a budget breach; they fail in the race job |
| N-1 | Info | The S-1 rule lengthens an attacker sink's hold on the ephemeral budget from ≈ 3.5 s to ≤ ≈ 15.5 s per cycle |
| N-2 | Info | The tie shortcut in `pick` can be defeated with equal-sized fills; the S-4 cost remains for direct pushes |
| N-3 | Info (suspected) | Test 19's `held` equality relies on r's socket having stopped absorbing bytes |

### N-4 · Low (merge blocker) · confirmed-read (from the CI log)
- **Where:** `internal/relay/fairness_test.go:170-182` (test 1,
  `presenceFloodKeepsMailDirect`) and `:502-528` (test 9). The code side is `relay.go:1017`
  and `:1056`, and `budget.go:87` (`fits`).
- **What goes wrong:** both tests wait for `Inflight() == 0` ("no drain reservation left") and
  then start `watchMax(e.s.Inflight)`. Some of their connections are only `authed`, never
  `waitDrained`: the attacker `ca` in test 1, and `h1` and both senders in test 9. When such a
  connection's first drain runs after the wait, it reserves
  `drainReserve = drainBatchBytes + MaxFrameBytes = 2 MiB`. An empty pool admits any single
  charge (`fits`: `b.used > 0 &&`), so 2 MiB is admitted even into a 1 MiB budget.
- **Evidence:** race job 109590041657 (run 36622175516):
  - `fairness_test.go:182: presence reached the outbound (mail) budget: 2097152 bytes`
  - `fairness_test.go:528: outbound budget reached 2097152, max 1048576`

  Both values are exactly `drainReserve`. The same tests passed in the race job at `dc27574`,
  so this is timing, and the race detector's slowdown exposes it. The fix round did not
  change the code involved: `reserve` still charges its first try at once.
- **Scenario:** none for an attacker. The relay behaves as specified. The assertion measures
  a transient reservation, not frames.
- **Fix direction:** `waitDrained` every connection before the `Inflight() == 0` wait (in
  test 1, `ca`; in test 9, `h1` and the senders), or watch a frame-only figure. Then rerun the
  race job; `-count` > 1 on these two tests would help.
- **Related:** S-3 (same class: a test that measures more than the ledger).

### N-1 · Info · confirmed-read
- **Where:** `internal/relay/budget.go:383` (`eligible`).
- **What goes wrong:** when the ephemeral budget pays, a holder's allowance now counts its
  mail as well. An attacker whose sinks also hold outbound mail pins its share of the ephemeral
  budget for up to ≈ 15.5 s before it becomes evictable. Before, this was ≈ 3.5 s.
- **Scenario:** a stranger with 8+ prefixes fills 6 MiB of ephemeral through its own non-reading
  sinks, and makes each prefix also hold ~6 MiB of mail. Honest presence and queued replies are
  dropped (OD-R55F1-4 (a)) for up to ~15 s at a time, instead of ~3.5 s. The attacker then
  reconnects within the upgrade/reconnect limits and repeats.
- **Spec:** relay-hosted.md step 2, as amended ("Both budgets count whichever one pays").
- **Fix direction:** none needed. Presence is best-effort, the outbound side already accepts
  14 s (OD-9 (b)), and the attacker must also spend its outbound share. This is recorded so the
  trade is visible.
- **Related:** S-1, OD-R55F1-9.

### N-2 · Info · confirmed-read
- **Where:** `internal/relay/budget.go:323-338` (`pick`); `conn.go:147` (`push`), which is not
  rate-limited.
- **What goes wrong:** prefixes that hold exactly equal `used` force `oldest` walks of every tied
  prefix. An attacker controls frame sizes, so it can make its prefixes tie on purpose, and a
  refused **push** (presence or direct mail) still runs `pick` every time.
- **Scenario:** as in S-4 (≈ 1000 keys in 16 prefixes, each holding the same byte count). A
  drain's rechecks now cost 5× less (250 ms), but refused pushes still do a full walk under
  `ledger.mu`. They are bounded by the per-prefix envelope, byte and ephemeral limits. This
  degrades the relay; it does not deny service.
- **Fix direction:** if profiling on 4.1p shows contention, keep the per-prefix oldest charge
  incrementally, or remember the earliest time any holder can be eligible (review 63's other
  two options).
- **Related:** S-4.

### N-3 · Info · suspected
- **Where:** `internal/relay/fairness_test.go` (`TestLimitHonestDrainNotEvicted`, the
  `n != held` check after `time.Sleep(time.Second)`).
- **What goes wrong:** the test reads `held` after `InflightFor` was stable over one 50 ms
  sample. If r's socket absorbs one more frame during the 1 s sleep (Linux loopback
  autotuning), `popWritten` lowers `InflightFor`, and the test reports an eviction that did not
  happen. It could not pass falsely, only fail falsely.
- **Scenario:** none observed. It passed 5/5 on Windows, and see CI below.
- **Fix direction:** compare with `InflightFor > 0 && count("evict_outbound") == 0`, or use
  `settle` before reading `held`, if it ever flakes.
- **Related:** S-6.

## Checked and fine

- **Exactly-once uncharge is unchanged.** The fix round changes no charge or uncharge path.
  `pick` is read-only under `ledger.mu`. Every `c.queue` mutation is in `budget.go` ledger
  methods (`:193`, `:203`, `:216-220`, `:263`) under `mu`, and `eligible` reads `c.queue[0]` and
  `c.hold` under the same `mu`, so the new read of `hold[kindEphemeral]` adds no race.
  `TestEvictionDoesNotDoubleUncharge` passes.
- **The tie logic in `pick`** is correct:
  - `aged` resets when a strictly heavier prefix is taken.
  - `bestAge` is computed lazily on the first tie.
  - The final comparison against `rUsed+n` computes `bestAge` if no tie did.

  The result is identical to the old eager version.
- **`reserve`'s `lastEvict`** is a local variable per drain, on the wall clock (not the fake
  clock), so tests using `clock.Advance` are unaffected.
- **S-5 does not disturb A7.** Acks (≤ 1 KiB) are still charged and never refused. The large
  frames' `has` → `spend` order means the clamp never forgives a real cost.
- **The test 11 helpers.** `shrinkListener` wraps `httptest`'s listener, so the relay side of
  every accepted connection is shrunk. The client side is shrunk through a custom
  `DialContext`. `newLimitEnvWith` keeps the same options, logger and cleanup as `start`.

## CI (PR #13, `d3462a6`)

Run 36622175516:

| Job | Result |
|---|---|
| test (ubuntu-latest) | pass (4m9s) |
| test (macos-latest) | pass (6m39s) |
| test (windows-latest) | pass (7m46s) |
| race | **fail** (12m19s): tests 1 and 9 (N-4). Test 11 and the new tests pass. There is no `DATA RACE` report. The logged panic is test R55-144's intended panic |
| lint, install-sh, unix service install ×2, flag-sensitive-paths | pass |
| build, cross, govulncheck | skipped |

For comparison, at `dc27574` (run 36619616463), ubuntu, macOS and race all failed on test 11
only.

## Commands run

- `gh pr checks 13` (read-only). Results are above.
- `gh api repos/Magazem/Dorylinae-Agentnet/commits/d3462a6 -H "Accept: application/vnd.github.diff"`
  gave the fix-round diff, used instead of `git show` because workers run no git commands.
- `gh run view 36622175516 --job 109590041657 --log-failed`: race job, two FAILs (N-4) and no
  `DATA RACE`.
- `gh run view 36619616463 --job 109581371804 --log-failed`: race job at `dc27574`, where test
  11 was the only FAIL.
- `go vet ./internal/relay`: clean.
- `go test ./internal/relay -run 'TestLimitEphemeralEvictionAndControlFrames|TestLimitHonestDrainNotEvicted|TestEphemeralEvictionCountsQueuedMail|TestByteBucketHasNoDebt|TestEvictionDoesNotDoubleUncharge' -count=5`
  (Windows): ok.
- `go test ./internal/relay ./cmd/relay -count=1` (Windows): ok.
