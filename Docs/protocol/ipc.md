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

### `identity`

Params: none (any params are ignored).

Result: the signed Agent Card, see [agent-card.md](agent-card.md):
`{"card": {...}, "signature": "<base64url>", "key_backend": "keychain|file"}`.
The private key never appears in any IPC message; there is no method that
returns or exports it.

### `pair_new`

Params: none. Asks the relay for a one-time code and waits at most one second.
Result: a pairing status (below) with `role: "issuer"`, carrying `code` and
`expires` if the relay answered in time.

### `pair_redeem`

Params: `{"code": "<code as typed>"}`. Redeems a code and waits at most one
second. Result: a pairing status with `role: "redeemer"`.

### `pair_status`

Params: `{"pairing_id": "<id>"}`. Result: the pairing status.

A pairing status is `{"pairing_id", "role": "issuer|redeemer", "state":
"pending|complete|failed", "code"?, "expires"?, "peer"?, "error"?}`, described
in [../cli/pair.md](../cli/pair.md). Setup failures (below) are IPC errors; a
relay refusal or a bad card is a status with `state: "failed"`.

### `peers`

Params: none. Result: `{"peers": [{"public_key", "name", "harness", "skills",
"paired_at"}]}`, see [../cli/peers.md](../cli/peers.md).

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

Sessions record `session.open` and `session.reject` (tampered, replayed,
reordered, unpaired or malformed session envelopes); details are in
[session.md](session.md#rejection).
