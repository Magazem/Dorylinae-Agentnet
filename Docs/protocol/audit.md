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
serialised. A second process (`agentnetd install`, which appends right after it has started
the daemon) is serialised by SQLite's write lock: `Append` runs its transaction on a
dedicated `sql.Conn` opened with an explicit `BEGIN IMMEDIATE` (database/sql's `BeginTx`
issues a deferred `BEGIN`), so the head is read under the write lock, and the DSN's
`busy_timeout(5000)` does the waiting (no extra retry loop; review 43 L9). The explicit `id`
is also the primary key, so two writers can never both append `head.id + 1`: the loser fails
and nothing forks. `AppendTx` runs inside the caller's (deferred) transaction; in WAL mode, if
the other process committed after that transaction's first read, the upgrade to a write fails
with `SQLITE_BUSY_SNAPSHOT`, which `busy_timeout` does not retry. That already holds for any
write today; it is rare (the second writer runs once per install) and the caller's usual
error handling applies. A failing `AppendTx` fails the caller's transaction, as today.

**Migrations and the second process** (review 43 M9). `agentnetd install` opens the store
through `store.Open`, which runs migrations, right after starting the daemon, which runs them
too. `store.migrate` reads the schema version **outside** the per-migration transaction, so
after an upgrade both processes can try migration 18: the loser fails with "duplicate column"
(18), "table already exists" or a rebuild error (19), and if the loser is the daemon it exits
at start. Ticket 3.6a fixes `store.apply`: each migration runs in a `BEGIN IMMEDIATE`
transaction that re-reads `MAX(version)` and skips the migration if another process has
applied it. A test runs two `store.Open` calls on one file concurrently and both succeed.

### Migration 18

```sql
-- migration 18 (3.6a): audit_chain
ALTER TABLE audit_events ADD COLUMN hash TEXT
    CHECK (hash IS NULL OR (length(hash) = 64 AND hash NOT GLOB '*[^0-9a-f]*'));
CREATE TRIGGER audit_events_chained BEFORE INSERT ON audit_events
WHEN NEW.hash IS NULL OR NEW.id IS NOT (SELECT COALESCE(MAX(id), 0) + 1 FROM audit_events)
BEGIN SELECT RAISE(ABORT, 'audit_events rows must be chained'); END;
```

The update and delete triggers of migration 1 stay. After migration 18 no writer can add an
unchained row, or a row other than the new head (`INSERT OR REPLACE` on an existing `id`
would otherwise delete the old row without firing the delete trigger; review 44 L1), so any
code that bypasses `internal/audit` fails loudly. The migration does
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
can tell tampering from an IPC error; exit 1 stays "error"). The walk takes about a second
per 10⁵ rows; the IPC 2-second rule is relaxed for `audit_verify` only, like `wait`: the CLI
calls it with `--timeout` (default 120 s).

**When the daemon will not start** (review 44 L2). After an unchained row the daemon's own
first append (`daemon.start`) fails, so the daemon does not run, and an IPC-only `--verify`
would be unusable exactly when it is needed. `agentnet log` therefore falls back to the
database file when nothing listens on the IPC endpoint: it opens `dorylinae.db` **read-only**
(`store.OpenReadOnly`: no migrations, `query_only`) and runs the same `Query`, `Head` and
`Verify` code in the CLI process, printing `agentnetd is not running; reading the database
directly (read-only)` on stderr. No daemon and no database file is exit 3. The failed append
names the command (`… run 'agentnet log --verify', which works without the daemon`). The
fallback is a read of a file the user owns, so it adds no trust: a same-user attacker can
already read and rewrite it.

