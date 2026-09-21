# Local IPC protocol (CLI <-> agentnetd)

Status: v1, introduced by ticket 0.2a. Implemented in `internal/ipc`.

The CLI (and later, adapters) talk to the daemon over a local, per-user
endpoint. This is a **local** API only; it never crosses the network.

## Endpoint

| OS | Endpoint | Access control |
|----|----------|----------------|
| Linux, macOS | Unix domain socket `<config dir>/agentnetd.sock` | Config dir is `0700`, socket is `0600` |
| Windows | Named pipe `\\.\pipe\dorylinae-<id>` where `<id>` is the first 8 bytes (hex) of SHA-256 of the config dir path | Pipe DACL grants access to the current user's SID only |

The config dir is `os.UserConfigDir()/dorylinae`, or the value of the
`DORYLINAE_HOME` environment variable when set (used by tests and for running
several daemons side by side). The SQLite database is `<config dir>/dorylinae.db`.

A second daemon on the same endpoint refuses to start. A stale Unix socket file
(no listener answering) is removed on start.

## Framing

Newline-delimited JSON (one JSON object per line, UTF-8). The client sends one
request line and reads one response line; a connection may carry several
request/response pairs in sequence. Lines are limited to 1 MiB. The server
closes connections that stay idle for more than 30 s.

## Request

```json
{"id": "1", "method": "status", "params": {}}
```

| Field | Type | Notes |
|-------|------|-------|
| `id` | string, optional | Echoed back in the response |
| `method` | string, required | Method name |
| `params` | object, optional | Method specific |

## Response

Success:

```json
{"id": "1", "ok": true, "result": {}}
```

Failure:

```json
{"id": "1", "ok": false, "error": {"code": "unknown_method", "message": "unknown method \"x\""}}
```

Error codes (stable, machine readable):

| Code | Meaning |
|------|---------|
| `bad_request` | Malformed JSON or missing `method` |
| `unknown_method` | No handler registered for `method` |
| `internal` | Handler failed; message has no sensitive detail |

## Methods

### `status`

Params: none.

Result:

