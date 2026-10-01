# Encrypted sessions (Noise XX)

Status: v1, introduced by ticket 0.6. Implemented in `internal/noise`
(handshake, static-key binding, transport nonces) and `internal/session`
(envelope framing, session table, ping). Change this document first.

> Not to be confused with the Phase 2 **work session** (`s-…` ids, `agentnet sessions`),
> the persisted unit of work an accepted request becomes: see [work-session.md](work-session.md).
> This document covers only the Noise transport session.

Two **paired** daemons (see [pairing.md](pairing.md)) talk end to end through
the relay inside a Noise session. The relay routes the envelopes of
[envelope.md](envelope.md) unchanged; after the handshake every envelope
payload is ciphertext and the relay cannot read, forge or undetectably change
it.

**Scope: interactive traffic only.** Sessions carry ping and, later, live
streams, which are useful only while both daemons are online. Application messages
(requests, decisions, results, grants, acks) do **not** use sessions. They travel
as sealed [mail](mail.md), which needs no session and survives either daemon
being offline. Session state is never persisted.

## Protocol name and keys

```
Noise_XX_25519_ChaChaPoly_SHA256
```

(flynn/noise). Each daemon has three kinds of key:

| Key | Kind | Lifetime | Purpose |
|-----|------|----------|---------|
| Identity key | Ed25519 | permanent, in the keystore | Who the agent is; in the Agent Card; authenticates to the relay |
| Noise static key | X25519 | generated at daemon start, memory only | The `s` key of the handshake |
| Noise ephemeral key | X25519 | one handshake | The `e` key; gives each session its own keys (forward secrecy) |

Every completed handshake produces fresh per-session transport keys (Noise
`Split()`), one per direction. Nothing session-related is written to disk.

## Binding the Noise static key to the identity

Noise XX authenticates that the peer holds the private half of its static key
`s`, but `s` is a bare X25519 key. It is bound to the Agent Card identity by a
signature from the identity key:

```
binding_sig = Ed25519-Sign(identity_private_key,
                           "dorylinae-noise-static-v1\n" || s_public)   // s_public: 32 bytes
```

The daemon computes this once at start. It is carried as the **encrypted
handshake payload** of messages 2 (responder) and 3 (initiator):

```json
{"v":1,"identity":"<key>","sig":"<base64url, 64 bytes>"}
```

`<key>` is the identity key in the wire form used everywhere (base64url, no
padding). On reading message 2 or 3 the receiver checks, and aborts the
handshake if any check fails:

1. The payload parses and `v` is `1`.
2. `identity` equals the envelope's `from` (the key the relay authenticated) and
   the key the handshake was started with/for.
3. `identity` is a paired peer (in the `peers` table).
4. `sig` verifies under `identity` over the domain string and the static key
   Noise just authenticated (`PeerStatic()`).

Why this is enough: a captured binding is useless without the static private
key, which never leaves the daemon's memory, and XX proves possession of it
(the `es`/`se` DH operations). The domain prefix keeps the signature from being
valid as a relay auth or Agent Card signature, and vice versa.

The **prologue** binds the handshake to both routing identities, so a relay
that rewrites `from`/`to` breaks the handshake:

```
prologue = "dorylinae-noise-xx-v1\n" || initiator_identity || "\n" || responder_identity
```

(identities in wire form, ASCII.)

## Envelopes

Session traffic uses the unchanged 0.4 envelope. `type` says what the payload
is; `payload` is binary as below (base64 on the wire as always). `sid` is a
16-byte random session ID chosen by the initiator.

| `type` | Direction | `payload` |
|--------|-----------|-----------|
| `session.init` | initiator -> responder | `sid` (16) \|\| Noise message 1 (`e`, empty payload) |
| `session.resp` | responder -> initiator | `sid` (16) \|\| Noise message 2 (`e, ee, s, es` + binding) |
| `session.fin`  | initiator -> responder | `sid` (16) \|\| Noise message 3 (`s, se` + binding) |
| `session.data` | either | `sid` (16) \|\| `counter` (8, big-endian) \|\| AEAD ciphertext |

Message 1 is the only handshake message with no encrypted part; it carries
only a fresh ephemeral public key. Handshake payloads other than the bindings
are empty.

### Transport messages

`session.data` is encrypted with the sender's direction key (ChaCha20-Poly1305)
using the explicit `counter` as the Noise nonce, and with associated data

```
"dorylinae-session-data-v1\n" || from || "\n" || to || "\n" || sid || counter
```

so the relay cannot move a ciphertext to another session, direction or
position. Counters start at 0 and increase by one per message; `2^64-1` is
never used (the session must be re-established before that).

### Replay and reordering

The receiver keeps, per session and direction, the next acceptable counter.
A message is accepted only if

1. its `counter` is **greater than or equal to** that value (otherwise it is a
   replay or arrived out of order: rejected with reason `replay`), and
2. it decrypts and authenticates (otherwise: reason `decrypt`).

