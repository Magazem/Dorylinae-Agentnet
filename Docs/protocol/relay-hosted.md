# Hosted relay: TLS, abuse limits, quotas, operations

Status: **draft** (Phase 4 spec, tickets 4.0a–4.0d and 4.1a–4.1c in
[../review/49-phase4-tickets.md](../review/49-phase4-tickets.md)). Not approved; no code
starts before the owner approves (HANDOFF rule 3). Open choices are marked **OD-P4-n** and
listed in the ticket plan.

This document changes the relay of [envelope.md](envelope.md) so that it can run on a public
address for invited beta teams. It covers two things:

- **The beta gate (D17).** Review-05 M2 (TLS and abuse limits) and review-08b L1/L5 (pairing
  limits) must be closed **before any non-loopback relay is offered to anyone**, hosted by us
  or self-hosted by a tester. These are tickets 4.0a–4.0d and they block 4.1.
- **Hosting** (4.1): persistence, backup and restore, health, monitoring and the team quota.
  Accounts, invites, telemetry and feedback have their own documents:
  [accounts.md](accounts.md), [invites.md](invites.md), [telemetry.md](telemetry.md),
  [feedback.md](feedback.md).

Nothing here changes what the relay can read. It stays **untrusted whoever runs it** (D17):
payloads are sealed mail or Noise ciphertext, and pairing v2 is safe against a hostile relay
([pairing.md](pairing.md#threat-model)).

## Threat model for a public relay

| Actor | Can | Must not be able to |
|---|---|---|
| Anyone on the internet | Open TCP/TLS connections, run the WebSocket handshake, create unlimited Ed25519 keys | Fill a victim's offline queue, exhaust memory, disk or file descriptors, lock out pairing for everyone, or use the relay as a free anonymous message bus (after 4.2) |
| A bound beta account | Everything a member may do, and misbehave within its own quota | Exceed its own or its team's quota, deny service to other teams, read other accounts' metadata |
| A network attacker between a daemon and the relay | Observe and modify traffic | Read routing metadata (`from`, `to`, sizes, timing inside the TLS stream), replay or forward a relay authentication to another relay |
| A malicious relay (or a relay host such as Fly.io / Hetzner staff) | Everything in [pairing.md](pairing.md#threat-model): read, drop, delay, inject frames; see metadata | Read content, forge mail, complete a pairing MITM. It **can** deny service and it **does** learn the metadata listed in [What the hosted relay learns](#what-the-hosted-relay-learns) |

## 1. Transport security (ticket 4.0a)

### Relay

- New flags: `--tls-cert FILE --tls-key FILE` (PEM), or `--acme-domain NAME
  [--acme-cache DIR] [--acme-email ADDR]` (Let's Encrypt via `golang.org/x/crypto/acme/autocert`,
  TLS-ALPN-01 on the listen port), or `--behind-proxy` (TLS terminated by a reverse proxy or
  the platform, see [Client IP](#client-ip-behind-a-proxy)). **OD-P4-3** chooses which of these
  the hosted relay uses; all three are specified so self-hosters can pick.
- `--allow-non-loopback` **alone is refused** (exit 2, "a non-loopback relay needs TLS:
  --tls-cert/--tls-key, --acme-domain or --behind-proxy"). This closes M2's "plaintext `ws://`
  over the network". Loopback listening keeps plain `ws://`.
- TLS 1.2 minimum (1.3 preferred), Go's default cipher suites, HTTP/1.1 only (WebSocket).
- `http.Server` timeouts: `ReadHeaderTimeout` 10 s, `IdleTimeout` 60 s, `MaxHeaderBytes`
  8 KiB. (Today only the handler exists; a slowloris client can hold connections open.)
- New endpoint `GET /healthz`: `200 {"ok":true,"version":"…"}` when the queue database
  answers a `SELECT 1` within 1 s, else `503`. No counts, no keys. Rate limited per IP like
  connections.

### Daemon

- `agentnetd` refuses a relay URL with scheme `ws://` whose host is not loopback (`127.0.0.0/8`,
  `::1`, `localhost`) at start and at `install` time: "a remote relay must use wss://". An
  escape hatch for LAN tests is `DORYLINAE_ALLOW_INSECURE_RELAY=1`, printed as a warning at
  every start and shown by `agentnet status` and `doctor`.
- `wss://` uses the system roots (Go default). No pinning (OD-P4-4 discusses it): TLS here
  protects metadata and the authentication binding below; content never depends on it.

### Relay authentication v2 (binds the relay's name)

Today the daemon signs `"dorylinae-relay-auth-v1\n" ‖ nonce`. The signature does not name the
relay, so a hostile relay X that a daemon connects to can **forward** the challenge of relay Y,
get it signed and log into Y as that daemon (a relay-in-the-middle). On a single local relay
this is harmless; with accounts it lets X spend the victim's quota on Y and, worse, **ack and
so delete the victim's queued mail on Y** (acks are honoured for the authenticated key).

v2 signature input:

```
"dorylinae-relay-auth-v2\n" ‖ nonce(32) ‖ u16be(len(origin)) ‖ origin
origin = lowercase(scheme "://" host [":" port])   of the URL the daemon dialled,
         default ports omitted (wss → 443, ws → 80)
```

- The relay knows its own origin(s) from `--public-origin URL` (repeatable; required with
  `--acme-domain`/`--behind-proxy`, defaults to the listen address on loopback) and accepts
  the v2 signature for any of them.
- `challenge` gains `"auth":["v1","v2"]`; `auth` gains `"v":2`. A v2 daemon always sends v2
  when offered. A relay started with `--require-auth-v2` (the default for any non-loopback
  listen) refuses v1 with `auth_failed`.
- Test vector in `tools/specvectors` (key, nonce, origin → signature), rechecked by
  `tools/verifyvectors`.

## 2. Abuse limits (ticket 4.0b)

All limits are **relay options with the defaults below**, logged when they trigger
(`event=limit`, with the limit name and a truncated key or a /24 (/48 for IPv6) prefix,
never a payload). Every refusal is an existing or new `error` code, so daemons can surface it.

### Per network source (before authentication)

The source is the client IP, grouped to a /24 (IPv4) or /48 (IPv6) prefix ("prefix").

| Limit | Default | On excess |
|---|---|---|
| New WebSocket upgrades per prefix | 30 / min, burst 60 | HTTP 429 before the upgrade |
| Concurrent connections per prefix | 64 | HTTP 429 |
| Failed authentications per prefix | 10 / 10 min | 429 for the rest of the window (the key is not blocked: keys are free) |
| Unauthenticated connections relay-wide | 256 | HTTP 503 |
| Authenticated connections relay-wide | 5000 (`--max-conns`) | `error` `relay_full`, close 1013 |

### Per authenticated key

| Limit | Default | On excess |
|---|---|---|
| Envelopes sent (non-ephemeral) | 120 / min, burst 240 | `error` `rate_limited` (ref = envelope id), envelope dropped, connection stays open |
| Bytes sent (non-ephemeral) | 32 MiB / min | same |
| Ephemeral (presence) | 600 / min (exists) | dropped silently (exists) |
| Control frames (`ack` excluded) | 60 / min | `rate_limited`; 3 windows in a row → close 1008 |
| Reconnects | 20 / min | HTTP 429 |

### Offline queue (M2)

The existing per-recipient caps (1000 envelopes, 32 MiB) stay. New:

| Limit | Default | On excess |
|---|---|---|
| Per **sender → recipient** | 300 envelopes, 8 MiB | `queue_full` to the sender (so one stranger cannot fill a victim's queue) |
| Per sender, all recipients | 2000 envelopes, 64 MiB | `queue_full` |
| Relay-wide queue | 4 GiB of frames (`--queue-max-total`) | `queue_full`; `/healthz` stays 200 but the monitor alert [fires](#5-monitoring-and-operations-tickets-41a-41b) at 80 % |
| Free disk under the queue file | 1 GiB | new envelopes get `internal` ("relay storage low"); acks and deletes still work |

A sender can fill **its own** share only. With accounts (4.2) only **bound keys of invited
accounts** can queue at all ([accounts.md](accounts.md#who-may-send-to-whom)), so an anonymous
stranger cannot, and a misbehaving member is identifiable and bounded by the per-pair cap. On
self-hosted relays without accounts the per-pair cap is the main protection.

### Pairing (review 08b L1 and L5)

- **L1:** `Options.DisablePairingV1` becomes `Options.AllowPairingV1` (zero value = **off**).
  `cmd/relay` sets it from `--allow-pairing-v1` exactly as today.
- **L5:** pairing limits get a per-prefix layer on top of the per-key one: `pair_new` 20 / 10
  min per prefix, failed `pair_redeem` 10 / min per prefix, and at most 50 outstanding codes
  per prefix. With accounts: 30 `pair_new` per account per day and 20 outstanding codes per
  account; `pair_new`/`pair_redeem` from an unbound key is refused (`account_required`) on a
  relay that requires accounts. `PairMaxCodes` (10000) stays as the global backstop.
- Pairing security itself does not change: guessing a lookup gains nothing (the MAC needs the
  50-bit secret); these limits are about **denial of service**.

### Client IP behind a proxy

With `--behind-proxy` the relay takes the client IP from one header named by
`--client-ip-header` (e.g. `Fly-Client-IP`, or `X-Forwarded-For`, using the **last** hop added
by the trusted proxy) and **only** if the TCP peer is in `--trusted-proxy CIDR` (repeatable).
From any other peer the header is ignored and the TCP address is used. Without a trusted
header every client looks like the proxy, and per-prefix limits would lock everyone out
together: `--behind-proxy` without `--client-ip-header` is a usage error.

### What the limits do not stop

- A botnet with many prefixes can still open connections up to the global caps (DoS). The
  beta accepts this; the uptime monitor tells us.
- A bound account can spend its own team's quota (its teammates' problem, visible in the
  per-team counters).

## 3. Persistence, backup and restore (tickets 4.1a, 4.1b)

### One relay database

The hosted relay keeps everything in one SQLite file (`--db PATH`, replacing `--queue-db`,
which stays as an alias): the existing `queue` table plus the account, invite, quota,
telemetry and feedback tables of the other Phase 4 documents. WAL mode, `synchronous=FULL`
(as today), `secure_delete=ON`.

**Relay migrations.** The relay database gets its own `relay_migrations` table and numbering,
**R1…**, independent of the daemon's 1…21 (22 is the next daemon number). R1 adopts the
existing `queue` table as is (a relay started on an old file keeps its queue).

### Backup

- **Daily** at a fixed UTC hour, and before every deploy: an online copy with SQLite's backup
  API (`VACUUM INTO` a temp file is also acceptable), then encrypted (with
  [age](https://age-encryption.org) or the repo's HPKE code, OD-P4-11) to an **offline operator key** (public key in the relay
  config, private key kept by the owner, not on the host), then uploaded to object storage in a
  different provider or region from the relay (OD-P4-1). Retention: 14 daily copies.
- What a backup holds: queued **ciphertext** frames, account emails / GitHub ids, key
  bindings, invite and quota state, telemetry counters, sealed feedback. That is personal data
  (accounts): the encryption and the 14-day retention are what the privacy note promises.
- `relay backup --out FILE` (a subcommand) does the online copy, for manual use and tests.

### Restore

- `relay restore --from FILE --db PATH` refuses to overwrite a non-empty database without
  `--force`, checks `PRAGMA integrity_check` and the migration table, then starts normally.
- What a restore loses: envelopes queued and acks received after the backup. Acked-but-restored
  envelopes are **delivered again**; daemons dedupe `mail` persistently ([mail.md](mail.md))
  and other types through the seen-set, so a restore causes duplicates at the relay layer but
  not in inboxes. Envelopes queued after the backup are lost: their senders' outboxes resend
  (mail keeps resending until the app ack, [mail.md](mail.md)), so **mail is not lost**, only
  delayed; ephemeral presence was never stored.
- **Restore drill:** a Go test restores a backup taken mid-traffic and checks that every
  unacked mail still reaches its recipient exactly once in the inbox. The owner runs one manual
  drill on the real host before wave 1 (checklist in `tests/phase4-manual.md`).

### Restart without loss

Plan 4.1's acceptance ("relay restarts without losing queued envelopes") already holds for
the queue with a file database (0.7, review-05 H2 fixed). 4.1 adds: graceful shutdown on
SIGTERM waits up to 10 s for writes, and `Close` waits for connection goroutines (review 05 L6)
before closing the database; a test kills and restarts the `relay` binary mid-traffic.

## 4. Quotas (ticket 4.1c)

The plan's free-tier cap is **300 relay sessions per team per month, with a 7-day offline
queue**. The relay cannot see requests, work sessions or teams (envelopes carry `team: ""`,
payloads are sealed). It must count something it can see. **OD-P4-5** chooses the unit:

| Option | Unit | 300 means | Pros | Cons |
|---|---|---|---|---|
| (a) | **Device-day**: a bound key that authenticates at least once in a UTC day | ~5 people × 2 devices × 30 days | Needs no new data; trivially explainable | Not about usage at all; an always-on daemon uses its day even when idle |
| (b) | **Mail volume**: non-ephemeral envelopes routed per billing team per month, cap = 300 × 100 = **30 000** envelopes and 3 GiB | ~300 request round trips including acks, key rotations and session mail | Tracks real relay cost; seen by the relay already (D7) | "Session" becomes an approximation users can't see directly |
| (c) | **Daemon-reported** work sessions, sent in the telemetry report | Exactly the plan's words | Precise | Trusts the client (a modified daemon under-reports), and needs per-kind telemetry, which is opt-out-able |

**Recommendation: (b)** with a **soft cap** during the private beta: at 80 % the team's
bound accounts get a daily `quota_warning` control frame (the daemon shows it in `status`,
`doctor` and a desktop notification once a day); at 100 % the operator is alerted and the team
is **not** cut off in wave 1 (cost at 10 teams is negligible, and a hard stop would destroy the
very usage the beta measures). A hard cap (`quota_exceeded`: new mail refused, acks, pairing and
presence still work) is implemented behind `--quota-mode hard` and switched on only by owner
decision. The 7-day queue TTL is the existing `--queue-ttl 168h`.

Counting is **per billing team** ([invites.md](invites.md#billing-teams)): each envelope is
charged to the **sender's** account's billing team. Counters are monthly (UTC calendar month),
stored as `quota_usage(team_id, month, envelopes, bytes)`, and reset by month key, not by a job.

## 5. Monitoring and operations (tickets 4.1a, 4.1b)

- **Uptime monitor** (external, e.g. a free-tier HTTP checker) on `/healthz` every minute,
  alert to the owner after 3 failures.
- **Operator metrics** on a separate listener bound to `127.0.0.1` (or a private network) with
  `--metrics-listen`: Prometheus text of connections, envelopes routed / queued / expired /
  refused by limit, queue rows and bytes, DB size, free disk, backup age. **No per-key or
  per-account labels** (per-team numbers live in the telemetry tables, [telemetry.md](telemetry.md)).
- Alerts: backup older than 26 h; queue at 80 % of the relay-wide cap; free disk < 2 GiB;
  auth-failure rate spike; any team at 100 % quota.
- Deploy: one container image built by CI from a tag (the owner authorises tags); the image
  runs as a non-root user; the database on a persistent volume; `relay --version` printed at
  start. Rollback = redeploy the previous image; migrations are forward-only, so a release with a
  new relay migration takes a backup first (the deploy script does it).
- Logs: as today (no payloads, abbreviated keys); retention on the host at most 14 days.
  Account emails never appear in logs (account ids only).

## What the hosted relay learns

This replaces the per-feature "What the relay sees" sections for the hosted case; nothing in
it is new **except the account binding**, which is the one real privacy change of Phase 4.

| Learns | From | Notes |
|---|---|---|
| Who each key belongs to (email or GitHub login) | Account binding (4.2) | **New.** Before 4.2 keys were pseudonymous. The communication graph below becomes a graph of named people |
| Communication graph: which keys send mail / presence to which, when, how much | Routing (`from`, `to`, sizes, timing) | Existing; inherent to a relay |
| Team membership, approximately | Presence fan-out + billing-team membership | Existing (presence.md) plus billing teams (new) |
| When a daemon is online, including when it is invisible to peers | Connections | Existing |
| That a pairing happened between two keys, and when | `pair_*` frames and `pair.confirm` | Existing; never the code secret |
| Client IP addresses | TCP / proxy header | Needed for limits; kept in memory for limits, and in logs for at most 14 days |
| Per-team counters, and per-kind counts **only** if the member's daemon sends the opt-out-able report | [telemetry.md](telemetry.md) | Never titles, briefs, results, file names, debate text |
| Feedback notes | [feedback.md](feedback.md) | Sealed to the operator's offline key; the relay host cannot read them |

It does **not** learn: message kinds, request titles, briefs, results, grants, fetched files,
debate content, Decisions, approval codes, pairing secrets, team names or rosters.

## Error codes added

| Code | Meaning |
|---|---|
| `rate_limited` | A per-key rate limit refused this envelope or control frame |
| `relay_full` | The relay is at its connection cap; retry later (the daemon's normal backoff) |
| `account_required` | The relay requires a bound account for this operation ([accounts.md](accounts.md)) |
| `quota_warning` | Informational control frame, `{"op":"quota_warning","used":…,"limit":…,"month":"2026-11"}` |
| `quota_exceeded` | Hard mode only: new mail refused this month |

Daemons map these to `status`/`doctor` output; the outbox treats `rate_limited` and
`relay_full` like a disconnect (retry with backoff), and `quota_exceeded` like `queue_full`.

## Acceptance (summary; per ticket in the plan)

- `relay --listen 0.0.0.0:8443 --allow-non-loopback` without TLS options exits 2.
- A daemon configured with `ws://relay.example.com` refuses to start; `wss://` works against a
  test TLS server with a test root.
- A relay-in-the-middle test: relay X forwards relay Y's challenge; Y (v2 required) refuses the
  signature made for X's origin.
- Each limit table row has a test that triggers it and checks the error code and that other
  keys / prefixes are unaffected.
- One stranger key cannot queue more than 300 envelopes for a victim; the victim's own
  teammates can still queue.
- Behind a proxy: a spoofed `Fly-Client-IP` from an untrusted peer is ignored.
- Backup, restore, and the restore-drill test; kill-and-restart of the binary loses no queued
  envelope.
