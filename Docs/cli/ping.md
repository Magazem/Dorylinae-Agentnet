# `agentnet ping`

Round-trips an end-to-end encrypted message to a paired agent through the
relay and reports the round-trip time. Introduced by ticket 0.6; the session
protocol is [../protocol/session.md](../protocol/session.md).

```
agentnet ping @peer [--json]          ping a peer by name or public key
agentnet ping --status <id> [--json]  check a ping that was still pending
```

`@peer` is a paired peer's name (case-insensitive) or its public key; the `@`
is optional. If several peers share the name, use the public key.

| Flag | Meaning |
|------|---------|
| `--status ID` | Show the state of a ping by its ID |
| `--json` | Machine-readable output on stdout |

The first ping to a peer sets up a Noise XX session (`handshake: true`); later
pings reuse it. The relay sees only ciphertext.

## Two-second rule

The command returns in under 2 seconds. The daemon waits at most one second
for the pong; if it has not arrived, the command exits 0 with `state:
"pending"` and a ping ID to poll with `--status`. A ping with no answer fails
after 10 seconds with `timeout`, and the session is dropped so the next ping
handshakes again (for example after the peer restarted).

**An offline peer looks like a timeout.** The relay does not answer `peer_offline` for
envelopes; it queues them for the peer ([envelope.md](../protocol/envelope.md#offline-queue)).
A ping to a peer that is not connected therefore fails with `timeout` after 10 seconds
(unless the peer comes back within that time). To send something that waits for an offline
peer, use mail ([mail.md](mail.md)), not ping.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Pong received, or still pending |
| 1 | Ping failed, or the request could not be made |
| 2 | Usage error |
| 3 | Daemon not running |

## Human output

```
pong from @bob: rtt 3.2 ms (encrypted)
```

Pending: `Ping ping-… to @bob is still in flight.` plus the `--status` command.
Failures go to stderr.

## `--json` output

```json
{
  "ok": true,
  "ping_id": "ping-1f2e3d4c5b6a7980",
  "peer": {"public_key": "<base64url>", "name": "bob"},
  "state": "complete",
  "rtt_ms": 3.2,
  "handshake": true
}
```

| Field | Meaning |
|-------|---------|
| `ok` | `false` only when `state` is `failed` |
| `state` | `pending`, `complete` or `failed` |
| `rtt_ms` | Complete only: time from sending the encrypted ping to receiving the pong, in milliseconds (excludes the handshake) |
| `handshake` | `true` if a new session was set up for this ping |
| `error` | Failed only: `{"code","message"}`; codes `timeout`, `handshake_failed`, `send_failed`, or a relay refusal such as `queue_full` |

Requests that cannot be made print `{"ok":false,"error":{"code","message"}}`
with code `unknown_peer`, `ambiguous_peer`, `no_relay`, `relay_unavailable`,
`unknown_ping`, `too_many_pings`, `daemon_not_running` or `usage`.
