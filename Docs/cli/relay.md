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
      [--tls-cert FILE --tls-key FILE | --acme-domain NAME [--acme-cache DIR] [--acme-email ADDR]
       | --behind-proxy --client-ip-header NAME --trusted-proxy CIDR...]
      [--public-origin URL]... [--allow-auth-v1]
      [--queue-db PATH] [--queue-ttl DURATION] [--allow-pairing-v1[=false]]
      [--min-client VERSION] [abuse limit flags, see below] [--verbose] [--version]
relay version [--json]
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--listen HOST:PORT` | `127.0.0.1:8787` | Address to listen on. Port `0` picks a free port |
| `--allow-non-loopback` | off | Permit `--listen` on a non-loopback address. Without it such an address is refused. **With it alone it is refused too** (exit 2, "a non-loopback relay needs TLS: --tls-cert/--tls-key, --acme-domain or --behind-proxy") |
| `--tls-cert FILE`, `--tls-key FILE` | | Serve `wss://` with this PEM certificate (chain) and key. TLS 1.2 minimum, HTTP/1.1 only |
| `--acme-domain NAME` | | Serve `wss://` with a Let's Encrypt certificate for `NAME` (TLS-ALPN-01 on the listen port, which the internet must reach on 443). `--acme-cache DIR` (default `relay-acme` next to the queue database) keeps the account and certificates; `--acme-email` is the optional contact address |
| `--behind-proxy` | off | TLS is terminated by a reverse proxy or the platform in front of the relay, which itself serves plain HTTP (usually on `127.0.0.1`). The relay is **public** anyway (see below). Needs `--client-ip-header` and `--trusted-proxy` (ticket 4.0b): without the client IP every client would share the proxy's address and the per-prefix limits would lock everyone out together |
| `--client-ip-header NAME` | | With `--behind-proxy` (required there, refused without it): the header in which the proxy passes the client IP, e.g. `Fly-Client-IP`, or `X-Forwarded-For`, of which the **last** entry (the hop the proxy added) is used. Honoured only when the TCP peer is a `--trusted-proxy`; from any other peer it is ignored and the TCP address is used. A missing or malformed header from a trusted proxy also falls back to the TCP address |
| `--trusted-proxy CIDR` | | A proxy address or range (`10.0.0.0/8`, or a single IP) whose `--client-ip-header` is believed (repeatable; at least one with `--client-ip-header`). List only the proxy's own addresses: a range that includes clients lets them choose their prefix |
| `--public-origin URL` | the loopback names of the listen port, on a relay that is not public | An origin daemons dial to reach this relay, e.g. `wss://relay.example.com` (repeatable). **Required** with `--tls-cert`, `--acme-domain` and `--behind-proxy`. Relay auth v2 signatures must name one of these ([../protocol/envelope.md](../protocol/envelope.md#auth-daemon---relay)). List only origins this relay serves exclusively: a hostname shared with another relay defeats the binding |
| `--allow-auth-v1` | off | Also accept relay auth v1 (Phase 0–3 daemons) on a public relay, for a migration window. Logs a warning at start. v1 does not name the relay, so a hostile relay can replay it here; upgrade the daemons, then drop the flag. A relay that is not public always accepts v1 |
| `--queue-db PATH` | `relay-queue.db` in the config dir (`$DORYLINAE_HOME`, else the OS user config dir + `dorylinae`) | SQLite file for envelopes queued for offline peers. Created, with its directory, if missing. If it cannot be opened the relay exits 1 with a message |
| `--queue-ttl DURATION` | `168h` (7 days) | How long an envelope waits for its recipient before it is dropped. Must be positive |
| `--allow-pairing-v1` | on for a relay that is not public, off for a public one | Accept the v1 pairing frames (`pair_new` without `lookup`, `pair_redeem` with `code`). With it off they get `pair_v1_disabled`. Pass `--allow-pairing-v1=false` to turn it off on a relay that is not public. v2 pairing (lookup only, entry kept until `pair_cancel`, expiry or 3 redemptions) is always on. See [../protocol/pairing.md](../protocol/pairing.md#v1-compatibility) |
| `--min-client VERSION` | none | The oldest daemon release (`MAJOR.MINOR.PATCH`) this relay supports, sent to every daemon as `ready.min_client` (ticket 4.4a). Advisory: no connection is refused; an older daemon logs a warning, `agentnet status` prints an `upgrade:` line and `agentnet doctor` fails its `binary` check. Anything but `MAJOR.MINOR.PATCH` is a usage error |
| Abuse limits | see below | Ticket 4.0b; every limit has a flag |
| `--verbose` | off | Also log every connect, disconnect and routed envelope (routing metadata only: abbreviated keys, `type`, `id`, byte counts) |
| `--version` | | Print the version and exit |
| `--help`, `-h` | | Print usage and exit |

Daemons connect to `ws://HOST:PORT/v1/connect`, or `wss://` with TLS. `GET /healthz`
answers `200 {"ok":true,"version":"…"}` while the queue database responds to `SELECT 1`
within a second, else `503`; it needs no authentication and shows no counts or keys. It is
rate limited per client prefix at the `--max-upgrades-per-min`/`--upgrade-burst` rate, in a
bucket of its own (HTTP 429).

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

### Abuse limits

Ticket 4.0b, [../protocol/relay-hosted.md §2](../protocol/relay-hosted.md#2-abuse-limits-ticket-40b).
They apply to every relay, public or not; the defaults are the spec's. Each value must be
positive (exit 2 otherwise). Sizes take a plain byte count or a `KiB`/`MiB`/`GiB` suffix
(`KB`/`MB`/`GB` mean the same binary units). A "prefix" is the client's /24 (IPv4) or /48
(IPv6), from the TCP peer or, behind a trusted proxy, from `--client-ip-header`.

| Flag | Default | Past it |
|------|---------|---------|
| `--max-upgrades-per-min N`, `--upgrade-burst N` | 30, 60 | New WebSocket upgrades per prefix: HTTP 429 before the upgrade |
| `--max-conns-per-prefix N` | 64 | Concurrent connections per prefix: HTTP 429 |
| `--max-auth-failures N` | 10 (per 10 min) | Failed authentications per prefix: HTTP 429 for the rest of the 10 minutes. The key is not blocked. A valid v1 signature refused by a v2-only relay, or a client that disconnects before answering, does not count |
| `--max-unauth-conns N` | 256 | Connections still authenticating, relay-wide: HTTP 503 |
| `--max-conns N` | 5000 | Authenticated connections, relay-wide: `error` `relay_full` right after `auth`, close 1013. A connected key reconnecting is always admitted (it replaces its old connection) |
| `--max-keys-per-prefix N` | 64 | Distinct keys connected from one prefix: `relay_full`, close 1013 |
| `--prefix-envelopes-per-min N` | 600 | Non-ephemeral envelopes from all keys of a prefix together: `rate_limited` |
| `--prefix-bytes-per-min SIZE` | 64MiB | Bytes of those envelopes: `rate_limited` |
| `--key-envelopes-per-min N`, `--key-envelope-burst N` | 120, 240 | Non-ephemeral envelopes from one key: `rate_limited` (`ref` = envelope id), envelope dropped, connection stays open |
| `--key-bytes-per-min SIZE` | 32MiB | Bytes of those envelopes: same |
| `--control-per-min N` | 60 | Control frames (`ack` excluded) from one key: `rate_limited`; over the limit in 3 one-minute windows in a row, close 1008 |
| `--reconnects-per-min N` | 20 | Authentications of one key: `rate_limited` right after `auth`, close 1013 |
| `--conn-buffer SIZE` | 4MiB | Bytes waiting in one connection's outbound buffer (on top of its 64 frames): further envelopes for it take the offline queue |
| `--max-inflight SIZE` | 256MiB | Bytes waiting in all outbound buffers together, queue batches being delivered included: further direct sends take the offline queue. Frames being read from daemons have a second budget of the same size: past it the reading connection is closed with 1013 (retry later) |
| `--queue-pair-max-envelopes N`, `--queue-pair-max-bytes SIZE` | 300, 8MiB | Envelopes one sender has queued for one recipient: `queue_full` |
| `--queue-sender-max-envelopes N`, `--queue-sender-max-bytes SIZE` | 2000, 64MiB | Envelopes one sender has queued for all recipients: `queue_full` |
| `--queue-max-total SIZE` | 4GiB | Bytes queued relay-wide: `queue_full` |
| `--queue-min-free-disk SIZE` | 1GiB | Free disk under the queue file: new envelopes get `internal` ("relay storage low"); acks and deletes still work |

The existing caps stay: 1000 envelopes / 32 MiB queued per recipient, and 600 ephemeral
(presence) envelopes per key per minute, dropped silently. Each refusal is logged once a
minute per limit and key or prefix as `event=limit limit=NAME peer=KEY8` or
`prefix=A.B.C.0` (warning level, so visible without `--verbose`), never with an id or
payload. What the limits do not stop (a botnet, many fresh keys on a relay without
accounts, a shared NAT) is in
[relay-hosted.md](../protocol/relay-hosted.md#what-the-limits-do-not-stop).

Behind Fly.io, for example:

```
relay --listen 127.0.0.1:8080 --behind-proxy --public-origin wss://relay.example.com \
      --client-ip-header Fly-Client-IP --trusted-proxy PROXY_CIDR
```

where `PROXY_CIDR` is the range the platform's proxy connects from (check the platform's
documentation; a range that also holds clients would let them pick their prefix).

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
| 2 | Usage error, including a non-loopback `--listen` without `--allow-non-loopback` or without TLS, `--tls-cert` without `--tls-key`, more than one of the TLS options, a TLS option without `--public-origin`, a malformed `--public-origin`, a non-positive `--queue-ttl`, `--behind-proxy` without `--client-ip-header`, `--client-ip-header` without `--behind-proxy` or without a `--trusted-proxy`, a malformed `--trusted-proxy`, a `--min-client` that is not `MAJOR.MINOR.PATCH`, or a limit flag that is not positive |

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
