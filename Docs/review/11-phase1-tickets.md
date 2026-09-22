# 11: Phase 1 tickets (1.1–1.9)

Status: approved (D11, 2026-09-21), amended for D11. Specs:
[team.md](../protocol/team.md), [presence.md](../protocol/presence.md),
[request.md](../protocol/request.md), [notify.md](../protocol/notify.md), and additions to
[ipc.md](../protocol/ipc.md), [mail.md](../protocol/mail.md) and
[envelope.md](../protocol/envelope.md). CLI:
[team](../cli/team.md), [presence](../cli/presence.md), [request](../cli/request.md),
[inbox](../cli/inbox.md), [notify](../cli/notify.md), [status](../cli/status.md) and
[peers](../cli/peers.md).

The specs were **approved** on 2026-09-21 (owner decision D11), with OD-P1-11 changed to (b)
`request.cancel`, request body size limits, ticket 1.H, and the known-limitations page
[../beta/known-limitations.md](../beta/known-limitations.md).

**Process (D11):**

- **1.1a, 1.2a, 1.2d and 1.4b start now.** They depend only on the approved specs.
- **1.4a may be built in parallel with 1.1** (1.1a–1.1d). It **merges only in migration
  order**: its migration 11 lands after 9 (1.1b) and 10 (1.2b) are merged, so it waits or
  is rebased onto them ([Migrations](#migrations-pre-assigned)).
- Everything else starts when its dependencies below are merged.

Every ticket follows the
plan's exit criteria: acceptance tests are automated Go tests, `go vet` passes, the `--help`
and `--json` output are documented, audit events exist, and new tests use
`internal/testutil.TempDir`.

## Migrations (pre-assigned)

`internal/store` requires consecutive versions, so **tickets that add a migration merge in
migration order**. If a later one is ready first, it waits for the earlier one or is rebased
onto it.

| # | Name | Ticket |
|---|---|---|
| 8 | `peers_trust_team` (peers rebuild: `trust` accepts `team`, adds `introduced_by`) | 1.1a |
| 9 | `teams` (`teams`, `team_members`, `team_invites`, `team_pending_joins`) | 1.1b |
| 10 | `presence` (`presence_peers`, `settings`) | 1.2b |
| 11 | `requests`, `request_cancels` | 1.4a |
| 12 | `requests_result` (`requests.result` column, D14) | 1.6a |
| 13 | `webhook_queue` (was 12 before D14) | 1.8b |

## Tickets

"Review" marks tickets that need an **Opus security review before merge** (HANDOFF rule 4).
Review 12 added 1.1a (trust rank, peer GC, table rebuild), 1.2a (relay abuse surface), 1.4c
(D5 and auto-decline) and 1.6a (lifecycle state from peers). Those four are small, so one
reviewer can take each as soon as it is ready. They cannot be batched with their successor,
which depends on them being merged.
"∥" lists tickets that can run in parallel with this one once its dependencies are merged.

| ID | Title | Depends on | Migration | Review | ∥ |
|---|---|---|---|---|---|
| 1.1a | Peers trust `team` + `introduced_by` | specs approved | 8 | **yes** | 1.2a, 1.2d, 1.4b |
| 1.1b | Team store and kinds `team.roster` / `team.join` / `team.leave` | 1.1a, 1.4b | 9 | **yes** | 1.2a, 1.2d |
| 1.1c | Team IPC and CLI: create, list, show, remove, rename, leave, delete | 1.1b | — | — | 1.2b |
| 1.1d | Team invite and join via pairing v2 | 1.1c | — | **yes** | 1.2b, 1.2c |
| 1.2a | Relay ephemeral envelopes + `ready.features` | specs approved | — | **yes** | 1.1a–d, 1.4b |
| 1.2b | Presence seal/open, body, order and replay rules | 1.1b, 1.2a | 10 | **yes** | 1.1c, 1.1d |
| 1.2c | Presence engine: heartbeats, agent activity, offline, `status --team` | 1.2b, 1.1c | — | — | 1.1d, 1.2d |
| 1.2d | `internal/idle` per-OS idle detection | specs approved | — | — | anything |
| 1.3 | Visibility: `presence` command, modes, goodbye | 1.2c | — | — | 1.4a |
| 1.4a | `internal/request`: schema, validation, size limits, canonical, `body_hash`, priority | specs approved to build; **merge** after 1.2b (migration order) | 11 | — | 1.1a–d, 1.3 |
| 1.4b | `mail.ErrBadBody` receiver path | specs approved | — | **yes** | 1.1a, 1.2a, 1.2d |
| 1.4c | `request` kind, `request_submit`, CLI `agentnet request` (1.5, 1.9) | 1.4a, 1.1d | — | **yes** | 1.3 |
| 1.6a | Lifecycle kinds, state machine, sender mirror, `request.cancel`, completion result (D14), `request show/list/resend/cancel` | 1.4c | 12 | **yes** | 1.8a |
| 1.6b | Inbox: `inbox_list` order, CLI `inbox/accept/decline/defer/complete` | 1.6a | — | — | 1.7, 1.8a |
| 1.7 | Urgency guards: sender and receiver budget, notes | 1.6b | — | — | 1.8a, 1.8b |
| 1.8a | Desktop notifications, `notify` settings, `agentnet notify --desktop/--event/--test` | 1.6a | — | **yes** | 1.6b, 1.7 |
| 1.8b | Webhook: secret, signing, queue, retry | 1.8a | 13 | **yes** | 1.7 |
| 1.9 | Offline end-to-end and audit/metrics check | 1.7, 1.8b | — | — | 1.H |
| 1.H | Headless agent harness run (Claude Code + one other harness) and the agent snippet | 1.6b | — | — | 1.7, 1.8a, 1.8b, 1.9 |
| 1.P | Phase 1 push (`main` to origin; the `phase-1` tag **only with the owner's OK**) | all above, including **1.H** | — | — | — |

Critical path: 1.1a → 1.1b → 1.1c → 1.1d → 1.4c → 1.6a → 1.6b → 1.7 → 1.9 (with 1.4b
before 1.1b). 1.1a, 1.2a, 1.2d and 1.4b start now (see Process above). 1.H runs off the
critical path once 1.6b is merged, and gates 1.P.

## Ticket details

Each ticket lists: **files**, the **interface** it must not change, and **acceptance** tests.
"e2e" means the in-process two-daemon + relay harness used by 1.0e
(`internal/daemon/outbox_harness_test.go` pattern). "Fake clock" means injecting `Now`.

### 1.1a Peers trust `team`

- Files: `internal/store/store.go` (migration 8), `internal/peers/store.go`,
  `internal/daemon/trust.go`, `cmd/agentnet/peers.go`, and tests.
- Rank `relay < team < code < fingerprint`. `Store.Add` never lowers trust. A direct pairing
  or `peers verify` clears `introduced_by`. The `peers` IPC result and `peers --json` gain
  `introduced_by`.
- Add `peers.Store.Introduce(tx, member, owner)` and `peers.Store.GCIntroduced(tx) ([]removed,
  error)`, with the semantics of [team.md §Introduced peers](../protocol/team.md#introduced-peers).
- Acceptance: migration 8 on a DB with rows at every trust level keeps all columns and the
  row count, leaves no `peers_new`, and `PRAGMA integrity_check` is `ok`
  (`TestMigration8PreservesPeers`); `trust='team'` is accepted and ranks correctly; a v2
  re-pair raises `team` to `code` and clears `introduced_by`; `GCIntroduced` removes only
  introduced peers with no active team, and fails their outbox rows `unpaired`.

### 1.1b Team store and kinds (review)

- Files: new `internal/team` (store, roster build and validation, kinds), migration 9,
  `internal/daemon/mail.go` (register kinds), and tests.
- The kinds `team.roster`, `team.join` and `team.leave`, exactly as in
  [team.md §Kinds](../protocol/team.md#kinds), including validation order, ignore
  reasons, apply, GC, and the after-commit `keys` push. Validation failures return
  `mail.ErrBadBody`.
- Owner-side operations as functions (`Create`, `AddMember`, `Remove`, `Rename`, `Leave`,
  `Delete`, `Broadcast`), without IPC yet.
- Acceptance: a unit test for every validation rule (forged owner, epoch ≤ stored, unknown
  team without a pending join, owner trust `relay` → ignored, bad card → `bad_body`,
  expired announcement → `mailbox: null` accepted, 33 members → `bad_body`); a roster
  removing self → state `removed` + GC; e2e with 3 daemons and 2 teams sharing one member
  (built with the functions, joins simulated through `team_pending_joins`): **each member
  sees only its own team's members** (`TestTwoTeamsSharedMember`); a replayed old roster
  mail changes nothing. From review 12: a higher-epoch roster for a team in local state
  `left`, `removed` or `dissolved` is ignored without a pending join and applied with one
  (rejoin after leave); two pending joins from one owner both succeed; the roster sent to a
  removed member lists only the owner; `peers remove` of an owner sets its teams `left` and
  GCs its introductions (this hooks the existing `peers_remove` handler).

### 1.1c Team IPC and CLI

- Files: `internal/daemon` (handlers), `cmd/agentnet/team.go`, `Docs/cli/team.md` (if the
  output differs), and tests.
- IPC per [ipc.md §Teams](../protocol/ipc.md#teams) except `team_invite` and `team_join`.
- Acceptance: CLI tests for every subcommand's human and `--json` output and exit codes;
  `ambiguous_team` with two same-named teams; `owner_cannot_leave`; `not_owner`.

### 1.1d Team invite and join (review)

- Files: `internal/peers/pairv2.go` (a pairing tag, and a completion hook carrying the lookup),
  `internal/team`, `internal/daemon`, `cmd/agentnet/team.go`, and tests.
- `team_invite` = `pair_new` tagged with the team. `team_join` = `pair_redeem` plus a pending
  join plus `team.join`. `team_invites` is written on `pair.complete`.
- Acceptance: e2e **1.1 acceptance**: A creates `x` and invites B; C creates `y` and invites
  B; B joins both; `team show x` on B lists A and B, `team show y` lists B and C, and A and C
  are not each other's peers (`TestTeamInviteJoinTwoTeams`). Plain `pair <invite code>`
  does not join. A `team.join` with a wrong lookup, or from another key, is ignored. An
  invite for a full team gives `team_full`. A third member D joining `x` gets A and B as
  `trust=team` peers, and B gets D.

### 1.2a Relay ephemeral envelopes

- Files: `internal/relay`, `internal/envelope` (`Ready.Features`), `internal/relayclient`
  (expose features, no ack for `presence`), and tests.
- Acceptance: `presence` to an offline peer is dropped with no `queued` or `error` frame and
  nothing stored (`Queued(key) == 0`); to a connected peer with a backlog it is forwarded
  ahead of the backlog; to a peer whose send buffer is over half full it is dropped while a
  `mail` frame is still forwarded directly; the 601st in a minute is dropped
  (`EphemeralPerMinute`); `relayclient` hands up 10 000 presence envelopes without evicting a
  `session.*` id from the seen-set, and sends no `ack` for them; the relay log has no payload
  marker; an old client ignores `features`.

### 1.2b Presence seal/open (review)

- Files: `internal/mail` (an opener mode for `p-` ids and kind `presence`, and seal reuse),
  new `internal/presence` (body, padding, replay/order check, store), migration 10, and tests.
- Acceptance: round trip; the plaintext length is a multiple of 256 for every combination of
  flags and 0–32 epochs; relabelling `presence` → `mail` fails at mail step 9, and `mail` →
  `presence` fails the kind check; replay of the same `(boot, seq)` → dropped; an old boot
  with an older `created` → dropped; a new boot → accepted; `created` 11 min old → dropped;
  `interval` 0 or 301 → dropped, 1 accepted; a rejected message writes no audit row.

### 1.2c Presence engine and `status --team`

- Files: `internal/presence` (sender loop, visible set, edges), `internal/ipc` (a hook that
  records the time of each dispatch), `internal/daemon` (Options: `PresenceInterval`,
  `AgentWindow`, `Idle`), `internal/mail/outbox.go` (`OnPeerOnline` wired),
  `cmd/agentnet/main.go` (`status --team`), and tests.
- Acceptance (1.2):
  - Fake clock unit test: the last heartbeat at t, `daemon_online` true at t+75 s, false at
    t+75.001 s (**< 90 s**).
  - e2e with `PresenceInterval = 1 s` (scaled): killing B's daemon (context cancel without
    goodbye, relay connection dropped) makes A show B offline within 2.5 s + 0.5 s.
  - e2e with the real 30 s interval and B idle > 5 min (fake agent clock): one IPC call on B
    → A shows `agent_active` within **1 s** (bound 30 s).
  - A graceful stop → A shows B offline at once.
  - The online edge sends B's queued outbox rows immediately (1.0e hook).
  - The `status --team x --json` shape matches ipc.md, including `self`.
  - Roster resync: a member with a lower epoch gets the roster again, at most once per
    10 min; a removed member reporting the old epoch gets the owner-only roster and ends
    `removed`.
  - `interval` is 30 s with 90 visible peers and 31 s with 91 (`max(30, ⌈n/3⌉)`).
  - A new member gets an immediate heartbeat when a roster adds it (team.md after-commit).
  - Leaving a team, or being removed, sends a goodbye to peers that leave the visible set
    (the Leave operation of 1.1c gains its presence step here).

### 1.2d `internal/idle`

- Files: new `internal/idle` (`idle_windows.go`, `idle_darwin.go`, `idle_linux.go`,
  `idle_other.go`), and tests.
- Acceptance: parser unit tests with recorded outputs (`ioreg`, both `gdbus` replies,
  `xprintidle`); the 1 s timeout is honoured with a hanging fake command; `Idle` returns
  unknown on failure. A **manual** check per OS is added to `tests/phase1-manual.md`: idle
  flips after 10 min.

### 1.3 Visibility

- Files: `internal/presence`, `internal/daemon`, `cmd/agentnet/presence.go`, and tests.
- Acceptance (1.3): e2e: B `--invisible` → A shows B offline immediately, with `last_seen` =
  the goodbye time, and **never online** over 5 intervals while B keeps running CLI
  commands. `--only-team x` → a member of `x` sees B online and a member of `y` only sees B
  offline. Leaving team `x` while `only_team x` → mode `invisible` + an audit row.
  `--human off` → peers get `human_present: null`.

### 1.4a `internal/request`

- Files: new `internal/request` (types, `Validate`, canonical, `BodyHash`, `Priority(base,
  n, a)`, artifact spec parser, `MaxRequestBody`), migration 11 (`requests` with the
  `cancel*` columns and state `cancelled`, and `request_cancels`), and tests.
- Build may start now, in parallel with 1.1; merge only after 1.2b (migration order).
- Acceptance:
  - a table test for every field rule in
    [request.md §Request object](../protocol/request.md#request-object);
  - **a table test for every cap** in [request.md §Size limits](../protocol/request.md#size-limits),
    each at the limit (accepted) and one over (rejected with the documented code and field
    name): `title` 120 / 121 code points (and 120 four-byte code points accepted, 480 bytes);
    `brief` 16384 / 16385 bytes (and a multi-byte character straddling the limit); 0 and 20 /
    21 artifacts; each artifact member at its min and max and max+1 (`url` 2048, `branch`
    255, `commit` 7 and 64, `path` 1024); `urgency_reason` 280 / 281; each
    `requested_grant` member; total `canonical(request)` 65536 / 65537 bytes, built from
    fields that are each valid → `request_too_large` (and `ErrBadBody` on the receive path);
  - a maximal valid request (65536 bytes) seals into one mail under `MaxMailPlaintext`;
  - the priority vectors table; the artifact `SPEC` parser (`key=value` pairs and JSON); a
    fuzz test of `Validate` against canonical round trip.

### 1.4b `mail.ErrBadBody` (review)

- Files: `internal/mail/receiver.go`, and tests.
- Acceptance: an `Apply` returning `ErrBadBody` → no kind rows; a `mail_seen` row; audit
  `mail.reject bad_body`; an ack under `unsupported`; a resend of the same id → re-acked as
  `unsupported` without calling `Apply`; any other `Apply` error → no ack (unchanged).

### 1.4c Request submit (1.4, 1.5, 1.9)

- Files: `internal/request` (submit, receive `Apply`, auto-decline), `internal/mail/outbox.go`
  (`SubmitTx`), `internal/daemon`, `cmd/agentnet/request.go` (with the brief template in
  `--help`), and tests.
- Acceptance:
  - **1.4**: e2e: A requests B with every field set; B's `in` row has every field
    byte-identical after canonicalisation; a relay-side tap sees only `type: mail` and no
    title or brief substring in any frame (`TestRequestFieldsIntactCiphertext`).
  - **1.5**: `agentnet request --help` contains the brief template;
    `--brief-from-file -` reads stdin. The *headless Claude Code run* is ticket
    [1.H](#1h-headless-agent-harness-run).
  - **1.9**: with B stopped, `agentnet request` returns in **< 2 s** with `status: queued`,
    `daemon_online: false` and `last_seen` (`TestRequestOfflineQueued`).
  - D5: a `trust=relay` peer on a non-loopback relay URL → `unverified_peer`, and auto-decline
    on receipt.
  - Not a team member → auto-decline `not_team_member` reaches the sender mirror (after 1.6a:
    stub the check here, and finish the assertion in 1.6a).
  - `--idempotency-key`: the same params → `duplicate: true`, one outbox row; different
    params → `idempotency_conflict`; two concurrent submits with one key → one row, and the
    loser gets `duplicate: true`.
  - Auto-decline: the declined row, `last_reply` and the outbox row of the decline are in
    one transaction (an injected failure after the insert leaves none of them).

### 1.6a Lifecycle and sender mirror

- Files: `internal/request` (including `ValidateComplete` and `MaxCompleteBody`), migration 12
  (`requests.result`), `internal/daemon`, `cmd/agentnet/request.go`, and tests. The
  `agentnet complete` flags of the result are added with the rest of `complete` in 1.6b;
  1.6a tests the result through IPC `request_complete`.
- Acceptance: every allowed transition and every refused one (`bad_state`); a `seq`
  out-of-order (`complete` before `accept`) ends `completed` on the sender;
  `request resend` after a forced `expired` → the receiver sees a duplicate and **re-sends
  the last answer**, and the sender mirror updates (`TestResendAfterExpiredIdempotent`, D10);
  a conflicting body under the same id → `request.conflict`, first kept; `request resend`
  of a request 21 d old → `bad_state`; a new request whose `created` is 31 d old →
  `bad_body`, while a duplicate of a stored id of that age is still recognised.
- **`request.cancel` (D11, OD-P1-11 (b))**, per
  [request.md §Cancel](../protocol/request.md#cancel-od-p1-11): the kinds `request.cancel` and
  `request.cancelled`, the `cancelled` state, the sender-mirror step 5, IPC `request_cancel`
  and CLI `agentnet request cancel <id> [--reason R]`. Acceptance (e2e unless noted):
  - cancel of a `pending` and of a `deferred` request → recipient `cancelled`, sender mirror
    `cancelled`, `request.cancelled` notification on the recipient, gone from `inbox`,
    present in `inbox --all` (`TestCancelPendingAndDeferred`);
  - cancel after `accept`, `decline` or `complete` known to the mirror → `bad_state` naming
    the state, nothing sent; the race (recipient accepts, the cancel is sent before the
    accept arrives) → recipient stays `accepted`, sender ends `accepted` with `cancel =
    refused`, audit `request.cancel_refused` (`TestCancelRefusedAfterAccept`);
  - idempotency: a second `cancel` while in flight or after `cancelled` → `duplicate: true`,
    one cancel mail; a cancel mail forced `expired` → `cancel` sends a new one; a duplicate
    cancel on the recipient changes nothing;
  - cancel before the request arrives (request held back) → tombstone, `request.cancelled
    seq 1`, then the request is stored `cancelled` with no notification and no inbox entry;
    the 1001st tombstone from one sender is ignored; tombstones are pruned after 31 d (fake
    clock);
  - `accept` of a `cancelled` request → `bad_state`; `request resend` with `cancel` set →
    `bad_state`;
  - budget: a cancelled `high` still counts toward both 7-day urgency budgets.
- **Completion result (D14)**, per
  [request.md §Result payload](../protocol/request.md#result-payload-d14): the optional
  `result` on `request.complete`, migration 12, and the `result` member of the request view.
  Acceptance:
  - **a table test for every cap** of `ValidateComplete`, each at the limit (accepted) and
    one over (rejected with the documented code and field name): `status` each of the four
    values and one unknown value; `summary` 280 / 281 code points (and 280 four-byte code
    points accepted) and a control character; `exit_code` −2147483648 and 4294967295
    accepted, one beyond each rejected, and a fraction rejected; `output` 32768 / 32769
    bytes (and a multi-byte character straddling the limit), with `\n` and `\t` accepted and
    ESC (U+001B), `\r` and U+007F rejected; `artifacts` 1 and 20 / 21, and `[]` rejected;
    each result artifact member at its min, max and max+1; `null` for any optional member
    rejected; an unknown member rejected; `result` on any other lifecycle kind rejected;
    total `canonical(complete body)` 65536 / 65537 bytes, built from fields that are each
    valid → `result_too_large` at IPC and `mail.ErrBadBody` on the sender-mirror path (the
    mirror keeps its previous state);
  - a maximal valid complete body (65536 bytes) seals into one mail under
    `MaxMailPlaintext`;
  - **round trip** (e2e): B completes with every result member set → A's `out` row
    `result` is byte-identical to `canonical(result)` on B's `in` row and inside B's
    `last_reply` (`TestCompleteResultRoundTrip`);
  - **the sender sees it**: A's `request_show` returns the full result with `output` and
    the right `output_bytes`; A's `request_list` returns it without `output`, with
    `output_bytes`; B's `inbox_list` with `all` likewise; a completion without a result
    leaves the column NULL and the view without `result` (the pre-D14 behaviour);
  - **oversize rejected**: an IPC `request_complete` over the total cap →
    `result_too_large`, and over a field cap → `bad_request`; in both cases the row stays
    `accepted` and nothing is sent;
  - echoes: a duplicate request after completion, and a cancel refused after completion,
    re-send the stored complete with the same result; the mirror ignores it by `seq` (and
    sets `cancel = refused` for the cancel) without changing `result`;
  - audit: `request.complete` and `request.state` carry `result_bytes`, `output_bytes` and
    `artifacts`; no `audit_events` row contains marker strings placed in the summary,
    output and artifact members, and none has a status or exit-code member.

### 1.6b Inbox

- Files: `internal/request` (`inbox_list` query), `cmd/agentnet/inbox.go` (`inbox`, `accept`,
  `decline`, `defer`, `complete`), and tests.
- Acceptance (1.6): three requests `low`, `high`, `normal` → inbox order `high`, `normal`,
  `low` (`TestInboxOrder`). A deferred request is hidden until `until`, then `due: true`.
  `--all` shows answered and cancelled requests; a cancelled one is not in the default list.
  `ambiguous_request` → `--from` resolves it. `agentnet complete` result flags
  ([inbox.md §Result](../cli/inbox.md#result)): a result flag without `--status` → exit 2;
  `--output-from-file` turns CRLF into LF and strips ANSI CSI sequences, and rejects another
  control character or invalid UTF-8 with `bad_request`; `-` reads stdin; `--artifact`
  uses the request `SPEC` parser; `request show` prints the result, and `request list` its
  `RESULT` column.

### 1.7 Urgency guards

- Files: `internal/request`, and tests.
- Acceptance (1.7): with an honest sender, the 6th `high` in 7 days arrives as `normal` with
  `urgency_declared: high` and `downgraded_by: sender`; with the sender budget disabled
  (test option), the same with `downgraded_by: receiver`; the 3rd `blocking` likewise; the
  budget frees up after 7 days (fake clock); auto-declines do not count against the budget.
- Priority: a scenario in which one sender has 4 urgent requests answered and 1 of them
  accepted first. The test **derives the expected priority from the formula in
  [request.md §Effective priority](../protocol/request.md#effective-priority)**, written out
  in the test (`2000 + ((base − 2) × 1000 × (a + 2)) / (n + 2)`, integer division), with
  `n` and `a` counted from the scenario's own answers and `base` from the urgency. It must
  **not** hard-code the result (no literal 2500), and must not call `request.Priority`, so a
  wrong `Priority` or a wrong `n`/`a` query fails the test. It checks `high` and `blocking`.

### 1.8a Desktop notifications (review)

- Files: new `internal/notify` (`Clean`, `Desktop` per OS, trigger dispatch), settings,
  `internal/daemon`, `cmd/agentnet/notify.go`, and tests.
- Acceptance (1.8): with a fake `Desktop`, the call arrives within **5 s** of the receiver
  commit (e2e); `Clean` table tests (controls, bidi, U+2028, truncation); per-OS command
  construction tests assert that **no peer text appears in the script source or the command
  line** (osascript argv, gdbus argv, PowerShell environment); the gdbus arguments are
  GVariant string literals that round-trip titles containing `'`, `\`, `"` and `@s`, and the
  Linux body has `&<>` escaped; a manual check per OS in
  `tests/phase1-manual.md`; event toggles are honoured, including `request.cancelled` (on
  by default, recipient side only); a `request.completed` with a result shows ` (<status>)`
  and none of the result's summary, exit code, output or artifacts (D14).

### 1.8b Webhook (review)

- Files: `internal/notify` (webhook, signing, queue worker), migration 13, keystore secret,
  `cmd/agentnet/notify.go`, and tests.
- Acceptance (1.8): an `httptest` receiver gets the payload, and the signature verifies with
  the printed secret (`TestWebhookSigned`); the body never contains the brief or artifacts,
  and contains the title only with `title: true`; a `request.completed` carries
  `request.result_status` only with `title: true`, and never the result's summary, exit
  code, output or artifacts (D14); a 500 then 200 → one retry then `sent`;
  a 404 → `failed`, no retry; a 302 → `failed`; an `http://` non-loopback URL →
  `bad_webhook`; a URL whose name resolves to `169.254.169.254` (fake resolver) fails
  `blocked_address` at dial time; a replay with the same id is answered `2xx` by the
  reference verifier without calling its handler twice, and a stale timestamp is rejected;
  `slack` text escapes `<!channel>`, and `discord` carries `allowed_mentions: {parse: []}`;
  `--webhook off` deletes the secret and fails pending rows.

### 1.9 Offline end-to-end and metrics

- Files: `tests/` (the e2e harness), `tests/phase1-manual.md`, and docs reconciliation.
- Acceptance: stop B → A requests (< 2 s, `queued`) → start B → B's inbox has it, A's
  `request show` → `delivered` → B accepts → A's mirror `accepted` and a notification. A
  second request is sent and then **cancelled** by A while B is stopped, and B, on restart,
  shows it `cancelled`. The audit log on both sides has every event of
  [request.md §Audit](../protocol/request.md#audit-and-metrics), with timestamps, including
  `request.cancel` (sender) and `request.cancel_in` (recipient), and no title, brief, reason
  or note text, **including the cancel reason** and the completion result (D14)
  (`TestAuditHasNoContent`: the requests, the cancel and a completion result carry marker strings that must appear in no `audit_events` row). The docs match
  the behaviour.

### 1.H Headless agent harness run

- Depends on 1.6b (the whole request → inbox → accept → complete path exists in the CLI).
  Runs in parallel with 1.7, 1.8a, 1.8b and 1.9. **1.P depends on it.**
- Files: `tests/harness/phase1-agents.sh` and `tests/harness/phase1-agents.ps1` (the
  scripts), `tests/harness/README.md` (prerequisites and how to run), the agent snippet
  [../agents/snippet.md](../agents/snippet.md), and the result record in
  `tests/phase1-manual.md`.
- The script starts a loopback relay and two daemons (A and B) with separate config
  directories, pairs them, creates a team, then drives **real agents headless**:
  - agent A (sender) is told in plain words to ask B's agent for a review of a given
    branch, with a stated `--idempotency-key`;
  - agent B (recipient) is told to check its AgentNet inbox, accept what is there, do
    nothing else, then mark it complete with a short note.
  Each agent's working directory holds only the snippet (as `CLAUDE.md` for Claude Code,
  `AGENTS.md` for the other harness). The prompts do not name any `agentnet` subcommand:
  the agent must find them from the snippet and `--help`.
- Harnesses: **Claude Code** (`claude -p`, non-interactive, with a tool allowlist that
  permits only the `agentnet` binary) and **one other** (Codex CLI `codex exec`, or another
  headless harness if Codex is unavailable, named in the record). The run is done twice,
  with the roles swapped, so each harness is once sender and once recipient.
- Acceptance (asserted by the script from `--json` output, not from agent text):
  A's `request show` ends `completed` with the note; B's `inbox --all` shows it `completed`;
  exactly one request exists (no duplicate from agent retries); the audit logs hold
  `request.submit`, `request.in`, `request.accept`, `request.complete` and `request.state`;
  the whole run finishes within 10 minutes. The script exits non-zero on any failure.
- Not in CI (it needs model API keys and installed harnesses). It is run by hand before 1.P,
  and the record in `tests/phase1-manual.md` states the date, the harness versions, pass or
  fail, and any snippet change it needed.

## Owner decisions needed

Each of these was not settled by the plan or D1–D10. The specs are written with the
recommendation, so approving the specs as they are accepts every recommendation below.

| # | Decision | Options | Recommendation |
|---|---|---|---|
| OD-P1-1 | Who may change a team's membership | (a) single owner; (b) any member may invite; (c) owner + admins | **(a)**. One writer means no merge conflicts and a simple roster. Admins can come later as a roster field |
| OD-P1-2 | How members who never paired with each other get each other's keys | (a) owner **introduces** members (new trust `team`); (b) every pair of members must pair directly (10 codes for 5 people) | **(a)**. It matches "pair once". The owner is already trusted by code pairing. A compromised owner can only affect its own team |
| OD-P1-3 | Presence transport. The plan says the relay "holds presence" | (a) sealed peer-to-peer heartbeats + an ephemeral relay type (the relay sees only the connection and from→to timing); (b) the relay tracks and broadcasts presence (the relay would learn team graphs and visibility, and see flags in clear) | **(a)**. It is the only way "invisible / only team" hides anything, and it keeps agent/human flags from the relay |
| OD-P1-4 | Urgency budget window and scope | window: rolling 7 d, or calendar week (UTC Monday); scope: sender-global (honest-sender) + receiver per-sender (enforced), or receiver-only | **Rolling 7 d, both**. There are no accounts until 4.2. The receiver side is the only tamper-proof check. The sender side gives the sender's agent immediate feedback |
| OD-P1-5 | "Acceptance-as-urgent" definition and weighting | the first response to an urgent request is `accept` (vs. `accept` at any time, or accept within N hours); prior `(a+2)/(n+2)`; floor at `normal` | **As specified**. It is simple, integer-only and deterministic, and new senders start at full weight |
| OD-P1-6 | Let a user stop sharing "human present" | (a) `presence --human off` (default on); (b) always shared | **(a)**. It is a small privacy control that costs one setting |
| OD-P1-7 | The immediate heartbeat on the agent-active edge reveals agent-activity timing to the relay | (a) accept; (b) send only on the 30 s tick (the acceptance bound becomes borderline) | **(a)**. The relay already sees mail timing |
| OD-P1-8 | Inviting an already-paired peer without a code | (a) code only in Phase 1 (re-pairing through the code is harmless); (b) also `team add @peer` with an accept step | **(a)**. It is one path to build and review |
| OD-P1-9 | Webhook payload privacy | title off by default (opt-in), or on by default. The brief is never included either way | **Off by default**. Webhooks usually post to third-party chat |
| OD-P1-10 | The owner's device is lost | (a) no ownership transfer in Phase 1: members leave and someone re-creates the team; (b) add transfer | **(a)**. Revisit if beta teams hit it |
| OD-P1-11 | The sender cancels (withdraws) a request | (a) not in Phase 1; (b) add `request.cancel` | **(b) — owner decision D11.** Allowed while the recipient has it `pending` or `deferred`, refused after accept, decline or complete, costs and refunds no budget. Specified in [request.md §Cancel](../protocol/request.md#cancel-od-p1-11), built in 1.6a |
| OD-P1-12 | "Completion" before Phase 2 sessions exist | (a) `request.complete` kind now, and Phase 2 keeps it for session-less requests; (b) no completion until 2.1 | **(a)**. The plan requires completion to be logged from day one |
| OD-P1-13 | Desktop notifier library | (a) own ~150-line per-OS code that never interpolates peer text; (b) `beeep` (the plan's suggestion), which formats AppleScript and PowerShell source from strings | **(a)**. This is a security reason (script injection through the request title) |
