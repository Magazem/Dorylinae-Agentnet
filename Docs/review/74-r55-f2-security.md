# 74: R55-F2 security review (High level, D47)

Reviewer: R55-F2sec-Opus (`claude-opus-5-5`), 2026-09-30. Scope: branch `p4/r55-f2` at
`6418b7e` (on main `f2e3415`), `git diff f2e3415..HEAD` (25 files). Measured against the approved spec
[66-r55-f2-spec.md](66-r55-f2-spec.md) (incl. review 66b and §8), `Docs/protocol/relay-hosted.md`,
`Docs/protocol/envelope.md`, [verify/R55-009.md](55-code-review/verify/R55-009.md) and
`deploy/early/agentnet-relay.service`.

## Verdict

**Changes needed (small).** No Critical and no High. R55-009's egress amplification is closed on
every path I tried. R55-010 and R55-011 are closed as specified, and the payload check matches the
daemon's parser. Two Medium findings should be fixed before merge:

- **M-1:** a slow reader can hold its prefix's retry turn, which breaks the spec's fairness bound.
- **M-2:** a crafted queue gets past the probe, so every reconnect reads about 1 MiB from the
  database again (review 66b M1 is open again for crafted queues).

Both have small, local fixes. The three Low findings can be fixed in the same pass.

## Findings

### M-1 · Medium · a slow reader holds its prefix's retry turn, far past the spec's bound

- **Where:** `internal/relay/limits.go:225` and `:310` (`redeliverAllowed`, `nextServed`),
  `internal/relay/relay.go:1250-1290` (`startRetry`, `retryRange`).
- **Mechanism:** the connection a retry serves stays `redeliverServed[prefix]` until
  `retryRange` returns (`defer s.lim.redeliverDone(c)`, `relay.go:1269`). Meanwhile every other
  connection of the prefix is refused and joins the wait list (`limits.go:225`), however full the
  buckets are. `nextServed` skips a prefix that has a served connection. So the turn lasts as long
  as the range takes to hand over, and that time is set by how fast the recipient reads, not by
  the budget. The only liveness bound is `writeTimeout` = 10 s per frame (`conn.go:471`).
- **Scenario (4.1p flags):**
  1. The attacker shares a /24 (NAT or carrier) with honest V. It queues about 32 MiB to each of
     about 30 own keys (the 1 GiB queue cap), in rows of about 32 KiB, and never acks.
  2. Each key's retry turn is paid in full from the prefix budget. The attacker then reads just
     fast enough that no single frame write exceeds 10 s (about 3 KB/s once the socket buffers
     are full).
  3. Each turn lasts about 2.7 h instead of the ≈ 15 min the budget implies. V waits behind about
     30 such turns: ≈ 3.5 days, against the spec's stated bound of `queue-max-total ÷ prefix rate`
     = 8 h (review 66b H2, OD-R55F2-10).
  4. If the prefix bucket runs short part way through V's own turn, V gives up its turn and goes
     to the **tail** (`limits.go:234`). It waits behind the whole line again.
  5. V's unacked rows can therefore reach the 7-day TTL: honest mail is lost, which is exactly
     what H2 was meant to prevent.
- **Proof:** `TestZZSec74ServedTurnHeldBySlowReader` (archived, see Files) passes on the branch.
  A served connection that does not read keeps the turn through 7 full refills of the prefix
  bucket, and V is never served.
- **Fix direction:**
  - End a served turn after one `redeliverStep` batch, or after a time slice (for example one
    sweep tick). An unfinished range goes back on the list at its old place (the head), not the
    tail. A served connection whose step blocks longer than the slice loses the turn.
  - Keep V's place when the prefix, not V, runs short mid-turn.
  - Add a test with a non-reading served connection and an honest waiter.

### M-2 · Medium · a tiny first old row gets past the probe; each reconnect reads ≈ 1 MiB of old frames

- **Where:** `internal/relay/relay.go:1143-1175` (`redeliverStep`); the call
  `s.q.nextRange(c.key, after, upto, drainBatch, drainBatchBytes)` at `:1156`.
- **Mechanism:** the probe decides on the first old row only (`seq`, `LENGTH`). If that row fits
  the buckets, `nextRange` reads a full batch of frames (up to `drainBatchBytes` = 1 MiB, 64 rows)
  before the other rows are charged. Most of them are then refused and skipped.
- **Scenario:**
  1. An attacker key queues one 100-byte row followed by 63 rows of 16 KiB. It takes the first
     delivery, then reconnects 20 times a minute and never acks.
  2. The default key bucket refills ≈ 28 KB between reconnects, so the tiny row always passes the
     probe.
  3. Every reconnect then reads ≈ 1 MiB of old frames on the relay's single SQLite connection
     (`SetMaxOpenConns(1)`), charges `drainReserve` of outbound budget, and sends ≈ 16 KiB.
  4. With 64 keys per /24 that is ≈ 1.25 GiB/min of database reads per prefix.
