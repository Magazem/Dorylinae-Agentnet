# Hosted relay: TLS, abuse limits, quotas, operations

Status: **approved by the owner 2026-09-27 (D36 in HANDOFF)** (Phase 4 spec, tickets 4.0a–4.0d
and 4.1a–4.1c in [../review/49-phase4-tickets.md](../review/49-phase4-tickets.md); adversarially
reviewed in [50-phase4-spec-review.md](../review/50-phase4-spec-review.md)). OD-P4-n choices for
this document are recorded in the ticket plan, not open, except OD-P4-20 (outside review
timing), decided during wave 1.

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
- **Public relay** (review 50 H1). A relay is *public* if it listens on a non-loopback
  address **or** is started with any of `--tls-cert`, `--acme-domain`, `--behind-proxy`, or a
  `--public-origin` whose host is not loopback. A reverse proxy on the same host (nginx, Caddy)
  usually forwards to `127.0.0.1`, so the listen address alone says nothing about who can reach
  the relay. **Every default that today depends on "loopback listen" depends on "public"
  instead:** auth v2 required, pairing v1 off (`cmd/relay` derives `--allow-pairing-v1` from
  the listen address today, `cmd/relay/main.go:79-84`), the per-prefix limits on, accounts
  frames advertised only as configured. `relay` prints `public: yes|no` at start.
- TLS 1.2 minimum (1.3 preferred), Go's default cipher suites, HTTP/1.1 only (WebSocket).
- `http.Server` timeouts: `ReadHeaderTimeout` 10 s (already set in `cmd/relay/main.go:121`),
  plus new `IdleTimeout` 60 s and `MaxHeaderBytes` 8 KiB. `ReadHeaderTimeout` does not cover a
  hijacked WebSocket; the challenge TTL (10 s) and the per-prefix unauthenticated cap bound a
  slow client there.
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
- **Private CAs for self-hosters** (review 50 M12): `agentnetd install --relay-ca FILE`
  (stored as `relay_ca` in the daemon config, PEM, added to the system roots for the relay
  connection only). Go ignores `SSL_CERT_FILE` on Windows and macOS, so without this flag a
  self-signed or LAN-CA relay cannot be used at all except through the insecure escape hatch.
  `doctor` shows which roots are in use. ACME stays the recommended path.
- **Relay-supplied strings are untrusted** (review 50 H3). Every string the relay sends
  (`message`, account `display`, `user_code`, `url`, `month`) is printed by the CLI only after
  replacing control, bidi and zero-width characters (the debate-constraint rule), and
  truncated to 200 characters.

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
  `--acme-domain`/`--behind-proxy`/`--tls-cert`, defaults to the listen address on loopback)
  and accepts the v2 signature for any of them. Listing an origin the relay does not
  exclusively serve (a platform's shared default hostname is fine, a hostname shared with
  another relay is not) defeats the binding.
- **Origin form** (review 50 L19): the host is the URL's host as configured, lower-cased;
  an IDN host in its ASCII (punycode) form; no trailing dot; IPv6 literals in brackets; the
  port omitted exactly when it is the scheme default; no path, query or user info. The
  daemon signs the origin of the **configured** URL. The WebSocket dial must not follow
  HTTP redirects (a redirect is a connection error), so the dialled and the configured origin
  are always the same. The vector set includes an upper-case host, an explicit `:443` and an
  IPv6 literal.
- `challenge` gains `"auth":["v1","v2"]`; `auth` gains `"v":2`.
- **Daemon rule, no downgrade** (review 50 H1): for a relay URL whose host is **not**
  loopback, the daemon signs **only v2**, whatever the challenge offers (a missing `auth`
  list, or `["v1"]`, is a connection error "relay does not support auth v2", shown by
  `status` and `doctor`). v1 is signed only for loopback URLs. Otherwise a hostile relay X
  would strip `v2` from the challenge it forwards and obtain a v1 signature usable at Y.
- **Relay rule:** a **public** relay (above) requires v2; `--allow-auth-v1` re-enables v1 for
  a migration window and logs a warning at start. There is no `--require-auth-v2` flag (the
  requirement follows from being public, so a same-host proxy cannot turn it off by
  accident). A v1 `auth` from a key with a valid v1 signature gets `auth_failed` with the
  message "this relay requires relay auth v2; update agentnet", and does **not** count
  towards the per-prefix failed-authentication limit (an old daemon in an office would
  otherwise lock out its whole prefix every 10 minutes).
