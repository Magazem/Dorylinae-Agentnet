# Retention and pruning

Status: **spec**, ticket R55-F13 (review 55 R55-018, R55-064; owner decision D50). Change
this document first for any change to what the daemon keeps or deletes.

A paired peer can make the daemon store data: requests, mail, work-session results, debate
entries, grants. Before R55-F13 all of it was kept forever, and nothing bounded how much of it
a peer could create (review 55 R55-018). Owner decision D50 fixes the policy:

1. **Per-peer caps** bound what a peer can make the daemon store: stored incoming requests
   ([request.md §Per-peer caps](request.md#per-peer-caps-r55-f13)), held grants per session
   ([grant.md §Kinds](grant.md#kinds)) and the Agent Card size
   ([agent-card.md §Size](agent-card.md#size)). A full cap is never silent: the sender's
   request ends declined with code `inbox_full` (the open cap, D50's clear refusal) or its mail
   ends `failed` (`rejected`) (every other cap). Keys introduced by one team owner in the last
   7 days share one more set of request caps, so an owner cannot multiply them by introducing
   fresh keys (review 71b F2); established members are bounded by their own caps only
   (owner decision D62).
2. **One copy.** The signed plaintext of an applied mail is not kept a second time:
   `mail_inbox.signed` is stored blank (`''`) for every kind
   ([mail.md §Dedupe and inbox](mail.md#dedupe-and-inbox)). Each kind keeps what it needs in
   its own tables.
3. **No automatic deletion of content.** The daemon never deletes a request, a result, a
   debate, a Decision or an experience record on its own. The user removes finished items
   with [`agentnet prune --older-than D`](../cli/prune.md).

The daily request cap counts by receipt time, so a relay that holds an honest peer's mail
and delivers it in one burst can bring that peer over it (review 81 L2,
[request.md §Per-peer caps](request.md#per-peer-caps-r55-f13)).

The automatic clean-ups that already exist are metadata or short-lived state, and stay as they
are: `mail_seen` after 35 d ([mail.md](mail.md#dedupe-and-inbox)), final outbox rows 30 d after
`updated` ([mail.md §Outbox](mail.md#states)), cancel tombstones after 31 d
([request.md](request.md#cancel-od-p1-11)), decided approvals after 30 d
([approval.md](approval.md)), webhook queue rows after 7 d ([notify.md](notify.md)), and the
team invite tables ([team.md](team.md)). The audit log is **append-only** and is never pruned,
by `prune` or anything else (owner decision D49).

## Finished items

`prune` removes an item only when it is **finished** and **older than the cutoff**, where
`cutoff = now − older_than` and `older_than ≥ 35 d` (see [Why 35 days](#why-35-days)).

A **request** row (`requests`, direction `in` or `out`) is finished when all of these hold:

- its `state` is final: `declined`, `completed` or `cancelled`;
- its `updated` is before the cutoff;
- its work session, if any (`work_sessions` with the same `peer` and `request_id`, role
  `worker` for an `in` row and `requester` for an `out` row), is `closed` and its `updated` is
  before the cutoff;
- its debate, if any (`debates` with the same `peer` and `request_id`, role `respondent` for
  an `in` row and `initiator` for an `out` row), is in phase `closed` or `broken` and its
  `updated` is before the cutoff.

Removing a finished request removes, **in the same transaction**, everything that belongs to
it:

| Table | Rows removed with the request |
|---|---|
| `work_sessions` | its session row |
| `grants` | every grant of that session, both directions |
| `experience_records` | the records of that session (any role) |
| `debates`, `debate_entries`, `debate_constraints` | its debate and all its entries and constraints |

`prune` also removes rows whose request is already gone, when they are finished and older than
the cutoff by the same rules: a `closed` work session (with its grants and experience records)
and a `closed` or `broken` debate (with its entries and constraints). Grants outside those two
cases follow their direction (review 71b F1):

- a **`held`** grant whose `exp`, or whose `revoked_at` when it is `revoked`, is before the
  cutoff, whatever state its session is in. A held row is only the holder's copy of a token
  that can no longer be used (grant.md [Verification](grant.md#verification)); nothing else
  reads it.
- an **`issued`** grant only when **no `work_sessions` row has its `session`** and its `exp`
  is before the cutoff. An issued grant in a session that still exists is removed only with
  that session. The reason is the [quarantine
  rule](work-session.md#quarantine-24): clause 1 holds for a result in a session that has
  **any** sensitive grant that was ever active, expired or revoked. Removing such a row from a
  session that is still open (sessions have no maximum life) would release the next result of
  that session unquarantined. Clause 2 (a sensitive grant to the same peer with `exp` later
  than `now − 7 d`) cannot be affected: every removed grant has `exp` before `now − 35 d`,
  since `exp ≤ nbf + 7 d` and a revoked row is kept until its `exp` passes the cutoff.

**`mail_inbox`** rows whose `received_at` is before the cutoff are removed. They hold no
content (`signed = ''`, or pre-R55-F13 plaintext; see [Existing rows](#existing-rows)); they
exist only for dedupe. Each call also **blanks** pre-R55-F13 rows of any age that still hold
plaintext (`signed <> ''` → `''`, counted as `inbox_blanked`), OD-F13-4. In the **same transaction**, `prune` first runs the regular
`mail_seen` prune (`received_at < now − 35 d`). The cutoff is at least 35 days old, so an id
never leaves `mail_inbox` while `mail_seen` still holds it: the two tables stay one dedupe set
([mail.md §Dedupe and inbox](mail.md#dedupe-and-inbox)).

**Never removed** by `prune`: `audit_events`; `decisions` (the signed record of a debate,
bounded at one per debate by [decision.md §Size](decision.md#size)); any item that is not
finished (an `open` session, a `pending` request, however old); peers, teams and team members;
keys; settings; non-final outbox rows; approvals (pruned on their own); device links.

An `out` request whose mail was never delivered (its outbox row ended `failed` or `expired`)
stays `pending` and is therefore never finished; the sender resends it
([request.md §Idempotency](request.md#idempotency)) or keeps it. This is the only kind of row
the user's own actions can leave unprunable, and it grows only with the user's own requests.

`prune` is irreversible, and any local process that can call IPC can run it (an agent
included, for example one misled by a brief it received). Its reach is bounded by the rules
above: nothing unfinished, nothing younger than 35 days, never a Decision or the audit log,
and every call is audited. And no removal happens without a **human approval** in the
approval window ([Approval](#approval), owner decision D57, OD-F13-8 = (b)), so an agent
cannot delete history on its own.

### Why 35 days

Every removed item is older than every window in which a peer's mail about it is treated as
new, so nothing is re-admitted after a prune:

- A request's `created` ≤ its `updated` < cutoff ≤ now − 35 d. A resend of a pruned `in`
  request therefore finds no row and is older than 30 days: it is refused as an [invalid
  body](request.md#invalid-bodies) (Receiving step 2), not stored again as new.
- `mail_seen` keeps an id for 35 days, and a mail older than the [receive age
  limit](mail.md#receive-age-limit) (14 d) is rejected, so a replayed mail of a pruned item is
  never applied again. A peer that re-uses an old mail id in a new mail (fresh `created`) sends
  a new mail: it is applied as one, and the kind's own idempotency and the points below decide
  what it does.
- A late lifecycle mail for a pruned `out` row is an orphan (ack, ignore, audit
  `request.orphan`, [request.md §Sender mirror](request.md#sender-mirror)); a late `ws.*` mail
  for a pruned session is `ws.orphan`; a late `grant.revoke` for a pruned grant is ignored.
- Session ids are derived from `(requester, worker, request id)`
  ([work-session.md §Session id](work-session.md#session-id)), and the Decision of a pruned
  debate is kept (`decisions.session` is unique). An honest peer never re-uses a request id,
  but a modified one can send a new `debate` request with the id of a pruned one (fresh
  `created`, so step 2's 30-day bound does not apply). Its derived session already has a
  Decision, so its close could never store one: `Apply` would fail on the unique key and the
  close mail would be retried forever. So a `debate` request whose derived session already
  has a `decisions` row is refused as an [invalid body](request.md#invalid-bodies) at
  Receiving step 2, before anything is stored (review 71b F8). A `work`
  session's rows go with it, so a re-used id there is simply a new request.

## Existing rows

- `mail_inbox` rows written before R55-F13 keep their plaintext until the user runs `prune`,
  which blanks them in batches whatever their age (OD-F13-4, see
  Docs/review/71-r55-f13-spec.md §Review 71b). The migration does **not** blank them: on a
  database that was flooded before R55-F13, one `UPDATE` over every row would need free disk
  space for about as much WAL as the text it overwrites (`secure_delete`) and would run before
  the daemon starts. On a full disk it would fail, the daemon would not start, and `prune`,
  which needs the daemon, could not be run either.
- `in` rows auto-declined before R55-F13 keep their body until `prune` removes them.
- `in` rows received before R55-F13 have `introducer` and `introduced_at` NULL, so they
  count only towards their sender's own caps, not an introducer's ([request.md §Per-peer
  caps](request.md#per-peer-caps-r55-f13)).
- Held grant rows beyond the per-session caps are kept; the caps apply to new grant mail only.

## Migration

Migration **24** `retention_caps` (R55-F13; 23 is reserved for R55-F24). Indexes and two
columns only, no data rewrite, so it runs in about the time of reading `requests` and
`mail_inbox` once:

```sql
CREATE INDEX requests_peer_state ON requests (direction, peer, state);        -- open cap
ALTER TABLE requests ADD COLUMN introducer TEXT;                               -- in only: the sender's introduced_by at receipt
ALTER TABLE requests ADD COLUMN introduced_at TEXT;                            -- in only: its introduction time (D62)
CREATE INDEX requests_introducer_state ON requests (direction, introducer, introduced_at, state);
CREATE INDEX requests_introducer_time ON requests (direction, introducer, introduced_at, received_at);
CREATE INDEX mail_inbox_received ON mail_inbox (received_at);                  -- prune
```

The `requests` columns are added with `ALTER TABLE … ADD COLUMN`, so the requests table is
not rebuilt. The rewind tests that go back past 24 without dropping `requests` and
`mail_inbox` undo it explicitly (drop the four indexes and the two columns).

Migration **25** `approval_kind_data_prune` rebuilds `approvals` (SQLite cannot alter a
CHECK) so that its kind CHECK also allows `data_prune` ([Approval](#approval)). It lists
R55-F24's kinds `peer_verify` and `team_invite` too, so the table ends the same whichever of
the two tickets merges first; it copies every row with explicit column lists, as migration 19
does.

The database runs with `secure_delete` on, so blanked text and deleted rows are overwritten in
the file. Neither blanking nor `prune` shrinks the database file: SQLite reuses the freed pages
for new rows. `prune` does not run `VACUUM` (OD-F13-7).

**Full disk.** With `secure_delete` and WAL, deleting or blanking a row writes its pages
again (zeroed) to the WAL before the checkpoint returns them, so a `prune` call needs free
space of about the content it removes. That is why each call is bounded in bytes
([IPC](#ipc)). On a disk with no free space at all, the user frees a few tens of MB elsewhere
first; the error is `io_error` and nothing is half-removed.

## IPC

`data_prune {"older_than_s": N, "dry_run"?: bool, "approval"?: "a-…"}` → `{"cutoff": "<RFC
3339>", "dry_run": bool, "counts": {"requests", "work_sessions", "grants", "debates",
"debate_entries", "debate_constraints", "experience_records", "mail_inbox", "inbox_blanked"},
"more": bool, "approval"?: <approval view>}`.

- Without `dry_run` and without `approval`, the call creates the [approval](#approval) and
  returns it (`approval`, state `pending`) with the counts it shows and `more: true`; nothing is
  removed. With `approval` (the same `older_than_s`), the call answers `approval` (still
  `pending`) until the human decides, then removes one batch per call as described below.
  A rejected approval is `approval_rejected`, an expired one `approval_expired`, and an id that
  is not (or no longer) a running prune `unknown_approval`; a different `older_than_s` is
  `bad_request`.

- `older_than_s` is an integer number of seconds, at least 3024000 (35 d), written as a plain
  JSON integer. A smaller value, or a missing, quoted or non-integer one, is `bad_request`.
- `dry_run: true` counts what a full prune would remove (and the rows it would blank), in one
  read-only SQL statement using the indexes of [Migration](#migration) (set-based counts, not a
  query per item: about 70 ms for 40,000 finished requests, review 81 L1), outside any write
  transaction, and removes nothing; `more` is `false`.
- Once approved, one call works in one transaction, in this order, and returns what it removed:
  1. finished requests, oldest `updated` first, each with everything that belongs to it: at
     most **500 requests**, and the call stops adding requests once the content of those
     already taken (the byte lengths of `requests.body`, `requests.result`, `last_reply`,
     their sessions' `result` and `changes`, and their debates' `debate_entries.entry`) reaches **8 MiB**.
     The first request is always taken, so a call always makes progress. The dependents of a
     taken request are never split across calls;
  2. orphans (sessions, debates, grants of [Finished items](#finished-items)), at most 5000
     rows of each table;
  3. the `mail_seen` prune, then at most 5000 `mail_inbox` rows older than the cutoff, then
     blanking of at most 5000 pre-R55-F13 `mail_inbox` rows, stopping once 8 MiB of `signed`
     text has been blanked (at least one row).

  `more: true` means rows remain; the caller calls again with the same `older_than_s`. The
  byte bounds keep each call within the 2-second rule of [ipc.md](ipc.md) with
  `secure_delete` on, keep the single database connection free for mail and IPC between
  calls, and bound the free disk space a call needs ([Full disk](#migration)). The daemon
  computes `cutoff` from its own clock: for a dry run at that call, for a removal once, when
  the approval is created ([Approval](#approval)); every batch removes against that fixed
  cutoff.
- Errors: `bad_request`; `io_error` when the database write fails (for example a full disk:
  a delete needs a little free space for its journal); the approval errors of
  [approval.md](approval.md#ipc-and-cli) (`approval_limit`, `approval_locked`,
  `approval_unavailable`, `approval_expired`, `unknown_approval`) and `approval_rejected`.

## Approval

Owner decision D57 (OD-F13-8 = (b)): `data_prune` removes or blanks nothing until a human has
approved it through [approval.md](approval.md), like every other approval: the approval
window on a desktop, the daemon's terminal in [terminal mode](approval.md#headless-machines),
and `approval_unavailable` where neither exists. The approval store, its limits (5 pending,
20 an hour, 3 attempts, 10 wrong codes a day) and the one summary builder are reused; the
kind is `data_prune`.

- **Created by the first removing call.** `data_prune {"older_than_s"}` (no `dry_run`, no
  `approval`) fixes the cutoff (`now − older_than`, from the daemon's clock), counts what a
  prune at that cutoff removes (as a dry run), and creates the approval. When the count is
  zero it creates nothing and returns the zero counts. The waiting object is kept in memory
  only (like `debate_constraint`), under a subject `n-` + 32 hex: a restart, which expires
  every pending approval, leaves nothing to remove.
- **Summary** ([approval.md §Contents per kind](approval.md#contents-per-kind)): the minimum
  age, the cutoff, and the counts, numbers only (never a peer, a request or any content).
- **Counts bound at creation.** The counts are taken once, when the approval is created, and
  bound to it; confirm rebuilds the summary from those counts and compares it with the one
  shown, but does not count again inside its write transaction on the daemon's single
  connection (review 81 L1). A change after counting can only take items out of the set: an
  item is in it only if its `updated` is before the fixed cutoff, and every change sets
  `updated` to now. So the shown counts are an upper bound on what the batches remove,
  except `mail_inbox` rows whose `mail_seen` row ages out in the meantime (each batch uses
  its own `now` for that rule).
- **Perform** removes nothing: it lets the calls that name the approval run for one hour.
  Each such call removes one bounded batch against the **approved cutoff**, in its own
  transaction, audited as below. The call that returns `more: false` ends the approval's
  use; a later call with it is `unknown_approval`.

## Audit

Every `data_prune` call that removed or blanked at least one row appends `data.prune` (actor `cli`) with
`{"older_than_s", "cutoff", "approval", "requests", "work_sessions", "grants", "debates",
"debate_entries", "debate_constraints", "experience_records", "mail_inbox", "inbox_blanked"}`
(`approval` is the id of the approval that allowed it)
(written with `AppendTx` in the prune transaction, so the counts and the removals commit
together): counts only,
never ids of what was removed, titles or content. A dry run writes no row; creating,
approving or rejecting the approval writes the usual `approval.*` rows. Audit rows that name a pruned request,
session or grant stay as they are: they hold only ids and enums, and the chain is never
rewritten.
