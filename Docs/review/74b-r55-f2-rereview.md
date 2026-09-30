# 74b: R55-F2 security re-review (High level, D47)

Reviewer: R55-F2rereview-Opus (`claude-opus-5-5`), 2026-09-30. Scope: branch `p4/r55-f2` at
`d6dac5d`, the fix commit for [review 74](74-r55-f2-security.md), rebased on main `fa691d6`.
I read the whole ticket (`git diff main...HEAD`) and measured it against
[66-r55-f2-spec.md](66-r55-f2-spec.md) §8 "Fixes after security review 74" and
`Docs/protocol/relay-hosted.md` §2.

## Verdict

**Approve, with two Low follow-ups (fix in this ticket if cheap, otherwise note them in the
spec).**

- M-1, M-2, L-1, L-2 and L-3 are fixed as review 74 asked.
- An honest recipient behind a hostile /24 now stays inside the spec's bound
  (`queue-max-total` ÷ prefix rate: 8 h with the 4.1p unit, 32 h at the default). Mail can no
  longer reach the 7-day TTL through the wait list.
- There is no new egress amplification and no new lock-order cycle.
- Two new Low findings:
  - **L-a:** connections served in the same tick size their reads before any of them charges
    its rows. Together they read about K× what the prefix pays. The M-2 read cap does not
    cover this case.
  - **L-b:** the "back to the head" rule reverses the order of connections served in the same
    tick. A connection that joined behind V can end up ahead of V. This does not break the
    bound.

## Fixes verified

### M-1 · time-sliced turns · fixed

- **Slow reader.**
  - `nextServed` ends every turn older than `redeliverTurn` (`internal/relay/limits.go:357`).
  - The slow reader's goroutine may still be blocked in `sendBatch`, but only on a batch it
    has already paid for. Once its turn ends, its next `redeliverAllowed` either sends it to
    the tail (`limits.go:244` with `isServed` false) or is allowed because the list is empty.
  - The turn no longer depends on how fast the reader reads. `TestRedeliverSlowReaderDoesNotHoldTurn`
    passes 3/3.
- **Head on prefix shortfall, tail after losing the turn.** Implemented as specified
  (`limits.go:240-245`, `:277`). A key-bucket shortfall mid-turn ends the retry
  (`sent == 0`), and the second loop of `retrySkipped` later sends the connection to the tail.
  That rule is unchanged from before the fix and is not a finding: it is the key's own budget.
- **Can a same-/24 attacker still starve V?** I tried several ways; none works, because every
  connection ahead of V has a **finite** range:
  - `retryRange` covers `[skipFrom, skipTo]` with `skipTo` ≤ H fixed at skip time, and the
    range only shrinks.
  - A connection leaves the served/wait state when its range ends.
  - To get a new range the attacker must reconnect. A reconnect is not served and the list is
    not empty, so it goes to the **tail**, behind V.
  - The head rule only keeps a connection ahead **within its current range**.
  - So the bytes redelivered ahead of V are bounded by the queued bytes of the connections
    ahead of it (≤ `queue-max-total`). V's wait is bounded by that ÷ the prefix rate, plus
    ≤ 1 tick per connection ahead. That is the spec's bound.

  Attacks tried:
  - many connections (≤ 64 keys per /24): each adds one finite range;
  - reconnecting around the slice: goes to the tail;
  - fast reading, so that each served connection runs short and returns to the head: bounded
    by its range;
  - keeping the prefix bucket empty: only served connections can spend it while the list is
    non-empty.
- **7-day TTL:** not reachable through the wait list at either the 1 GiB or the 4 GiB queue
  cap.
- **Info · a turn can last two ticks.** A turn's start is stamped after that tick's `Sweep`
  (`limits.go:381`, `relay.go:436`), and `Sweep` may use up to 30 s. A turn that started after
  a long sweep can be < 1 min old at the next tick and survive until the one after. The bound
  is 2 min, which is harmless. It could be fixed by comparing tick counts, or by using
  `redeliverTurn − sweepTickBudget`.

### L-3 · several connections served per tick · fixed, but see L-a

- The walk charges `pending + size` against the prefix bucket (`limits.go:370`) and stops at
  the first waiter the bucket cannot pay. FIFO is kept.
- Egress: every row is still charged before it is sent, so there is no new egress
  amplification.
