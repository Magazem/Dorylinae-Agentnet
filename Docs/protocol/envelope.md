# Envelope and relay protocol

Status: v1, introduced by ticket 0.4. Implemented in `internal/envelope` (wire
types), `internal/relay` (server) and `internal/relayclient` (daemon side).

A daemon keeps one persistent WebSocket connection to a relay. Everything
that crosses it is a JSON text message ("frame"). There are two kinds:

- **Envelope frames**: a message from one daemon to another, routed by the relay.
- **Control frames**: relay <-> daemon housekeeping (challenge, auth, ready, error).
  They always carry an `op` field; envelopes never do.

Binary WebSocket messages are a protocol error.

## Peer identity

A peer is identified by its Ed25519 public key (RFC 8032, 32 bytes) encoded as
base64url without padding (43 characters), the same form as `public_key` in the
[Agent Card](agent-card.md). Wherever this document says "key", it means that string.

## Envelope

```json
{
  "from": "<key>",
  "to": "<key>",
  "team": "backend",
  "type": "ping",
  "id": "01J9Z3M8Q2V6X0N4",
  "ts": "2026-01-02T03:04:05.123Z",
  "payload": "<base64>"
}
```

| Field | Type | Rules |
|-------|------|-------|
| `from` | string | Sender's key. The relay requires it to equal the key that authenticated the connection |
| `to` | string | Recipient's key. The relay routes on this and nothing else |
| `team` | string | Team label, `""` allowed (teams arrive in Phase 1). At most 128 characters from `[A-Za-z0-9._:-]` |
| `type` | string | Message type, 1-64 characters from `[a-z0-9._-]`, e.g. `ping` |
| `id` | string | Sender-chosen identifier, 1-128 characters from `[A-Za-z0-9._:-]`. Unique per sender; used to correlate error frames |
| `ts` | string | Sender's clock, RFC 3339. The relay checks that it parses, not that it is fresh |
| `payload` | string | Opaque bytes, standard base64 (RFC 4648, with padding). May be `""` |

### Envelope types

The relay routes every type the same way. It never interprets `type`. The daemon
dispatches on it:

