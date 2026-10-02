# Private experience record

Status: **draft** for Phase 3 (plan step 3.7). Ticket: 3.7 in
[../review/42-phase3-tickets.md](../review/42-phase3-tickets.md). Change this document first.

## What it is

When a [work session](work-session.md) closes (any kind, including a
[debate](debate.md)), each daemon writes one **local, private** record of it for its own
side: the raw material for a later feature that learns from past work. **Nothing reads it in
Phase 3** (plan): no IPC method, no CLI command, no mail kind, no webhook and no
notification returns or mentions its content.

The daemon has no model, so it cannot write "what worked" in prose. Every field is taken
from data the daemon already holds for the session, by the fixed mapping below. The record
is therefore a **snapshot**, not an analysis; a later phase may let the local agent add its
own notes (OD-P3-8).

## Record

Canonical JSON, one per `(session, role)`:

| Plan field | Member | Source |
|---|---|---|
| — | `v`, `session`, `request`, `role` (`requester`/`worker`, or `initiator`/`respondent` for a debate), `kind` (`work`/`debate`), `peer` (key), `team` (id), `type` (request type) | the session and request rows. `team` is the request's team id for **every** kind, debates included (R55-F29) |
| problem | `problem: {"title", "brief"}` | the request (for a debate: title and topic) |
| approach | `approach: {"grants"?: [{"action", "sensitive", "state"}], "rounds", "changes"?: <last changes text>}`; debate: `{"positions"?: {"initiator"?: <claim>, "respondent"?: <claim>}, "rounds_used"}` | grants of the session (action, sensitivity and final state only, never paths or labels), `work_sessions.round` and `changes` (Phase 2 keeps only the **last** changes text); debate: the [covered entries](#covered-debate-entries) |
| what worked | `worked?: {"status", "summary"?, "verification"}`; debate: `worked?: {"decision"}` | the **accepted** result's D14 `status` and `summary`; debate (`agreed` only): the Decision's `final_agreement.decision` |
| what failed | `failed?: {"rounds_rejected", "last_changes"?}`; debate (`escalated`): `{"remaining_disagreement_points"}`; any cancelled session: `{"cancelled_by", "cause"?}` | round count − 1 and the last changes text; debate: the number of items in the Decision's `remaining_disagreement` (rule 8 of [decision.md §Derivation](decision.md#derivation): the proposal's and the answer's, duplicates dropped; 0 when absent). `cancelled_by`: `requester`, `worker` or `timeout` for a work session; `initiator`, `respondent` or `timeout` for a debate. `cause` only on B's own closes for a bad peer: `bad_reveal` ([debate.md §Commit–reveal](debate.md#commitreveal)) or `decision_refused` ([decision.md §If B refuses](decision.md#signing)), both with `cancelled_by: "respondent"` |
| verification | `verification: "none"|"tests_passed"|"human_accepted"`, always present; `verification_by?: "requester"|"worker"` | the session. `none` for every debate and every cancelled session. `verification_by` is present iff `verification` is not `none`: `requester` for `human_accepted`, `worker` for `tests_passed` (R55-F29) |
| acceptance | `acceptance: {"outcome", "age_s", "decision"?: {"id", "hash"}}` | the close; the Decision for a debate. No signature state: A writes its record at `closing`, before B signs, and the record is never updated (review 43 L2); the `decisions` table has the current state. On B's `decision_refused` close, `decision` is B's **own** unsigned record (its id and B's hash) when B stored one, absent when it could not derive one |
| — | `opened`, `closed` | the session |

Optional members are absent when there is no data. The record's canonical size is capped at
**65536 bytes**; members are dropped in the order `approach.changes`, `failed.last_changes`,
`problem.brief`, `approach.grants` until it fits, with `"truncated": true` added. Without
those four members every remaining string is bounded by a field cap (title 120 code points;
the result summary, the claims and the decision line 280 code points each), so the fourth
step always fits. The builder
never returns a record over the cap (R55-F29; a test builds a session with 2000 grants).

### Covered debate entries

A debate record describes the debate **as it closed on this side**, the same transcript its
Decision uses (R55-F29, review 55 R55-170):

- **A:** entries in state `applied` (what `entries` in A's close counts).
- **B, applying A's close** (signed or refused): entries in state `applied` or `sent` at
  slots below the close's `entries`. A B entry A cut is `late` and is not covered.
- **B, abandon or bad reveal** (no close applied): entries in state `applied` or `sent`.

`approach.positions` takes the claim of slot 0 and slot 1 from the covered entries only, and is
absent when neither is covered. `rounds_used` counts the covered moves. `worked` and
`failed.remaining_disagreement_points` are read from the Decision this side stored at the close
(A's at `closing`, B's signed or refused one), never by scanning the transcript. A
`cancelled` debate has no Decision, so it has neither member.

**Never in the record** (enforced by the builder and tested with markers):

- a result that was **quarantined and never released**, or discarded, or replaced by
  request-changes without release (OD-P2-6 (c), D18): those bytes were deleted in their own
  transaction and must not be resurrected here. Only the result the requester **accepted**
  (after a release when there was one) contributes `status` and `summary`; its `output`,
  `artifacts` and `notes` are not copied;
- a dropped early-complete result or a withheld `ws.cancel` reason;
- file contents, paths, grant labels or scopes, helper commands, argv or output, context
  file texts, approval codes, tokens.

## When and where

- Written **in the transaction that closes the session** on each side (A's close
  transition; B's mirror applying `closed`; for a debate, A's close, B's apply of
  `debate.close`, B's local abandon (`cancelled_by: "respondent"`), B's `broken` close
  (`cause: "bad_reveal"`), or B's `decision.refuse` close (`cause: "decision_refused"`)), so a
  crash never leaves a closed session without its record, and a
  rolled-back close leaves no record. Exactly one record per `(session, role)` (a second
  close is impossible by the state machine).
- Stored in the daemon's SQLite database, migration **21** (ticket 3.7):

```sql
CREATE TABLE experience_records (
    session TEXT NOT NULL,
    role    TEXT NOT NULL,
    record  TEXT NOT NULL CHECK (json_valid(record)),   -- canonical record (content)
    created TEXT NOT NULL,
    PRIMARY KEY (session, role)
);
```

`experience_records` goes into the DROP lists of both rewind tests.

- **Retention:** never deleted automatically, like `work_sessions` and `requests`, from
  which it is derived (owner decision D50). [`agentnet prune`](../cli/prune.md) removes it
  with its session ([retention.md](retention.md#finished-items)). `peers remove` does not
  delete it. Deleting the database deletes it.

## Who can read it

- **Peers:** never. No mail kind carries it and no handler reads the table. Test: after
  closed sessions on both sides, marker strings from the record appear in no mail sent
  (outbox rows), no IPC result and no webhook row.
- **The local agent:** not through AgentNet in Phase 3: there is no IPC method or command.
  It is ordinary local data in the user's database, so an agent with raw file access to the
  config dir could read it, exactly like `requests` and `work_sessions`; harness confinement
  ([approval.md §Threat model](approval.md#threat-model)) is the control. Everything in it
  is data that side already saw (its own texts, or peer texts that were shown to it).
- **The local user:** the same; a read command is deferred (OD-P3-8).

## Audit

`experience.write {session, role, bytes, truncated?}` (daemon), in the closing
transaction. Never the record's content.

## Acceptance (plan 3.7)

"Record exists per closed session; no command exposes it to peers": a test closes one work
session (accepted), one cancelled session, one session whose quarantined result was
discarded, and one debate, and checks a record per side for each; the discarded result's
marker is in no record; no IPC method (the test lists every registered method) and no mail
kind returns record content.
