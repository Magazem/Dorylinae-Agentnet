# Requests, inbox and urgency

Status: **draft for review**. Covers Phase 1 tickets 1.4 (request object), 1.5 (the brief is
written by the sender), 1.6 (inbox and lifecycle), 1.7 (urgency guards) and 1.9 (offline
handling), plus `request.cancel` and the body size limits (owner decision D11). The ticket split is 1.4a–1.9 in
[../review/11-phase1-tickets.md](../review/11-phase1-tickets.md). Nothing here is
implemented yet. Change this document first.

A **request** is a unit of work one agent asks another for: a review, a task or a question.
It travels as sealed mail ([mail.md](mail.md)), kind `request`. The recipient answers with
the lifecycle kinds `request.accept`, `request.decline`, `request.defer` and
`request.complete`. The sender may withdraw a request that is not yet accepted with
`request.cancel` ([Cancel](#cancel-od-p1-11), owner decision D11), which the recipient
confirms with `request.cancelled`. The **recipient's daemon is authoritative** for the
state. The sender keeps a mirror.

Conventions follow [mail.md](mail.md): `<key>` is an identity key, wire times are RFC 3339
UTC with `Z` and whole seconds, SQLite times carry milliseconds, JSON is canonical, and
parsing is strict. "Code points" counts Unicode scalar values. "No control characters"
means no U+0000–U+001F and no U+007F.

## Request object

The body of kind `request` is `{"request": <object>}`, and nothing else. The object:

| Member | Req. | Type | Rules |
|---|---|---|---|
| `v` | yes | integer | `1` |
| `id` | yes | string | `r-` + 32 lowercase hex characters (16 bytes from `crypto/rand`). Unique per sender. **The idempotency key on the wire** ([Idempotency](#idempotency)) |
| `from` | yes | `<key>` | Must equal `msg.from` |
| `to` | yes | `<key>` | Must equal `msg.to` |
| `team` | yes | string | Team id `t-` + 32 hex ([team.md](team.md)) |
| `type` | yes | string | `review`, `task` or `question` |
| `title` | yes | string | 1–120 code points, no control characters |
| `brief` | yes | string | 1–16384 bytes of UTF-8. No control characters except `\n` (U+000A) and `\t` (U+0009) |
| `urgency` | yes | string | `low`, `normal`, `high` or `blocking`: the urgency **as sent**, after any sender-side downgrade |
| `urgency_declared` | no | string | Present **only** when the sender downgraded: the urgency the user asked for (`high` or `blocking`). `urgency` must then be `normal` |
| `urgency_reason` | cond. | string | 1–280 code points, no control characters. **Required** when `urgency_declared` or `urgency` is `high` or `blocking`. Otherwise it is optional, and absent when empty |
| `artifacts` | yes | array | 0–20 [artifacts](#artifacts), in order |
| `requested_grant` | no | object | [Requested grant](#requested-grant). Informational only |
| `deadline` | no | string | Time. Must be later than `created` |
| `created` | yes | string | Time. Set once at first submit. **Not** changed by a resend. Must be ≤ `msg.created` |

No other members are allowed. Optional members are absent, never `null`. The canonical form of
the object (`canonical(request)`) is what `body_hash` covers:
`body_hash = lowercase hex SHA-256(canonical(request))`.

### Artifacts

Each artifact is an object with **at least one** of these members, and no others:

| Member | Rules |
|---|---|
| `url` | 1–2048 bytes. Scheme (case-insensitive) `https`, `http`, `ssh` or `git`, then `://`. No spaces or control characters |
| `branch` | 1–255 bytes. No spaces, control characters, `~ ^ : ? * [ \`, and not `..` |
| `commit` | 7–64 lowercase hex characters |
| `path` | 1–1024 bytes. No control characters |

Artifacts are pointers. They grant nothing. The recipient's agent fetches them with its own
access (Phase 2 grants add scoped access).

### Size limits

Every limit below is checked by `internal/request.Validate`, which both sides run: the
sender at `request_submit` (Submitting step 5), the recipient in `Apply` (Receiving step 1).

| Limit | Value | Sender error (IPC / CLI exit 1) | Recipient |
|---|---|---|---|
| `title` length | 1–120 code points (so at most 480 bytes of UTF-8) | `bad_request`, field `title` | `bad_body` |
| `brief` length | 1–16384 bytes of UTF-8, after CRLF → LF | `bad_request`, field `brief` | `bad_body` |
| `urgency_reason` length | 1–280 code points | `bad_request`, field `urgency_reason` | `bad_body` |
| Artifact count | 0–20 | `bad_request`, field `artifacts` | `bad_body` |
| Each artifact field | `url` 1–2048 bytes, `branch` 1–255 bytes, `commit` 7–64 characters, `path` 1–1024 bytes ([Artifacts](#artifacts)) | `bad_request`, field `artifacts[i].<member>` | `bad_body` |
| `requested_grant` fields | `action` 1–64, `resource` 1–512 bytes, `note` 1–280 code points | `bad_request`, field `requested_grant.<member>` | `bad_body` |
| **Total body** | `len(canonical(request))` ≤ **65536 bytes** (`MaxRequestBody`) | **`request_too_large`** (the message gives the size and the limit) | `bad_body` |

The total cap is checked **after** the field checks, because the field maxima add up to more
than 64 KiB once JSON escaping is counted (a brief of 16384 `"` characters is 32768 bytes
canonical). A request that fails only the total cap must shrink its brief or artifacts.

**Relation to the mail cap.** The mail layer already bounds every message:
`canonical(signed)` ≤ 716800 bytes (`MaxMailPlaintext`), and the sealed payload is at most
716800 + 57 bytes ([mail.md §Message](mail.md#message)). A request of 65536 bytes plus the `msg`
members, the `{"request": …}` wrapper and the signature is under 67 KiB, so **a valid request
always fits in one mail** with a wide margin. The mail cap is the transport backstop (and the
relay frame limit). The request caps are the application limit: they keep `requests.body`,
`mail_inbox`, the inbox view and IPC lines small, and they are what users and agents see.
The lifecycle bodies are bounded by their own fields (`reason` 500, `note` 2000 code points).

### Requested grant

`{"action": "repo.read", "resource": "github.com/org/repo#feat-x", "note": "..."}`. Here
`action` is 1–64 characters from `[a-z0-9._-]`, `resource` is 1–512 bytes with no control
characters, and `note` is optional, 1–280 code points. It is shown to the recipient as a
hint and **never grants authority**. Phase 2 (2.2) turns it into a real, daemon-issued grant.

### The brief (1.5)

The sender's agent writes the brief. There is no model on the platform. `agentnet request
--help` prints this template, which is recommended and not enforced:

```
What: <one sentence: what you need>
Why: <one or two sentences of context>
Done when: <how the other side knows it is finished>
```

`--brief-from-file PATH` reads the brief from a file (`-` means stdin). It must be valid UTF-8
within the limits above, and CRLF is normalised to LF before validation.

## Submitting

IPC `request_submit` ([ipc.md](ipc.md#requests)), CLI `agentnet request @peer <type> ...`
([../cli/request.md](../cli/request.md)). The daemon runs these steps in order:

1. Resolve `to`: `unknown_peer`, `ambiguous_peer`. The peer needs a mailbox key
   (`no_mailbox_key`).
2. **D5 policy.** If the configured relay host is not loopback (`127.0.0.0/8`, `::1`,
   `localhost`) and the peer's `trust` is `relay`, refuse with `unverified_peer`. On a
   loopback relay, log a warning and continue.
3. Resolve the team. With `team`: an active team that has both self and `to` as members, or
   `not_team_member`, `unknown_team`, `ambiguous_team`. Without it: the active teams with
   both as members. Exactly one is used. None gives `no_shared_team`. More than one gives
   `ambiguous_team` (pass `team`).
4. **Client idempotency.** If `idempotency_key` is given and an `out` row exists with
   `(peer, idem_key)`, compare `params_hash`. If equal, return that request with
   `duplicate: true` and do **not** send again. If different, fail with
   `idempotency_conflict`.
5. Validate every field ([Request object](#request-object)). A failure is `bad_request`
   with a message naming the field. Then check the total size of the built object
   ([Size limits](#size-limits)): over 65536 bytes is `request_too_large`. `deadline` may be an RFC 3339 time or a duration
   (`90m`, `2h`, `3d`: Go `time.ParseDuration`, plus a `d` suffix meaning 24 h) resolved
   against `now`, and the result is truncated to whole seconds.
6. **Sender-side urgency budget** ([Urgency guards](#urgency-guards-17)). This may downgrade
   `high` or `blocking` to `normal` and set `urgency_declared`.
7. Build the object (`id` fresh, `created = now`). In **one SQLite transaction**, insert the
   `out` row (`state = pending`) and the outbox row. That needs `Outbox.SubmitTx(tx, to,
   kind, body)`, added by 1.4c. Audit `request.submit`. If the insert hits the
   `requests_idem` unique index (a concurrent submit with the same key won the race), roll
   back and answer as step 4 against the winning row.
8. Return the [submit result](#submit-result-19). The whole call must take **under 2 s** and
   must never wait for the relay or the peer.

`params_hash` = lowercase hex SHA-256 of the canonical JSON of the IPC params **as given**
(`to` resolved to a key, `team` resolved to an id, `deadline` as typed, and without
`idempotency_key`).

### Submit result (1.9)

```json
{
  "id": "r-0123456789abcdef0123456789abcdef",
  "mail_id": "m-...",
  "status": "queued",
  "duplicate": false,
  "team": {"id": "t-...", "name": "backend"},
  "urgency": "normal",
  "urgency_declared": "high",
  "urgency_note": "sent as normal: your weekly budget of 5 high requests is used",
  "peer": {"name": "bob", "public_key": "<key>", "daemon_online": false, "last_seen": "2026-10-01T09:12:00Z"}
}
```

- `status` is always `queued`: the request is stored and will be delivered by the outbox. The
  sender's agent **never** gets a timeout for an offline peer.
- `peer.daemon_online` and `peer.last_seen` come from [presence.md](presence.md#receiving).
  `last_seen` is `null` if the peer was never heard from. `daemon_online` is `false` in that
  case too.
- `urgency_declared` and `urgency_note` are present only when the request was downgraded.
- For a `duplicate`, `mail_id` and `status` describe the original: `status` is the current
  delivery state (`queued`, `relayed`, `delivered`, `expired`, `failed`), or `unknown` if the
  outbox row was pruned.

## Receiving

Kind `request` is registered with `Inbox: true`, so the signed plaintext is kept in
`mail_inbox` as proof. `Apply` runs inside the mail dedupe transaction:

1. **Strict body** ([Request object](#request-object)), including every
   [size limit](#size-limits), `from = msg.from`, `to = msg.to`, and `created ≤ msg.created`.
   A failure is [invalid](#invalid-bodies).
2. **Wire idempotency.** If an `in` row exists for `(msg.from, id)`:
   - with the same `body_hash`, it is a **duplicate**. Change nothing. After commit, if the row's
     state is not `pending` and its `last_reply` was not re-sent in the last 10 minutes,
     re-submit `last_reply` as a new mail (the same kind and body, so the same `seq`). This tells
     a sender that resent after `expired` what already happened. Audit `request.duplicate`.
   - with a different `body_hash`, keep the first one and change nothing. Audit `request.conflict
     {request, peer}`.
   - with no row, and `now − request.created > 30 d` (receiver clock): [invalid](#invalid-bodies).
     A resend re-wraps the old object in a new mail, which passes the 14-day mail limit, so
     without this bound a request could surface months later as new. An honest sender never
     resends after 21 d ([Idempotency](#idempotency)). Duplicates of a known id are still
     recognised at any age.
   - with no row, and a [cancel tombstone](#cancel-od-p1-11) for `(msg.from, id)`: store the
     row with `state = cancelled`, `state_seq = 1`, `first_response` NULL, `reason` from the
     tombstone, and
     `last_reply` = the `request.cancelled` with `seq = 1` that the tombstone already sent
     (it is **not** submitted again). Delete the tombstone. No notification. Skip steps 3–5.
3. **Policy auto-decline.** The row is stored with `state = declined`, `decline_code`,
   `state_seq = 1` and `first_response` NULL. In the **same transaction**, the
   `request.decline` with that `code` and `seq = 1` is stored as `last_reply` and submitted
   with `Outbox.SubmitTx(tx, …)`, so a crash cannot leave a declined row with no reply. The
   codes, checked in order:
   - `unverified_peer`: the rule of Submitting step 2, applied to the sender (D5).
   - `unknown_team`: `team` is not a local team in state `active`.
   - `not_team_member`: `msg.from` or self is not in `team_members` of that team.
4. **Receiver-side budget** ([Urgency guards](#urgency-guards-17)). This may lower the stored
   `urgency` to `normal`, with `downgraded_by = receiver`. If the body carries
   `urgency_declared`, then `downgraded_by = sender`, `urgency_declared` is taken from the
   body, and `urgency` from the body.
5. Insert the `in` row (`state = pending`, `received_at = now`).

After commit, for a new row: audit `request.in` (or `request.auto_decline`), and
[notify](notify.md) if the row is `pending`.

### Invalid bodies

This is a change to `internal/mail` (1.4b), and it applies to every Phase 1 application kind
(`request*`, `team.*`). `Apply` returns an error wrapping the sentinel `mail.ErrBadBody`. The
receiver then:

1. rolls back the transaction;
2. in a new transaction inserts only the `mail_seen` row and commits, so a resend is
   re-acked without being re-evaluated;
3. audits `mail.reject {peer, id, reason: "bad_body"}`, rate-limited like other rejects;
4. acks the id under `unsupported`. The sender's outbox row ends `failed`
   (`unsupported_kind`).

Any other `Apply` error keeps the current behaviour: roll back, no ack, and the sender
resends. A well-behaved sender never triggers `bad_body`, because it validates with the
same code (`internal/request.Validate`) before submitting.

## Lifecycle

### Kinds (recipient → sender)

Each body is strict, with exactly the listed members:

| Kind | Body | Extra rules |
|---|---|---|
| `request.accept` | `{"at", "request", "seq"}` | |
| `request.decline` | `{"at", "code", "reason"?, "request", "seq"}` | `code`: `user`, `not_team_member`, `unknown_team` or `unverified_peer`. `reason`: 1–500 code points, required when `code = user`, absent otherwise |
| `request.defer` | `{"at", "request", "seq", "until"}` | `at < until ≤ at + 90 d` |
| `request.complete` | `{"at", "note"?, "request", "seq"}` | `note`: 1–2000 code points |
| `request.cancelled` | `{"at", "request", "seq"}` | Confirms a [cancel](#cancel-od-p1-11). Sent only by the recipient's daemon, never by a user action |

`request` is the request id, `seq` is an integer ≥ 1 (the row's `state_seq` after this
change), and `at` is the recipient's time of the change. All five kinds are outboxed, acked
and registered with `Inbox: true`.

### Kind (sender → recipient)

| Kind | Body | Extra rules |
|---|---|---|
| `request.cancel` | `{"at", "reason"?, "request"}` | `at`: the sender's time, ≤ `msg.created`. `reason`: 1–500 code points, no control characters except `\n` |

It carries **no `seq`**: `seq` counts the recipient's state changes, which the sender does
not own. It is outboxed, acked and registered with `Inbox: true`, and it is idempotent by
`(msg.from, request)` ([Cancel](#cancel-od-p1-11)).

### State machine (authoritative on the recipient)

```
            accept              complete
pending ───────────▶ accepted ───────────▶ completed
 │ │  │                ▲
 │ │  │ defer          │ accept
 │ │  └────────▶ deferred ──┐ defer (again)
 │ │                │ │ ▲────┘
 │ │ decline        │ │ decline
 │ └───────▶ declined ◀┘ │
 │ cancel (sender)       │ cancel (sender)
 └───────▶ cancelled ◀───┘
```

| From | Allowed |
|---|---|
| `pending` | `accept`, `decline`, `defer`; `cancel` (sender's mail) |
| `deferred` | `accept`, `decline`, `defer`; `cancel` (sender's mail) |
| `accepted` | `complete` |
| `declined`, `completed`, `cancelled` | nothing (final) |

Any other transition is `bad_state` at IPC (so `accept` of a `cancelled` request is
`bad_state`). For each allowed action, in **one transaction**:
update the `in` row (`state`, `state_seq += 1`, `state_at`, plus `deferred_until`,
`decline_code = user` with `reason`, or `note`), set `first_response` and
`first_response_at` if they are NULL and the action is `accept`, `decline` or `defer`, store
the lifecycle mail as `last_reply`, and `Outbox.SubmitTx` the lifecycle mail. After commit,
write the audit event ([Audit](#audit-and-metrics)).

Phase 2 (2.1) replaces `accepted → completed` with a session. `request.complete` stays valid
for requests that never open a session.

### Sender mirror

On `request.accept`, `decline`, `defer`, `complete` or `cancelled` from `msg.from`:

1. Strict body. A failure is `bad_body`.
2. Find the `out` row `(peer = msg.from, id = body.request)`. If there is none, ack and ignore,
   and audit `request.orphan {request, peer, kind}`.
3. If `seq ≤ state_seq`, ignore it: it is a duplicate or arrived out of order.
4. Otherwise set `state`, `state_seq = seq`, `state_at = at`, and `deferred_until`,
   `decline_code` with `reason`, or `note`. **The sender does not check the transition.** The
   recipient is authoritative, and a higher `seq` always wins, so a `complete` that overtakes
   its `accept` still ends in `completed`.
5. **Cancel refused.** If the row has `cancel = requested` and, after step 3 or 4, its
   `state` is `accepted`, `declined` or `completed`, set `cancel = refused`. This runs even
   when step 3 ignored the mail, because a refused cancel is answered by re-sending a reply
   the sender may already hold ([Cancel](#cancel-od-p1-11)).

After commit: audit `request.state {request, peer, state, seq}` (when step 4 applied),
`request.cancel_refused {request, peer, state}` (when step 5 applied), and
[notify](notify.md).

### Cancel (OD-P1-11)

Owner decision D11: the sender may withdraw a request the recipient has not taken on. IPC
`request_cancel` ([ipc.md](ipc.md#requests)), CLI `agentnet request cancel <id>`
([../cli/request.md](../cli/request.md)).

**Rules chosen:**

| Recipient state | Result |
|---|---|
| no row yet (the request is late, lost, or overtaken by the cancel) | Cancelled: a **tombstone** is kept, and the request is stored `cancelled` if it arrives |
| `pending`, `deferred` | **Cancelled** (final) |
| `cancelled` | Nothing changes (idempotent). The last reply is echoed |
| `accepted`, `declined`, `completed` | **Refused.** The state stands; the sender learns it from the echoed reply |

- **Budget.** A cancel counts against no budget and is not rate-limited beyond mail itself.
  It also **refunds nothing**: the cancelled request stays in the sender-side and
  receiver-side 7-day urgency counts ([Urgency guards](#urgency-guards-17)), otherwise
  `high` + cancel would reset the budget. `first_response` is not changed by a cancel, so
  the [effective priority](#effective-priority) counts are unchanged too (a cancelled row
  with `first_response` NULL is not in `n`).
- **After accept the sender cannot cancel.** The way out is to ask, outside AgentNet, for
  the recipient to stop; the recipient then completes it (with a note).

**Sender (`request_cancel {id, reason?}`)**, in one transaction on the `out` row:

1. No `out` row with that id: `unknown_request`.
2. `state = cancelled`: return the row with `duplicate: true` and send nothing.
3. `state` is `accepted`, `declined` or `completed` (as the mirror knows it): `bad_state`,
   and the message names the state ("bob already accepted r-…"). Nothing is sent.
4. `cancel = requested` and the current cancel mail (`cancel_mail_id`) is `queued`,
   `relayed` or `delivered`: return the row with `duplicate: true`. Nothing is sent.
5. Otherwise (state `pending` or `deferred`, and no cancel in flight: none yet, or the last
   one `expired` or `failed`): build `request.cancel` (`at = now`, `reason` if given, checked
   as above, else `bad_request`), `Outbox.SubmitTx` it, and set `cancel = requested`,
   `cancel_at = now`, `cancel_mail_id`. Audit `request.cancel {request, peer, mail}`.

The `out` row's `state` does **not** change here: the recipient is authoritative, and the
mirror moves to `cancelled` only on `request.cancelled`. The view shows `cancel:
"requested"` meanwhile. `request_resend` refuses a row with `cancel` set (`bad_state`).

**Recipient (`Apply` of `request.cancel`)**, inside the mail dedupe transaction:

1. Strict body. A failure is [invalid](#invalid-bodies).
2. Find the `in` row `(msg.from, body.request)`:
   - `pending` or `deferred`: set `state = cancelled`, `state_seq += 1`, `state_at = now`,
     `reason` = the body's `reason` (or NULL); leave `first_response` alone; store
     `request.cancelled {at: now, request, seq: state_seq}` as `last_reply` and
     `Outbox.SubmitTx` it.
   - `cancelled`, `accepted`, `declined` or `completed`: change nothing. After commit,
     re-submit `last_reply` under the same 10-minute rule as a duplicate request
     ([Receiving](#receiving) step 2). For `accepted`, `declined` and `completed` this is
     the refusal: the sender mirror sees a final or accepted state while `cancel =
     requested` (Sender mirror step 5).
   - **No row:** if no tombstone exists for `(msg.from, request)`, insert one into
     `request_cancels` and submit `request.cancelled {at: now, request, seq: 1}`. A later
     request with that id is stored `cancelled` ([Receiving](#receiving) step 2). A sender
     may hold at most **1000** tombstones on this daemon: beyond that the cancel is acked and
     ignored, with audit `request.cancel_in {result: "tombstone_limit"}`. Tombstones older
     than 31 d are pruned (a request that old is refused as new anyway).
3. After commit: audit `request.cancel_in {request, peer, result, state}`, where `result`
   is `cancelled`, `refused`, `duplicate`, `early` (tombstone) or `tombstone_limit`, and
   `state` is the row's state before the cancel (absent for no row). For `cancelled`, also
   [notify](notify.md) `request.cancelled` so a human or agent who saw the request stops.

**Ordering, resend and inbox.**

- `request.cancel` is a separate mail from the request, so it can arrive first, which is the
  tombstone case. The tombstone reply uses `seq = 1`, and the stored row then also has
  `state_seq = 1` in state `cancelled`, so the mirror sees one consistent `seq`.
- Recipient races are serialised by the `in` row's transaction: whichever of a local
  `accept` and an arriving `cancel` commits first wins. If `accept` wins, the cancel is
  refused. If `cancel` wins, the `accept` gets `bad_state`.
- The sender's mirror follows the usual `seq` rule. If an `accept` (`seq` n) and the refusal
  echo of it both arrive, the second is ignored by step 3 but still sets `cancel = refused`
  (step 5).
- A cancelled request leaves the default `inbox_list` (it is not `pending` or due
  `deferred`). `inbox --all` shows it with `state: cancelled` and the sender's `reason`.
- `request_resend` of a cancelled request, or one with a cancel in flight, is `bad_state`.

## Idempotency

The D10 requirement: an outbox row that ends `expired` means **delivery unknown**, so any
resubmission must be safe.

- **The wire key is `(from, request.id)`.** A resubmission is a *new mail* (new `m-` id), which
  `mail_seen` cannot recognise, carrying the *same request object* (the same `id`, `created`
  and every other member). The receiver dedupes on the key and compares `body_hash`
  ([Receiving](#receiving) step 2).
- **`agentnet request resend <id>`** (IPC `request_resend`) resubmits the stored canonical
  `body` unchanged, in a new mail, and sets the `out` row's `mail_id` to it. It is allowed only
  when the row is `pending`, has no `cancel`, its current mail is `expired` or `failed`,
  and `now < request.created + 21 d`. Otherwise it returns `bad_state` (the mail is still in flight, or
  was delivered, or the request was already answered, or it is too old: send a new request).
  Audit `request.resend {request, peer, mail}`.
- **Harness retries.** A harness that times out and runs the same `agentnet request` again
  would create a second request. `--idempotency-key K` (1–64 characters from
  `[A-Za-z0-9._:-]`, scoped per peer) makes the retry return the first request
  ([Submitting](#submitting) step 4). The key is local, never sent, and kept as long as the row.
- Lifecycle mails are idempotent through `seq`. `request.cancel` is idempotent through the
  recipient's state and tombstones ([Cancel](#cancel-od-p1-11)).

## Inbox (1.6)

`inbox_list` returns `in` rows that are `pending`, plus `deferred` rows with `deferred_until ≤
now` (these carry `due: true`). With `all`, it returns every `in` row. An optional `team`
filter applies.

**Ordering:** `priority` descending, then `received_at` ascending (older first), then `peer`,
then `id`.

### Effective priority

Integer thousandths, computed at query time. Let `base` be 1 for `low`, 2 for `normal`, 3 for
`high` and 4 for `blocking`, taken from the row's effective `urgency`. For the row's sender
`s`, over the last 30 days on this daemon:

- `n` = number of `in` rows from `s` with effective urgency `high` or `blocking`,
  `received_at ≥ now − 30 d`, and `first_response IS NOT NULL` (the user responded, not an
  auto-decline);
- `a` = how many of those have `first_response = accept`. This is **acceptance as urgent**:
  the user's first reaction to an urgent claim was to take it, not defer or decline it.

```
priority = base × 1000                                      if base ≤ 2
priority = 2000 + ((base − 2) × 1000 × (a + 2)) / (n + 2)   if base ≥ 3   (integer division)
```

The prior `(a + 2)/(n + 2)` starts a new sender at full weight (1.0). An urgent claim decays
toward `normal` (2000), never below it, as the sender's urgent requests are deferred or
declined. For example, a sender with n = 4 and a = 1 gets `high` = 2500 and `blocking` = 3000.
The formula is OD-P1-5. Unit tests cover these vectors:

| base | n | a | priority |
|---|---|---|---|
| 1 | any | any | 1000 |
| 2 | any | any | 2000 |
| 3 | 0 | 0 | 3000 |
| 4 | 0 | 0 | 4000 |
| 3 | 4 | 1 | 2500 |
| 4 | 4 | 1 | 3000 |
| 4 | 10 | 0 | 2333 |
| 3 | 1 | 1 | 3000 |

Acceptance test (1.6): three requests from one new sender (`low`, `high`, `normal`, sent in
that order) list as `high`, `normal`, `low`.

## Urgency guards (1.7)

Budget: **5 `high` and 2 `blocking` per sender per rolling 7 days** (OD-P1-4). There are no
accounts in Phase 1, so the budget is enforced twice:

- **Sender-side (courtesy, global).** Let `h` be the number of `out` rows with `urgency = high`
  and `created ≥ now − 7 d`, across all peers, and `b` the same for `blocking`. A new `high`
  with `h ≥ 5`, or `blocking` with `b ≥ 2`, is sent as `normal` with `urgency_declared` set.
  Resends create no rows and do not count. The result carries
  `urgency_note = "sent as normal: your weekly budget of 5 high requests is used"` (or "2
  blocking").
- **Receiver-side (enforcement, per sender).** A modified sender cannot bypass this. Let `h`
  be the number of `in` rows from this sender with effective `urgency = high`, `received_at ≥
  now − 7 d`, and `decline_code` NULL or `user` (auto-declines do not count). Same for `b`.
  An arriving `high` with `h ≥ 5`, or `blocking` with `b ≥ 2`, is stored as `normal`, with
  `urgency_declared` = the sent urgency and `downgraded_by = receiver`.

`urgency_note` in the inbox is derived from `downgraded_by`:

- `sender`: `"sent as normal: the sender's weekly budget of 5 high requests was used"`, or
  "2 blocking".
- `receiver`: `"shown as normal: this sender has used its weekly budget of 5 high requests
  to you"`, or "2 blocking".

Acceptance test (1.7): six `high` requests from A to B inside 7 days. B's inbox shows the
sixth with `urgency: "normal"`, `urgency_declared: "high"` and a note. This holds both with an
honest sender (`downgraded_by: sender`) and with sender-side enforcement disabled in a test
build (`downgraded_by: receiver`).

## Offline (1.9)

Sending to a stopped daemon: `request_submit` returns in under 2 s with `status: "queued"`,
`peer.daemon_online: false` and `peer.last_seen`. The outbox delivers the request when the
peer returns. The relay queue holds it for 7 days, and the presence online edge triggers an
immediate resend ([presence.md](presence.md#receiving) step 6). `agentnet request show <id>`
reports delivery (the outbox state of `mail_id`) and the lifecycle state separately.

## Audit and metrics

Every accept, decline, defer, completion and cancel is logged with a timestamp (`audit_events.ts`)
from day one. These rows are the daemon-side data for the metrics in plan §9 (time to accept,
accept rate, requests per team per week) under D7. **Never** titles, briefs, reasons, notes,
artifacts or bodies.

| Action | Side / actor | Detail |
|---|---|---|
| `request.submit` | sender / `cli` | `{request, peer, team, type, urgency, urgency_declared?, mail}` |
| `request.resend` | sender / `cli` | `{request, peer, mail}` |
| `request.in` | recipient / `daemon` | `{request, peer, team, type, urgency, urgency_declared?, downgraded_by?}` |
| `request.auto_decline` | recipient / `daemon` | `{request, peer, team, code}` |
| `request.accept`, `request.decline`, `request.defer`, `request.complete` | recipient / `cli` | `{request, peer, team, type, urgency, seq, age_s, code?, until?}`. `age_s` = whole seconds since `received_at`, which is the time-to-accept measure |
| `request.state` | sender / `daemon` | `{request, peer, state, seq}` |
| `request.duplicate`, `request.conflict` | recipient / `daemon` | `{request, peer}` |
| `request.orphan` | sender / `daemon` | `{request, peer, kind}` |
| `request.cancel` | sender / `cli` | `{request, peer, mail}` |
| `request.cancel_in` | recipient / `daemon` | `{request, peer, result, state?}`. `result`: `cancelled`, `refused`, `duplicate`, `early` or `tombstone_limit`. For `cancelled`, also `team`, `type`, `urgency`, `seq` and `age_s` |
| `request.cancel_refused` | sender / `daemon` | `{request, peer, state}` |

The cancel `reason` is content, like the decline `reason` and the `note`, and is never
audited.

## Tables

```sql
-- migration 11 (1.4a): requests
CREATE TABLE requests (
    direction         TEXT NOT NULL CHECK (direction IN ('in', 'out')),
    peer              TEXT NOT NULL,              -- in: sender; out: recipient
    id                TEXT NOT NULL,              -- r-<32 hex>
    team_id           TEXT NOT NULL,
    type              TEXT NOT NULL CHECK (type IN ('review', 'task', 'question')),
    urgency           TEXT NOT NULL CHECK (urgency IN ('low', 'normal', 'high', 'blocking')),
    urgency_declared  TEXT NOT NULL CHECK (urgency_declared IN ('low', 'normal', 'high', 'blocking')),
    downgraded_by     TEXT CHECK (downgraded_by IN ('sender', 'receiver')),
    body              TEXT NOT NULL CHECK (json_valid(body)),   -- canonical request object
    body_hash         TEXT NOT NULL,
    state             TEXT NOT NULL CHECK (state IN ('pending', 'accepted', 'declined', 'deferred', 'completed', 'cancelled')),
    state_seq         INTEGER NOT NULL DEFAULT 0,
    state_at          TEXT,
    deferred_until    TEXT,
    decline_code      TEXT,
    reason            TEXT,                       -- decline reason, or the sender's cancel reason
    note              TEXT,
    first_response    TEXT CHECK (first_response IN ('accept', 'decline', 'defer')),
    first_response_at TEXT,
    created           TEXT NOT NULL,              -- request.created
    received_at       TEXT,                       -- in only
    mail_id           TEXT NOT NULL,              -- out: current carrying mail; in: first mail
    last_reply        TEXT CHECK (last_reply IS NULL OR json_valid(last_reply)),  -- in: {"kind","body"}
    last_reply_sent   TEXT,                       -- in: last echo time
    idem_key          TEXT,                       -- out only
    params_hash       TEXT,                       -- out only
    cancel            TEXT CHECK (cancel IN ('requested', 'refused')),  -- out only
    cancel_at         TEXT,                       -- out only
    cancel_mail_id    TEXT,                       -- out only: current request.cancel mail
    updated           TEXT NOT NULL,
    PRIMARY KEY (direction, peer, id)
);
CREATE UNIQUE INDEX requests_idem ON requests (peer, idem_key)
    WHERE direction = 'out' AND idem_key IS NOT NULL;
CREATE INDEX requests_state ON requests (direction, state);
CREATE INDEX requests_peer_time ON requests (direction, peer, received_at);

-- recipient side: cancels that arrived before their request (Cancel, OD-P1-11)
CREATE TABLE request_cancels (
    peer        TEXT NOT NULL,                    -- sender
    id          TEXT NOT NULL,                    -- r-<32 hex>
    reason      TEXT,
    received_at TEXT NOT NULL,                    -- pruned after 31 d
    PRIMARY KEY (peer, id)
);
```

For `out` rows, `urgency_declared` = `urgency` unless the sender downgraded. For `in` rows,
it is the body's `urgency_declared` if present, else the body's `urgency`. Rows are kept
indefinitely in Phase 1. `peers remove` does not delete them.

A request id is unique per sender, not globally. A CLI or IPC reference by id alone that
matches `in` rows from several peers is `ambiguous_request`, and the caller passes `from`.
