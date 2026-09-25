# Audit log

Status: **draft** for Phase 3 (plan step 3.6). Ticket split:
[../review/42-phase3-tickets.md](../review/42-phase3-tickets.md). Change this document first.

## What exists (Phases 0–2)

`internal/audit` appends rows to `audit_events` (migration 1): `id INTEGER PRIMARY KEY
AUTOINCREMENT, ts TEXT, actor TEXT, action TEXT, detail TEXT (JSON)`. Triggers reject every
`UPDATE` and `DELETE`. About 75 call sites append through `Log.Append` (own statement) or
`audit.AppendTx` (inside the caller's transaction); `agentnetd install`/`uninstall` append
from a separate process (`cmd/agentnetd/install.go`). Every Phase 1 and Phase 2 spec lists
its actions, and the **no-content invariant** holds and is tested
(`TestPhase2AuditHasNoContent`): detail carries only ids, keys, enums, counts and sizes,
never titles, briefs, results, notes, reasons, paths, context, commands or codes.

The plan's actions (pair, request, accept, grant, fetch, revoke, release, result, decision)
map to `pair.*`, `request.*`, `grant.*` (`grant.fetch` per fetch, summarised beyond 60 a
minute), `ws.release`, `ws.result`/`ws.result_in` and the new `decision.*`
([decision.md](decision.md#audit)). Phase 3 adds the chain, the `log` command and the
debate/decision/experience actions; it does not change what a row may contain.

## The chain (3.6)

Every row gets a `hash` that commits to the row and to the previous row's hash:

```
genesis    = SHA-256("dorylinae-audit-genesis-v1")                       (32 bytes)
row_c(r)   = canonical({"action": r.action, "actor": r.actor, "detail": r.detail,
                        "id": r.id, "ts": r.ts})
hash(r_n)  = SHA-256("dorylinae-audit-v1\n" ‖ hash(r_{n-1}) ‖ row_c(r_n))  (hash(r_0) = genesis)
```

- The previous hash enters as **32 raw bytes**; the stored value is lowercase hex.
- `row_c` is canonical JSON ([agent-card.md](agent-card.md#canonical-serialisation)) of the
  **stored** values: `detail` is the stored JSON **text as a string** (not re-parsed or
  re-canonicalised), `ts` the stored string, `id` the integer. So verification never depends
  on how a Go version marshals a map, and every stored byte is covered.
- The chain runs over **all** rows in `id` order, including the rows written before the
  migration ([Existing rows](#existing-rows)).

### Appending

`Append` and `AppendTx` keep their signatures, so no call site changes. Inside the
transaction that inserts the row (the caller's for `AppendTx`, an own `BEGIN IMMEDIATE`
one for `Append`):

1. Read the head: `SELECT id, hash FROM audit_events ORDER BY id DESC LIMIT 1`.
2. If there is no row, or the head's `hash` is NULL (the first append after migration 18),
   first insert the [chain start](#existing-rows) row.
3. Insert the new row with an **explicit** `id = head.id + 1` and its `hash`.

The daemon's store has one connection (`SetMaxOpenConns(1)`), so in-process appends are
serialised. A second process (`agentnetd install`) is serialised by SQLite's write lock:
`Append` uses `BEGIN IMMEDIATE`, and on `SQLITE_BUSY` it retries once after 100 ms and then
returns the error. A failing `AppendTx` fails the caller's transaction, as today.

### Migration 18

```sql
-- migration 18 (3.6a): audit_chain
ALTER TABLE audit_events ADD COLUMN hash TEXT
    CHECK (hash IS NULL OR (length(hash) = 64 AND hash NOT GLOB '*[^0-9a-f]*'));
CREATE TRIGGER audit_events_chained BEFORE INSERT ON audit_events
WHEN NEW.hash IS NULL
BEGIN SELECT RAISE(ABORT, 'audit_events rows must be chained'); END;
```

The update and delete triggers of migration 1 stay. After migration 18 no writer can add an
unchained row, so any code that bypasses `internal/audit` fails loudly. The migration does
not compute hashes (migrations are plain SQL); the first append does ([Appending](#appending)
step 2).

**Rewind tests.** Migration 18 is the first migration that alters a migration-1 table. The
two rewind tests in `internal/store/store_test.go` (`TestMigration8PreservesPeers`,
`TestMigrationAddsPeerTrust`) delete `migrations` rows above 7 or 2 and reopen, which
re-runs 18: `ADD COLUMN hash` would fail ("duplicate column") and so would the trigger.
So 3.6a adds to **both** DROP lists `DROP TABLE audit_events` (which drops its index and
triggers) followed by the migration-1 `CREATE TABLE`/`INDEX`/`TRIGGER` statements, so the
rewound database is exactly schema version 7 (or 2). Every later migration that alters an
existing table needs the same treatment; the ticket plan says so per migration.

### Existing rows

Rows written before migration 18 have `hash = NULL` and can never be updated (the trigger
forbids it). They are chained **virtually**: verification computes their hashes in `id`
order from `genesis`, exactly as for stored rows, and uses the last one as the previous
hash of the first stored row. That first stored row is always:

```
action "audit.chain_start", actor "daemon", detail {"legacy_last_id": <id or 0>, "legacy_rows": <n>}
```

So the legacy rows are protected **from the moment of the migration on**: changing,
inserting or deleting one afterwards changes the virtual chain and breaks the stored hash of
`audit.chain_start`. What happened to them **before** the migration cannot be checked, and
`log --verify` says so (`legacy_rows`). A fresh install writes `audit.chain_start` with
`legacy_rows: 0` as its first row.

### Vector

```
genesis  fa4302605d936ae73c80ffaba49180f3676a0c659a72f4c06d88fb64c6a979dd
row 1 (legacy, hash NULL)
  {"action":"daemon.start","actor":"daemon","detail":"{\"pid\":4242,\"version\":\"0.3.0\"}","id":1,"ts":"2026-10-01T09:00:00.123456789Z"}
  virtual hash 554876a662aa875734c153e722b7b6acd327058b14b02a3af13bd798d0d03f48
row 2
  {"action":"audit.chain_start","actor":"daemon","detail":"{\"legacy_last_id\":1,\"legacy_rows\":1}","id":2,"ts":"2026-10-01T09:00:05.5Z"}
  hash c2878d547c6df62488566622d96d9c70e5891cffc849a5dc462a5aa56df24d85
row 3
  {"action":"peer.verify","actor":"cli","detail":"{\"fingerprint\":\"abcd\",\"peer\":\"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc\"}","id":3,"ts":"2026-10-01T09:00:06Z"}
  hash 3daa4dae406cb5ab73902738474b6386b3112dd17eec7344978c9a665d88b6a2
```

Reproduced by `tools/specvectors` and `tools/verifyvectors` (ticket 3.6a).

## Verification

`agentnet log --verify` (IPC `audit_verify`) walks every row in `id` order and reports the
first failure:

| Check | Failure `reason` |
|---|---|
| ids strictly increase by 1 from the first stored-hash row on (legacy rows may have gaps) | `gap` |
| no NULL `hash` after the first stored one | `unchained` |
| the first stored row is `audit.chain_start` with `legacy_rows` = the number of rows before it and `legacy_last_id` = the id of the last of them | `chain_start` |
| each stored `hash` equals the recomputed one | `hash_mismatch` |
| each `detail` is valid JSON and `ts` parses | `malformed` |
| with `--anchor ID:HASH` (repeatable): row `ID` exists and its hash is `HASH` | `anchor_mismatch` / `anchor_missing` |

Result: `{"ok": true, "verify": {"status": "ok"|"broken", "rows", "legacy_rows",
"chained_from", "head": {"id", "hash", "ts"}, "first_bad"?: id, "reason"?}}`. Exit 0 when
`ok`, **exit 5** when `broken` (a new exit code, documented in `Docs/cli/log.md`, so scripts
can tell tampering from an IPC error; exit 1 stays "error"). The walk streams rows and takes
about a second per 10⁵ rows; the IPC 2-second rule is relaxed for `audit_verify` only, like
`wait`: the CLI calls it with `--timeout` (default 120 s).

### What the chain proves, and what it does not

It **detects**, for every row from `audit.chain_start` on (and the legacy rows from the
migration on):

- a changed field in any row (the plan's acceptance: "tampering with one line breaks the
  chain and `log --verify` says so");
- a row deleted, inserted or reordered anywhere **before the head**;
- an unchained row written by code that bypasses `internal/audit`.

It **does not** detect, on its own:

- **A rewrite of the whole chain.** The hash has no secret. Anyone who can write the
  database file (which includes any program running as the user, by the boundary of
  [approval.md §Threat model](approval.md#threat-model)) can drop the triggers, change rows
  and recompute every hash. The chain makes tampering **evident**, not impossible.
- **Truncation of the newest rows** (removing the tail leaves a valid, shorter chain).
- **Rows that were never written** (a modified daemon, or an action the code does not audit).
- **Wrong timestamps.** `ts` is the local wall clock, covered by the hash but not checked
  against anything.

A keyed chain (an HMAC key in the keystore) was considered and rejected: the same user can
read the keystore's file fallback, and a keyed chain can no longer be checked by anyone
else.

### Anchors

Both gaps above close for everything **before an anchor**: a copy of `(id, hash)` kept
somewhere the attacker cannot rewrite. Phase 3 provides the cheap form (OD-P3-6):

- `agentnet log --head [--json]` prints `{"id", "hash", "ts"}` of the newest row.
- `agentnet log --verify --anchor ID:HASH` checks that a head recorded earlier is still in
  the chain unchanged. The owner can paste a head into a commit message, a ticket or a
  message to a teammate; any later rewrite of the rows up to it is then detected.

Deferred options (not needed for the acceptance test): a periodic `audit.checkpoint` signed
with the identity key (it adds nothing against a same-user attacker who can read the key
file fallback), and sending the local head to the peer inside the Decision signature so each
debate anchors both logs (it reveals the peer's row count). OD-P3-6 lists them.

## `agentnet log`

```
agentnet log [--since DURATION|TIME] [--until TIME] [--session ID] [--action PREFIX]
             [--limit N] [--json]
agentnet log --verify [--anchor ID:HASH]… [--json]
agentnet log --head [--json]
```

IPC `audit_list {since?, until?, session?, action?, limit?, after_id?}` →
`{"events": [{"id", "ts", "actor", "action", "detail", "hash"}], "next_after_id"?}`, oldest
first, at most `limit` (default 1000, max 5000) per call; the CLI pages with `after_id`.

- `--since` takes a Go duration (`24h`, `90m`) or an RFC 3339 time; `--until` a time.
- `--session s-…` shows the rows whose `detail.session` is that id, **plus** the rows of its
  request (`detail.request` = the session's request id and `detail.peer` = its peer), its
  grants (`detail.grant` in the session's grant ids), its decision (`detail.id` = the
  derived `d-` id) and its approvals (`detail.subject` = the session or one of its grant
  ids). An `r-` id resolves to its session. A request without a session shows its request
  rows.
- `--action` filters by prefix (`grant.`).
- Human output: one line per row, `ts actor action key=value …` with detail values printed
  as JSON scalars (detail is content-free by construction, but values still go through the
  control-character cleaner of `notify.Clean`, so a key or name from a peer cannot inject
  terminal escapes).
- `--json`: `{"ok": true, "events": […]}`.

Filters are views; `--verify` always checks the whole chain. The command reads through the
daemon (IPC), like every other command.

## Scope: every daemon action

3.6 adds an **inventory test** (`TestAuditInventory`): for each IPC method and each
registered mail kind, one e2e step asserts that at least one audit row with the documented
action appears (polled with a deadline). Methods that only read (`*_list`, `*_show`,
`status`, `audit_*`) are exempt and listed in the test. Gaps found while writing this spec,
fixed in 3.6b: `grant.orphan` lacks the `grant` id (review 28, L9).

## No content, still

- The chain adds only hashes of rows that are already content-free. A hash of content-free
  data reveals nothing new, and `audit.chain_start` carries counts.
- New Phase 3 actions ([debate.md](debate.md#audit), [decision.md](decision.md#audit),
  [experience.md](experience.md#audit)) carry ids, enums, counts, sizes and hashes only.
- `TestPhase3AuditHasNoContent` (ticket 3.9) extends the Phase 2 test with markers in
  topics, positions, arguments, evidence, challenges, proposals, answers, constraints,
  context files and experience records, on both sides.
- Pruning the log would break the chain; nothing prunes it in Phase 3. A later "checkpoint
  then prune" design is deferred until a beta user's log gets large.
