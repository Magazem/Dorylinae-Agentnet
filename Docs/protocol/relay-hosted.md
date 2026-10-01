# Hosted relay: TLS, abuse limits, quotas, operations

Status: **approved by the owner 2026-09-27 (D36 in HANDOFF)** (Phase 4 spec, tickets 4.0a–4.0d
and 4.1a–4.1c in [../review/49-phase4-tickets.md](../review/49-phase4-tickets.md); adversarially
reviewed in [50-phase4-spec-review.md](../review/50-phase4-spec-review.md)). OD-P4-n choices for
this document are recorded in the ticket plan, not open, except OD-P4-20 (outside review
timing), decided during wave 1.

**Pending change (R55-F1, 2026-09-29):** [§2 Memory budgets and fairness](#memory-budgets-and-fairness-r55-f1)
replaces the review-50 M2 memory-bound paragraph and reopens the R-4.0 H1 "close the reader"
choice. It is a proposal until the owner approves it after its adversarial review; open
decisions are [OD-R55F1-1…9](#open-decisions-r55-f1) at the end of this document. Summary and
acceptance tests: [../review/56-r55-f1-spec.md](../review/56-r55-f1-spec.md).

**Pending change (R55-F2, 2026-09-30):** [§2 Offline queue delivery and expiry](#offline-queue-delivery-and-expiry-r55-f2)
adds a **redelivery budget** per key and per prefix (R55-009), refuses envelopes whose payload
is not base64 (R55-010, see [envelope.md](envelope.md#forwarding)) and makes the expiry sweep
work in **bounded batches** with a **bounded WAL** (R55-011). It is a proposal until the owner
approves it after its adversarial review; open decisions are
[OD-R55F2-1…10](#open-decisions-r55-f2) at the end of this document. Summary, plan and
acceptance tests: [../review/66-r55-f2-spec.md](../review/66-r55-f2-spec.md).

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
| Queued bytes **redelivered** to all keys of a prefix together (R55-F2) | 128 MiB / hour, burst 128 MiB (`--queue-redeliver-per-prefix`) | the redelivery is skipped for now; see [below](#offline-queue-delivery-and-expiry-r55-f2) |

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
| Queued bytes **redelivered** to the key: rows it was sent before and has not acked (R55-F2) | 32 MiB / hour, burst 32 MiB (`--queue-redeliver-per-key`) | the redelivery is skipped for now and retried when the budget refills; new rows are still delivered. See [below](#offline-queue-delivery-and-expiry-r55-f2) |
| Envelope whose `payload` is not a JSON string of standard base64 (R55-F2) | — | `error` `bad_envelope`, dropped (never queued) |

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
- **One ledger per connection (review 56a).** Everything a connection holds in any budget is
  recorded on the connection, under one lock (today `bmu`): its outbound bytes, its ephemeral
  bytes, its drain reservation and the read charge of its current frame. Every charge and
  every uncharge goes through that ledger, and every path checks the same `dead` flag that
  `release` and eviction set. In particular, today's two private counters must move onto the
  ledger: `drainStep`'s `reserved` (returned by a `defer` straight to the budget) and
  `readFrame`'s `charged` (returned by `done`). Otherwise an eviction followed by those
  returns uncharges the same bytes twice, the budget goes negative, and `tryAdd` then admits
  more than the budget: the memory bound silently breaks.

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
being replaced still counts until its charges are released. As the relay-wide budget does
today (`tryAdd` accepts any charge while the budget is empty), a prefix that holds nothing
of X may always take one charge, so a frame larger than a (test-sized) share is never
refused forever.

At the 4.1p flags one prefix's outbound share (6 MiB) is exactly one connection's maximum
(4 MiB buffer + a 2 MiB drain reservation). Two recipients behind one NAT address (an office,
a carrier-grade NAT) that both receive a backlog at once therefore take turns: the second
one's mail takes the queue path and its drain waits until the first one's buffer empties.
This is a delay, not a loss, and only at the 4.1p size (at the 256 MiB default a share is
32 MiB).

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
   - read budget, paying prefix **other than R's**: every holder is eligible. That prefix
     holds strictly more than R's (step 1), so it pays whatever the age of its frames
     (review 56a: an age rule here let an attacker keep its heaviest prefix's frames younger
     than a long honest frame, and so get the honest frame closed by the fallback);
   - read budget, paying prefix **R's own** (R's prefix is at its share): H's current frame
     started before R's current frame;
   - outbound and ephemeral budgets: H is **not keeping up** (`evictStale`): the oldest frame
     waiting in H's outbound buffer has waited at least
     `2 s + (bytes H holds in the outbound and ephemeral budgets together) ÷ 512 KiB/s`
     (`evictMinRate`; 14 s for a full 6 MiB, ≈ 2.5 s for 264 KiB of presence alone). Both
     budgets count whichever one pays: mail and presence wait in one buffer, so the oldest
     frame waits behind all of it, and counting only X would make an honest drainer of a
     mail backlog eligible through the few KiB of presence it also holds (review 63 S-1).
     The drain reservation adds to what H holds, but its own age does not count. A reader
     whose link delivers at least 512 KiB/s (4 Mbit/s) is never eligible, however much it
     holds (OD-R55F1-9). A flat 2 s would make an honest daemon that drains a 4 MiB backlog over anything slower than 2 MiB/s eligible, and, at 6 MiB,
     the heaviest holder there is.
   If no holder in the paying prefix is eligible, nobody is evicted.
3. **Evict H.** Under H's ledger lock, mark H dead and uncharge everything H holds in all
   three budgets at once; from then on no path charges or uncharges H (see "One ledger per
   connection"), and a frame H finishes reading after this point is discarded, never routed.
   Then close H **in its own goroutine**, never on R's path: close 1013 "relay busy; retry
   later" with at most a 1 s wait, then `CloseNow` (do not wait for the peer's close reply;
   `ws.Close` alone waits up to 5 s + 5 s). Log `event=limit
   limit=evict_read|evict_outbound|evict_ephemeral prefix=<paying prefix>` (the usual
   once-a-minute rule).
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
not lost (OD-R55F1-6). An evicted reader's unfinished frame is resent the same way. Frames a
queue drain had put in the buffer stay in the queue until acked and are sent again on the
next connection. That is today's reconnect behaviour (R55-009 / T10-02, which R55-F2 owns):
an eviction costs one more re-drain, within the key's 20 reconnects a minute, so it adds no
new amplification.

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
a timer armed when `Reader` returns and stopped at EOF; a context on `Reader` alone does not
start at the first byte. On expiry the connection is handled exactly like an eviction (step 3
above): all its charges are released and it is marked dead at once, and the close runs with
the same 1 s bound. A plain `ws.Close` from the timer is not enough: with the reader still
blocked mid-frame it waits up to 10 s for a close reply the attacker never sends, and the
frame stays charged meanwhile.

At 30 s a maximum-size frame (1 MiB) needs about 35 KiB/s of upload. A daemon on a slower link
can still send mail of normal size. Today's daemon does not tell the user about this case: it
logs `relay_disconnect` (Warn), reconnects with backoff, and its outbox retries the frame with
its own backoff until the mail expires. So a maximum-size mail from a link under ~35 KiB/s is
never delivered, and its only visible trace is the log and the outbox state. (For comparison,
the relay's existing 10 s `writeTimeout` already needs about 100 KiB/s to *deliver* a 1 MiB
frame.)

#### Bytes charged at read (R55-035)

Every complete frame read after `auth` is charged, before it is parsed, to its key's and
prefix's **byte** buckets (32 MiB / min per key, 64 MiB / min per prefix, the rows above). A
frame over either is dropped without being parsed, with `error` `rate_limited` and an empty
`ref`. `sendAllowed` then checks only the envelope counts, so a valid envelope is not charged
twice. Presence (at most 600 × 8 KiB ≈ 4.7 MiB a minute per key) and control frames now count
towards the byte buckets too. The envelope-count rows are unchanged. One exception (review
56a): a frame of at most **1 KiB** is charged but never refused at this step. Parsing it is
cheap, and it is how `ack`s arrive: otherwise one heavy sender behind a shared NAT could spend
the prefix's 64 MiB / min and have every neighbour's acks dropped, so their queued mail would
be kept and redelivered. The per-frame rules after parsing (control 60 / min, envelope counts)
still apply to it. Such frames never drive a bucket below empty (review 63 S-5): with a
debt, a minute of flooding acks would lock the whole prefix out for many minutes after.

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
| 256 unauthenticated connections × one auth frame (`MaxAuthFrameBytes` 4 KiB) | ≈ 1 MiB |
| **Total** | **≈ 255 MiB**, under `GOMEMLIMIT=400MiB` (≈ 145 MiB headroom for the GC) |

Two terms outside the table (review 56a):

- **Evicted connections until their close completes.** An evicted connection is uncharged at
  once, but its current read buffer and the frame its write loop is writing (≤ 1 MiB each)
  stay reachable until the close ends (≤ 1 s, step 3). So the heap can exceed the budgets by
  what was evicted in the last second. Repeating that needs the attacker to re-upload the
  evicted bytes every second (48 MiB/s for a whole budget); the 145 MiB headroom covers
  anything short of that.
- **Caddy is a separate process**, outside `GOMEMLIMIT`. As an estimate (not measured), a
  proxied WebSocket costs Caddy on the order of 0.1 MiB (two 32 KiB copy buffers, TLS record
  buffers, goroutines), so ≈ 250 MiB at 2000 + 256 connections. The VM therefore needs about
  1 GB, not 512 MB. Every current Hetzner type has more ([early-relay-deploy.md](../ops/early-relay-deploy.md)
  step 3), but the "512 MB-class" wording of review 52 should not be read as enough for both.

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
| Same shape with **mail** to the attacker's own slow readers (found while writing this spec; read, not tested) | 1 prefix today: 12 sinks × 4 MiB of 64 KiB frames, 48 MiB in ~1.5 min within the prefix send rates, then ~77 KiB/s to keep each sink's oldest write under 10 s: direct mail queued, drains wait | 1 prefix holds at most 6 MiB. With ≥ 8 prefixes, each honest direct send or drain that finds the budget full evicts a sink that is not keeping up (`evictStale`) first; the attacker must refill 4 MiB per evicted sink within the 64 MiB / min per-prefix rate. **No denial** from sinks that do not read. To keep its sinks ineligible the attacker must make them download: a 4 MiB sink needs ≥ 410 KiB/s (oldest frame < 10 s), so holding 48 MiB costs ≈ 12 sinks × 410 KiB/s ≈ 4.8 MiB/s (≈ 40 Mbit/s) each way, sustained, from ≥ 8 recipient prefixes and ≥ 5 sender prefixes (64 MiB / min each). That still delays direct mail (queue path, drains wait) while it lasts (review 56a; with a flat 2 s rule it would cost ≈ 24 MiB/s, but honest drains would be evicted, OD-R55F1-9) |
| **R55-034**, frames pin up to 2 × the bytes charged | +48 MiB uncounted | 0: frames are trimmed to their length (or `cap` is charged) |
| **R55-035**, invalid 1 MiB frames parsed uncharged | limited only by upload bandwidth | 32 parses a minute per key, 64 a minute per prefix; the rest dropped unparsed |

**Residuals** (added to [What the limits do not stop](#what-the-limits-do-not-stop)):

- An attacker with **many prefixes**, each holding less than one honest prefix, makes the
  honest prefix the heaviest. Its frames are then the ones evicted. This needs about
  `B / h` prefixes, where h is what the honest prefix holds, within `--max-conns`, plus the
  re-upload above. For a lone daemon sending one 1 MiB frame that is ≈ 48 prefixes at 4.1p;
  for a **busy shared prefix** (an office or carrier-grade NAT) that holds its full 6 MiB
  share it is only ≈ 8 (review 56a): such a prefix is the cheapest target. It hurts large
  honest frames (they are retried); small ones (acks, presence, typical mail) come from light
  prefixes and evict the heavy one. IPv6 /48s are cheap, so this is realistic on IPv6
  (OD-R55F1-7).
- An attacker who can send to an honest recipient faster than it downloads can make that
  recipient's buffer stale and so eligible for outbound eviction while the budget is full.
  The recipient reconnects; its mail comes again from the queue and from senders' outboxes.
  With `evictMinRate` this needs a recipient whose link is below 512 KiB/s, and it is evicted
  only if its prefix is also the heaviest.
- An attacker that makes its sinks download fast enough to stay ineligible (≈ 40 Mbit/s each
  way at 4.1p, above) can still hold the outbound budget and delay direct mail while it pays
  for that bandwidth.
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

### Offline queue delivery and expiry (R55-F2)

Review 55 found three ways a stranger abuses the queue on a relay without accounts. The caps
above bound what is **stored**; nothing bounded what is **sent back out**, what is stored
**forever-unackable**, or how much work **one expiry** does:

- **R55-009** (T10-02; treated as High, D47): a row stays queued until it is acked, and every
  new connection of its recipient re-sends the whole queue from the start. A key that never
  acks turns one upload into up to 20 downloads a minute (the reconnect limit) for 7 days.
  One /24 with 12 recipient keys × 32 MiB makes the relay send ≈ 7.5 GiB a minute, and every
  re-send is a SQLite read on the relay's single database connection.
- **R55-010** (T6a-02): the relay queues an envelope whose `payload` is not base64. The
  recipient cannot parse it, so it never acks it, and it fills the victim's queue for the
  whole TTL instead of until the next connect.
- **R55-011** (C02-02): the sweep deletes every expired row in one statement, under the queue
  lock, on the single connection. 1 GiB expiring together (≈ 16 prefixes filling the queue in
  one minute, 7 days earlier) stalls all queue work for 11–14 s and leaves a 1 GiB WAL; at
  2 GiB (reachable at the 4 GiB default) the statement times out, rolls back and repeats
  every minute (livelock, `queue_full` for everyone).

#### First delivery and redelivery (R55-009)

A queued row is **delivered** when a drain hands it to a connection of its recipient (to the
connection's outbound buffer). It stays queued until acked or expired, as before. Sending it
again to the same key, on a later connection, is a **redelivery**.

- **First delivery is not budgeted.** Its bytes were charged once, when the sender uploaded
  them, to the sender's and its prefix's byte rates ([Per authenticated key](#per-authenticated-key)).
  So first deliveries cost the relay no more egress than it took in, like direct forwarding.
  A daemon that comes online to a full 32 MiB backlog gets it at once, as today.
- **Redelivery is charged to the recipient**, by frame length, to two token buckets: its key
  (`--queue-redeliver-per-key`, default **32 MiB per hour, burst 32 MiB**) and the prefix of
  the connection it is delivered on (`--queue-redeliver-per-prefix`, default **128 MiB per
  hour, burst 128 MiB**). Both are charged or neither (as the send rates). The charge is
  taken before the frame is handed to the buffer; a frame handed over whose connection then
  drops is still counted.
- **Which rows are redeliveries.** Drains deliver a key's rows in `seq` order and a new row
  always gets a larger `seq`, so the rows a key has been sent are exactly those with
  `seq ≤ H(key)`, its **delivered high-water mark**. The relay keeps H persistently in a
  small table `queue_delivered (to_key PRIMARY KEY, seq)` (relay migration **R3**). It
  survives a restart, so a restart does not turn redeliveries into free first deliveries. The
  sweep deletes marks of keys that have no queued rows left.
- **H is raised before the frames are sent (review 66b H1).** A drain **claims** its batch:
  in one transaction under the queue lock it reads H, reads the batch, and raises H to the
  batch's highest first-delivery `seq`, and only then hands the frames to the buffer. A
  connection that drops half-way through a batch therefore cannot get the same rows again as
  "first" deliveries on its next connection (raising H after the send would have let a key
  that closes mid-batch replay its first batch, ≈ 2 MiB, on every one of its 20 reconnects a
  minute). Two connections of one key (a replacement) cannot both claim the same rows as
  first deliveries. A claimed row that never reached the daemon is, on the next connection, a
  redelivery: the honest case is covered by the budget below. If the claim fails (database
  error), nothing is sent.
- **An honest daemon rarely pays.** It acks as it receives, so after a dropped connection it
  is redelivered only what was in flight: at most its outbound buffer (4 MiB), one drain batch
  (≈ 2 MiB) and what was in transit. The key's 32 MiB burst covers several such reconnects
  in a row.

#### Redelivery policy for unacked rows

Each drain step reads the connection's rows oldest first from its cursor, as today:

1. A row with `seq > H` is a first delivery: it is claimed (H raised, above) and sent.
2. A row with `seq ≤ H` is sent only if both buckets hold its bytes (they are taken), and
   only if no other connection of the same prefix is waiting for redelivery budget ahead of
   it (rule 6).
3. Otherwise it is **skipped**: not sent now, left in the queue. The connection records
   `skipFrom` (that row's `seq`), `skipTo` (H at that moment) and the row's size, and jumps its
   cursor to `skipTo`. It goes on with the first deliveries after `skipTo`, so **new mail is
   still delivered**, and the skipped rows' frames are never read again on this connection
   until the retry.
4. **No frame is read to decide a skip (review 66b M1).** Before reading frames at a cursor
   below H, the drain reads only the first such row's `seq` and size (`LENGTH(frame)` is read
   from the record header, not the blob) and checks the buckets and rule 6. A skip therefore
   costs an index lookup, not a 1 MiB read, also on the **first** batch of every new
   connection: a key that reconnects 20 times a minute with its budget spent makes the relay
   read no frame of its old rows. When the first row is paid, the batch **takes** from both
   buckets what they still hold (at most one batch), reads the rows' lengths first and then
   only the frames that budget pays for, and gives back what it did not send (review 74 M-2,
   74b L-a). A tiny first row cannot make the relay read a whole 1 MiB batch it will not
   send, and connections served in the same tick cannot each read by the same tokens.
5. **Retry.** Once a minute (the sweep tick) the relay serves the waiting connections of each
   prefix in the order they started waiting (rule 6). A connection whose key's and prefix's
   buckets now hold its first skipped row's size drains the range `[skipFrom, skipTo]` again
   under the rules above (rows acked meanwhile are gone; rows skipped again keep a new,
   narrower range), then resumes at the cursor it had before the retry (rows it already got
   as first deliveries are not sent again). During the retry the connection is draining, so
   direct sends to it take the queue path, as for any backlog.
6. **Fair order within a prefix (review 66b H2).** The prefix bucket is shared by every key
   behind one /24 or /48, including an attacker's. Without an order, a key that asks 20 times
   a minute takes each refill before an honest neighbour's once-a-minute retry, and the
   honest key's unacked rows could wait until they expire: **loss**, not delay. So a
   connection that is skipped for want of **prefix** budget joins its prefix's wait list
   (in memory, one entry per connection, removed when the connection closes). While the list
   is not empty, a redelivery on any connection of that prefix that is not being served from
   the list is skipped and that connection joins the tail; refills go to the list in order.
   Each tick serves, in order, every waiting connection whose first skipped row the prefix
   bucket can still pay after those served before it in the tick (review 74 L-3). A
   connection whose own **key** bucket cannot pay keeps its place and the next one is
   served. A served connection that the **prefix** bucket cannot pay part way through its
   range goes back to **its place** in the list, ahead of every connection that joined
   after it: the prefix ran short, not it (review 74 M-1). Each connection gets a join ticket
   when it starts waiting and keeps it while it is served, so connections served in the same
   tick come back in their old order (review 74b L-b). A turn lasts one sweep tick
   (`redeliverTurn`, 1 min, stamped with the time the tick began, before its sweep): a
   connection still served then, typically one that reads slowly, loses its turn, and its
   next redelivery joins the tail with a new ticket. A slow reader therefore holds no one up for longer than a tick, and a
   key's wait is
   therefore bounded by what the connections ahead of it may redeliver: at most
   `--queue-max-total` ÷ the prefix rate (8 h with the 4.1p unit's 1 GiB and 128 MiB/h;
   32 h at the 4 GiB default), well inside the 7-day TTL.
7. A key's next connection starts from cursor 0 as today, and its rows `≤ H` are
   redeliveries charged as above (it joins the wait list anew; a place is not kept across
   connections).

Retried rows reach the daemon after newer ones. That is already possible today (a
redelivery after a reconnect follows the frames the daemon received before the drop), and
the layers above do not rely on it: mail dedupes and re-acks, the relay client's seen-set
drops repeats, and `session.*` frames have their own replay protection
([session.md](session.md)).

Limit names logged: `queue_redeliver_key` (`peer=`), `queue_redeliver_prefix` (`prefix=`),
once a minute per subject as for every limit. Metrics (§5): `relay_queue_redelivered_bytes_total`
and `relay_queue_redeliveries_skipped_total`, no per-key labels.

#### Envelopes the recipient cannot parse (R55-010)

The relay refuses, with `bad_envelope`, an envelope whose `payload` is not a JSON string of
standard base64 ([envelope.md §Envelope](envelope.md#envelope), step 1 of
[§Forwarding](envelope.md#forwarding)). Such a frame is never queued or forwarded. The
check reads the payload in place and keeps no decoded copy; its CPU is bounded by the bytes
charged at read (R55-035). As defence in depth, and for rows already queued by an older
relay, the daemon **acks** a frame it cannot parse when its routing fields are valid
([envelope.md §Client behaviour](envelope.md#client-behaviour-daemon)).

#### Expiry sweep in bounded batches (R55-011)

- The sweep deletes expired rows **oldest first in batches of at most 32 rows** (at most
  32 MiB of frames) through the `queue_by_age` index. Each batch is its own transaction and
  takes the queue lock only for that batch; the lock is released between batches, with a
  5 ms pause, so `add`, `ack` and drains run in between (without the pause a waiter loses
  the lock to the next batch once before Go's mutex hands it over, and waits two batches).
  The totals are adjusted after the batch commits.
- A batch that fails rolls back only itself. The batches before it stay deleted, and the next
  tick carries on, so a large expiry cannot livelock at any `--queue-max-total`. A 32 MiB
  batch takes about 0.35 s with `secure_delete=ON` on the NVMe disk measured in
  `verify/C02-02.md` (≈ 95 MiB/s).
- One tick works for at most 30 s and then stops until the next tick, so a relay that was
  down for days catches up over a few minutes without holding the connection for long.
- **WAL bound.** The relay database is opened with `journal_size_limit` = **64 MiB**, and a
  tick that deleted more than 64 MiB ends with `PRAGMA wal_checkpoint(TRUNCATE)`. The WAL then
  stays near one batch (≈ 2 × 32 MiB with `secure_delete`) while a sweep runs and returns to at
  most 64 MiB after it. A backup (`VACUUM INTO`) running at the same time holds a read
  snapshot, so the WAL can grow while it runs; the next large sweep truncates it.
- **The `TRUNCATE` checkpoint never waits (review 66b M3).** `TRUNCATE` calls the busy
  handler until no other connection reads an old snapshot, and the relay's `busy_timeout` is
  5 s. A `relay backup` (another process) running at that moment would hold the relay's only
  database connection, and so every add, ack and drain, for up to 5 s: R55-011 again, smaller.
  So the relay runs it as `PRAGMA busy_timeout = 0`, `PRAGMA wal_checkpoint(TRUNCATE)`,
  `PRAGMA busy_timeout = 5000` on its connection, under the queue lock. A busy result is not
  an error: the checkpoint is skipped (the limit above still applies at the next WAL reset)
  and tried again after the next large sweep. The `busy_timeout` restore runs with a
  context of its own; if it fails, the connection is discarded so that the pool opens a new
  one with the DSN's pragmas (review 74 L-2).
- `add` starts its 10 s deadline after it takes the queue lock (today: before), so an add
  that waited behind a batch still has its full time.
- The free-disk floor (`--queue-min-free-disk`) is checked on `add` only; after this change
  a sweep needs about one batch of WAL headroom, not the size of the expiry.

#### Flags for the early private relay (4.1p), R55-F2

`deploy/early/agentnet-relay.service` adds, explicitly (they equal the defaults):

```
    --queue-redeliver-per-key 32MiB \
    --queue-redeliver-per-prefix 128MiB \
```

Both are per hour with a burst of the same size, and each must be at least 1 MiB (one frame):
a smaller bucket could never pay for a large row, which would then wait until it expires
(review 74 L-1). The sweep batch (32 rows), the tick budget
(30 s) and the WAL limit (64 MiB) are constants, not flags (OD-R55F2-7, -8). With the unit's
`--queue-max-total 1GiB`, the worst expiry is 32 batches (≈ 11 s of work spread over one tick
with the lock released between batches), and the WAL is back under 64 MiB after it. The
runbook's disk budget (`Docs/ops/early-relay-deploy.md`) no longer needs room for a WAL as
large as the queue.

#### Attacks from review 55, after R55-F2

Rough costs at the 4.1p flags. "Prefix" is a /24 or a /48.

| Attack | Today | After R55-F2 |
|---|---|---|
| **R55-009 / T10-02**, redelivery amplification | 1 prefix, 12 keys × 32 MiB queued (384 MiB, uploaded once), 20 reconnects / min / key without acks: ≈ 7.5 GiB / min egress for 7 days, plus a full-queue SQLite read per reconnect | First delivery: 384 MiB once (what it uploaded; H is claimed before the send, so closing mid-batch does not replay it). Then at most 128 MiB burst + 128 MiB / hour per recipient prefix (≈ 0.3 Mbit/s). 16 prefixes: ≈ 2 GiB / hour (≈ 5 Mbit/s). Reconnecting gains nothing, and once the budget is spent no old row's frame is read from the database again, not even on a new connection's first batch |
| **R55-010 / T6a-02**, unparseable junk fills a victim's queue | 4 keys, 1000 junk rows: the victim gets `queue_full` for 7 days and re-downloads the junk at every connect | The junk is refused at the door (`bad_envelope`). Parseable junk is acked and deleted at the victim's next connect (O-015, as accepted); rows already queued by an older relay are acked by updated daemons |
| **R55-011 / C02-02**, one-statement sweep | ≈ 16 prefixes fill 1 GiB in one minute: 7 days later an 11–14 s stall and a 1 GiB WAL; at 2 GiB a rollback livelock | Batches of ≤ 32 MiB: the lock is held ≈ 0.35 s at a time, the WAL is ≤ 64 MiB after the tick, and no size of expiry can livelock |

**Residuals** (added to [What the limits do not stop](#what-the-limits-do-not-stop)):

- The prefix redelivery budget is shared. An attacker in the same /24 as an honest recipient
  (a shared NAT) can spend it; the honest recipient's unacked rows then wait their turn in the
  prefix's wait list (rule 6), at most `--queue-max-total` ÷ the prefix rate (8 h on the 4.1p
  unit), inside the 7-day TTL. New rows still arrive. Delay, not loss. (A row whose redelivery
  falls in its last hours before expiry can still expire while it waits.)
- Many prefixes scale the budget linearly (N × 128 MiB / hour). There is no relay-wide
  redelivery ceiling (OD-R55F2-9); the early relay is IPv4-only (OD-R55F1-7 (c)), so N costs
  one /24 each.
- A relay restart refills every redelivery bucket (they are in memory, as every other
  limit): one burst per key and prefix per restart. Only the operator restarts the relay.
- First deliveries are not budgeted: a prefix can make the relay send what it uploaded, as it
  can already with direct forwarding.

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
  4.1p flags against one daemon, ≈ 8 against a busy office or carrier-grade NAT prefix; cheap
  with IPv6 /48s, OD-R55F1-7). Small frames still pass. See
  [Residuals](#attacks-from-review-55-after-r55-f1).
- (R55-F1) An attacker that out-sends a recipient's download speed (below 512 KiB/s) can get
  that recipient evicted while the outbound budget is full and its prefix is the heaviest; it
  reconnects and its mail is delivered again.
- (R55-F1) An attacker that pays ≈ 40 Mbit/s each way, sustained, from many prefixes can
  hold the outbound budget with sinks that read fast enough not to be evicted, and so delay
  direct mail while it pays (OD-R55F1-9).
- (R55-F2) An attacker behind the same /24 as an honest recipient can spend that prefix's
  redelivery budget; the recipient's unacked rows then wait their turn in the prefix's wait
  list, at most `--queue-max-total` ÷ the prefix rate (new rows still arrive). Many prefixes scale the redelivery budget linearly, and
  first deliveries are not budgeted (they cost the attacker the same upload). See
  [Residuals](#attacks-from-review-55-after-r55-f2).
- A bound account can spend its own team's quota (its teammates' problem, visible in the
  per-team counters).

## 3. Persistence, backup and restore (tickets 4.1a, 4.1b)

### One relay database

The hosted relay keeps everything in one SQLite file (`--db PATH`, replacing `--queue-db`,
which stays as an alias): the existing `queue` table plus the account, invite, quota,
telemetry and feedback tables of the other Phase 4 documents. WAL mode, `synchronous=FULL`
(as today), `secure_delete=ON`, and from R55-F2 `journal_size_limit` = 64 MiB (the WAL is
truncated back to it after checkpoints; a large sweep ends with a `TRUNCATE` checkpoint,
[§2](#expiry-sweep-in-bounded-batches-r55-011)). R3 (R55-F2) adds the table
`queue_delivered (to_key TEXT PRIMARY KEY, seq INTEGER NOT NULL)`, the delivered high-water
mark per recipient ([§2](#first-delivery-and-redelivery-r55-009)).

**Relay migrations.** The relay database gets its own `relay_migrations` table and numbering,
**R1…**, independent of the daemon's 1…21 (22 is the next daemon number). R1 adopts the
existing `queue` table as is (a relay started on an old file keeps its queue) and adds the
index `queue_by_sender (from_key, enqueued)` that the 4.0b caps need (review 50 M3).

**R3 and rollback (R55-F2, review 66b M2).** R3 is `R3_queue_delivered`; it takes the number
the Phase 4 plan had given `beta_invites`, whose tables move to R4 and the later ones up by one
([49-phase4-tickets.md](../review/49-phase4-tickets.md)). R3 is one idempotent
`CREATE TABLE IF NOT EXISTS`, so a half-applied run (R55-038) re-applies cleanly. A relay
binary older than R3 **refuses to open** an R3 database ("schema version 3 is newer than this
binary"), so rolling F2 back needs one of: restore the backup taken before the upgrade (the
deploy takes one: `relay backup` before replacing the binary), or, with the relay stopped,
`DELETE FROM relay_migrations WHERE version = 3; DROP TABLE queue_delivered;` with the
`sqlite3` tool. Both are safe: without H the next binary treats every queued row as a first
delivery once. Re-upgrading re-creates the table. The runbook
(`Docs/ops/early-relay-deploy.md`) gets both steps. Note that a newer `relay admin` run
against the database of an older running relay applies R3 too, and the older relay then
refuses to **restart**: upgrade the relay binary and `relay` admin tool together.

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
  The copy is made beside `--db`, synced and checked first, and only then moved over `--db`,
  so a corrupt backup leaves the existing database untouched. Stop the relay first: a running
  relay holds an exclusive lock on `<db>.lock` and restore refuses while it is held (R55-039,
  review 88 F5). A restore run as root gives the new file the owner of the existing database,
  or of its directory (review 88 F2).
- `relay backup` opens `--db` read-only and refuses a missing database and an existing
  `--out`; the output file is created mode 0600 before it is filled (R55-040, R55-199).
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
`Close` disconnects every peer, then waits up to 4 s for the read loops to finish the frame
each is handling (direct frames such as `pair.confirm` and `session.*` in flight at a restart
are routed or queued, not lost: `Close` first marks every connection draining, so a frame
routed to a peer that is already closing is queued and acknowledged `queued`, review 88 F3),
and only then closes the database; a connection that
authenticates after `Close` has begun is refused. The early relay's `TimeoutStopSec=20`
covers the 5 s HTTP shutdown, this wait and the 10 s drain wait (R55-036).

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
  refused by limit, queue rows and bytes, DB size, free disk, backup age. (Built so far:
connections, queue rows and bytes, redelivery counters, `relay_db_bytes` and
`relay_disk_free_bytes`; the rest, backup age included, arrives with 4.1b's backup job.
`/metrics` answers 500 with no values when the relay's stats cannot be read.) **No per-key or
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
- R55-F2 (full list in [66-r55-f2-spec.md](../review/66-r55-f2-spec.md#4-acceptance-tests)):
  a key that never acks is redelivered at most its budget (and its prefix at most the prefix
  budget) however often it reconnects, while new rows still reach it; an honest reconnect
  gets its unacked rows at once; skipped rows are retried when the budget refills, in wait-list
  order within a prefix; closing mid-batch replays nothing; a
  non-base64 payload gets `bad_envelope` and is not queued; a 1 GiB expiry is deleted in
  batches of ≤ 32 rows with the lock released between them and leaves a WAL ≤ 64 MiB.
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
  (c) For 4.1p only: no IPv6 service until (b) exists. The 4.1p VM **has** IPv6 today and
  publishes an AAAA record ([early-relay-deploy.md](../ops/early-relay-deploy.md) steps 4 and
  the DNS table). Removing the AAAA record alone is not enough: Caddy listens on `*:443`, so
  a client that finds the VM's v6 address (Hetzner's `<prefix>::1` convention makes it
  guessable) still connects. (c) means: drop the AAAA record **and** block TCP 443 on IPv6 in
  the Hetzner firewall (or bind Caddy to the IPv4 address).
  **Recommended: (a) now, with (c) for 4.1p (it has IPv6 today); revisit (b) before the
  public launch** (with the outside review, D41). (c) is an ops change: the code ticket
  updates `Docs/ops/early-relay-deploy.md` and `deploy/early/`, and the owner applies it on
  the VM.
- **OD-R55F1-8: charge bytes at read (R55-035).**
  (a) Charge every frame to the key's and prefix's byte buckets before parsing; drop it
  unparsed when over.
  (b) Charge only frames that fail `Classify`/`ParseHeader`/`from`, after parsing them once.
  (c) Leave R55-035 in the Low backlog.
  **Recommended: (a):** one rule, and it bounds the parse CPU a key can cause. With the
  review-56a exception: frames of at most 1 KiB (acks) are charged but never refused.
- **OD-R55F1-9 (review 56a): when is an outbound or ephemeral holder "not keeping up"?**
  Eviction needs a rule that separates an attacker's sink from an honest daemon that is
  receiving a lot. The oldest waiting frame's age grows with what is buffered, so a flat age
  punishes honest daemons with a full buffer.
  (a) Flat 2 s (the draft). An honest daemon draining a 4 MiB backlog over anything slower
  than 2 MiB/s (16 Mbit/s) is eligible, and with its 2 MiB reservation it holds a full 6 MiB
  share, so it is the heaviest holder there is: under a cheap fill (8 prefixes, ~50 KiB/s to
  keep sinks alive) it is evicted, reconnects, re-drains its queue from the start and is
  evicted again. An attacker needs ≈ 24 MiB/s to keep its own sinks ineligible.
  (b) `2 s + held ÷ 512 KiB/s` (14 s for 6 MiB). Honest daemons on ≥ 4 Mbit/s are never
  eligible. An attacker needs ≈ 4.8 MiB/s (≈ 40 Mbit/s) each way, sustained, to hold the
  outbound budget with ineligible sinks, and then delays direct mail while it pays.
  (c) `2 s + held ÷ 128 KiB/s` (50 s for 6 MiB). Protects slower honest links (≥ 1 Mbit/s);
  the attacker's price drops to ≈ 1.4 MiB/s (≈ 12 Mbit/s).
  **Recommended: (b).** (a) turns the fix against the honest daemons that receive the most;
  (c) makes holding the budget affordable again. The drain reservation counts towards what
  H holds but its own age never does, whichever option is chosen.

## Open decisions (R55-F2)

Each has a recommendation; the owner decides after the adversarial review.

- **OD-R55F2-1: what the new budget counts (R55-009).**
  (a) **Redeliveries only**, per recipient key and per recipient prefix; first deliveries are
  already paid for by the sender's upload.
  (b) **Every** byte delivered from the queue, first deliveries too. Simpler to explain, but an
  honest daemon coming online to a 32 MiB backlog, or an office prefix receiving many, is
  slowed although the relay's egress there equals its ingress.
  (c) No byte budget: a hold-back rule (a row is not re-sent to the same key within N minutes,
  nor more than K times). Needs per-row state written on every redelivery, and N × K has to
  be tuned against honest flapping links.
  **Recommended: (a).** It removes the amplification exactly and leaves honest first
  deliveries untouched.
- **OD-R55F2-2: what happens to a redelivery over budget.**
  (a) **Skip** it, go on with first deliveries (new mail), retry the skipped range once a
  minute when the buckets have refilled.
  (b) Pause the whole drain until the buckets refill. New mail to that key waits too, and an
  attacker in a shared /24 then delays all queued mail of its neighbours.
  (c) Close the connection 1013. The key reconnects (20 / min) and starts again.
  **Recommended: (a).**
- **OD-R55F2-3: budget sizes.**
  (a) Per key 32 MiB / hour (burst 32 MiB), per prefix 128 MiB / hour (burst 128 MiB).
  (b) 8 MiB / 32 MiB per hour: cheaper to attack, but an honest daemon that reconnects a few
  times while a 32 MiB backlog drains can hit it.
  (c) 128 MiB / 512 MiB per hour: generous for large offices, ≈ 1.2 Mbit/s per attacking
  prefix.
  **Recommended: (a).** It covers several honest reconnects mid-drain (≈ 6 MiB each) and keeps
  one attacking prefix at ≈ 0.3 Mbit/s.
- **OD-R55F2-4: where the "already delivered" state lives.**
  (a) A persistent high-water mark per recipient key, table `queue_delivered` (migration R3);
  one upsert per drained batch with a first delivery; pruned by the sweep.
  (b) A `delivered` column on each queue row plus an index `(to_key, delivered, seq)`. The
  column is after the frame blob, so without the index every check walks the blob's overflow
  pages; the index build reads the whole table once at migration.
  (c) In memory only. A relay restart makes every queued row a free first delivery again, and
  the map needs its own size bound.
  **Recommended: (a).** It relies on drains delivering in `seq` order, which the ordering
  rule of envelope.md already requires; a test pins it.
- **OD-R55F2-5: the relay's payload rule (R55-010).**
  (a) `payload` must be present and a JSON string without escape sequences whose content is
  standard base64 with padding (`""` allowed); checked for **every** envelope, presence
  included; checked in place, no decoded copy kept.
  (b) As (a), but also accept a missing or `null` payload, as the daemon's parser does today.
  (c) No relay change; only the daemon acks what it cannot parse (OD-R55F2-6).
  **Recommended: (a).** Every daemon builds envelopes with `envelope.Marshal`, which always
  writes a padded base64 string and never escapes it, so nothing honest is refused; and the
  relay's rule is stricter than the daemon's, so whatever the relay accepts the daemon can
  parse.
- **OD-R55F2-6: the daemon acks frames it cannot parse.**
  (a) Ack when `envelope.ParseHeader`'s decoding of the frame (the relay's own) yields a valid
  `from` and `id` and a non-ephemeral `type`; do not hand the frame up and do not add it to
  the seen-set; log one Warn per minute with a count.
  (b) Do not ack; rely on the relay refusing such frames. Rows queued before the relay is
  updated, and any other parse failure, keep the victim's queue full until the TTL.
  (c) As (a), and also show the count in `agentnet doctor`.
  **Recommended: (a).** The ack must name the `(from, id)` the relay checked at ingress, or
  it could delete someone else's row: see envelope.md §Client behaviour.
- **OD-R55F2-7: sweep batch.**
  (a) At most 32 rows per transaction (≤ 32 MiB, ≈ 0.35 s), lock released between batches, at
  most 30 s per tick.
  (b) Batches bounded by bytes (e.g. 8 MiB, with a running sum in the query): a steadier lock
  time with 1 MiB frames, many more transactions with small frames.
  (c) 256 rows per batch: fewer transactions, but the lock is held up to ≈ 2.7 s at a time.
  **Recommended: (a).**
- **OD-R55F2-8: WAL bound.**
  (a) `journal_size_limit` 64 MiB, plus a non-waiting `wal_checkpoint(TRUNCATE)` after a tick that deleted
  more than 64 MiB.
  (b) `journal_size_limit` only: the file shrinks only when the next checkpoint resets the
  WAL, which it does on the next write, so it can stay large while the relay is idle.
  (c) `wal_checkpoint(TRUNCATE)` after every tick that deleted anything: a checkpoint (and
  an fsync) every minute on a busy relay.
  **Recommended: (a).**
- **OD-R55F2-9: a relay-wide redelivery ceiling (review 66b).**
  (a) None: the redelivery egress grows with the number of attacking prefixes
  (N × 128 MiB / hour); on the IPv4-only early relay each costs a /24.
  (b) A third bucket, relay-wide (e.g. 2 GiB / hour ≈ 4.8 Mbit/s), served through the same
  wait lists: a hard ceiling on the uplink and the traffic allowance, but then enough
  prefixes delay every recipient's redeliveries, not only their neighbours'.
  (c) (a) now, (b) with accounts or before the relay gets an AAAA record.
  **Recommended: (c).** At 16 prefixes the egress is ≈ 5 Mbit/s (≈ 1.6 TB / month of a
  20 TB allowance); the ceiling's cost (relay-wide delay) is worth paying only once /48s
  make prefixes cheap.
- **OD-R55F2-10: order within a prefix (review 66b H2).**
  (a) A wait list per prefix, served in order; a key's wait is at most
  `--queue-max-total` ÷ the prefix rate (8 h on the 4.1p unit).
  (b) No order (the draft): an attacker behind the same NAT can take every refill, and the
  honest key's unacked rows can wait until they expire (loss).
  (c) Exempt a key from the prefix bucket once it has waited an hour: simpler, but every
  attacker key gets the same exemption, so one prefix again redelivers up to
  `--queue-max-total` an hour.
  **Recommended: (a).**