| Field | Type | Notes |
|-------|------|-------|
| `pid` | integer | Daemon process ID |
| `started_at` | string | RFC 3339 UTC start time |
| `uptime_seconds` | number | Seconds since start |
| `version` | string | Daemon version |
| `outbox` | object | `{queued, relayed, expired}`: sender outbox rows in each state ([mail.md](mail.md#outbox)) |
| `presence` | object | Phase 1 (1.2c): `{"mode": "visible"\|"invisible"\|"only_team", "team"?: {"id","name"}, "relay": "connected"\|"disconnected"\|"unsupported"\|"none", "agent_active": bool, "human_present": bool\|null}`. The last two are this machine's own values, detected locally, even when not shared |
| `team` | object | Only with the param `team` (Phase 1, 1.2c), see below |

Params (Phase 1): `{"team"?: "<team ref>"}`. A team ref is a team id or a unique name of a
team on this daemon ([team.md](team.md#local-names)). Without `team`, the result is as above.
With it, `team` is added:

```json
{
  "id": "t-0123456789abcdef0123456789abcdef",
  "name": "backend",
  "owner": "<key>",
  "epoch": 3,
  "state": "active",
  "members": [
    {
      "name": "alice", "public_key": "<key>", "fingerprint": "2ED9TGVER47163MCC451",
      "self": true, "owner": true, "trust": null,
      "daemon_online": true, "agent_active": true, "human_present": null,
      "last_seen": "2026-10-01T09:12:03Z",
      "agent_last_active": "2026-10-01T09:12:03Z", "human_last_present": null
    }
  ]
}
```

Members are listed owner first, then by `name`, then by `public_key`. For peers, the values
come from [presence.md §Receiving](presence.md#receiving) (effective state at call time).
`last_seen`, `agent_last_active` and `human_last_present` are RFC 3339 UTC with whole
seconds, or `null`. `trust` is the peer's trust value. For `self`, `trust` is `null`,
`daemon_online` is `relay == "connected"`, `last_seen` is now, and the other two come from
the local values. A member that is not a peer (for example, not yet introduced) has
`daemon_online: false` and every time `null`. Errors: `unknown_team`, `ambiguous_team`.

### `identity`

Params: none (any params are ignored).

Result: the signed Agent Card, see [agent-card.md](agent-card.md):
`{"card": {...}, "signature": "<base64url>", "key_backend": "keychain|file"}`.
The private key never appears in any IPC message; there is no method that
returns or exports it.

### `pair_new`

Params: none. Generates a v2 code (15 characters, shown `LLLLL-SSSSS-SSSSS`),
registers its lookup with the relay and waits at most one second. Result: a
pairing status (below) with `role: "issuer"`, carrying `code` and `expires` (the
daemon's own 10-minute limit) if the relay answered in time.

### `pair_redeem`

Params: `{"code": "<code as typed>", "v1"?: true}`. Redeems a code and waits at
most one second. A 15-character code is v2; a 10-character (legacy) code is
refused with `bad_code` unless `v1` is true. Result: a pairing status with
`role: "redeemer"`.

### `pair_status`

Params: `{"pairing_id": "<id>"}`. Result: the pairing status.

A pairing status is `{"pairing_id", "role": "issuer|redeemer", "state":
"pending|complete|failed", "code"?, "expires"?, "peer"?, "error"?}`, described
in [../cli/pair.md](../cli/pair.md). Setup failures (below) are IPC errors; a
relay refusal, a bad card or mailbox key, a failed confirmation or a reused code
(`code_used`) is a status with `state: "failed"`.

### `peers`

Params: none. Result: `{"peers": [{"public_key", "name", "harness", "skills",
"paired_at", "trust", "fingerprint", "introduced_by"}]}` (`introduced_by` from Phase 1, [team.md](team.md#introduced-peers)), see [../cli/peers.md](../cli/peers.md).
`identity` also returns `"fingerprint"` (own key).

### `peers_verify`

Params: `{"peer": "<name or public key>", "fingerprint": "<as typed>"}`. The
fingerprint is normalised and compared in constant time with `fp(peer key)`. On
a match the peer's trust becomes `fingerprint`; result `{"peer": {...}}`. Errors:
`bad_fingerprint` (not 20 characters of the alphabet), `fingerprint_mismatch`
(nothing changed), `unknown_peer`, `ambiguous_peer`, `bad_request`.

### `peers_remove`

Params: `{"peer": "<name or public key>"}`. Deletes the peer; result
`{"peer": {...the removed peer...}}`. Later session envelopes from that key are
rejected as `unpaired`. Errors: `unknown_peer`, `ambiguous_peer`, `bad_request`.

Pairing setup error codes: `no_relay` (daemon has no relay), `relay_unavailable`
(not connected), `bad_code`, `unknown_pairing`, `too_many_pairings`;
`bad_request` for missing params.

### `ping`

Params: `{"peer": "<name or public key, optional leading @>"}`. Sends an
encrypted ping over a Noise session ([session.md](session.md)), handshaking
first if needed, and waits at most one second for the pong. Result: a ping
status.

### `ping_status`

Params: `{"ping_id": "<id>"}`. Result: the ping status.

A ping status is `{"ping_id", "peer": {"public_key", "name"}, "state":
"pending|complete|failed", "rtt_ms"?, "handshake", "error"?}`, described in
[../cli/ping.md](../cli/ping.md). A ping with no pong fails after 10 s
(`timeout`); a relay refusal fails it with the relay's code (for example `queue_full`). Since ticket 0.7 an offline peer is not a refusal: the relay queues the handshake message and the ping times out unless the peer returns within 10 s.

Ping setup error codes: `unknown_peer`, `ambiguous_peer` (several peers share
the name), `no_relay`, `relay_unavailable`, `unknown_ping`, `too_many_pings`;
`bad_request` for missing params.

### `mail_submit`

Params: `{"to": "<peer name or public key>", "kind": "<kind>", "body": {...}}`. `body` is
optional and must be a JSON object. Result: `{"id": "m-...", "state": "queued"}`. The mail is
signed, sealed and stored in the outbox; the call never waits for the relay (under 2 s). The
daemon resends it until the peer acks it ([mail.md](mail.md#outbox)). A relay refusal such as
`peer_offline` or `peer_busy` never fails the mail: it stays `queued`. The only CLI front end
is the debug command `agentnet mail send` ([../cli/mail.md](../cli/mail.md)).

Error codes: `unknown_peer`, `ambiguous_peer`, `unpaired`, `no_mailbox_key` (the peer was
paired with v1 and must re-pair); `bad_request` for missing params, a bad kind or body, or
kind `ack`.

## Phase 1 methods

These are specified in [team.md](team.md), [presence.md](presence.md),
[request.md](request.md) and [notify.md](notify.md). Every method below returns within 2 s
and never waits for the relay or a peer, except `team_invite` and `team_join`, which wait at
most 1 s like `pair_new` and `pair_redeem`.

**Agent activity.** The IPC server records the time of **every** request it dispatches (any
method, including `status`), before the handler runs. This time drives *agent active*
([presence.md](presence.md#levels)). A request that makes it active again, after 5 min or
more without one, triggers an asynchronous presence send. The handler never waits for it.

Common objects:

- **team summary** `{"id", "name", "owner", "epoch", "state", "role": "owner"|"member", "members": <int>}`
- **peer ref** `{"name", "public_key", "fingerprint"}`
- **presence brief** `{"daemon_online": bool, "last_seen": "<time>"|null}`

New error codes, which the CLI maps to exit 1 unless stated otherwise:

| Code | Meaning |
|---|---|
| `unknown_team`, `ambiguous_team` | Team reference not found, or it matches several teams |
| `team_exists` | `team_create`: an active team with that name exists |
| `bad_team_name` | Not `^[a-z0-9][a-z0-9-]{0,31}$` |
| `not_owner` | Operation needs the team owner |
| `owner_cannot_leave` | `team_leave` by the owner (use `team_delete`) |
| `not_member` | The peer is not a member of the team |
| `team_inactive` | The team is `left`, `removed` or `dissolved` |
| `team_full` | The team already has 32 members (`team_invite`) |
| `no_shared_team`, `not_team_member` | `request_submit` team resolution |
| `unverified_peer` | D5: `trust=relay` peer on a non-loopback relay |
| `unknown_request`, `ambiguous_request` | Request reference not found, or it matches `in` rows from several peers (pass `from`) |
| `bad_state` | Lifecycle transition, or `request_resend`, is not allowed in the current state |
| `idempotency_conflict` | Same `idempotency_key` for this peer with different params |
| `bad_webhook` | Webhook URL rejected (scheme, host or length) |

### Teams

| Method | Params | Result |
|---|---|---|
| `team_create` | `{"name"}` | `{"team": <team summary>}` |
| `team_list` | `{"all"?: bool}` | `{"teams": [<team summary>]}`: `active` teams only unless `all`. Sorted by name, then id |
| `team_show` | `{"team"}` | `{"team": <team summary> + "members": [{<peer ref>, "added", "owner": bool, "self": bool}]}`. Here `members` is the list, replacing the count |
| `team_invite` | `{"team"}` | A pairing status ([pair_new](#pair_new)) plus `"team": {"id","name"}`. Owner only (`not_owner`). Errors as for `pair_new`, plus `team_full` and `team_inactive` |
| `team_join` | `{"code"}` | A pairing status ([pair_redeem](#pair_redeem)). v2 codes only (`bad_code` for 10-character codes). On `complete`, the daemon writes the pending join and submits `team.join` |
| `team_remove` | `{"team", "peer"}` | `{"team": <team summary>}`. Owner only. `not_member`, `bad_request` (removing self) |
| `team_rename` | `{"team", "name"}` | `{"team": <team summary>}`. Owner only. `team_exists`, `bad_team_name` |
| `team_leave` | `{"team"}` | `{"team": <team summary>}` (state `left`). `owner_cannot_leave` |
| `team_delete` | `{"team"}` | `{"team": <team summary>}` (state `dissolved`). Owner only |

`pair_status` also reports team invites and joins, with their `pairing_id`.

### Presence

| Method | Params | Result |
|---|---|---|
| `presence_get` | none | `{"mode", "team"?: {"id","name"}, "human_share": bool}` |
| `presence_set` | `{"mode": "visible"\|"invisible"\|"only_team", "team"?: "<team ref>", "human_share"?: bool}` | Same as `presence_get`. `team` is required with `only_team` and forbidden otherwise (`bad_request`). The team must be `active`. Setting only `human_share` is allowed (`mode` may be omitted) |

### Requests

**`request_submit`**. Params:

```json
{"to": "<peer>", "type": "review", "team"?: "<team ref>", "title": "...", "brief": "...",
 "urgency"?: "normal", "urgency_reason"?: "...",
 "artifacts"?: [{"url"?, "branch"?, "commit"?, "path"?}],
 "requested_grant"?: {"action", "resource", "note"?},
 "deadline"?: "<RFC 3339 or duration>", "idempotency_key"?: "..."}
```

`urgency` defaults to `normal`. Result: the [submit result](request.md#submit-result-19).
Errors: `unknown_peer`, `ambiguous_peer`, `no_mailbox_key`, `unverified_peer`,
`unknown_team`, `ambiguous_team`, `no_shared_team`, `not_team_member`,
`idempotency_conflict`, `bad_request` (with a message naming the field).

**Request view** (used by the methods below):

```json
{
  "id": "r-...", "direction": "in"|"out", "peer": <peer ref>, "team": {"id", "name"},
  "type", "title", "brief", "urgency", "urgency_declared", "downgraded_by": "sender"|"receiver"|null,
  "urgency_note"?: "...", "urgency_reason"?, "artifacts": [...], "requested_grant"?, "deadline"?,
  "created", "received_at"?: "<in only>", "state", "state_at": "<time>"|null,
  "deferred_until"?, "due"?: true, "decline_code"?, "reason"?, "note"?,
  "priority"?: 3000,
  "delivery"?: "queued"|"relayed"|"delivered"|"expired"|"failed"|"unknown",
  "mail_id"
}
```

`priority` and `due` are present on `in` views. `delivery` is present on `out` views: the
outbox state of `mail_id`, or `unknown` once pruned. `out` views also carry `"presence":
<presence brief>` for the peer.

| Method | Params | Result |
|---|---|---|
| `request_show` | `{"id", "from"?: "<peer>"}` | `{"request": <view>}`. Looks up `out` rows first, then `in` rows (`from` narrows the `in` lookup) |
| `request_list` | `{"state"?, "team"?, "peer"?}` | `{"requests": [<out view>]}`: the sender's own requests, newest `created` first |
| `request_resend` | `{"id"}` | `{"id", "mail_id", "status": "queued"}`. `bad_state` unless the row is `pending`, its mail is `expired` or `failed`, and the request is under 21 d old |
| `inbox_list` | `{"team"?, "all"?: bool}` | `{"requests": [<in view>]}` in [inbox order](request.md#inbox-16) |
| `request_accept` | `{"id", "from"?}` | `{"request": <in view>, "mail_id"}` |
| `request_decline` | `{"id", "from"?, "reason"}` | Same. `reason` is required, 1–500 code points |
| `request_defer` | `{"id", "from"?, "until": "<RFC 3339 or duration>"}` | Same. `until` must be in the future and at most 90 d away |
| `request_complete` | `{"id", "from"?, "note"?}` | Same |

Lifecycle errors: `unknown_request`, `ambiguous_request`, `bad_state`, `bad_request`.

### Notifications

| Method | Params | Result |
|---|---|---|
| `notify_get` | none | `{"desktop": bool, "events": {...}, "webhook": null \| {"url", "format", "title": bool, "pending": <int>, "failed_7d": <int>}}` |
| `notify_set` | `{"desktop"?: bool, "events"?: {"<event>": bool}, "webhook_url"?: "<url>"\|"", "format"?, "title"?: bool, "rotate_secret"?: bool}` | `notify_get`'s result plus `"secret"?: "whsec_..."`, present only when a secret was just created or rotated. `webhook_url: ""` removes the webhook |
| `notify_test` | none | `{"desktop": "shown"\|"failed"\|"disabled", "webhook": "queued"\|"none"}` |

Errors: `bad_webhook`, `bad_request`.

## Compatibility

New methods and new result fields may be added without a version bump.
Removing or changing the meaning of a field requires a new protocol version and
an entry here first.

## Audit events written by the daemon lifecycle

The `audit_events` table (`internal/audit`) records `daemon.start` and
`daemon.stop` with `actor = "daemon"`. Detail JSON: `{"pid": N, "version": "..."}`.
The table is append-only (triggers reject UPDATE and DELETE) and is not yet
hash-chained; ticket 3.6 adds a chain column by migration.

The daemon also records `identity.create` when it creates an identity or
re-creates a missing card (detail in [agent-card.md](agent-card.md)); it holds
the public key and key backend, never the private key.

Pairing records `pair.start`, `pair.complete` and `pair.fail`; details are in
[pairing.md](pairing.md#daemon-side-ticket-05b).

`agentnet peers verify` records `peer.verify` (or `peer.verify_fail` on a wrong
fingerprint) and `peers remove` records `peer.remove`, all with `actor = "cli"`
and detail `{"peer": "<public key>", "name", "fingerprint", "trust"?}`.

Sessions record `session.open` and `session.reject` (tampered, replayed,
reordered, unpaired or malformed session envelopes); details are in
[session.md](session.md#rejection).

Phase 1 events are listed in [team.md](team.md#audit), [presence.md](presence.md#audit),
[request.md](request.md#audit-and-metrics) and [notify.md](notify.md#audit).
