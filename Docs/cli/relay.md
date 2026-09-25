# `relay`

The AgentNet relay: accepts WebSocket connections from daemons, authenticates
them by a signed challenge, and forwards envelopes between them. It never reads
or logs envelope payloads. Protocol: [../protocol/envelope.md](../protocol/envelope.md).

Phase 0 runs it locally over plain `ws://` (no TLS). Envelopes for an offline
peer are stored in a SQLite file and delivered in order when the peer
reconnects; they survive a relay restart. The relay never answers `peer_offline` for an
envelope, so an offline peer shows up to a sender as silence (a ping times out; mail stays
queued and is acked later). Tickets 0.4, 0.7.

```
relay [--listen HOST:PORT] [--allow-non-loopback] [--queue-db PATH] [--queue-ttl DURATION] [--allow-pairing-v1[=false]] [--verbose] [--version]
relay version [--json]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--listen HOST:PORT` | `127.0.0.1:8787` | Address to listen on. Port `0` picks a free port |
| `--allow-non-loopback` | off | Permit `--listen` on a non-loopback address. Without it such an address is refused, because the Phase 0 relay has no TLS |
| `--queue-db PATH` | `relay-queue.db` in the config dir (`$DORYLINAE_HOME`, else the OS user config dir + `dorylinae`) | SQLite file for envelopes queued for offline peers. Created, with its directory, if missing. If it cannot be opened the relay exits 1 with a message |
| `--queue-ttl DURATION` | `168h` (7 days) | How long an envelope waits for its recipient before it is dropped. Must be positive |
| `--allow-pairing-v1` | on if `--listen` is loopback, else off | Accept the v1 pairing frames (`pair_new` without `lookup`, `pair_redeem` with `code`). With it off they get `pair_v1_disabled`. Pass `--allow-pairing-v1=false` to turn it off on loopback. v2 pairing (lookup only, entry kept until `pair_cancel`, expiry or 3 redemptions) is always on. See [../protocol/pairing.md](../protocol/pairing.md#v1-compatibility) |
| `--verbose` | off | Also log every connect, disconnect and routed envelope (routing metadata only: abbreviated keys, `type`, `id`, byte counts) |
| `--version` | | Print the version and exit |
| `--help`, `-h` | | Print usage and exit |

Daemons connect to `ws://HOST:PORT/v1/connect`.

## Output

With no flags, `relay` listens on `127.0.0.1:8787` and runs in the foreground until
interrupted (Ctrl+C or SIGTERM); it does not exit on its own and prints nothing further
unless `--verbose` is set or something goes wrong.

On start, one line on stderr:

```
relay listening on 127.0.0.1:8787; Ctrl+C to stop
```

Further logs also go to stderr, as `slog` text lines. By default only warnings (for
example rejected authentication) appear. Logs never contain payloads or raw frames.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Stopped cleanly (SIGINT/SIGTERM) or `--help`/`--version` |
| 1 | Could not listen, could not open the offline queue, or the server failed |
| 2 | Usage error, including a non-loopback `--listen` without `--allow-non-loopback` or a non-positive `--queue-ttl` |

The relay has no `--json` output.

## Daemon side

`agentnetd` connects to a relay when given its URL:

| Setting | Meaning |
|---------|---------|
| `--relay URL` | e.g. `ws://127.0.0.1:8787`. A URL without a path gets `/v1/connect` |
| `DORYLINAE_RELAY_URL` | Same, used when `--relay` is not given |

To make an installed service use a relay, pass `--relay` to `agentnetd install`
([agentnetd-install.md](agentnetd-install.md)). All daemon flags: [agentnetd.md](agentnetd.md).

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
