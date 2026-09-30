# 66: R55-F2 spec — relay queue abuse

Author: R55-F2spec-Opus (`claude-opus-5-5`), 2026-09-30. Ticket R55-F2 from
[55-code-review/99-report.md](55-code-review/99-report.md) §4. Findings: **R55-009** (T10-02,
treated as **High**, D47), **R55-010** (T6a-02, Medium), **R55-011** (C02-02, Medium, verified
in `verify/C02-02.md`). Owner context: D43 (4.1p is deployed; **F2 before the beta**), D47
(F2 has priority and gets a High-level Opus security review). Based on main `eadf189`, after
R55-F1 (merged; `internal/relay/relay.go`, `limits.go`, `budget.go` read as they are now).
Status: **draft, awaiting adversarial review, then owner approval** (HANDOFF rule 3).

Spec changes (all docs, no code):

- `Docs/protocol/relay-hosted.md`: status note; §2 table rows (per prefix and per key);
  new subsection **Offline queue delivery and expiry (R55-F2)** (first delivery vs
  redelivery, redelivery policy, payload refusal, batched sweep and WAL bound, 4.1p flags,
  attacks after the fix, residuals); a residual in "What the limits do not stop"; §3
  `journal_size_limit` and migration R3; acceptance summary; new section
  **Open decisions (R55-F2)** at the end.
- `Docs/protocol/envelope.md`: `payload` row; new paragraph **Payload shape check**;
  Forwarding step 1; Offline queue "Exactly once" (redelivery budget) and "Expiry"
  (batches); `bad_envelope` row; Client behaviour: **Frames it cannot parse** (ack rules and
  the ack-forgery guard).

## 1. Summary for the owner

**The problems.** On a relay without accounts (4.1p today), a stranger can:

- **R55-009:** make the relay send the same queued bytes over and over. It queues 32 MiB for
  each of 12 of its own keys, then has each key connect, download, never confirm and
  reconnect 20 times a minute. The relay sends ≈ 7.5 GiB a minute (≈ 1 Gbit/s), from one home
  network, until the 7-day expiry. That fills the VM's uplink and Hetzner's traffic allowance.
- **R55-010:** fill a victim's offline queue with 1000 messages the victim's daemon cannot
  read. Because it cannot read them it never confirms them, so they stay for 7 days and every
  honest sender gets "queue full" for that victim.
- **R55-011:** time 1 GiB of queued messages to expire in the same minute. Seven days later
  the relay deletes them in one go: all queue work stops for 11–14 s and a 1 GiB log file
  (WAL) stays on disk. At 2 GiB it never finishes and repeats every minute.

**The fix:**

1. **Re-sends have a budget; first sends do not.** Sending a message the first time costs the
   relay what the sender uploaded, so it stays free. Sending it **again** to a key that did
   not confirm it is charged to that key (32 MiB an hour) and to its network (128 MiB an
   hour). Over budget, the relay skips the re-send for now, still delivers new mail, and tries
   the skipped ones again when the budget has refilled. An honest daemon confirms as it
   receives, so after a dropped connection it only needs a few MiB again; it never notices.
2. **Unreadable messages are refused at the door.** The relay checks that the payload is
   base64 (without reading what it means) and answers `bad_envelope`. And the daemon now
   **confirms** a message it cannot read, so junk queued by an older relay cannot linger
   either. That confirmation is built so it can only ever delete the junk itself, never a
   real message from one of your peers.
3. **Expiry in small steps.** The relay deletes expired messages 32 at a time, lets other
   work run in between, and keeps its WAL under 64 MiB.

**After the fix** (4.1p flags):

- R55-009: one network gets its upload back once, then at most ≈ 0.3 Mbit/s of re-sends;
  16 networks ≈ 5 Mbit/s. Reconnecting gains nothing. Before: ≈ 1 Gbit/s from one network.
