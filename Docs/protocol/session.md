# Encrypted sessions (Noise XX)

Status: v1, introduced by ticket 0.6. Implemented in `internal/noise`
(handshake, static-key binding, transport nonces) and `internal/session`
(envelope framing, session table, ping). Change this document first.

Two **paired** daemons (see [pairing.md](pairing.md)) talk end to end through
the relay inside a Noise session. The relay routes the envelopes of
[envelope.md](envelope.md) unchanged; after the handshake every envelope
payload is ciphertext and the relay cannot read, forge or undetectably change
it.

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

## Rejection

The receiver drops, without answering, and writes the audit event
`session.reject`:

| Reason | When |
|--------|------|
| `unpaired` | Any `session.*` envelope from a key that is not a paired peer (including a handshake) |
| `malformed` | Payload too short, or an unknown `session.*` type |
| `unknown_session` | `sid` does not name a handshake or session with that peer |
| `bad_handshake` | Noise rejected a handshake message, or a message arrived in the wrong state |
| `bad_binding` | The binding payload failed a check above |
| `decrypt` | A `session.data` ciphertext failed authentication |
| `replay` | A `session.data` counter below the next acceptable value |

Detail: `{"peer":"<key>","type":"session.data","reason":"decrypt","session":"<first 8 hex of sid>"}`.
Never ciphertext or plaintext. At most 30 rejects per minute are audited; the
rest are counted in the daemon log (`event=session_reject_suppressed`) so a
misbehaving peer or relay cannot flood the audit log.

`session.open` detail: `{"peer":"<key>","role":"initiator|responder","session":"<8 hex>"}`.

## What the relay sees

Routing fields (`from`, `to`, `team`, `type`, `id`, `ts`) and payload sizes.
The relay never logs payloads (see [envelope.md](envelope.md)); from this
ticket on they are ciphertext anyway, apart from the ephemeral public key in
`session.init`.