| `type` | Payload | Spec |
|--------|---------|------|
| `session.init`, `session.resp`, `session.fin`, `session.data` | Noise XX handshake / transport (interactive traffic only: ping, later live streams) | [session.md](session.md) |
| `pair.confirm` | Pairing v2 key confirmation, `{"lookup","tag","v":2}` as canonical JSON (a MAC, not encrypted). Accepted only for a pending pairing, so it is exempt from the "paired peers only" rule | [pairing.md](pairing.md#pairconfirm-envelope) |
| `mail` | `0x01 ‖ key_id(8) ‖ HPKE enc(32) ‖ ct`: every application message (requests, results, grants, acks, key updates). The kind is inside the ciphertext | [mail.md](mail.md#envelope) |
| `presence` | Same layout as `mail`, kind `presence`, id `p-…`. **Ephemeral** (Phase 1, 1.2a): never queued, see below | [presence.md](presence.md#relay-ephemeral-envelopes) |

A daemon drops envelopes of any other type, and envelopes of types other than
`pair.confirm` from keys that are not paired peers.

The character sets keep `team`, `type` and `id` safe to log. They are metadata,
not content: do not put secrets in them.

**Payload is opaque to the relay.** The relay parses only the routing fields,
never decodes `payload`, never logs it, and forwards the frame it received
**byte for byte**: it does not re-encode, reorder or add fields. Unknown extra
fields are forwarded untouched. From ticket 0.6 the payload is ciphertext
(see [session.md](session.md)).

Maximum frame size is 1 MiB (`1048576` bytes). A larger frame closes the
connection (WebSocket close code 1009).

## Connection and authentication

Endpoint: `GET /v1/connect`, upgraded to WebSocket. A loopback relay may use
plain `ws://`; a remote one uses `wss://` (from Phase 4, ticket 4.0a: the daemon
refuses `ws://` to a non-loopback host, see
[relay-hosted.md](relay-hosted.md#daemon)). The daemon does not follow HTTP
redirects on this request: a redirect is a connection error, so the origin it
signs (below) is always the one it dialled. `GET /healthz` is the relay's
unauthenticated health check (`200 {"ok":true,"version":"…"}`, or `503` when
its database does not answer).

```
daemon                                relay
  |--- WebSocket upgrade ------------->|
  |<-- {"op":"challenge",...} ---------|   random nonce, valid for a short time
  |--- {"op":"auth",...} ------------->|   public key + signature over the nonce
  |<-- {"op":"ready",...} -------------|   connection is now in the registry
  |<== envelopes both ways ===========>|
```

### `challenge` (relay -> daemon)

```json
{"op":"challenge","version":1,"nonce":"<base64url, 32 bytes>","expires":"2026-01-02T03:04:15Z","auth":["v1","v2"]}
```

The nonce is 32 random bytes generated per connection and usable once, on that
connection only. `expires` is informational; the relay enforces its own clock
(default 10 seconds after issuing).

`auth` (Phase 4, ticket 4.0a) lists the relay authentication versions the relay
accepts on this connection: `"v2"` when it knows at least one of its own origins,
`"v1"` unless it is a **public** relay without `--allow-auth-v1`
([relay-hosted.md](relay-hosted.md#relay)). A challenge without `auth` comes
from a relay that predates the list and accepts v1 only.

### `auth` (daemon -> relay)

v2 (Phase 4):

```json
{"op":"auth","v":2,"public_key":"<key>","signature":"<base64url, 64 bytes>"}
```

`signature` is an Ed25519 signature over the bytes

```
"dorylinae-relay-auth-v2\n" || nonce(32) || u16be(len(origin)) || origin
```

`origin` is the relay's origin as the daemon dialled it:
`lowercase(scheme "://" host [":" port])` of the **configured** relay URL,
with an IDN host in its ASCII (punycode) form, no trailing dot, IPv6 literals in
brackets, the port omitted exactly when it is the scheme default (`wss` 443,
`ws` 80), and no path, query or user info. So `wss://Relay.Example.COM:443/v1/connect`
signs `wss://relay.example.com`. The relay accepts a v2 signature for any of its
own origins (`relay --public-origin`, or its loopback names on a local relay).
Naming the origin is what stops a hostile relay from forwarding another relay's
challenge and logging in there as the daemon (relay-in-the-middle,
[relay-hosted.md](relay-hosted.md#relay-authentication-v2-binds-the-relays-name)).

v1 (Phase 0–3 daemons; no `v`, or `"v":1`):

```json
{"op":"auth","public_key":"<key>","signature":"<base64url, 64 bytes>"}
```

signed over `"dorylinae-relay-auth-v1\n" || nonce`. A v1 signature does not
name the relay.

In both versions `nonce` is the 32 decoded bytes, and the domain prefix makes
the signature useless in any other protocol context (agent cards use a
different prefix). `auth` must be the first frame the daemon sends.

**Which version the daemon signs** (no downgrade): v2 whenever the challenge
offers it. v1 only if the relay URL's host is loopback (`localhost`,
`127.0.0.0/8`, `::1`) and the challenge has no `auth` list or lists `"v1"`.
For any other URL the daemon signs nothing and the connection fails with
"relay does not support auth v2", whatever the challenge offers: otherwise a
hostile relay could strip `v2` from a challenge it forwards and replay the v1
answer.

The relay rejects the connection if the signature is invalid (for v2: made for
none of its origins), the version was not offered, the challenge has expired,
the first frame is not `auth`, or nothing arrives in time. A signature captured
from another connection is invalid because each connection has a fresh nonce.
On rejection the relay sends an `error` frame with code `auth_failed` (the
message never says which check failed), then closes with WebSocket close code
1008. One exception to the message rule: a **valid** v1 signature on a relay
that requires v2 gets the message "this relay requires relay auth v2; update
agentnet", and is not counted as a failed authentication. Frames before
authentication are limited to 4 KiB.

#### Relay auth v2 vector

Key `key_I` of [pairing.md](pairing.md) (seed `00 01 … 1f`), nonce `80 81 … 9f`
(`gIGCg4SFhoeIiYqLjI2Oj5CRkpOUlZaXmJmam5ydnp8`). Printed by
`go run ./tools/specvectors`, recomputed independently by
`go run ./tools/verifyvectors` (`vectors.json`, `relay_auth`), and checked
against `internal/envelope` by its tests.

| Configured URL | Origin | Signature (base64url) |
|---|---|---|
| `wss://Relay.Example.COM/v1/connect` | `wss://relay.example.com` | `VD-DbBEUg3A3UOgbG-wGq9W2ILO_SPA4GaYHCgrWqp7CTh0jqNjzR2g-9oQJws_VKTzNHFF_nYZot79-g9LODw` |
| `wss://relay.example.com:443` | `wss://relay.example.com` | same as above |
| `wss://[2001:DB8::1]:8443/v1/connect` | `wss://[2001:db8::1]:8443` | `5HmkkA1Fv28kbJBDFqGq2ry7wnoY0ecNvWSk1GYfwuOEeiCFbKvb1KbKUq49sQ296o3g4SEa1vECrtQ_8r3eBw` |
| `wss://relay.example.com.:8443` | `wss://relay.example.com:8443` | `ua_ZqQqx7gn0yhmCbj0o8KCMhqMPKfYk9dMb-BhVQ8cuWNMuHbN8B56qcpBn1cH3Qm63f7uynFx3trXnbIwMBw` |
| `ws://127.0.0.1:8787` | `ws://127.0.0.1:8787` | `Qf-_-YXx26uqkjD1V4H3avV2LjGSgUQpIwR4RM4ql7vRMHWM73CNcy8wmnI2ZWJRsRU9SH5h8n4EyQ4gJqUKDg` |

Signed message for the first row (hex):

```
646f72796c696e61652d72656c61792d617574682d76320a
808182838485868788898a8b8c8d8e8f909192939495969798999a9b9c9d9e9f
0017
7773733a2f2f72656c61792e6578616d706c652e636f6d
```

Auth frame for the first row:

```json
{"op":"auth","v":2,"public_key":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","signature":"VD-DbBEUg3A3UOgbG-wGq9W2ILO_SPA4GaYHCgrWqp7CTh0jqNjzR2g-9oQJws_VKTzNHFF_nYZot79-g9LODw"}
```

### `ready` (relay -> daemon)

```json
{"op":"ready","public_key":"<key>"}
```

From Phase 1 (1.2a) the relay adds `"features": ["ephemeral"]`. **Ephemeral** envelope
types (only `presence`) are forwarded only to a connected recipient whose send buffer is at
most half full, regardless of backlog. Otherwise they are dropped silently: no `queued`, no
`error`, never stored. The recipient does not `ack` them, and `relayclient` hands them up
without the seen-set. Rate limit: 600 per minute per sender. See [presence.md](presence.md#relay-ephemeral-envelopes).

From Phase 4 (4.2a) a relay started with `--accounts github|email|both` adds the feature
`accounts` and an `account` member with the key's account state (`unbound`, `bound`,
`suspended`). Such a relay routes an envelope only between bound keys of active accounts,
serves the binding frames `bind_start`, `bind_poll`, `bind_cancel` and `unbind`, and sends
`account_changed`; everything about it is in [accounts.md](accounts.md). A daemon sends
those frames only when `ready` lists `accounts`.

From Phase 4 (ticket 4.4a) the relay may add `"min_client": "MAJOR.MINOR.PATCH"`, the
oldest daemon release it supports (`relay --min-client`). It is **advisory**: the relay
never refuses a connection because of it, and the daemon keeps the connection. A daemon
keeps the value only if it is exactly `MAJOR.MINOR.PATCH` (decimal, no leading zeros; the
text comes from the relay, so anything else is dropped, never logged or shown). If its own
version is a release older than it, the daemon logs a warning, and `status` / `doctor`
report it ([../cli/status.md](../cli/status.md), [../cli/doctor.md](../cli/doctor.md)). A dev
build (not `MAJOR.MINOR.PATCH`) is not compared: `doctor` warns that it cannot tell. Absent
means no minimum; older daemons ignore the member.

### One connection per key

If a key connects while it already has a live connection, the new connection
wins and the old one is closed with code 1008 and reason `replaced`.

## Forwarding

After `ready`, the daemon sends envelope frames. For each one the relay:

0. From R55-F1, charges the frame's bytes to the sender's and its prefix's byte rates
   before parsing it ([relay-hosted.md §2](relay-hosted.md#bytes-charged-at-read-r55-035)).
   Over either: `error` frame `rate_limited` with an empty `ref`, dropped unparsed.
1. Parses the routing fields. Invalid: `error` frame `bad_envelope`, frame dropped.
2. Checks `from` equals the authenticated key. Otherwise: `error` frame `bad_sender`, dropped.
3. From ticket 4.0b, charges a non-ephemeral envelope to the sender's and its network
   prefix's send rates ([relay-hosted.md §2](relay-hosted.md#2-abuse-limits-ticket-40b));
   from R55-F1 the envelope counts only, the bytes having been charged at step 0.
   Over either: `error` frame `rate_limited` (`ref` = the envelope id), dropped; the
   connection stays open.
4. Looks `to` up in its in-memory registry. If the recipient is connected, has
   no backlog and has room in its send buffer (64 frames and, from 4.0b, 4 MiB, within
   the relay-wide `--max-inflight`), forwards the original bytes to that connection and
   stays silent.
5. Otherwise (not connected, still receiving a backlog, or send buffer full)
   stores the envelope in the [offline queue](#offline-queue) and answers the
   sender with a `queued` frame. If the queue refuses it, an `error` frame
   `queue_full` (or `internal`) is sent instead and the envelope is dropped.

Frames from one sender to one recipient arrive in the order sent, including
across the queue: an envelope is never forwarded directly while older ones for
the same recipient are still queued.

Errors on a single envelope never close the connection. Load shedding is not an error on an
envelope: from R55-F1 a connection may be closed 1013 when it holds the most of a spent
memory budget (eviction), or when one frame takes longer than `--frame-read-timeout` to
arrive ([relay-hosted.md §2](relay-hosted.md#memory-budgets-and-fairness-r55-f1)); the daemon
reconnects with its normal backoff. A daemon that sends a
control frame after `auth` other than `ack` and the pairing requests `pair_new`,
`pair_redeem` and `pair_cancel` (see [pairing.md](pairing.md)), and on a relay with accounts
the binding frames of [accounts.md](accounts.md), or a binary message has its
connection closed with code 1008. On a relay with accounts, a frame or envelope the key's
account state does not allow gets `account_required` instead and the connection stays open
([accounts.md](accounts.md#relay-states-of-a-connection)). From 4.0b control frames other than `ack` are limited to
60 a minute per key: past it each gets `rate_limited` (`ref` = the frame's `ref`), and a key
over the limit in three one-minute windows in a row is closed with 1008.

## Offline queue

Introduced by ticket 0.7. It replaces the earlier behaviour of answering
`peer_offline` and dropping the envelope.

### Semantics

- **What is stored.** The relay stores the frame exactly as received, plus the
  routing metadata it already sees (`from`, `to`, `id`) and the time it was
  queued. The payload stays opaque: it is never decoded, and from ticket 0.6 it
  is ciphertext anyway. Like the rest of the relay's state, nothing in the queue
  is logged.
- **Queued acknowledgement.** The sender gets `{"op":"queued","ref":"<id>"}`
  once the envelope is durably stored. `queued` means "the relay has it", not
  "the peer has it"; end-to-end delivery is still confirmed by whatever the
  layer above does (for example a pong).
- **Delivery.** When a peer authenticates, and whenever something new is queued
  for a connected peer, the relay sends its queued envelopes oldest first, as
  ordinary envelope frames, before forwarding anything newer directly. Frames
  are byte-for-byte what the sender sent.
- **Acknowledgement and deletion.** The daemon confirms each envelope it
  receives with `{"op":"ack","from":"<sender key>","ref":"<envelope id>"}`.
  Only then does the relay delete its copy. Only the recipient can ack: the
  relay deletes rows addressed to the key that authenticated the connection, so
  an `ack` naming someone else's envelope does nothing. Acks for unknown
  envelopes (every directly forwarded envelope is acked too) are ignored.
- **Exactly once.** The relay delivers at least once: an envelope whose ack is
  lost, or that was sent but not acked when the connection dropped, is sent
  again on the next connection. The daemon (`internal/relayclient`) keeps the
  last 8192 `(from, id)` pairs it handed up and drops repeats, still acking
  them. Together that is exactly-once delivery to the daemon's handlers within
  that window. **Exception (ticket 1.0d):** envelopes of type `mail` bypass this
  set and are handed up every time, because the mail layer dedupes persistently
  and must see resends to re-ack them ([mail.md](mail.md#receiving-verification-order)). The window is in memory, so a daemon restarted between handling
  an envelope and acking it can see it again; the session layer's own replay
  protection ([session.md](session.md)) is the backstop, and for `mail` the
  receiver's persistent `(from, id)` dedupe ([mail.md](mail.md#dedupe-and-inbox)).
- **Sender retries.** Queueing the same `(from, to, id)` twice stores it once
  and answers `queued` both times, so a sender may safely retry an envelope it
  never saw acknowledged.
- **Expiry.** An envelope older than the TTL (default 7 days, `Options.QueueTTL`)
  is never delivered, and a sweep (every minute by default) deletes it. The TTL
  counts from the time the relay queued it.
- **Limits.** One recipient may have at most 1000 envelopes and 32 MiB
  waiting (`Options.QueueMaxEnvelopes`, `Options.QueueMaxBytes`). From ticket 4.0b
  also: one sender may have at most 300 envelopes / 8 MiB waiting for one recipient,
  2000 envelopes / 64 MiB for all recipients together, and the whole queue holds at
  most 4 GiB (`--queue-max-total`); see
  [relay-hosted.md §2](relay-hosted.md#offline-queue-m2). Beyond any of these,
  the sender gets `queue_full` and the envelope is dropped. With under 1 GiB free
  on the disk holding the queue file, new envelopes get `internal` ("relay storage
  low"); acks and deletes still work. The relay does not know who is paired with
  whom, so any authenticated key can queue for a recipient up to these limits; keys
  that are not paired with the recipient are refused by the daemon's session layer,
  not by the relay. The per-sender caps mean one key cannot fill a victim's queue
  alone; many fresh keys still can on a relay without accounts (OD-P4-21).
- **Slow recipients.** A connected peer whose send buffer is full no longer
  gets `peer_busy` and a dropped envelope: the envelope is queued behind the
  peer's backlog like an offline one.
- **Direct forwarding is still best effort.** An envelope forwarded straight to
  an idle, connected peer is not stored first. If that connection has silently
  died, the envelope is lost as before; only queued envelopes get the
  ack-and-redeliver treatment.
- **Restart.** With a database file (`Options.QueuePath`) the queue survives a
  relay restart. Without one the queue lives in memory: envelopes are queued
  and delivered as above but are lost when the relay stops.

### Storage

A single SQLite table, `queue(seq, to_key, from_key, id, enqueued, frame)`,
`seq` being an ever-increasing integer that defines delivery order, with a
unique index on `(to_key, from_key, id)`. A relay process owns its database; do
not share one file between relays. No cap check scans the table: the per-recipient
and per-pair counts are index searches, and the per-sender and relay-wide totals are
kept in memory, rebuilt by one scan at start-up and adjusted on add, ack and sweep.

### Frames

`queued` (relay -> daemon):

```json
{"op":"queued","ref":"01J9Z3M8Q2V6X0N4"}
```

`ack` (daemon -> relay):

```json
{"op":"ack","from":"<sender key>","ref":"01J9Z3M8Q2V6X0N4"}
```

## `error` frame (relay -> daemon)

```json
{"op":"error","code":"queue_full","message":"recipient's offline queue is full","ref":"01J9Z3M8Q2V6X0N4"}
```

| Field | Meaning |
|-------|---------|
| `code` | Machine-readable, one of the table below |
| `message` | Human-readable, never contains payload or frame contents |
| `ref` | The `id` of the envelope that caused it; omitted when unknown or not applicable |

| Code | Meaning |
|------|---------|
| `auth_failed` | Authentication rejected; connection is closed |
| `bad_envelope` | Frame is not a valid envelope (bad JSON, missing or malformed field) |
| `bad_sender` | `from` does not match the authenticated key |
| `queue_full` | A queue cap refused the envelope (the recipient's, this sender's for the recipient or overall, or the relay-wide total); envelope dropped |
| `internal` | The relay could not store the envelope (message `relay storage low` when the disk is nearly full); dropped |
| `rate_limited` | 4.0b: a send rate refused this envelope or control frame (`ref` names it; dropped, connection stays open), or, right after `auth` with no `ref`, the key reconnects too often (then close 1013). Retry later |
| `relay_full` | 4.0b: right after `auth`, the relay is at its connection cap (`--max-conns`) or this network prefix at its distinct-key cap; close 1013. Retry later with the normal reconnect backoff |
| `peer_offline` | Pairing only: the code's issuer is not connected. No longer used for envelopes |
| `peer_busy` | No longer sent (see [Offline queue](#offline-queue)); pairing replies may still use it |
| `pair_invalid`, `pair_rate_limited`, `pair_limit`, `pair_lookup_taken`, `pair_v1_disabled`, `bad_pairing` | Pairing failures, see [pairing.md](pairing.md#errors) (`peer_offline` / `peer_busy` are also used there) |
| `account_required`, `account_suspended`, `account_revoked`, `already_bound`, `bind_expired`, `bind_denied` | 4.2a, relays with accounts only: see [accounts.md](accounts.md). `account_suspended` and `account_revoked` are followed by close 1008 |

**The daemon's reading (R55-F9).** The relay is untrusted, so the daemon never takes an
`error` frame's strings as they arrive. `internal/relayclient` converts every `error`
frame exactly once, in one function, and every consumer (the handshake, `OnError` → outbox,
pairing and sessions) receives only the converted form:

- **`code`** is kept only if it is exactly one of the codes in the table above. Anything else
  (unknown, empty, or with any other byte) becomes the fixed code `relay_error`. A newer relay
  code therefore reaches an older daemon as `relay_error`; the outbox treats it as it treats
  any code it does not list (the row stays `relayed`).
- **`message`** becomes `displayLine(message, 200)`, the shared one-line rule of
  [approval.md §Sanitising](approval.md#sanitising-one-character-rule-two-renderings):
  every `hidden(r)` rune is removed (the ones that render as space become one space), runs of
  spaces collapse, at most 2 combining marks stay on one base, the result is trimmed and cut
  to 200 bytes on a rune boundary (a cut adds `…`). It can therefore hold no control
  character, no escape sequence, no line break and no bidi control. The daemon reads at most
  the first 4 KiB of a longer message, so a 1 MiB message costs no more than a short one.
- **`ref`** is kept only if it is a valid envelope `id` (1–128 characters from
  `[A-Za-z0-9._:-]`); otherwise it is empty, so the frame refers to nothing.

The relay's own `message` is advisory text, never a basis for a decision.

## Logging rule

The relay logs connection and routing events with abbreviated keys (first 8
characters), `type`, `id` and byte counts, including `queue`, `queue_flush`
and `queue_expire` events for the offline queue. It never logs `payload`, nor the
raw frame. `internal/relay` has a test that fails if a payload marker appears in
its log output. An abuse limit that refuses something logs `event=limit` with the limit
name and an abbreviated key (`peer=`) or a /24 (/48 IPv6) prefix (`prefix=`), never an id
or payload, at most once a minute per limit and subject (`suppressed_before` counts the
repeats in between).

## Client behaviour (daemon)

`internal/relayclient` holds one persistent connection. On any failure it
reconnects with exponential backoff (500 ms doubling to 30 s, with jitter). The
backoff resets only after a connection **stayed up for at least 30 s after `ready`**
(R55-F9, review 55 C04-02). A connection that fails earlier is a failure like any
other, even though it authenticated: the delay keeps doubling. Otherwise a relay that sends
`ready` and closes at once would drive a reconnect about twice a second, each with a TLS
handshake, a signature, an outbox re-send and a presence send. `relay_full` or `rate_limited`
in place of `ready` is such a failure (the connection never became ready), so it is
retried with the growing backoff. The mail outbox treats `rate_limited` and
`relay_full` naming one of its rows like `queue_full`: back to queued, resent after
its backoff ([mail.md](mail.md)). Sending while disconnected
fails immediately with `ErrNotConnected`; nothing is buffered on the daemon side
(the relay's [offline queue](#offline-queue) buffers for the *recipient*, not the sender).

For every envelope received the client calls `OnEnvelope` (unless it has already
handed up the same `(from, id)` and the type is not `mail`, see the queue section) and
then sends the `ack`.
`queued` frames are delivered to `OnQueued`, not `OnControl`.

An envelope whose `to` is not the client's own key (R55-F9, review 55 C04-03) is not handed
up and does not enter the seen-set. It is still acked, so the relay does not redeliver it,
and it is logged at Debug only (`event=relay_misrouted`, `type`, `id`). Every consumer binds
the recipient inside its crypto anyway (mail `msg.to`, Noise sessions), so this is defence
in depth for a future type that would not.

### Relay-supplied text (daemon)

Every string the relay chooses is bounded and made display-safe before the daemon stores,
logs, audits or serves it over IPC. Parse once in `internal/relayclient`; consumers never
re-read the raw frame.

| Relay text | Rule |
|------------|------|
| `error` `code`, `message`, `ref` | [The daemon's reading](#error-frame-relay---daemon) above |
| `ready.min_client` | Kept only if it is exactly `MAJOR.MINOR.PATCH` (4.4a, above) |
| `ready.features` | Only compared against known feature names; never logged or shown |
| `ready.account` (4.2c, not yet read) | `state` kept only if it is `unbound`, `bound` or `suspended`; `display` goes through `displayLine` and is cut to 128 bytes before it is stored or shown (review 55 C04-04) |
| The `op` of an unexpected control frame | Never echoed: the handshake error is `unexpected frame from relay` |
| WebSocket close reason | Never kept: a close is reported as `closed by relay (status N)` |
| HTTP headers in a failed upgrade | Covered by the `last_error` bound below |

**`last_error`** (`State().LastError`, served as `status.relay.last_error`) is content-free
([../cli/status.md](../cli/status.md)):

- For an `error` frame in place of `challenge` or `ready`: `relay: <code>`, with the
  converted code only, never the message.
- For any other connection error: its text rendered with `displayLine` and cut to 256
  bytes on a rune boundary (a cut adds `…`).

The `relay_disconnect` log line carries this same string as `error`. So one relay
connection writes at most a few hundred bytes to the daemon log, not up to 1 MiB (review 55
T5-01). How often such lines may appear is ticket R55-F14's rule.