- **Replay and freshness:** the nonce is 32 random bytes per connection and is valid only
  on that connection for the challenge TTL (10 s, unchanged); no nonce is accepted twice
  because none is accepted outside its own connection. Channel binding to the TLS session
  (RFC 9266 exporter) is **not** used: with `--behind-proxy` the relay does not see the
  daemon's TLS session. Origin binding stops the relay-in-the-middle; an attacker who can
  obtain a certificate for Y's name is Y as far as metadata goes (OD-P4-4).
- **Migration of old daemons:** Phase 0–3 daemons only speak v1. They keep working against
  loopback and non-public relays and against a public relay started with `--allow-auth-v1`;
  the hosted relay does not set it (every beta tester installs a Phase 4 build). Self-hosters
  upgrade the daemons first, then the relay.
- **Residual:** two different relays reachable under the same origin string (two LANs that
  both use `wss://192.168.1.10:8443`) are not told apart. The self-hosting guide recommends a
  DNS name.
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
| Failed authentications per prefix | 10 / 10 min | 429 for the rest of the window (the key is not blocked: keys are free). A valid v1 signature on a v2-only relay does not count |
| Unauthenticated connections relay-wide | 256 | HTTP 503 |
| Authenticated connections relay-wide | 5000 (`--max-conns`) | `error` `relay_full`, close 1013 |
| Distinct authenticated keys per prefix (review 50 M1) | 64 concurrent | `error` `relay_full`, close 1013 |
| Non-ephemeral envelopes sent, all keys of a prefix together (review 50 M1) | 600 / min | `rate_limited` |
| Bytes sent, all keys of a prefix together (review 50 M1) | 64 MiB / min | `rate_limited` |

### Per authenticated key

| Limit | Default | On excess |
|---|---|---|
| Envelopes sent (non-ephemeral) | 120 / min, burst 240 | `error` `rate_limited` (ref = envelope id), envelope dropped, connection stays open |
| Bytes sent (non-ephemeral) | 32 MiB / min | same |
| Ephemeral (presence) | 600 / min (exists) | dropped silently (exists) |
| Control frames (`ack` excluded) | 60 / min | `rate_limited`; 3 windows in a row → close 1008 |
| Reconnects | 20 / min | `error` `rate_limited` right after `auth`, close 1013 (the key is only known after the upgrade, so this cannot be an HTTP 429; review 50 L2) |
| Frames waiting in the connection's outbound buffer (review 50 M2) | 4 MiB (in addition to the existing 64 frames) | the envelope takes the queue path (`directBusy`), as for a full buffer today |

**Memory bound (review 50 M2).** Today a connection's outbound buffer holds up to 64 frames
of up to 1 MiB each (`internal/relay/relay.go:27`, `envelope.MaxFrameBytes`), so a reader that
stops reading pins 64 MiB until the 10 s write timeout; a few dozen such keys exhaust a
256–512 MB host. The byte cap above plus a relay-wide in-flight budget (`--max-inflight`,
default 256 MiB; past it direct sends take the queue path) bound memory. The 4.0b load test
sends **maximum-size** frames to non-reading recipients, not only idle connections.

### Offline queue (M2)

The existing per-recipient caps (1000 envelopes, 32 MiB) stay. New:

