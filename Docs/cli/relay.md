# `relay`

The AgentNet relay: accepts WebSocket connections from daemons, authenticates
them by a signed challenge, and forwards envelopes between them. It never reads
or logs envelope payloads. Protocol: [../protocol/envelope.md](../protocol/envelope.md).

On loopback it serves plain `ws://`. Anywhere else it needs TLS: its own
certificate (`--tls-cert`/`--tls-key`), a Let's Encrypt certificate
(`--acme-domain`), or a reverse proxy or platform in front (`--behind-proxy`)
(Phase 4, ticket 4.0a; [../protocol/relay-hosted.md](../protocol/relay-hosted.md#1-transport-security-ticket-40a)). Envelopes for an offline
peer are stored in a SQLite file and delivered in order when the peer
reconnects; they survive a relay restart. The relay never answers `peer_offline` for an
envelope, so an offline peer shows up to a sender as silence (a ping times out; mail stays
queued and is acked later). Tickets 0.4, 0.7.

```
relay [--listen HOST:PORT] [--allow-non-loopback]
      [--tls-cert FILE --tls-key FILE | --acme-domain NAME [--acme-cache DIR] [--acme-email ADDR] | --behind-proxy]
      [--public-origin URL]... [--allow-auth-v1]
      [--queue-db PATH] [--queue-ttl DURATION] [--allow-pairing-v1[=false]] [--verbose] [--version]
relay version [--json]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--listen HOST:PORT` | `127.0.0.1:8787` | Address to listen on. Port `0` picks a free port |
| `--allow-non-loopback` | off | Permit `--listen` on a non-loopback address. Without it such an address is refused. **With it alone it is refused too** (exit 2, "a non-loopback relay needs TLS: --tls-cert/--tls-key, --acme-domain or --behind-proxy") |
| `--tls-cert FILE`, `--tls-key FILE` | | Serve `wss://` with this PEM certificate (chain) and key. TLS 1.2 minimum, HTTP/1.1 only |
| `--acme-domain NAME` | | Serve `wss://` with a Let's Encrypt certificate for `NAME` (TLS-ALPN-01 on the listen port, which the internet must reach on 443). `--acme-cache DIR` (default `relay-acme` next to the queue database) keeps the account and certificates; `--acme-email` is the optional contact address |
| `--behind-proxy` | off | TLS is terminated by a reverse proxy or the platform in front of the relay, which itself serves plain HTTP (usually on `127.0.0.1`). The relay is **public** anyway (see below) |
| `--public-origin URL` | the loopback names of the listen port, on a relay that is not public | An origin daemons dial to reach this relay, e.g. `wss://relay.example.com` (repeatable). **Required** with `--tls-cert`, `--acme-domain` and `--behind-proxy`. Relay auth v2 signatures must name one of these ([../protocol/envelope.md](../protocol/envelope.md#auth-daemon---relay)). List only origins this relay serves exclusively: a hostname shared with another relay defeats the binding |
| `--allow-auth-v1` | off | Also accept relay auth v1 (Phase 0–3 daemons) on a public relay, for a migration window. Logs a warning at start. v1 does not name the relay, so a hostile relay can replay it here; upgrade the daemons, then drop the flag. A relay that is not public always accepts v1 |
| `--queue-db PATH` | `relay-queue.db` in the config dir (`$DORYLINAE_HOME`, else the OS user config dir + `dorylinae`) | SQLite file for envelopes queued for offline peers. Created, with its directory, if missing. If it cannot be opened the relay exits 1 with a message |
| `--queue-ttl DURATION` | `168h` (7 days) | How long an envelope waits for its recipient before it is dropped. Must be positive |
| `--allow-pairing-v1` | on for a relay that is not public, off for a public one | Accept the v1 pairing frames (`pair_new` without `lookup`, `pair_redeem` with `code`). With it off they get `pair_v1_disabled`. Pass `--allow-pairing-v1=false` to turn it off on a relay that is not public. v2 pairing (lookup only, entry kept until `pair_cancel`, expiry or 3 redemptions) is always on. See [../protocol/pairing.md](../protocol/pairing.md#v1-compatibility) |
| `--verbose` | off | Also log every connect, disconnect and routed envelope (routing metadata only: abbreviated keys, `type`, `id`, byte counts) |
| `--version` | | Print the version and exit |
| `--help`, `-h` | | Print usage and exit |

Daemons connect to `ws://HOST:PORT/v1/connect`, or `wss://` with TLS. `GET /healthz`
answers `200 {"ok":true,"version":"…"}` while the queue database responds to `SELECT 1`
within a second, else `503`; it needs no authentication and shows no counts or keys.

### Public relays

A relay is **public** if it listens on a non-loopback address, or is started with
`--tls-cert`, `--acme-domain`, `--behind-proxy`, or a `--public-origin` whose host is not
loopback. A reverse proxy on the same host usually forwards to `127.0.0.1`, so the listen
address alone does not say who can reach the relay (review 50 H1). A public relay:

- requires relay auth v2: a Phase 0–3 daemon gets `auth_failed`, "this relay requires relay
  auth v2; update agentnet", unless `--allow-auth-v1` is set;
- has pairing v1 off unless `--allow-pairing-v1` is given.

A relay that is not public offers v1 and v2 auth, for the origins `ws://127.0.0.1:PORT`,
`ws://localhost:PORT` and `ws://[::1]:PORT` of the port it listens on (or the
`--public-origin` values given).

HTTP limits: request headers must arrive within 10 s and fit in 8 KiB; idle keep-alive
connections close after 60 s. A WebSocket, once upgraded, is bounded by the 10 s challenge
instead.

## Output

With no flags, `relay` listens on `127.0.0.1:8787` and runs in the foreground until
interrupted (Ctrl+C or SIGTERM); it does not exit on its own and prints nothing further
unless `--verbose` is set or something goes wrong.

On start, two lines on stderr:

```
relay listening on 127.0.0.1:8787; Ctrl+C to stop
public: no; origins: ws://127.0.0.1:8787 ws://localhost:8787 ws://[::1]:8787
```

With `--allow-auth-v1` on a public relay a warning line follows.

Further logs also go to stderr, as `slog` text lines. By default only warnings (for
example rejected authentication) appear. Logs never contain payloads or raw frames.

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Stopped cleanly (SIGINT/SIGTERM) or `--help`/`--version` |
| 1 | Could not listen, could not open the offline queue, could not load `--tls-cert`/`--tls-key`, or the server failed |
| 2 | Usage error, including a non-loopback `--listen` without `--allow-non-loopback` or without TLS, `--tls-cert` without `--tls-key`, more than one of the TLS options, a TLS option without `--public-origin`, a malformed `--public-origin`, or a non-positive `--queue-ttl` |

The relay has no `--json` output.

## Daemon side

`agentnetd` connects to a relay when given its URL:

| Setting | Meaning |
|---------|---------|
| `--relay URL` | e.g. `ws://127.0.0.1:8787` or `wss://relay.example.com`. A URL without a path gets `/v1/connect`. `ws://` is refused for a host that is not loopback |
| `DORYLINAE_RELAY_URL` | Same, used when `--relay` is not given |
| `--relay-ca FILE` | PEM CA for a `wss://` relay with a private or self-signed certificate ([agentnetd.md](agentnetd.md)) |

To make an installed service use a relay, pass `--relay` to `agentnetd install`
([agentnetd-install.md](agentnetd-install.md)). All daemon flags: [agentnetd.md](agentnetd.md).

With neither set, the daemon runs without a relay. The daemon keeps one
persistent connection and reconnects with backoff (500 ms doubling to 30 s)
when it drops. An invalid URL (not `ws://` or `wss://`), or `ws://` to a host
that is not loopback, stops the daemon at start with an error. The daemon signs
the relay's challenge with its identity key (relay auth v2, naming the relay's
origin; v1 only to a loopback relay that does not offer v2); the key is read from
the keystore only for each signature. It never follows a redirect from the relay
URL.

## Try it

```
relay --listen 127.0.0.1:8787
agentnetd --relay ws://127.0.0.1:8787
```