- Database load: this change is what lets L-a happen (below).

### M-2 · reads capped at the probed row plus the tokens available · fixed for a single connection

- `nextRange` reads `seq, LENGTH(frame)` first. It then fetches `seq ≤ last LIMIT n` with
  `maxBytes = total` (`queue.go:437-466`).
- Boundary checks:
  - The first row is always taken (`n > 0` guard).
  - `readRows` with `maxBytes == total` reads exactly the `n` rows.
  - Rows cannot appear between the two queries at `seq ≤ last` (`AUTOINCREMENT`). Acks or
    expiry between them only shorten the read.
- **A frame larger than the budget:**
  - The probe requires the bucket to hold the row (`has`). `cmd/relay` guarantees
    burst ≥ 1 MiB ≥ any row, so the row is paid once the bucket refills; it is never
    permanently unpayable.
  - `drainBatchBytes − size` can be 0, which makes `avail` 0; the probed row is still read.
  - Only `Options` set directly in tests or by library users can go below 1 MiB (documented
    in §8).
- `TestRedeliverReadsOnlyWhatBudgetPays` passes: 20 reconnects read exactly the bytes they
  redeliver.

### L-1 · flag minimums · fixed

- `cmd/relay/limits.go` refuses either flag below `envelope.MaxFrameBytes` and names the flag.
- `-h` states the 1 MiB minimum.
- `cmd/relay` tests pass.

### L-2 · TRUNCATE restore · fixed and safe

- `restoreBusyTimeout` uses its own `context.Background()` timeout. It runs as a `defer`
  before the deferred `conn.Close()` (LIFO), under `q.mu`.
- On error, `conn.Raw` returns `driver.ErrBadConn`. In Go 1.27's `database/sql`, `Raw`
  releases with that error, and `closemuRUnlockCondReleaseConn` then calls `c.close(err)`.
  The driver connection is marked bad, closed and not returned to the pool, so the one open
  slot (`SetMaxOpenConns(1)`) is freed.
- The later `conn.Close()` returns `ErrConnDone`, which is ignored.
- No transaction can leak: only PRAGMAs run on that `Conn`.
- A new connection gets every pragma from the DSN (`queue.go:243-244`). The in-memory DSN
  never reaches this path (`q.path == ""` returns early).

## New findings

### L-a · Low · connections served in the same tick read about K× what the prefix bucket pays

- **Where:** `internal/relay/relay.go:1158` (`redeliverAvail` used to size `nextRange`),
  `internal/relay/limits.go:255` (`redeliverAvail` reads the tokens but takes none), and
  `limits.go:351-385` (L-3: several served per tick).
- **Mechanism:** each served connection's `redeliverStep` works in this order:
  1. charges only its probed row;
  2. reads the bucket's current tokens;
  3. reads frames up to that many bytes;
  4. only then charges the other rows one by one.

  K connections started in the same tick (`startRetry` runs them in parallel) all see the
  same tokens. Each reads up to `min(tokens, 1 MiB)` of frames, and all but the first are
  refused after a row or two.
- **Scenario:**
  1. The attacker has K ≤ 64 keys in one /24. Each key has a tiny row followed by rows of
     16 KiB, delivered once and never acked.
  2. The attacker spends the prefix bucket, then reconnects each key, so all K wait in the
     list.
  3. Their first skipped rows are tiny, so `pending` stays small and the next tick serves all
     K at once.
  4. Each reads up to about 1 MiB and sends a fraction of it. Those refused go back to the
     head, and the next tick repeats.
  5. At 128 MiB/h (≈ 2.2 MiB per minute), that is up to K × 1 MiB ≈ 64 MiB of frame reads per
     /24 per minute on the single SQLite connection, for ≈ 2.2 MiB sent.
- **Proof:** `TestZZSec74bConcurrentServedReadAmplification` (archived, see Files). With
  16 served connections in one tick and a 1 MiB prefix budget, it read 4.97 / 6.75 / 7.87 MB
  of old frames for 1.03 MB redelivered: **4.8–7.6×**, varying with scheduling.
- **Impact:** well below review 74 M-2 (≈ 1.25 GiB/min per /24), with no egress effect, and it
  needs a real /24 per multiple. But the new §2 text claims that "a tiny first row cannot make
  the relay read a whole 1 MiB batch it will not send", and that holds per connection only.