Only an accepted message moves the counter forward (to `counter + 1`). A
tampered or dropped message therefore leaves a gap but does not break the
session: later valid messages still decrypt. The relay forwards one
connection's frames in order, so honest traffic never trips the check.

### Plaintext of `session.data`

```json
{"type":"ping","id":"ping-..."}
{"type":"pong","id":"ping-..."}
```

`fetch.req` and `fetch.resp` ([grant.md §Transport](grant.md#transport)) are handled through
registered handlers (`Manager.Handle`; replies use `Manager.SendData`, which never starts a
handshake); a handler runs on the receive goroutine and must hand slow work to its own workers.
Unknown types are ignored. Later tickets add types; the envelope `type` stays
`session.data` so the relay learns only that a session message was sent.

## Session lifecycle

- A daemon starts a handshake (as initiator) the first time it needs to send to
  a paired peer and has no session with it. Messages wait (at most 16 per peer)
  until the handshake completes, then are sent in order.
- The initiator considers the session open once it has sent `session.fin`; the
  responder once it has verified `session.fin`. Both audit `session.open`.
- If both sides start at once, both handshakes complete; each side sends on the
  session that opened last and accepts on either. At most 4 open sessions are
  kept per peer (oldest dropped); unfinished handshakes expire after 10 s.
- Sessions live only in memory. After a daemon restart the peer's messages for
  the old `sid` are rejected (`unknown_session`); a ping that gets no answer
  within 10 s drops the session so the next ping handshakes again.
- **An offline peer is a timeout.** The relay queues session envelopes for a peer that is
  not connected ([envelope.md](envelope.md#offline-queue)) instead of answering
  `peer_offline`, so a ping to an offline peer simply fails with `timeout` after 10 s.

## Rejection

The receiver drops the envelope without answering and logs the reject (below). Since R55-F14
it writes **no audit row**:

| Reason | When |
|--------|------|
| `unpaired` | Any `session.*` envelope from a key that is not a paired peer (including a handshake) |
| `malformed` | Payload too short, or an unknown `session.*` type |
| `unknown_session` | `sid` does not name a handshake or session with that peer |
| `bad_handshake` | Noise rejected a handshake message, or a message arrived in the wrong state |
| `bad_binding` | The binding payload failed a check above |
| `decrypt` | A `session.data` ciphertext failed authentication |
| `replay` | A `session.data` counter below the next acceptable value |

**Why no audit row (R55-F14, D49; review 55 R55-015).** Every reason above can be caused by
the relay alone. `unpaired` comes from any key (D49). For the others the relay uses a paired
peer's key as `from`: a handshake message 1 needs no secret, so a forged `init` followed by
a forged `fin` gives `bad_binding` or `bad_handshake`; garbage gives `decrypt` or
`unknown_session`; and a captured `session.data` replayed gives `replay`. Before R55-F14 each
of these wrote a `session.reject` row (30 a minute, in an audit log that is never pruned).
Older logs still hold such rows; no new ones are written.

**Log line.** Every reject is counted into one log line per minute, written at the end of
the minute: `event=session_reject`, with `count`, `reasons` (the count per reason), and
`reason`, `type`, `peer` and `session` (the first 8 hex of `sid`, when the payload has one)
of the first reject in the minute. Never the envelope `id`, ciphertext or plaintext. This is
the daemon's [relay-driven log rule](envelope.md#relay-driven-log-lines-daemon). A full
session inbox (`event=session_drop`) and a failed send (`event=session_send_failed`; a
forged `init` makes the daemon answer with a `resp`) follow the same rule. Every reject
except `unpaired` is also counted into the daily `relay.reject_summary` audit row if the
owner picks OD-F14-7 (b) ([audit.md](audit.md#who-may-cause-a-row-r55-f14)).

`session.open` detail: `{"peer":"<key>","role":"initiator|responder","session":"<8 hex>"}`.

**Size check before queueing (R55-F13, review 55 R55-052).** The relay read loop hands
`session.*` envelopes to the session worker through a queue of at most **256 envelopes and
16 MiB** of decoded payload. Before queueing, an envelope whose decoded payload is longer than
**65559 bytes** (the 16-byte `sid`, the 8-byte `counter` and one maximal 65535-byte Noise
message, the largest any honest `session.*` payload can be) is dropped. So is an envelope that
would exceed either queue bound. These drops happen before the sender is known to be a paired
peer, so they are **not audited**. They are counted in the limited log line `event=session_drop` (one per minute, with `reasons`
counting `oversize` and `queue_full`, see [Rejection](#rejection)). Before
R55-F13 the queue was bounded by count only, and a relay could pin about 190 MiB of 1 MiB
frames in it while the worker was slow.

## What the relay sees

Routing fields (`from`, `to`, `team`, `type`, `id`, `ts`) and payload sizes.
The relay never logs payloads (see [envelope.md](envelope.md)); from this
ticket on they are ciphertext anyway, apart from the ephemeral public key in
`session.init`.
