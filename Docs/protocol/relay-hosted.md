# Hosted relay: TLS, abuse limits, quotas, operations

Status: **approved by the owner 2026-09-27 (D36 in HANDOFF)** (Phase 4 spec, tickets 4.0a–4.0d
and 4.1a–4.1c in [../review/49-phase4-tickets.md](../review/49-phase4-tickets.md); adversarially
reviewed in [50-phase4-spec-review.md](../review/50-phase4-spec-review.md)). OD-P4-n choices for
this document are recorded in the ticket plan, not open, except OD-P4-20 (outside review
timing), decided during wave 1.

**Pending change (R55-F1, 2026-09-29):** [§2 Memory budgets and fairness](#memory-budgets-and-fairness-r55-f1)
replaces the review-50 M2 memory-bound paragraph and reopens the R-4.0 H1 "close the reader"
choice. It is a proposal until the owner approves it after its adversarial review; open
decisions are [OD-R55F1-1…8](#open-decisions-r55-f1) at the end of this document. Summary and
acceptance tests: [../review/56-r55-f1-spec.md](../review/56-r55-f1-spec.md).

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
| Share of each memory budget held by one prefix (R55-F1) | 1/8 of the budget, see [below](#memory-budgets-and-fairness-r55-f1) | the prefix's own largest holder is evicted, or the frame takes today's fallback |

### Per authenticated key

| Limit | Default | On excess |
|---|---|---|
| Envelopes sent (non-ephemeral) | 120 / min, burst 240 | `error` `rate_limited` (ref = envelope id), envelope dropped, connection stays open |
| Bytes sent (from R55-F1: **every** frame read after `auth`, charged before it is parsed; R55-035) | 32 MiB / min | same; a frame refused here is dropped **unparsed**, and the error has an empty `ref` |
| Ephemeral (presence) | 600 / min (exists) | dropped silently (exists) |
| Ephemeral and control bytes waiting to be written (R55-F1) | the ephemeral budget, [below](#memory-budgets-and-fairness-r55-f1) | presence dropped silently; see below for control frames |
| One frame being read (R55-F1) | must finish within `--frame-read-timeout` (30 s) of its first byte | close 1013 "frame too slow; retry later" |
| Control frames (`ack` excluded) | 60 / min | `rate_limited`; 3 windows in a row → close 1008 |
| Reconnects | 20 / min | `error` `rate_limited` right after `auth`, close 1013 (the key is only known after the upgrade, so this cannot be an HTTP 429; review 50 L2) |
| Frames waiting in the connection's outbound buffer (review 50 M2) | 4 MiB (in addition to the existing 64 frames) | the envelope takes the queue path (`directBusy`), as for a full buffer today |

**Memory bound (review 50 M2).** Today a connection's outbound buffer holds up to 64 frames
of up to 1 MiB each (`internal/relay/relay.go:27`, `envelope.MaxFrameBytes`), so a reader that
stops reading pins 64 MiB until the 10 s write timeout; a few dozen such keys exhaust a
256–512 MB host. The byte cap above plus a relay-wide in-flight budget (`--max-inflight`,
default 256 MiB; past it direct sends take the queue path) bound memory. The 4.0b load test
sends **maximum-size** frames to non-reading recipients, not only idle connections.

### Memory budgets and fairness (R55-F1)

Review 55 found that the memory bound above is a bound, but not a fair one: whoever takes the
budget first keeps it. R55-001 (C01-02): about 50 unfinished frames from **one** /24 hold the
whole read budget indefinitely, and every other connection is closed 1013 on its next frame.
R55-002 (C01-01): presence to the attacker's own slow readers is charged to `--max-inflight`
but never refused, so all direct mail takes the queue path and every drain waits. This
section replaces "first come, first served" with four rules: a **deadline** per frame, a
**per-prefix share** of every budget, a **separate ephemeral budget**, and **eviction of the
heaviest holder** instead of refusing the newcomer. It also takes in R55-034 (frames pin up to
twice the bytes charged), R55-035 (invalid frames are parsed without being charged) and R55-144
(`release` not deferred).

#### Three budgets

Every byte the relay holds for a connection is charged to exactly one of three relay-wide
budgets, and to the connection and its prefix inside that budget:

| Budget | What is charged | Size | 4.1p |
|---|---|---|---|
| **Outbound** (`--max-inflight`, exists) | Non-ephemeral envelopes waiting in a connection's outbound buffer, and queue-drain reservations (`drainReserve`, R-4.0 H1) | 256 MiB | 48 MiB |
| **Read** (exists, same size as `--max-inflight`) | The buffer of the frame being read from each connection, by capacity as it grows (R-4.0 H1) | = `--max-inflight` | 48 MiB |
| **Ephemeral** (new, `--max-inflight-ephemeral`) | Presence envelopes and relay-generated control frames waiting in outbound buffers (`queued`, `error`, `pair_*`, `bind_*`, `account_changed`) | `max(--max-inflight / 8, 1 MiB)` | 6 MiB |

- **Charge what is pinned (R55-034).** A frame is charged by the memory it pins. `readFrame`
  may leave `cap(frame)` up to twice `len(frame)`; before a frame enters an outbound buffer
  the relay copies it into an exact-length slice when `cap > len`, so the outbound charge
  (`len`) is the real size. (Charging `cap` instead is acceptable if the implementation
  prefers it; either way the outbound budget must match the heap it stands for.)
- **Every charge is refused when it does not fit.** No outbound, ephemeral or read charge
  uses an unconditional add any more, with one exception: the `ready` frame, the first frame
  of every connection (≈ 200 bytes, at most one per connection, bounded by `--max-conns`), is
  charged to the ephemeral budget without a check. Frames written straight to the socket
  before a connection is served (auth errors, `relay_full`, `rate_limited` after `auth`) are
  not buffered and are not charged.
- **Every charge is released on every exit path (R55-144).** The connection's release runs
  in a `defer` placed right after admission, so a panic while routing cannot leak budget.
  A connection that is evicted is uncharged at the moment of eviction (below), and its
  later release must not uncharge the same bytes again.

#### Shares: per key and per prefix

Inside each budget no single key and no single prefix (a /24 or /48, as elsewhere in §2) can
hold more than its share:

| Budget | Per key (one connection per key) | Per prefix | 4.1p per prefix |
|---|---|---|---|
| Outbound | 4 MiB buffer (`--conn-buffer`, exists) + one drain reservation (2 MiB) | `share(B, 6 MiB)` | 6 MiB |
| Read | one frame, at most `MaxFrameBytes + 1` (exists: one frame at a time) | `share(B, 2 MiB)` | 6 MiB |
| Ephemeral | half the frame slots (32 of 64, exists) × 8 KiB, plus its own control replies | `share(B, 512 KiB)` | 768 KiB |

`share(B, floor) = min(B, max(B / 8, floor))`: an eighth of the budget, never less than the
floor (enough for one connection's normal use), never more than the budget. With a budget
smaller than the floor (tests) the share is the whole budget. A key's old connection that is
being replaced still counts until its charges are released.

#### When a charge does not fit: evict the heaviest holder

A charge of `n` bytes to budget X for connection R (the reader for the read budget, the
recipient whose buffer the frame goes into for the other two) is refused if R's prefix
would pass its share, or X would pass its size. Then, **once**, before the fallback:

1. **Choose the prefix that pays.** If R's own prefix would pass its share, it is R's prefix.
   Otherwise it is the prefix holding the most of X, ties broken by the prefix holding the
   oldest charge; but if that prefix holds less of X than R's prefix would after the charge
   (or the same, and its oldest charge is not older than R's), nobody is evicted: the
   requester's prefix is itself the heaviest, and it pays by the fallback.
2. **Choose the holder.** In the paying prefix, the connection H ≠ R holding the most of X,
   ties broken by the oldest charge, that is **eligible**:
   - read budget: H's current frame started before R's current frame;
   - outbound and ephemeral budgets: the oldest frame waiting in H's buffer (or H's drain
     reservation) is at least **2 s** old (`evictStale`): H is not keeping up. A reader that
     keeps up is never evicted, however much it holds.
   If no holder in the paying prefix is eligible, nobody is evicted.
3. **Evict H.** Uncharge everything H holds in all three budgets at once, mark H so no later
   path charges or uncharges it again, and close it: close 1013 "relay busy; retry later",
   written with at most a 1 s timeout, then drop the TCP connection without waiting for the
   peer's close reply. Log `event=limit limit=evict_read|evict_outbound|evict_ephemeral
   prefix=<paying prefix>` (the usual once-a-minute rule).
4. **Retry R's charge once.** If it still does not fit (another charge took the room), take
   the fallback. There is no loop and no waiting.

The **fallbacks** are today's behaviour: the read budget closes R with 1013 "relay busy;
retry later" (R-4.0 H1); a direct envelope takes the queue path (`directBusy`); a queue drain
waits for room, and each of its rechecks (every 50 ms, `spaceRecheck`) is a new charge that
may evict once; presence is dropped
silently; a control reply is dropped (OD-R55F1-4). `ready` never gets here.

**What eviction costs an honest daemon.** It is reconnected by its normal backoff. Mail that
was waiting in its outbound buffer was forwarded directly and so is not in the relay's queue:
the sender's outbox resends it until the recipient acks ([mail.md](mail.md)), so it is delayed,
not lost (OD-R55F1-6). An evicted reader's unfinished frame is resent the same way.

**Why this does not bring back the R-4.0 H1 deadlock.** Nothing waits for another
connection's frame. Eviction and fallback are both immediate, so an attacker holding the budget
cannot make the relay stall; it can only make the relay close the attacker's own connections
first, because they are the heaviest and the oldest.

#### Frame read deadline

A frame must be read completely within **`--frame-read-timeout` (default 30 s)** of the moment
its header arrives (`ws.Reader` returns). Past it the connection is closed 1013 "frame too
slow; retry later", its read charge is released, and `event=limit limit=frame_read_timeout
peer=<short key>` is logged. It covers the whole frame, not the gap between fragments, and
does not apply between frames (an idle connection holds no read budget). Implementation hint:
a timer armed when `Reader` returns and stopped at EOF, which closes the connection; a
context on `Reader` alone does not start at the first byte.

At 30 s a maximum-size frame (1 MiB) needs about 35 KiB/s of upload. A daemon on a slower link
can still send mail of normal size; a frame that repeatedly times out is reported by the daemon
like any other 1013.

#### Bytes charged at read (R55-035)

Every complete frame read after `auth` is charged, before it is parsed, to its key's and
prefix's **byte** buckets (32 MiB / min per key, 64 MiB / min per prefix, the rows above). A
frame over either is dropped without being parsed, with `error` `rate_limited` and an empty
`ref`. `sendAllowed` then checks only the envelope counts, so a valid envelope is not charged
twice. Presence (at most 600 × 8 KiB ≈ 4.7 MiB a minute per key) and control frames now count
towards the byte buckets too. The envelope-count rows are unchanged.

#### Memory bound after R55-F1

With `--max-conns` C and `--max-inflight` B: `B` (outbound, drain reservations inside it) +
`B` (read) + `max(B / 8, 1 MiB)` (ephemeral) + C × `ready` (≈ 200 bytes) + C × the idle cost
of a connection (R-4.0 M2 measured ~58 KiB with both ends in one process, so this is an upper
estimate for the relay's side) + the process's fixed cost. At the 4.1p flags:

| Part | 4.1p |
|---|---|
| Outbound + read + ephemeral | 48 + 48 + 6 = 102 MiB |
| 2000 idle connections | ≈ 113 MiB |
| `ready` frames | < 1 MiB |
| Runtime, SQLite page cache, limit maps, queue totals (estimate) | ≈ 40 MiB |
| **Total** | **≈ 255 MiB**, under `GOMEMLIMIT=400MiB` (≈ 145 MiB headroom for the GC) |

Before R55-F1 the same flags allowed presence alone to pin ≈ 0.5 GiB at 2000 connections
(verify C01-01) plus up to 48 MiB of uncounted backing arrays (R55-034), above
`GOMEMLIMIT`.

#### Flags for the early private relay (4.1p)

`deploy/early/agentnet-relay.service` keeps `--max-conns 2000 --max-inflight 48MiB` and
`GOMEMLIMIT=400MiB`, and adds, explicitly (they equal the defaults, but the unit should show
every memory number):

```
    --max-inflight-ephemeral 6MiB \
    --frame-read-timeout 30s \
```

The unit's sizing comment and `Docs/ops/early-relay-deploy.md` (its `--max-inflight` row says
"about 96 MiB at most") change to the table above: ≈ 102 MiB of budgets, ≈ 255 MiB in total.
Caddy needs no stream timeout for this: the relay now bounds unfinished frames itself.

#### Attacks from review 55, after R55-F1

Rough costs at the 4.1p flags. "Prefix" is a /24 or a /48.

| Attack | Today (verify file) | After R55-F1 |
|---|---|---|
| **R55-001 / C01-02**, unfinished frames hold the read budget | 1 prefix, ~51 connections, 29–45 MiB uploaded once, then nothing: every honest frame closed 1013, indefinitely | **1 prefix holds at most 6 MiB**: the other 42 MiB serve everyone else, and the attacker's 7th frame evicts one of its own. To fill 48 MiB: ≥ 8 prefixes, ~48 connections, 48 MiB re-uploaded every 30 s (≈ 1.6 MiB/s, ≈ 13 Mbit/s, sustained). Even then an honest frame that finds the budget full evicts the attacker's oldest frame in its heaviest prefix instead of being closed. **No denial**; the attack costs bandwidth and reconnects (30 upgrades / min per prefix) |
| **R55-002 / C01-01**, presence to slow readers spends the mail budget | ~4 prefixes, ~190 keys, 50–100 MiB ramp, then ~200 KiB/s each way: all direct mail queued, drains stall | **Closed at any cost:** presence cannot touch the outbound budget. Presence itself: filling the 6 MiB ephemeral budget needs ≥ 8 prefixes (768 KiB each, ~3 sinks), and each refused honest presence first evicts the stalest sink of the heaviest prefix; the attacker must reconnect and refill it (264 KiB per sink, 30 upgrades / min per prefix). Presence is best effort and repeats; mail is unaffected |
| Same shape with **mail** to the attacker's own slow readers (found while writing this spec; read, not tested) | 1 prefix today: 12 sinks × 4 MiB of 64 KiB frames, 48 MiB in ~1.5 min within the prefix send rates, then ~77 KiB/s to keep each sink's oldest write under 10 s: direct mail queued, drains wait | 1 prefix holds at most 6 MiB. With ≥ 8 prefixes, each honest direct send or drain that finds the budget full evicts a stale sink (≥ 2 s behind) first; the attacker must refill 4 MiB per evicted sink within the 64 MiB / min per-prefix rate. **No denial** |
| **R55-034**, frames pin up to 2 × the bytes charged | +48 MiB uncounted | 0: frames are trimmed to their length (or `cap` is charged) |
| **R55-035**, invalid 1 MiB frames parsed uncharged | limited only by upload bandwidth | 32 parses a minute per key, 64 a minute per prefix; the rest dropped unparsed |

**Residuals** (added to [What the limits do not stop](#what-the-limits-do-not-stop)):

- An attacker with **many prefixes**, each holding less than one honest prefix, makes the
  honest prefix the heaviest. Its frames are then the ones evicted. This needs about
  `B / (one honest frame)` prefixes (≈ 48 at 4.1p for 1 MiB frames) within `--max-conns`,
  plus the re-upload above. It hurts large honest frames (they are retried); small ones
  (acks, presence, typical mail) come from light prefixes and evict the heavy one. IPv6 /48s
  are cheap, so this is realistic on IPv6 (OD-R55F1-7).
- An attacker who can send to an honest recipient faster than it downloads can make that
  recipient's buffer stale and so eligible for outbound eviction while the budget is full.
  The recipient reconnects; its mail comes again from the queue and from senders' outboxes.
- One misbehaving host behind a shared NAT uses its prefix's share for everyone behind it
  (as for the other per-prefix limits).

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
  per-prefix limits for everyone behind it, including its prefix's share of the memory
  budgets (R55-F1).
- (R55-F1) An attacker with many prefixes, each holding less of a budget than one honest
  prefix, can get the honest prefix's large frames evicted and retried (≈ 48 prefixes at the
  4.1p flags; cheap with IPv6 /48s, OD-R55F1-7). Small frames still pass. See
  [Residuals](#attacks-from-review-55-after-r55-f1).
- (R55-F1) An attacker that out-sends a recipient's download speed can get that recipient
  evicted while the outbound budget is full; it reconnects and its mail is delivered again.
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
- R55-F1 (full list in [56-r55-f1-spec.md](../review/56-r55-f1-spec.md#3-acceptance-tests)):
  unfinished frames from one prefix hold at most its share and an honest mail is still
  delivered; a frame that never completes is closed within `--frame-read-timeout`; a presence
  flood to non-reading recipients never raises the outbound budget and honest mail between
  reading peers is forwarded directly; past a full budget the heaviest prefix's holder is
  evicted, not the newcomer; the outbound charge equals the heap the frames pin.
- A restore followed by `--replay-journal` keeps an unbind and an invite redemption made after
  the backup.
- Each limit table row has a test that triggers it and checks the error code and that other
  keys / prefixes are unaffected.
- One stranger key cannot queue more than 300 envelopes for a victim; the victim's own
  teammates can still queue.
- Behind a proxy: a spoofed `Fly-Client-IP` from an untrusted peer is ignored.
- Backup, restore, and the restore-drill test; kill-and-restart of the binary loses no queued
  envelope.

## Open decisions (R55-F1)

Each has a recommendation; the owner decides after the adversarial review.

- **OD-R55F1-1: reopen R-4.0 H1 ("close the reader, never wait").** R52 H1 closed the reader
  that asked when the read budget was spent, to avoid a deadlock; review 55 showed that hands
  the budget to whoever took it first (R55-001).
  (a) Past a full budget, **evict the heaviest prefix's oldest holder** (§2 Memory budgets),
  with the frame deadline as a backstop; no waiting, so no deadlock.
  (b) Keep closing the newcomer; add only the per-prefix share and the deadline. One prefix no
  longer suffices, but 8 prefixes and ≈ 13 Mbit/s deny service again.
  (c) Let the reader wait up to 1 s for room, then close it. Brings back goroutines parked on
  an attacker's budget and still hands the budget to the first holders.
  **Recommended: (a).** This reopens a review choice, not an owner decision (HANDOFF rule 9
  does not apply); it is a concrete availability flaw, confirmed by test.
- **OD-R55F1-2: frame read deadline.**
  (a) Fixed 30 s from the frame's first byte (`--frame-read-timeout`).
  (b) Scaled: 10 s + 1 s per 32 KiB read so far (≈ 42 s for 1 MiB, 10 s for small frames).
  (c) Fixed 60 s.
  **Recommended: (a).** With eviction the deadline is only a backstop; a fixed value is
  simpler to test and explain. (b) is tighter for small frames if the review finds it matters.
- **OD-R55F1-3: prefix share.**
  (a) 1/8 of each budget, with the floors in §2 (6 MiB of 48 MiB at 4.1p).
  (b) 1/4 (an attacker needs 4 prefixes; an office gets twice the room).
  (c) 1/16 (16 prefixes; a busy office or carrier-grade NAT hits its share sooner).
  **Recommended: (a).**
- **OD-R55F1-4: a control reply that does not fit the ephemeral budget** (after the eviction
  attempt). `queued`, `error`, `pair_*`, `bind_*`.
  (a) Drop it (`ready` is exempt). The daemon's outbox resends until acked, a pairing times
  out and is retried, and `pair_peer` to an issuer already answers `peer_busy`.
  (b) Close the connection the reply is for with 1013 (it is the one not reading its replies).
  (c) Keep control frames on an unconditional add and bound them per connection (64 frames).
  At 2000 connections × 64 × ~1 KiB that is ≈ 125 MiB, too much for the 4.1p VM.
  **Recommended: (a).** (b) is a reasonable alternative if the review finds a reply whose loss
  confuses a daemon.
- **OD-R55F1-5: ephemeral budget size.**
  (a) New flag `--max-inflight-ephemeral`, default `max(--max-inflight / 8, 1 MiB)` (6 MiB at
  4.1p), on top of the outbound budget.
  (b) Fixed 16 MiB default, independent of `--max-inflight`.
  (c) No new flag: carve 1/8 out of `--max-inflight` (mail keeps 7/8).
  **Recommended: (a):** it keeps mail's budget whole and scales with the operator's sizing.
- **OD-R55F1-6: mail in an evicted connection's outbound buffer.** It was forwarded directly,
  so it is not in the relay's queue.
  (a) Accept: the sender's outbox resends it until the recipient acks it (delay, not loss).
  Documented in §2.
  (b) Move the buffered mail back into the offline queue before closing: SQLite writes on the
  attack path, and the queue caps may refuse them anyway.
  (c) No outbound eviction; shares only. Then ≥ 8 prefixes of slow readers deny direct mail
  again (the mail variant in §2).
  **Recommended: (a).**
- **OD-R55F1-7: IPv6 and the many-prefix residual.** /48s are cheap, so "8 prefixes" is not
  a high bar on IPv6, and "≈ 48 prefixes" (the residual) is within reach.
  (a) Keep /48 as the prefix and document the residual (§2 What the limits do not stop).
  (b) Add a second share level: all /48s of one /32 together hold at most 2 × a prefix share.
  (c) For 4.1p only: publish no AAAA record for `relay.dorylinae.net` until (b) exists.
  **Recommended: (a) now, with (c) for 4.1p if the VM has IPv6 today; revisit (b) before the
  public launch** (with the outside review, D41).
- **OD-R55F1-8: charge bytes at read (R55-035).**
  (a) Charge every frame to the key's and prefix's byte buckets before parsing; drop it
  unparsed when over.
  (b) Charge only frames that fail `Classify`/`ParseHeader`/`from`, after parsing them once.
  (c) Leave R55-035 in the Low backlog.
  **Recommended: (a):** one rule, and it bounds the parse CPU a key can cause.