**The walk must not hold the daemon's only connection** (review 43 M8). The store has one
connection (`SetMaxOpenConns(1)`), so a single streaming query over 10⁶ rows would stall mail
delivery, the sweeps and every other IPC call for as long as it runs. `Verify` therefore
reads the head once, then walks **pages** of at most 2000 rows (`WHERE id > ? ORDER BY id
LIMIT 2000`, carrying the previous hash between pages), each page its own short read that
releases the connection before the next. Rows appended during the walk are after the head it
read and are not checked in that run; the result's `head` says where it stopped. A test
checks that an IPC call and a mail apply complete while a verify of 10⁵ rows is running.

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
  the chain unchanged. An anchor's hash only ever comes from a row with a stored hash (from
  `audit.chain_start` on), so **an anchor on a legacy row is `anchor_mismatch`** (decision D31:
  it is tampering, not a caller error; a row whose hash was nulled looks exactly the same).
  That is decided after the walk: if the walk fails first, that failure is reported, and an
  anchored row with a NULL hash after `audit.chain_start` is `unchained` (review 44 M1). Only
  a malformed anchor (not `ID:HASH`) is `bad_request`. The owner can paste a head into a
  commit message, a ticket or a message to a teammate; any later rewrite of the rows up to it
  is then detected.

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
first, at most `limit` (default 1000, a larger value is clamped to 5000) per call; when more
rows match, `next_after_id` is the id to pass as `after_id`, and the CLI pages with it until
it is absent (`--limit N` stops after N rows; without it the CLI prints every match).
`since` and `until` are RFC 3339 times (the CLI turns `24h` into one); a bad time, a
negative `limit` or `after_id`, or a `session` that is neither `s-` nor `r-` is `bad_request`.
`audit_list` shows **every row, `audit.chain_start` included** (`hash` is absent on a legacy
row); only `Log.List`, the internal helper of the Phase 1 tests, still hides it. `--since`
and `--until` compare the parsed time (the stored `ts` has a variable number of fractional
digits, so a text comparison would misorder `…:05.5Z` and `…:05Z`).
IPC `audit_head {}` → `{"head": {"id", "hash", "ts"} | null}` is what `--head` prints, and
`audit_verify {anchors?: ["ID:HASH", …]}` → `{"verify": {…}}` is [Verification](#verification).
All three only read. `audit_verify` is exempt from the IPC 2-second rule; `audit_list` and `audit_head` are quick reads.

- `--since` takes a Go duration (`24h`, `90m`) or an RFC 3339 time; `--until` a time.
- `--session s-…` shows the rows whose `detail.session` is that id, **plus** the rows of its
  request (`detail.request` = the session's request id and `detail.peer` = its peer), its
  grants (`detail.grant` in the session's grant ids), its decision (`detail.id` = the
  derived `d-` id) and its approvals (`detail.subject` = the session or one of its grant
  ids). An `r-` id resolves to its session. A request without a session shows its request
  rows, with its peer. An `r-` id whose sessions and request rows, taken together, belong to
  more than one `(direction, peer)` (a `requester` session counts as `out`, a `worker`
  session as `in`) is `ambiguous_request`, even when only one of them has a session: use the
  `s-` id (R55-F20). Only when no row has the
  id any more (pruned) are the rows naming `detail.request` shown for any peer.
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
`status`, `audit_*`) are exempt and listed in the test; so are `notify_test` (one probe, only
its failure is audited as `notify.fail`) and the mail kind `keys` (no `mail.in` by design,
[mail.md](mail.md); rotation is `mailbox.rotate`). The test lists every method and mail kind
in a table with the action it documents, calls each state-changing method through two live
daemons (and the device methods through a linked pair), and a second test compares the table
with the `srv.Handle(...)` and mail-kind registrations in the source, so a method added
without an entry fails the build of the test suite. Gaps found while writing this spec:

- `grant.orphan` lacked the `grant` id (review 28, L9): the holder now audits `{grant, peer}`
  with the id from the verified token, and a test covers it.
- `service.install` (and `service.uninstall`) carried `home` and `executable`, filesystem
  paths, against the rule above (review 44 L4). The detail is now `{platform, custom_home,
  changed}` ([agentnetd-install.md](../cli/agentnetd-install.md)); the install test asserts no
  path separator in it, and `TestAuditInventory` asserts none in any row of either daemon's
  log.

Gaps closed by R55-F31 ([review 84](../review/84-r55-f31-spec.md)):

- The source scan saw only kinds registered by a string literal (`kinds["team.roster"]`). The
  five debate kinds are registered by constant (`kinds[debate.MailEntry]`), so the test could
  not see them (R55-135). The scan now also reads the `Mail… = "debate.…"` constants of
  `internal/debate`. The table maps `debate.entry` → `debate.entry_in`, `debate.reveal` →
  `debate.reveal_in`, `debate.close` → `debate.close_in`, `debate.constraint` →
  `debate.constraint_in` and `debate.sign` → `decision.sign_in`.
- `ws.open` was never written (R55-121). It is now written whenever a session row is created
  ([work-session.md §Audit](work-session.md#audit)), and the inventory expects it for
  `request_accept` on B and for the `request.accept` kind on A.
- Grants ended by a session close or a peer removal now get their own `grant.revoke` row,
  with `reason` `session_closed` or `peer_removed` and actor `daemon` (R55-124,
  [grant.md §Audit](grant.md#audit)).
- Approvals rejected by the daemon now name the cause in `reason`, not in `via`, with actor
  `daemon` (R55-123, [approval.md §Audit](approval.md#audit)).

## When the row cannot be written (R55-F31, D64)

Before R55-F31, no rule said what an action does when its audit row fails. Most sites
appended after their commit and ignored the error. A few returned the error after
committing, so the caller saw a failure for an action that had happened (review 55
R55-142). Owner decision D64 sets the rule: **security-relevant actions write their row with
`AppendTx` in the action's own transaction, so no row means no action. Every other action
logs the failure and reports success.**

An append fails when the database cannot be written (disk full, I/O error, a lock held by
another process past `busy_timeout`) or when the chain refuses the row (an unchained row was
planted, [Migration 18](#migration-18)). It never fails because of what the detail holds.

### Classes

| Class | Actions | How the row is written | If the row fails |
|---|---|---|---|
| **S: grants access or trust** | `approval.create`, `approval.approve`; `grant.create` and `grant.auto` on the policy path, `grant.issue`, `grant.policy_add`; `ws.release`; `peer.verify`, `pair.complete`, `team.roster_apply`, `team.invite_issued` (no transaction, see below); `device.link_intent`, `device.link_active`, `device.scope_set`; `debate.constraint`; `data.prune`; `decision.create`, `decision.sign_in` | `audit.AppendTx` inside the transaction that makes the change | The transaction rolls back and the action fails. The caller gets the error (IPC `internal`), and nothing changed: no state, no outbox mail, no row |
| **S-: removes access or trust** | `approval.reject` (every reason), `approval.bad_code`, `approval.locked`; `grant.revoke` (every reason), `grant.revoked_in`, `grant.policy_remove`; `peer.remove` (also the team GC's `reason: "team"` rows); `device.unlink`, `device.scope_clear`; `decision.refuse` | The same transaction, through `audit.AppendTxSoft` (OD-F31-1 (b), recommended) | The row alone is rolled back to a savepoint, the failure is logged, and the change commits |
| **L: lifecycle** | `daemon.start`, `daemon.stop`, `daemon.stop_requested`, `approval.mode`, `identity.create`, `service.install`, `service.uninstall`, `audit.chain_start` | Before the action, or as its condition, as today | The action does not happen (the daemon does not start; `shutdown` is refused). Unchanged |
| **N: everything else** | Every other action (requests, work sessions apart from `ws.release`, debates apart from the rows above, experience, mail, mailbox, sessions, pairing apart from `pair.complete`, teams apart from the rows above, presence, notify, `grant.in`/`orphan`/`conflict`/`refused`/`fetch`, `grant.create` on the approval path, `peer.verify_fail`, `approval.open`, `approval.limit`, `device.run`, `device.out_of_scope`, `relay.reject_summary`) | `Log.Append` after the commit, as today. A new N row written where a transaction is already open uses `audit.AppendTxSoft` (`ws.open`) | The failure is logged and the action reports success. An N site **never** returns an audit error to its caller |

**Why S- differs (OD-F31-1).** Rolling back a revocation, a rejection or a wrong-code count
because its row failed would keep access open: a grant would stay active, an approval would
stay confirmable, a wrong code would not count. The chain refusing rows is exactly the state
an attacker who planted a row would want. So a narrowing change never waits for its row
(within the limit of "What S- really guarantees" below). If
the owner picks (a) instead, S- joins S, and the S- column of this table is dropped.

`team.invite_issued` (S) has no transaction: the approved invite starts a pairing in memory.
It is written right after `StartTagged`, and if that append fails the daemon cancels the
pairing it just started before it returns the error. So no code is ever released without
its row. The cancel is a new `peers.Manager.Cancel(id)`: it ends the session as failed with
the new code `cancelled` (which writes the usual `pair.fail {id, role, code: "cancelled"}`
row, class N) and withdraws the lookup from the relay as any issuer failure does. The code
was never returned to anyone, so nobody can redeem it in between. The approval stays spent
(the existing rule); the user asks for a new one.

### Mechanics

- `audit.AppendTx(ctx, tx, …)` is unchanged: its error is the caller's, and the caller
  returns it, which rolls back the transaction. It wraps the error as `*audit.WriteError{Action}`,
  so callers and tests can tell a failed row from a failed change.
- `audit.AppendTxSoft(ctx, tx, …)` runs `SAVEPOINT audit_row`, the same insert, and on
  failure `ROLLBACK TO audit_row` and `RELEASE audit_row`. It logs the failure and returns nil.
  A failure of the savepoint statements themselves is returned, because then the transaction
  is unusable.
- **What S- really guarantees (review 84b F1).** SQLite rolls back the *whole* transaction
  itself on some errors (`SQLITE_FULL`, `SQLITE_IOERR`, `SQLITE_NOMEM`, a `RAISE(ROLLBACK)`),
  and then `ROLLBACK TO` fails too. So the savepoint saves the change only from a failure
  that ends the statement alone: the chain refusing the row (Go error before the insert), a
  constraint, a `RAISE(ABORT)`. That is the case S- exists for (a planted row). After a
  transaction-killing error, `AppendTxSoft` returns `*audit.TxLostError`, and the S- caller
  runs its change **once more in a new transaction without the row** (the row's failure is
  already logged). If that also fails, the action fails as any database error of the change
  does. The retry never applies to S rows.
- **Reads inside the new transactions go through the transaction.** The daemon has one SQLite
  connection (`SetMaxOpenConns(1)`). A read on `*sql.DB` (for example `approval.Store.kindSubject`)
  while that connection holds the transaction waits for it forever. Every read that moves
  into a transaction with its row (Confirm, expiry, attempts, lock, reject) uses `tx`.
- Every failed append is logged once, inside `internal/audit`, through the logger the daemon
  installs at start (`audit.SetErrorLog`): `level=ERROR msg="audit write failed"
  event=audit_error action=<action> error=<driver error>`. The action is a fixed name and the
  driver error is SQLite's text. The detail is never logged. Sites that log their own audit
  failure today (`mail`, `mailbox`, `session`, `presence`, `peers`, the reject summary) drop
  their line, so each failure is logged once.
- An S row and its change share one transaction. So a row that is written always matches a
  change that committed, and a crash between them cannot leave one without the other.
  `approval.approve` therefore moves into Confirm's transaction, after the precondition
  check and before `Perform`, so it still precedes the rows `Perform` writes. If that row fails, Confirm behaves as for a failing `Perform`: the
  approval stays `pending` and its timer restarts.

New audit actions must name their class in their spec. The review of each spec checks it,
together with the relay bound of [Who may cause a row](#who-may-cause-a-row-r55-f14).

## No content, still

- The chain adds only hashes of rows that are already content-free. A hash of content-free
  data reveals nothing new, and `audit.chain_start` carries counts.
- New Phase 3 actions ([debate.md](debate.md#audit), [decision.md](decision.md#audit),
  [experience.md](experience.md#audit)) carry ids, enums, counts, sizes and hashes only.
- `TestPhase3AuditHasNoContent` (ticket 3.9) extends the Phase 2 test with markers in
  topics, positions, arguments, evidence, challenges, proposals, answers, constraints,
  context files and experience records, on both sides.
- Pruning the log would break the chain; nothing prunes it in Phase 3. A later "checkpoint
  then prune" design is deferred until a beta user's log gets large. This rests on the
  rule in [Who may cause a row](#who-may-cause-a-row-r55-f14) below. Without that rule a
  relay could grow the log for ever (review 55 R55-015). **D49 keeps the log append-only**
  and removes the relay-driven rows instead.

## Who may cause a row (R55-F14)

The no-prune decision assumed that rows come from the user's own activity and their paired
peers. Review 55 (R55-015, T10-01) showed that the relay could also cause rows. It injected
one junk `mail` and one junk `session.*` frame a second from unpaired keys. That gave 30
`mail.reject` and 30 `session.reject` rows a minute, about 29 MB a day, for ever.

The rule since R55-F14 (owner decision D49, extended by OD-F14-1): **a row is written only
for an event that the user, their own devices or an authenticated peer caused.** Something
that a relay alone can cause, including by forging a paired peer's `from` or replaying an
envelope it carried, is **logged** under the daemon's
[relay-driven log rule](envelope.md#relay-driven-log-lines-daemon) and never audited:

| Event | Before | Since R55-F14 |
|---|---|---|
| Mail reject, steps 1–7 (`unpaired`, `malformed` at 2 or 5, `key_miss`, `decrypt`, `sender_mismatch`, `bad_signature`) | `mail.reject`, 30/min | log only |
| Mail reject `stale` (step 11, receive age limit) | `mail.reject`, 30/min | log only (a relay replays old mail) |
| Mail reject step 12 `bad_keys` for an expired announcement only | `mail.reject`, 30/min | log only (a relay replays a genuine `keys` mail after `not_after`) |
| Mail reject, steps 8, 9, 10, 12 (other cases), `bad_body`, and `limit` (a refused application mail, R55-F13) | `mail.reject`, 30/min | `mail.reject`, once per `(peer, id)` per run, 30/min, `id` only when valid ([mail.md](mail.md#receiving-verification-order)) |
| Every session reject | `session.reject`, 30/min | log only ([session.md](session.md#rejection)) |
| `mail.in` of a kind this daemon does not register | `kind` as sent | `kind: "unknown"` |

**Daily summary (OD-F14-7, recommended (b); review 72b).** Logged-only rejects leave no
lasting trace: under a flood the rotated daemon log keeps only hours of history, and a relay
can push older lines out on purpose. Yet some of them are the evidence a user wants when a
relay turns hostile: `bad_signature`, `sender_mismatch` or `decrypt` under a **paired**
peer's `from` (the relay tampering with or forging that peer's traffic), or session
`bad_binding`. So, if the owner picks (b), the daemon keeps in memory a count per
`(layer, reason)` of every logged-only reject **except `unpaired`** (D49: those are counted
in the log only), and writes one row `relay.reject_summary {since, until, mail: {reason:
count}, session: {reason: count}}` (actor `daemon`) once a day and at a clean stop, only if
a count is non-zero. The keys are the fixed reason names of
[mail.md](mail.md#receiving-verification-order) and [session.md](session.md#rejection);
counts are integers. So a relay can change the numbers but not the number of rows: at most
one a day plus one per stop the user makes (a crash writes none), about 365 rows and
< 150 KB a year. No peer key is in the row (a per-peer map would let the relay choose its
size); the log's first-occurrence fields name one.

Rows written before R55-F14 stay; the chain is not touched. A new audit action that could be
caused by the relay must name its bound in its spec. The review of each spec checks this.
Known peer-driven growth (a paired peer sending many valid mails, each giving `mail.in`) is
R55-F13's concern ([review 55 T10-03](../review/55-code-review/99-report.md)), not the
relay's.