| Limit | Default | On excess |
|---|---|---|
| Per **sender → recipient** | 300 envelopes, 8 MiB | `queue_full` to the sender (so one stranger cannot fill a victim's queue) |
| Per sender, all recipients | 2000 envelopes, 64 MiB | `queue_full` |
| Relay-wide queue | 4 GiB of frames (`--queue-max-total`) | `queue_full`; `/healthz` stays 200 but the monitor alert [fires](#5-monitoring-and-operations-tickets-41a-41b) at 80 % |
| Free disk under the queue file | 1 GiB | new envelopes get `internal` ("relay storage low"); acks and deletes still work |

A sender **key** can fill its own share only. With accounts (4.2) only **bound keys of invited
accounts** can queue at all ([accounts.md](accounts.md#who-may-send-to-whom)), so an anonymous
stranger cannot, and a misbehaving member is identifiable and bounded by the per-pair cap
(at most 4 keys per account, so 1200 envelopes per account and victim; the per-recipient cap
of 1000 still wins).

**Without accounts the per-pair cap does not close M2** (review 50 M1). Keys are free: four
fresh keys fill a victim's 1000-envelope queue, and ~64 keys at 64 MiB each fill the relay-wide
4 GiB cap. The per-prefix send rows above slow this to one prefix filling the relay-wide cap in
about an hour; many prefixes do it faster. The attacker must know the victim's public key
(learnt by pairing, a team, or a shared card). The effect is delay, not loss: the sender's
outbox keeps resending on `queue_full` (`internal/mail/outbox.go:497`) until the mail expires.
What to do on self-hosted relays without accounts is **OD-P4-21**.

**Implementation note (review 50 M3).** The new caps must not scan the queue per envelope. R1
adds the index `queue_by_sender (from_key, enqueued)`; the per-sender and relay-wide totals are
kept in memory, rebuilt by one scan at open and adjusted on add, ack and sweep.

### Pairing (review 08b L1 and L5)

- **L1:** `Options.DisablePairingV1` becomes `Options.AllowPairingV1` (zero value = **off**).
  `cmd/relay` sets it from `--allow-pairing-v1`; its default is on only for a relay that is
  not **public** ([above](#relay)), no longer "listen address is loopback".
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
- On a relay **without accounts**, a stranger who knows a victim's key and uses many keys can
  fill the victim's queue, and many keys from several prefixes can fill the relay-wide queue
  (above, OD-P4-21).
- One misbehaving host behind a shared NAT (an office, a carrier-grade NAT) can trigger the
  per-prefix limits for everyone behind it.
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
existing `queue` table as is (a relay started on an old file keeps its queue) and adds the
index `queue_by_sender (from_key, enqueued)` that the 4.0b caps need (review 50 M3).

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
- **On the host** (review 50 L9): the unencrypted copy is written with mode 0600 in the
  database's directory, encrypted, then deleted in the same job (a failed job deletes it
  too); the upload credentials may **create** objects but not delete or overwrite them
  (bucket versioning or object lock; expiry by a lifecycle rule), so a host compromise cannot
  destroy the backup history.

### Restore

- `relay restore --from FILE --db PATH` refuses to overwrite a non-empty database without
  `--force`, checks `PRAGMA integrity_check` and the migration table, then starts normally.
- What a restore loses: envelopes queued and acks received after the backup. Acked-but-restored
  envelopes are **delivered again**; daemons dedupe `mail` persistently ([mail.md](mail.md)).
  The relay client's seen-set is in memory and bounded (`internal/relayclient/seen.go`), so
  it does **not** stop old non-mail envelopes after a daemon restart (review 50 L11); they are
  harmless for their own reasons: a replayed `pair.confirm` matches no pending attempt, and an
  old `session.*` frame fails its session's Noise decryption. Envelopes queued after the backup
  are lost: their senders' outboxes resend (mail keeps resending until the app ack,
  [mail.md](mail.md)), so **mail is not lost**, only delayed; ephemeral presence was never
  stored.
- **Security state goes back in time too** (review 50 M6). A restore brings back every
  binding, account, suspension and invite as of the backup: a key unbound after it (a stolen
  laptop) is bound again, a deleted account (an erasure request) exists again, a suspended
  account is active, and a beta invite redeemed after it is open again. So the relay writes
  every security-relevant change (`unbind`, `account_delete`, `suspend`/`unsuspend`,
  `invite_redeem`/`invite_revoke`, `team_remove`) as one content-free line to an append-only
  **security journal** kept **off the host** (the log sink, same 14-day retention as logs;
  account ids and key prefixes, no emails). `relay restore --replay-journal FILE` re-applies
  every journal entry newer than the backup before the relay accepts connections; the restore
  drill checks that an unbind and an invite redemption made after the backup survive the
  restore.
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
| (b) | **Mail volume**: non-ephemeral envelopes routed per quota group per month, cap = 300 × 100 = **30 000** envelopes and 3 GiB | ~300 request round trips including acks, key rotations and session mail | Tracks real relay cost; seen by the relay already (D7) | "Session" becomes an approximation users can't see directly |
| (c) | **Daemon-reported** work sessions, sent in the telemetry report | Exactly the plan's words | Precise | Trusts the client (a modified daemon under-reports), and needs per-kind telemetry, which the user can switch off |

**Recommendation: (b)** with a **soft cap** during the private beta: at 80 % the quota group's
bound accounts get a daily `quota_warning` control frame (the daemon shows it in `status`,
`doctor` and a desktop notification once a day); at 100 % the operator is alerted and the quota
group is **not** cut off in wave 1 (cost at 10 teams is negligible, and a hard stop would destroy the
very usage the beta measures). A hard cap (`quota_exceeded`: new mail refused, acks, pairing and
presence still work) is implemented behind `--quota-mode hard` and switched on only by owner
decision. The 7-day queue TTL is the existing `--queue-ttl 168h`.

Counting is **per quota group** ([invites.md](invites.md#quota-groups)): each envelope is
charged to the **sender's** account's quota group. Counters are monthly (UTC calendar month),
stored as `quota_usage(group_id, month, envelopes, bytes)`, and reset by month key, not by a job.

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
| Team membership, approximately | Presence fan-out + quota-group membership | Existing (presence.md) plus quota groups (new) |
| When a daemon is online, including when it is invisible to peers | Connections | Existing |
| That a pairing happened between two keys, and when | `pair_*` frames and `pair.confirm` | Existing; never the code secret |
| Client IP addresses | TCP / proxy header | Needed for limits; kept in memory for limits, and in logs for at most 14 days |
| Per-team counters, and per-kind counts **only** if the member's daemon sends the weekly report (default per OD-P4-8) | [telemetry.md](telemetry.md) | Never titles, briefs, results, file names, debate text. The relay sees which key sent each report while it adds it to the totals; a team with one active member has per-person counters |
| Feedback notes | [feedback.md](feedback.md) | Sealed to the operator's offline key; the relay host cannot read them |

It does **not** learn: message kinds, request titles, briefs, results, grants, fetched files,
debate content, Decisions, approval codes, pairing secrets, team names or rosters.

## Error codes added

| Code | Meaning |
|---|---|
| `rate_limited` | A per-key rate limit refused this envelope or control frame |
| `relay_full` | The relay is at its connection cap; retry later (the daemon's normal backoff) |
| `account_required` | The relay requires a bound account for this operation ([accounts.md](accounts.md)) |
| `quota_exceeded` | Hard mode only: new mail refused this month |

New control frame (not an error code; review 50 L3): `quota_warning`,
`{"op":"quota_warning","used":…,"limit":…,"month":"2026-11"}`.

Daemons map these to `status`/`doctor` output. The outbox (`internal/mail/outbox.go`,
`HandleError`) moves a row back to `queued` with its backoff for `rate_limited`,
`quota_exceeded` and `account_required` (the recipient may bind later), as it does for
`queue_full` today; `relay_full` arrives before `ready` and is a connection failure (normal
reconnect backoff). Today an unknown code leaves the row `relayed` until its next attempt,
which also retries, only later (review 50 L8).

**Feature gating** (review 50 L7): the relay closes a connection that sends a control frame
it does not know (`internal/relay/pairing.go:117-133`, `relay.go:296-298`, "unexpected control frame"). A daemon
therefore sends a Phase 4 control frame (`bind_*`, `unbind`, `invite_redeem`,
`pair_admit`, `telemetry_report`, `feedback`) only when the relay's `ready` lists the feature
(`accounts`, `telemetry`, `feedback`).

## Acceptance (summary; per ticket in the plan)

- `relay --listen 0.0.0.0:8443 --allow-non-loopback` without TLS options exits 2.
- A daemon configured with `ws://relay.example.com` refuses to start; `wss://` works against a
  test TLS server with a test root.
- A relay-in-the-middle test: relay X forwards relay Y's challenge; Y (v2 required) refuses the
  signature made for X's origin. A downgrade test: X forwards the challenge with `auth` removed
  or set to `["v1"]`; the daemon (non-loopback URL) signs nothing.
- A relay listening on `127.0.0.1` with `--behind-proxy` (or `--public-origin
  wss://relay.example`) is public: it refuses v1 auth and v1 pairing by default.
- A recipient that stops reading, sent 1 MiB frames by several keys, holds at most 4 MiB in its
  buffer; the relay stays under `--max-inflight`.
- A restore followed by `--replay-journal` keeps an unbind and an invite redemption made after
  the backup.
- Each limit table row has a test that triggers it and checks the error code and that other
  keys / prefixes are unaffected.
- One stranger key cannot queue more than 300 envelopes for a victim; the victim's own
  teammates can still queue.
- Behind a proxy: a spoofed `Fly-Client-IP` from an untrusted peer is ignored.
- Backup, restore, and the restore-drill test; kill-and-restart of the binary loses no queued
  envelope.