- **Impact:** far below pre-F2 (the whole queue on every reconnect), and egress stays bounded, so
  this is not R55-009 again. But it is the single-DB load half of R55-009 that review 66b M1
  closed, and the claim "a key over budget costs no database reads" (test 6) holds only when the
  first row is refused.
- **Proof:** `TestZZSec74ProbeBypassReadAmplification`: 20 reconnects read 20 161 KiB of old
  frames and redelivered 18 384 bytes (≈ 1100×).
- **Fix direction:** cap the read by the budget.
  - Pass `maxBytes = max(size of the probed row, min(key tokens, prefix tokens, drainBatchBytes))`
    to `nextRange`. `readRows` already stops at `maxBytes`, at most one frame over.
  - Or select `seq, LENGTH(frame)` for the range first and fetch only the frames that fit.
  - Extend test 6 with a tiny first row.

### L-1 · Low · redelivery flags accept values smaller than one frame, so such rows are never redelivered

- **Where:** `cmd/relay/limits.go:119-125` checks only `> 0`.
- **Problem:** `bucketSet.has` never succeeds when the cost exceeds the burst (`limits.go:581`).
  With `--queue-redeliver-per-key 64KiB`, for example, a dropped row larger than 64 KiB is skipped
  on every connection and every retry until it expires. That is honest loss by misconfiguration.
- **Fix:** require both flags to be ≥ `envelope.MaxFrameBytes` (better: ≥ `drainReserve`) and say
  so in `-h`.

### L-2 · Low · the `busy_timeout` restore after the non-waiting checkpoint can fail silently

- **Where:** `internal/relay/queue.go:653-656`.
- **Problem:** the restore `PRAGMA busy_timeout = 5000` runs with the operation's context and its
  error is discarded. If that context has ended, the relay's only pooled connection keeps
  `busy_timeout = 0`. From then on, any write racing another process (`relay admin`, a restore)
  gets `SQLITE_BUSY` at once: adds fail with `internal` and drains kick connections. This is
  unlikely, because the checkpoint returns quickly.
- **Fix:** restore with a fresh `context.Background()` timeout. If the restore fails, discard the
  connection (return `driver.ErrBadConn` via `conn.Raw`) so the pool reopens it with the DSN
  pragmas.

### L-3 · Low · with a wait list present, a prefix gets at most one redelivering connection per minute

- **Where:** `internal/relay/limits.go:310-340` picks one connection per prefix per tick and breaks.
- **Problem:** while an attacker keeps its prefix's wait list non-empty, every honest redelivery in
  that /24 — even one frame after an F1 eviction — waits at least one tick per connection ahead of
  it, even when the budget could pay them all. Behind a busy carrier NAT this is minutes to hours
  of delay. It is only delay, and within what the spec accepts for shared NAT, but it is cheap to
  improve.
- **Fix:** in `nextServed`, keep picking from the list in order while the prefix bucket holds the
  next waiter's row (for example, charge the row size speculatively during the pick). This fits
  naturally with the M-1 time slice.

### Info (no change needed)

- Two rare over-charges only err towards the budget:
  - a probed row acked before `nextRange` makes the next row pay again (`relay.go:1163`);
  - a widened skip range can redeliver rows that were redelivered in between.
- Log lines `queue_redeliver` and `queue_flush` are one per batch and bounded by the budgets.
  Refusals go through `lim.hit`, one per (limit, subject) per minute. The client logs one Warn
  per minute. An attacker cannot drive log or metric volume beyond what F1 already allows.

## Checks that held

**Redelivery amplification (R55-009) — closed on every path I tried.**

- **Close mid-batch:** first-delivery batches are claimed (H raised) in one transaction under
  `q.mu` before any frame is sent (`queue.go` `claim`). Rows claimed but not sent are
  redeliveries on the next connection.
- **Replacement (two connections of one key):** claims serialise on `q.mu`, and H only rises
  (`MAX(seq, excluded.seq)`).
- **Key rotation:** fresh keys still share their prefix's 128 MiB/h bucket.
- **Many recipient keys:** each first delivery is paid for by an upload.
- **Reconnect storms:** a reconnect gains nothing; the cursor jumps to H after a skip.
- **H mark cannot be reset or undercut:**
  - `queue.seq` is `AUTOINCREMENT` (`queue.go:143`), so a new row can never get a `seq` ≤ H.
  - The prune removes H only when the key has no rows, expired ones included (`NOT EXISTS` over
    all rows).
  - A DB restore brings back rows, H and `sqlite_sequence` together; DB loss loses rows and H
    together. The attacker can trigger neither.
- **Retry ranges stay inside H:** `skipTo` ≤ H, and every row sent is charged first.

**Honest recipients never lose mail (except via M-1).**

- A skip moves the cursor to H and first deliveries continue in the same step, so new mail is
  never held back.