- **Fix direction (any one):**
  - Make `redeliverAvail` a **take**: charge the bytes to be read from both buckets up front,
    then refund what the batch did not send (or was refused), under `limits.mu`.
  - Or serialise a prefix's redelivery reads (probe → read → charge) behind a per-prefix
    mutex.
  - Or, at least, have the served connections of one tick read `tokens ÷ K` each.

  Then turn the probe into a regression test.

### L-b · Low · "back to the head" reverses the order of connections served together

- **Where:** `internal/relay/limits.go:244` and `:277` (`slices.Insert(…, 0, c)`).
- **Mechanism:** when several served connections of one prefix run short, each is inserted at
  index 0, so the **last** one to run short comes first. A connection that joined the list
  behind V, and was served in the same tick (L-3), can come back ahead of V.
- **Proof:** `TestZZSec74bHeadReinsertReordersWaiters`. V joins, then A joins; both are
  served in one tick; V runs short, then A. The list becomes `av`, not `va`.
- **Impact:**
  - Fairness only. The reordered connections were served in the same tick anyway, and each has
    a finite range. The M-1 bound above still holds.
  - An attacker who reads faster than V, and so runs short later, stays ahead of V on every
    tick they share, within its range.
  - This contradicts the spec's wording in §8 ("goes back to the **head**; the prefix ran
    short, not it"), whose intent is that V keeps its place.
- **Fix direction:** give each connection a join ticket (a monotonic counter) when it first
  waits, and keep it across a served→short return. Re-insert by ticket, not at index 0. Add the
  probe as a test.

### Info · flaky test under load

`TestQueueRedeliveryBudgetPerPrefix` (`internal/relay/redelivery_test.go:185`) failed once
with "first connection of recipient 3 got 3 frames, want 4". This was in the first of two
`-count=3` runs of the targeted set. It passed in the rerun and 20/20 in isolation. The failure
is in a **first** delivery, which the fix commit does not touch. `readQuiet`'s 50 ms quiet
window is too short for four ~1 MiB frames when the machine is loaded. Suggestion: raise it to
about 250 ms, or wait for the expected count with a deadline.

## Races and lock order in the changed code

- New or changed state: `redeliverServed` (now a nested map) and the `waiting` /
  `waitClosed` fields. They are read and written only under `limits.mu`, including in
  `waitingOrServed` and `endTurnLocked`.
- Deleting map entries while ranging over the map in `nextServed` is defined behaviour in Go.
- The walk ranges over `slices.Clone(list)`, so `removeWaitLocked` cannot disturb it.
- `redeliverAvail` takes only `limits.mu`, and is called with `c.mu` released
  (`redeliverStep` runs outside `c.mu`).
- `skipped` and `start` still take `conn.mu` under `limits.mu`. That order is unchanged, and
  nothing takes `limits.mu` while holding `conn.mu`.
- `restoreBusyTimeout` runs under `q.mu`. That is the same order as before (`q.mu` → pool
  connection).
- No cycle. The race detector was not run (it runs in CI only).

## Tests run

- `go vet ./internal/relay ./cmd/relay`: clean.
- `go test ./internal/relay -run 'Redeliver|Redelivery|SkipNewMail|FairOrder|AckForgery|Sweep|HighWater|Turn|Truncate|WAL' -count=3`,
  run twice:
  - run 1: one failure, `TestQueueRedeliveryBudgetPerPrefix` (see Info);
  - run 2: ok (105 s);
  - that test alone at `-count=20`: ok.
- `go test ./cmd/relay ./internal/envelope ./internal/relayclient -count=1`: ok.
- The two probes at `-count=3`: both fail on `d6dac5d` (the defects are present):
  - L-a: 4.8×, 6.5×, 7.6×;
  - L-b: `av` all three times.

## Files

- **Created:** `Docs/review/74b-r55-f2-rereview.md` (this file).
- **Created:** `Docs/review/74b-tests/zz_sec74b_test.go.relay.txt` (`package relay`, the L-a
  and L-b probes). Both **fail** while the defect exists, so a fix should make them pass
  unchanged.
- **Temporary:** the probe file was run as `internal/relay/zz_sec74b_test.go`, then deleted.
- No production code was changed, and no git write command was run.