- R55-010: junk is refused; queue flooding with *readable* junk stays what O-015 accepted
  (it clears at the victim's next connect).
- R55-011: the relay pauses ≈ 0.35 s at a time, never 11 s, and the WAL is ≤ 64 MiB after.

**Cost to honest users:** none in normal use. A daemon behind the same office or carrier NAT
as an attacker can have its *re-sends* delayed (new mail still arrives). A future change of
the envelope format must be negotiated, because old daemons now discard what they cannot
parse.

## 2. The deltas

### 2.1 R55-009: redelivery budget (relay-hosted.md §2)

- Definitions: **first delivery** (a row's first hand-over to a connection of its recipient)
  and **redelivery** (the same row again, on a later connection, because no ack came).
- First deliveries are unbudgeted (paid by the sender's upload, like direct forwarding).
- Redeliveries are charged, by frame length, to two new token buckets of the **recipient**:
  per key 32 MiB / hour (burst 32 MiB, `--queue-redeliver-per-key`) and per prefix of the
  receiving connection 128 MiB / hour (burst 128 MiB, `--queue-redeliver-per-prefix`), both or
  neither.
- "Already delivered" = `seq ≤ H(key)`, a persistent high-water mark in a new table
  `queue_delivered (to_key PRIMARY KEY, seq)` (migration R3), raised once per drained batch
  that contains a first delivery. This relies on drains handing a key's rows over in `seq`
  order and new rows always getting a larger `seq` (both true today; a test pins them).
- Over budget: the row is **skipped**; the connection records `skipFrom`, `skipTo` (= H) and
  the first skipped row's size, jumps its cursor to `skipTo` and goes on with first
  deliveries. No frame of a skipped row is read again on this connection until the retry.
- Retry: on the per-minute sweep tick, a connection with a skipped range whose key and prefix
  buckets hold the first skipped row's size drains `[skipFrom, skipTo]` again (same rules).
- Logging (`queue_redeliver_key`, `queue_redeliver_prefix`) and metrics
  (`relay_queue_redelivered_bytes_total`, `relay_queue_redeliveries_skipped_total`).

### 2.2 R55-010: payload refusal and client ack (envelope.md)

- Relay, Forwarding step 1: `payload` must be present, a JSON string token without `\`
  escapes, containing standard padded base64 (`=` only at the end; `""` allowed). Checked on
  every envelope (presence too), in place, constant memory, using **the same JSON decoding
  as the routing fields** (so duplicate and case-variant keys resolve exactly as the
  recipient's parser resolves them). Otherwise `bad_envelope`, never queued or forwarded. The
  `ref` is the id when the routing fields parsed.
- Why it cannot refuse an honest daemon: `envelope.Marshal` always writes a padded base64
  string and base64's alphabet needs no JSON escape. Why the recipient can parse whatever is
  accepted: the relay rule is stricter than `envelope.Parse` (which also accepts missing /
  `null` payloads and escaped strings), and all other fields are validated identically.
- Daemon: a frame that fails `envelope.Parse` is **acked** (not handed up, not added to the
  seen-set) when it is not a control frame, its `type` is not ephemeral, and the **relay's
  own header decoding** yields a valid `from` and `id`. The ack names exactly that
  `(from, id)`.
- **Ack-forgery guard.** The relay checked at ingress that the decoded `from` is the sending
  key, so the ack can only delete the sender's own row. The hole would be a *parser
  differential*: if the client read `from` differently from the relay (e.g. the first of two
  `from` keys while the relay took the last, or a case-variant `From`), a stranger could make
  the victim ack a queued envelope of one of the victim's peers whose id it guessed. So the
  spec requires one shared decoding function, and a test feeds duplicate and case-variant
  keys through both sides.
- Logging on the daemon: one Warn per minute with a count (C16-02 log growth), not one per
  frame.
- Consequence recorded in envelope.md: an envelope-format change must be negotiated.

### 2.3 R55-011: batched sweep and WAL bound (relay-hosted.md §2, §3)

- Delete expired rows oldest first in batches of ≤ 32 rows, one transaction per batch, the
  queue lock held only per batch; totals adjusted after each commit.
- A failed batch rolls back only itself; no livelock at any cap.
- At most 30 s of sweeping per tick; the rest continues next tick.
- `journal_size_limit` = 64 MiB in the DSN; `wal_checkpoint(TRUNCATE)` after a tick that
  deleted > 64 MiB.
- `add` starts its 10 s deadline after taking the lock.
- The sweep also prunes `queue_delivered` marks of keys with no rows, batched the same way.

## 3. Implementation plan

One Opus ticket (D26: peer-input parsing, relay DoS), one commit, Opus security review at
High level before merge (D47, rule 4). Files and functions:

**`internal/envelope/envelope.go`**

- A private decoding struct used by both `ParseHeader` and the new ack helper:
  the `Header` fields plus `Payload payloadCheck \`json:"payload"\``.
- `payloadCheck` implements `UnmarshalJSON(raw []byte) error` by **recording** the verdict
  for `raw` (overwriting any earlier one) and returning nil, so that for a repeated key the
  last value decides, exactly as `encoding/json` does for `Envelope.Payload`. It never copies
  `raw`. Rule: `raw` starts and ends with `"`, contains no `\`, the inner length is a multiple
  of 4, every byte is in `[A-Za-z0-9+/]` except that the last one, or the last two, may be
  `=`. (This is exactly what `base64.StdEncoding` accepts for escape-free input; `encoding/json`
  decodes `[]byte` with `StdEncoding`.) `null` is also passed to `UnmarshalJSON` and is refused;
  a missing key leaves the check "not seen" and is refused.
- `ParseHeader(frame)`: decode, `Validate()`, then the payload verdict
  (`payload: not a base64 string`). Return the decoded header together with the payload
  error, so `route` can put the id in `ref`.
- New `AckTarget(frame []byte) (from, id, typ string, ok bool)`: the same decode, no payload
  check; `ok` when `from` is a valid key and `id` valid.
- `Parse` is unchanged (the daemon stays lenient).

**`internal/relay/relay.go`**

- `route`: `bad_envelope` with `ref = h.ID` when the header itself was valid and only the
  payload failed; unchanged otherwise. Runs before the ephemeral branch, so presence is
  covered.
- `drainStep`: after `q.next`, read H with `q.lastDelivered(c.key)` (a primary-key lookup,
  once per batch, so a replaced connection's late raise is seen). For each row: `seq > H` →
  send; `seq ≤ H` → `s.lim.redeliverAllowed(c.key, c.prefix, len(frame))`; refused → record
  the skip in `c` (under `c.mu`), move `c.cursor` to `max(c.cursor, H)` and skip the batch's
  remaining rows `≤ H`. After the batch's frames are handed over, `q.markDelivered(c.key,
  maxFirstSeq)` if the batch had a first delivery. The F1 reservation and eviction logic is
  unchanged; skipped rows release their share of the reservation (`unreserve`, as unused
  bytes are today).
- Retry: `Server.retrySkipped()` called from `sweepLoop` after `Sweep`: snapshot `s.conns`;
  for each connection with a skipped range whose buckets `has` the recorded size, start a
  range drain (`seq BETWEEN skipFrom AND skipTo`) under `drainWG`, serialised with the normal
  drain through `c.draining` (only when not draining; otherwise try next tick). When the
  range is done it falls through to a normal `drainStep` loop, because direct forwarding was
  off meanwhile.
- `Sweep`: loop `q.sweepBatch()` until it deletes nothing, 30 s have passed, or `stopSweep`
  is closed; sum counts and bytes; if bytes > 64 MiB run `PRAGMA wal_checkpoint(TRUNCATE)`;
  then `q.pruneDelivered()`; log once per tick as today (`queue_expire`, count).
- `Stats` gains the two counters; `cmd/relay/main.go` prints them on the metrics listener.

**`internal/relay/queue.go`**

- `openRelayDB`: add `&_pragma=journal_size_limit(67108864)` to the file DSN.
- Migration `{3, "R3_queue_delivered", "CREATE TABLE IF NOT EXISTS queue_delivered (to_key
  TEXT PRIMARY KEY, seq INTEGER NOT NULL) WITHOUT ROWID;"}`.
- `lastDelivered(to) (int64, error)`; `markDelivered(to, seq) error` =
  `INSERT … ON CONFLICT(to_key) DO UPDATE SET seq = MAX(seq, excluded.seq)` (never lowers H).
- `nextRange(to, from, to2, limit, maxBytes)` for the retry (or a parameter on `next`).
- `sweepBatch() (n int64, bytes int64, err error)`: its own 10 s context taken **after**
  `q.mu.Lock()`; `DELETE FROM queue WHERE seq IN (SELECT seq FROM queue WHERE enqueued < ?
  ORDER BY enqueued LIMIT 32) RETURNING from_key, LENGTH(frame)`; collect, and call `adjust`
  only after the statement finished without error (today `adjust` runs while scanning, so a
  late error would leave the totals wrong).
- `pruneDelivered()`: `DELETE FROM queue_delivered WHERE to_key IN (SELECT d.to_key FROM
  queue_delivered d WHERE NOT EXISTS (SELECT 1 FROM queue q WHERE q.to_key = d.to_key)
  LIMIT 256)`, in a loop with the same time budget. `NOT EXISTS` uses `queue_by_recipient`.
- `add`: create the context after `q.mu.Lock()`.
- `sweep()` stays as a thin wrapper over the loop, or is removed with its callers updated.

**`internal/relay/limits.go`**

- Defaults `defaultQueueRedeliverPerKey = 32 << 20`, `defaultQueueRedeliverPerPrefix =
  128 << 20` (per hour; burst = the same amount).
- Two `bucketSet`s (`redeliverKey`, `redeliverPrefix`) at `rate = bytes / 3600`.
- `redeliverAllowed(key, prefix string, n int) string` (both or neither, via `has` then
  `take`, returning `limitRedeliverKey` / `limitRedeliverPrefix`), and `redeliverHas(key,
  prefix, n) bool` for the retry check.
- Limit names `queue_redeliver_key`, `queue_redeliver_prefix`.

**`internal/relay/conn.go`**: under `c.mu`: `skipFrom, skipTo, skipSize int64`.

**`internal/relay/relay.go` `Options`**: `QueueRedeliverPerKey`, `QueueRedeliverPerPrefix
int64` (0 = default, negative = off, tests only).

**`internal/relayclient/relayclient.go` `dispatch`**: on `envelope.Parse` error →
`envelope.AckTarget(frame)`; if ok and `!envelope.IsEphemeral(typ)` → send the ack for
`(from, id)`; count the frame (acked or not) and emit at most one Warn per minute
(`event=relay_bad_frame`, `count`, `acked`). The `Classify` failure branch is counted in the
same line.

**`cmd/relay/limits.go`**: `--queue-redeliver-per-key`, `--queue-redeliver-per-prefix`
(`byteSize`, per hour), in `-h` with defaults, printed on the start line.

**`deploy/early/agentnet-relay.service`**: the two flags written out, and the sizing comment
("redelivery 32 MiB/h per key, 128 MiB/h per prefix; the sweep keeps the WAL ≤ 64 MiB").
**`Docs/ops/early-relay-deploy.md`**: the flags, and the disk budget no longer reserves a
WAL as large as the queue.

**`export_test.go`**: `RetrySkipped()`, `Delivered(key) int64`, `RedeliveredBytes()`,
`SetSweepBatchHook(func(i int) error)` (or an internal-package test instead of a hook).

## 4. Acceptance tests

Reviewer tests are in `Docs/review/55-code-review/tests/` (`.txt` with a `.path` file). They
currently **pass when the defect exists**; each becomes a permanent test with its assertion
inverted. Fake clock: `limitEnv.clock`.

### Converted reviewer tests

1. **T10-02** (`zz_review55_T10-02_test.go` → `TestQueueRedeliveryBudgetPerKey`,
   `internal/relay`).
   - Same setup (8 × 700 KiB queued from one sender, 40 reconnects over 2 fake minutes, never
     acks), with `QueueRedeliverPerKey` 2 MiB and the prefix budget large.
   - Reads use a short timeout (e.g. 300 ms) and count what arrives instead of failing.
   - Assert: the first connection gets all 8 (first delivery is not budgeted); in total
     `downloaded ≤ uploaded + 2 MiB + 2 min × (2 MiB / h) + one frame`; `Queued` is still 8.
   - Then advance 1 h and reconnect: redeliveries resume (the budget refilled).
2. **C02-02** (`zz_review55_C02-02_test.go` → `TestQueueSweepBatched`, internal
   `package relay`, production DSN via `openQueue`).
   - Default fill 256 MiB of expired 1 MiB rows (the `ZZ_MB` / `ZZ_FRAME` knobs stay for a
     manual 1 GiB and 2 GiB run; the report records the timings).
   - Assert: every row deleted, totals 0, no transaction deleted more than 32 rows (hook or
     counter), a concurrent `add` started during the sweep returns within 1 s, the WAL file
     is ≤ 64 MiB + 1 MiB afterwards, and `PRAGMA journal_size_limit` is 67108864.

### New tests

3. **Prefix budget.** Four recipient keys in 10.2.0.0/24, key budget large, prefix budget
   4 MiB: their redelivered bytes together stay ≤ 4 MiB + one frame. A recipient in
   10.3.0.0/24 in the same minute gets all of its redeliveries.
4. **Honest reconnect.** A recipient reads 3 of 8 queued frames, acks them, drops the
   connection with 5 in flight; on reconnect all 5 arrive at once and nothing is skipped
   (no `queue_redeliver_*` line).
5. **Skip, new mail, retry.** A key whose budget is spent: a new envelope for it is
   delivered while its old rows are skipped; after the clock passes the refill and
   `RetrySkipped()` runs, the skipped rows arrive, oldest first, and are charged.
6. **No database reads after a skip.** With the budget spent, a reconnect reads no frame of
   an old row (a query counter, or `next` called only with `seq > H`).
7. **H is persistent and monotonic.** With a queue file: deliver, restart the relay,
   reconnect without acking → the rows count as redeliveries. Two overlapping connections of
   one key (replacement) never lower H (`markDelivered` with a smaller seq is a no-op).
8. **Delivery order pins the H rule.** Rows added while a drain is running, and busy-path
   rows of a connected key, all get `seq > H` at the time they are added.
9. **Migration R3.** An R2 database with queued rows opens, gets `queue_delivered`, and its
   rows are first deliveries. `relay admin` and `relay backup/restore` still work on it.
10. **Payload refused (`bad_envelope`)** — `TestRelayRefusesBadPayload`. Each of these gets
    `bad_envelope`, is not queued (`Queued` 0) and is not forwarded to an online recipient:
    `"payload":1`, `{}`, `[]`, `null`, no `payload`, `"not base64!"`, `"QQ"` (unpadded),
    `"QQ==QUFB"` (padding inside), `"QUFB"` (escape), a good `"payload":"QQ=="`
    followed by `"payload":1`, and a good one followed by `"PAYLOAD":1`. Accepted and
    forwarded byte for byte: `""`, `"QQ=="`, `"QUFB"`, a 700 KiB valid payload, and a bad
    `"payload":1` followed by a good `"payload":"QQ=="` (last wins). A presence envelope
    with `"payload":1` gets `bad_envelope`.
11. **Validator equals the decoder.** A fuzz test: for escape-free strings, the payload check
    accepts exactly what `base64.StdEncoding.DecodeString` accepts; and every
    `envelope.Marshal` output passes `ParseHeader`.
12. **The daemon acks what it cannot parse** (`internal/relayclient`, fake relay). A frame
    with valid routing fields and `"payload":1` is acked with its own `(from, id)`, not
    handed to `OnEnvelope`, and not added to the seen-set; the same with an ephemeral
    `type` is not acked; a frame whose `from` is invalid is not acked; 100 such frames give
    one log line with `count=100`.
13. **Ack-forgery guard.** For a corpus of frames with two `from` keys, `From`/`FROM`
    variants and two `id` keys, the `(from, id)` the client acks equals what the relay's
    `ParseHeader` decoding yields for the same frame. End to end on a real relay with an
    older-relay test switch that skips the payload check: stranger A sends
    `{"from":"<P>",…,"from":"<A>","id":"<id of P's queued mail>","payload":1}` to victim V;
    V acks `(A, id)` and P's queued mail to V is still delivered.
14. **Sweep failure keeps progress.** A hook fails the 3rd batch: batches 1–2 stay deleted,
    the in-memory totals equal a `rebuildTotals` scan, and the next `Sweep` finishes.
15. **Tick budget.** With a tiny time budget the sweep deletes at least one batch per tick
    and finishes over later ticks; `Close` during a sweep returns promptly (stop checked
    between batches).
16. **Prune.** `queue_delivered` loses the mark of a key whose rows were all acked or
    expired, and keeps the mark of a key that still has rows.
17. **cmd/relay.** The two flags parse with units, appear in `-h` with their defaults, are
    printed on the start line, and the metrics endpoint shows the two new counters.

### Merge gate

HANDOFF rule 5 as usual, plus the per-OS lint. Run tests 1, 3, 5 and 13 with `-count=3`.
Delete any `zz_review55_T10-02_test.go` / `zz_review55_C02-02_test.go` from `internal/relay`
if present (the converted tests are new files). Update `deploy/early/agentnet-relay.service`
and `Docs/ops/early-relay-deploy.md`.

## 5. Defaults for the early relay (4.1p)

| Setting | Value | Why |
|---|---|---|
| `--queue-redeliver-per-key` | 32 MiB / h, burst 32 MiB | An honest reconnect mid-drain needs ≈ 6 MiB again (4 MiB buffer + a batch); this covers ≈ 5 in a row, and a key's whole 32 MiB queue once |
| `--queue-redeliver-per-prefix` | 128 MiB / h, burst 128 MiB | Several daemons behind one NAT each reconnecting mid-drain; one attacking prefix ≈ 0.3 Mbit/s |
| Sweep batch | 32 rows (≤ 32 MiB), constant | ≈ 0.35 s of lock at C02-02's measured ≈ 95 MiB/s |
| Sweep time per tick | 30 s, constant | 1 GiB (the unit's `--queue-max-total`) takes ≈ 11 s, so one tick |
| `journal_size_limit` | 64 MiB, constant | WAL ≤ 64 MiB after a sweep, instead of the size of the expiry |
| `--queue-max-total`, `--queue-min-free-disk` | 1 GiB, 512 MiB (unchanged) | The floor now only needs one batch of WAL headroom |

With 1 GiB of queue, the worst the attacker can do after F2: 1 GiB delivered once (it
uploaded it), then ≈ 128 MiB / h per attacking prefix; and one 1 GiB expiry as ≈ 32 short
pauses within one minute.

## 6. Open decisions

Full text with options in
[relay-hosted.md §Open decisions (R55-F2)](../protocol/relay-hosted.md#open-decisions-r55-f2).

| OD | Question | Options | Recommended |
|---|---|---|---|
| OD-R55F2-1 | What the new budget counts | (a) redeliveries only, per key + per prefix; (b) every queue delivery; (c) hold-back timer (N min, K times) instead of bytes | **(a)** |
| OD-R55F2-2 | A redelivery over budget | (a) skip it, deliver new rows, retry when refilled; (b) pause the whole drain; (c) close 1013 | **(a)** |
| OD-R55F2-3 | Budget sizes | (a) 32 MiB/h key, 128 MiB/h prefix; (b) 8 / 32; (c) 128 / 512 | **(a)** |
| OD-R55F2-4 | Where "already delivered" lives | (a) persistent per-key high-water table (R3); (b) per-row column + index; (c) memory only | **(a)** |
| OD-R55F2-5 | Relay payload rule | (a) required string, no escapes, padded std base64, all envelopes; (b) as (a) but accept missing/`null`; (c) no relay change | **(a)** |
| OD-R55F2-6 | Daemon acks what it cannot parse | (a) ack via the relay's own header decoding, non-ephemeral only, not handed up, one Warn/min; (b) never ack; (c) (a) + `doctor` count | **(a)** |
| OD-R55F2-7 | Sweep batch | (a) 32 rows, lock released between, 30 s per tick; (b) byte-bounded batches; (c) 256 rows | **(a)** |
| OD-R55F2-8 | WAL bound | (a) `journal_size_limit` 64 MiB + `TRUNCATE` after big sweeps; (b) limit only; (c) `TRUNCATE` after every sweep | **(a)** |

None reopens an owner decision. OD-R55F2-1 (a) and -3 (a) together are what D47's "High"
rating asks for: they remove the amplification, not just slow it.

## 7. Notes for the adversarial reviewer

Points I am least sure of:

- **The high-water rule.** It assumes a key's delivered rows are exactly `seq ≤ H`. Check:
  rows filtered by `next` because they expired (never delivered, `seq < H`: harmless, the
  sweep deletes them); a replaced connection whose old drain raises H after the new one read
  it (H is re-read per batch; at worst one batch of the old connection's rows is a free first
  delivery on the new one); the retry range; rows queued through the busy path while
  connected. A counter-example would let an attacker turn redeliveries into free first
  deliveries.
- **Can H be reset?** Only by the prune (a key with no rows has nothing to redeliver) or a
  restore (the backup restores rows and H together). Check that nothing else deletes marks.
- **Retry and ordering.** Retried rows come after newer ones. envelope.md's ordering rule is
  about direct forwarding versus the queue; I argue the retry keeps it (direct forwarding is
  off while the retry drains). Check against `session.*` users.
- **Ack-forgery.** The guarantee rests on one decoding function shared by relay and client,
  and on the relay's `from` check at ingress. A hostile relay can already delete any row,
  so it gains nothing. Check whether any path hands the client a frame that did not pass
  the relay's ingress (pairing replies are control frames, so they are excluded).
- **Payload check vs. the daemon parser.** Relay-accepted ⇒ daemon-parseable relies on
  `Envelope` and the header struct sharing field names and on the escape-free rule. Probe
  with invalid UTF-8, `\u0000`, very long strings and BOMs.
- **Shared NAT.** The prefix redelivery budget is the one thing an attacker can spend on an
  honest neighbour's behalf. The effect is delay of re-sends only; check that new mail really
  is never held back by a skip.
- **WAL.** `journal_size_limit` truncates at the next WAL reset; a `VACUUM INTO` backup
  holding a snapshot delays it. Check the sizes against the runbook's disk budget.
- **R55-009 verification** (D47): `verify/R55-009.md` on main CONFIRMS it on current main
  (F1 changed nothing: 291 MiB downloaded for 7 MiB uploaded; 145 MiB/min for one 5.5 MiB key
  at the 4.1p flags) and recommends High. Its fix notes match this spec: a persisted per-key
  delivery mark (here H), a delivered-bytes bucket charged in `drainStep` before
  `sendReserved` (here: redeliveries only, OD-R55F2-1), genuine drops still redeliverable
  (here: within budget at once, else by the retry; its per-row counter / not-before
  alternative is OD-R55F2-1 (c) and -4 (b)), no reliance on F1's memory budgets, and
  R55-010 closed in the same ticket.