- A prefix skip joins the wait list; a skip on the key bucket alone is retried by the second loop
  of `retrySkipped`.
- A closed connection leaves the list (`unregister` → `redeliverForget`). The rows remain for the
  next connection.

**Payload check vs the daemon's `Parse`: no differential in either direction.**

- `wireHeader` has `Envelope`'s field names and tags, so repeated, case-variant and escaped keys
  (`"payload"`) resolve the same way on both sides.
- §8 deviation 1 (every `payload` occurrence judged) is required: `encoding/json` keeps the first
  error, so the daemon refuses any bad occurrence too.
- The relay's rule is stricter than the daemon's:
  - arrays like `[65]` decode into a `[]byte` on the daemon but are refused by the relay;
  - `null` and a missing payload are refused;
  - escapes are refused;
  - non-zero trailing bits (`"QR=="`) are accepted by both (`StdEncoding` is not `Strict`).
- **Fuzz:** a whole-frame differential (`FuzzZZSec74RelayAcceptedDaemonParses`, 90 s, 18.7 M
  execs, seeds with duplicate, case-variant, escaped and BOM frames) found no frame that the relay
  accepts and `Parse` refuses or reads with different routing fields. It also found no case where
  `AckTarget`'s (from, id) differs from `ParseHeader`'s.

**Ack forgery — no path found.**

- `AckTarget` uses the same decoding as the relay and requires a clean decode plus `Validate`.
  `Classify` is shared, so control frames never reach it.
- The relay's `from == sender.key` check at ingress, and its to-key-scoped delete, mean an ack can
  only remove the attacker's own row.
- Rows stored by an older relay were decoded with the same `Header` field rules, so the old and
  new decodings agree on (from, id).

**Sweep (batching and the 5 ms pause) is correct under concurrent add and ack.**

- Each batch is its own transaction under `q.mu`, and `adjust` runs only after commit.
- §8 deviation 2 (the pause) is sound and cheap: about 32 pauses per GiB.
- `add`'s deadline now starts after it takes the lock.
- `stopSweep` is checked between batches, and `retrySkipped` cannot start a drain after `Close`:
  `sweepLoop` exits before `closing` is set.
- `journal_size_limit` is set in the DSN.
- `TRUNCATE` runs with `busy_timeout = 0` and treats busy as a skip (see L-2 for the restore).

**Migration R3.** It is one idempotent `CREATE TABLE`. The forward path and the rollback are
tested (`TestRelayMigrationR3`, `TestRelayR3Rollback`), and the runbook gives both rollback
routes.

**Lock order — no cycle.** The orders in use are:

- `s.mu → limits.mu` (`startRetry`);
- `limits.mu → conn.mu` (`nextServed`);
- `conn.mu → q.mu → pool conn` (`drainStep` → `claim`);
- `bmu → ledger.mu` (F1).

Nothing takes `limits.mu` while holding `conn.mu`: `redeliverAllowed` is called with `c.mu`
released, and `skipRedelivery` releases it before `lim.hit`, which takes only `logMu`. F1
eviction takes `bmu`, `ledger.mu` and `logMu` only. An evicted served connection ends its retry
(`reserve` reports dead) and releases the turn. The `waiting` and `waitClosed` fields are only
touched under `limits.mu`.

**Flags.** Defaults 32 MiB and 128 MiB, validated positive (see L-1), shown in `-h`, printed on the
start line, written out in the unit, and exported as two metrics.

**§8 deviations — all five accepted.**

1. Every payload occurrence judged: **required** (see above).
2. 5 ms pause between sweep batches: **accepted**.
3. The probe as its own statement: **accepted**. H only rises between `claim` and `probe`, so
   deciding outside the transaction is safe. The gap is after the probe (M-2).
4. Widening the skip range: **accepted** (over-charge only).
5. When the client's Warn line is written: **accepted**.

## Tests run

- `go vet` on the four packages: clean.
- `go test ./internal/relay ./cmd/relay ./internal/envelope ./internal/relayclient -count=1`:
  all ok.
- `go test ./internal/relay -run 'Redeliver|Redelivery|SkipNewMail|FairOrder|AckForgery|Sweep|HighWater' -count=3`:
  ok.
- The two reviewer tests and the 90 s fuzz, as described above.
- Race detector: not run (CI only).

## Files

- **Created:** `Docs/review/74-r55-f2-security.md` (this file).
- **Created:** `Docs/review/74-tests/zz_sec74_test.go.relay.txt` — `package relay`; the tests for
  M-1 and M-2. Both pass while the defect exists.
- **Created:** `Docs/review/74-tests/zz_sec74_test.go.envelope.txt` — `package envelope_test`; the
  differential fuzz.
- **Temporary:** the two test files were run as `internal/relay/zz_sec74_test.go` and
  `internal/envelope/zz_sec74_test.go`, then deleted. No production code was changed and no git
  write command was run.
