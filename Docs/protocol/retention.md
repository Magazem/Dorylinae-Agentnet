# Retention and pruning

Status: **spec**, ticket R55-F13 (review 55 R55-018, R55-064; owner decision D50). Change
this document first for any change to what the daemon keeps or deletes.

A paired peer can make the daemon store data: requests, mail, work-session results, debate
entries, grants. Before R55-F13 all of it was kept forever, and nothing bounded how much of it
a peer could create (review 55 R55-018). Owner decision D50 fixes the policy:

1. **Per-peer caps** bound what a peer can make the daemon store: stored incoming requests
   ([request.md §Per-peer caps](request.md#per-peer-caps-r55-f13)), held grants per session
   ([grant.md §Kinds](grant.md#kinds)) and the Agent Card size
   ([agent-card.md §Card](agent-card.md#card)). A full cap is refused clearly, never silently.
2. **One copy.** The signed plaintext of an applied mail is not kept a second time:
   `mail_inbox.signed` is stored blank (`''`) for every kind
   ([mail.md §Dedupe and inbox](mail.md#dedupe-and-inbox)). Each kind keeps what it needs in
   its own tables.
3. **No automatic deletion of content.** The daemon never deletes a request, a result, a
   debate, a Decision or an experience record on its own. The user removes finished items
   with [`agentnet prune --older-than D`](../cli/prune.md).

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
the cutoff by the same rules: a `closed` work session (with its grants and experience records),
a `closed` or `broken` debate (with its entries and constraints), and a grant whose `exp`, or
whose `revoked_at` when it is `revoked`, is before the cutoff. A grant past its `exp` can never
be used again (grant.md [Verification](grant.md#verification)), so it is finished whatever
state its session is in.

**`mail_inbox`** rows whose `received_at` is before the cutoff are removed. They hold no
content (`signed = ''`, or pre-R55-F13 plaintext; see [Existing rows](#existing-rows)); they
exist only for dedupe. In the **same transaction**, `prune` first runs the regular
`mail_seen` prune (`received_at < now − 35 d`). The cutoff is at least 35 days old, so an id
never leaves `mail_inbox` while `mail_seen` still holds it: the two tables stay one dedupe set
([mail.md §Dedupe and inbox](mail.md#dedupe-and-inbox)).

**Never removed** by `prune`: `audit_events`; `decisions` (the signed record of a debate,
bounded at one per debate by [decision.md §Size](decision.md#size)); any item that is not
finished (an `open` session, a `pending` request, however old); peers, teams and team members;
keys; settings; non-final outbox rows; approvals (pruned on their own).

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

## Existing rows

- `mail_inbox` rows written before R55-F13 keep their plaintext until `prune` removes them
  (older than 35 days) or the [migration](#migration) blanks them, per OD-F13-4 (see
  Docs/review/71-r55-f13-spec.md).
- `in` rows auto-declined before R55-F13 keep their body until `prune` removes them.
- Held grant rows beyond the per-session cap are kept; the cap applies to new grant mail only.

## Migration

Migration N (R55-F13; N is the next free version when it merges):

```sql
CREATE INDEX requests_peer_state ON requests (direction, peer, state);   -- request.md, open cap
UPDATE mail_inbox SET signed = '' WHERE signed <> '';                    -- OD-F13-4 (a)
```

The database runs with `secure_delete` on, so blanked text and deleted rows are overwritten in
the file. Neither blanking nor `prune` shrinks the database file: SQLite reuses the freed pages
for new rows. `prune` does not run `VACUUM` (OD-F13-7).

## IPC

`data_prune {"older_than_s": N, "dry_run"?: bool}` → `{"cutoff": "<RFC 3339>", "dry_run":
bool, "counts": {"requests", "work_sessions", "grants", "debates", "debate_entries",
"debate_constraints", "experience_records", "mail_inbox"}, "more": bool}`.

- `older_than_s` is an integer number of seconds, at least 3024000 (35 d). A smaller value,
  or a missing or non-integer one, is `bad_request`.
- `dry_run: true` counts what a full prune would remove, in one call, and removes nothing;
  `more` is `false`.
- Otherwise one call removes at most **500 requests** (with everything that belongs to them),
  then orphans, then `mail_inbox` rows, at most 5000 rows of each other table, in one
  transaction, and returns what it removed. `more: true` means rows remain; the caller calls
  again with the same `older_than_s`. Each call returns within the 2-second rule of
  [ipc.md](ipc.md). The daemon computes `cutoff` from its own clock at each call.
- Errors: `bad_request`; `io_error` when the database write fails (for example a full disk:
  a delete needs a little free space for its journal).

## Audit

Every `data_prune` call that removed at least one row appends `data.prune` (actor `cli`) with
`{"older_than_s", "cutoff", "requests", "work_sessions", "grants", "debates",
"debate_entries", "debate_constraints", "experience_records", "mail_inbox"}`: counts only,
never ids, titles or content. A dry run writes no row. Audit rows that name a pruned request,
session or grant stay as they are: they hold only ids and enums, and the chain is never
rewritten.
