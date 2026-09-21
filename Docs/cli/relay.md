# `relay`

The AgentNet relay: accepts WebSocket connections from daemons, authenticates
them by a signed challenge, and forwards envelopes between them. It never reads
or logs envelope payloads. Protocol: [../protocol/envelope.md](../protocol/envelope.md).

Phase 0 runs it locally, in memory, over plain `ws://` (no TLS, no queue for
offline peers, no persistence). Ticket 0.4.

```
relay [--listen HOST:PORT] [--allow-non-loopback] [--verbose] [--version]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--listen HOST:PORT` | `127.0.0.1:8787` | Address to listen on. Port `0` picks a free port |
| `--allow-non-loopback` | off | Permit `--listen` on a non-loopback address. Without it such an address is refused, because the Phase 0 relay has no TLS |
| `--verbose` | off | Also log every connect, disconnect and routed envelope (routing metadata only: abbreviated keys, `type`, `id`, byte counts) |
| `--version` | | Print the version and exit |
| `--help`, `-h` | | Print usage and exit |

Daemons connect to `ws://HOST:PORT/v1/connect`.

## Output

On start, one line on stdout:

```
relay listening on 127.0.0.1:8787
```

Logs go to stderr as `slog` text lines. By default only warnings (for example
rejected authentication) appear. Logs never contain payloads or raw frames.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Stopped cleanly (SIGINT/SIGTERM) or `--help`/`--version` |
| 1 | Could not listen, or the server failed |
| 2 | Usage error, including a non-loopback `--listen` without `--allow-non-loopback` |

The relay has no `--json` output.

## Daemon side

`agentnetd` connects to a relay when given its URL:

| Setting | Meaning |
|---------|---------|
| `--relay URL` | e.g. `ws://127.0.0.1:8787`. A URL without a path gets `/v1/connect` |
| `DORYLINAE_RELAY_URL` | Same, used when `--relay` is not given |

With neither set, the daemon runs without a relay. The daemon keeps one
persistent connection and reconnects with backoff (500 ms doubling to 30 s)
when it drops. An invalid URL (not `ws://` or `wss://`) stops the daemon at
start with an error. The daemon signs the relay's challenge with its identity
key; the key is read from the keystore only for each signature.

## Try it

```
relay --listen 127.0.0.1:8787
agentnetd --relay ws://127.0.0.1:8787
```
