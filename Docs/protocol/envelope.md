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

Endpoint: `GET /v1/connect`, upgraded to WebSocket. Phase 0 runs the relay
locally over plain `ws://`; TLS (`wss://`) arrives with the hosted relay.

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
{"op":"challenge","version":1,"nonce":"<base64url, 32 bytes>","expires":"2026-01-02T03:04:15Z"}
```

The nonce is 32 random bytes generated per connection and usable once, on that
connection only. `expires` is informational; the relay enforces its own clock
(default 10 seconds after issuing).

### `auth` (daemon -> relay)

```json
{"op":"auth","public_key":"<key>","signature":"<base64url, 64 bytes>"}
```

`signature` is an Ed25519 signature over the bytes

```
"dorylinae-relay-auth-v1\n" || nonce
```

where `nonce` is the 32 decoded bytes. The domain prefix makes the signature
useless in any other protocol context (agent cards use a different prefix).
It must be the first frame the daemon sends.

The relay rejects the connection if the signature is invalid, the challenge has
expired, the first frame is not `auth`, or nothing arrives in time. A
signature captured from another connection is invalid because each connection
has a fresh nonce. On rejection the relay sends an `error` frame with code
`auth_failed` (the message never says which check failed), then closes with
WebSocket close code 1008. Frames before authentication are limited to 4 KiB.

### `ready` (relay -> daemon)

```json
{"op":"ready","public_key":"<key>"}
```

### One connection per key

If a key connects while it already has a live connection, the new connection
wins and the old one is closed with code 1008 and reason `replaced`.

## Forwarding

After `ready`, the daemon sends envelope frames. For each one the relay:

1. Parses the routing fields. Invalid: `error` frame `bad_envelope`, frame dropped.
2. Checks `from` equals the authenticated key. Otherwise: `error` frame `bad_sender`, dropped.
3. Looks `to` up in its in-memory registry. If the recipient is connected, has
   no backlog and has room in its send buffer, forwards the original bytes to
   that connection and stays silent.
4. Otherwise (not connected, still receiving a backlog, or send buffer full)
   stores the envelope in the [offline queue](#offline-queue) and answers the
   sender with a `queued` frame. If the queue refuses it, an `error` frame
   `queue_full` (or `internal`) is sent instead and the envelope is dropped.

Frames from one sender to one recipient arrive in the order sent, including
across the queue: an envelope is never forwarded directly while older ones for
the same recipient are still queued.

Errors on a single envelope never close the connection. A daemon that sends a
control frame after `auth` other than `ack` and the pairing requests `pair_new`,
`pair_redeem` and `pair_cancel` (see [pairing.md](pairing.md)) or a binary message has its
connection closed with code 1008.

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
  that window. The window is in memory, so a daemon restarted between handling
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
  waiting (`Options.QueueMaxEnvelopes`, `Options.QueueMaxBytes`). Beyond that,
  senders get `queue_full` and the envelope is dropped. The relay does not know
  who is paired with whom, so any authenticated key can fill a recipient's
  queue up to these limits; keys that are not paired with the recipient are
  refused by the daemon's session layer, not by the relay.
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
not share one file between relays.

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
| `queue_full` | The recipient already has the maximum number of envelopes queued; envelope dropped |
| `internal` | The relay could not store the envelope; dropped |
| `peer_offline` | Pairing only: the code's issuer is not connected. No longer used for envelopes |
| `peer_busy` | No longer sent (see [Offline queue](#offline-queue)); pairing replies may still use it |
| `pair_invalid`, `pair_rate_limited`, `pair_limit`, `pair_lookup_taken`, `pair_v1_disabled`, `bad_pairing` | Pairing failures, see [pairing.md](pairing.md#errors) (`peer_offline` / `peer_busy` are also used there) |

## Logging rule

The relay logs connection and routing events with abbreviated keys (first 8
characters), `type`, `id` and byte counts, including `queue`, `queue_flush`
and `queue_expire` events for the offline queue. It never logs `payload`, nor the
raw frame. `internal/relay` has a test that fails if a payload marker appears in
its log output.

## Client behaviour (daemon)

`internal/relayclient` holds one persistent connection. On any failure it
reconnects with exponential backoff (500 ms doubling to 30 s, with jitter); the
backoff resets once a connection has authenticated. Sending while disconnected
fails immediately with `ErrNotConnected`; nothing is buffered on the daemon side
(the relay's [offline queue](#offline-queue) buffers for the *recipient*, not the sender).

For every envelope received the client calls `OnEnvelope` (unless it has already
handed up the same `(from, id)`, see the queue section) and then sends the `ack`.
`queued` frames are delivered to `OnQueued`, not `OnControl`.
