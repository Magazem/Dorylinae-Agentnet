# Mail (sealed application messages)

Status: v1, specified by ticket 1.0a (design: `Docs/review/06-pairing-session-options.md`
Part B, owner decisions §7.3–7.5). Implemented by 1.0b (mailbox keys), 1.0c
(`internal/mail`: seal/open), 1.0d (receiver dedupe and ack) and 1.0e (sender outbox).
Change this document first.

Every application message between paired daemons is a self-contained **mail**:

- signed by the sender's identity key (Ed25519);
- sealed with HPKE to the recipient's current **mailbox key** (X25519);
- carried in one relay envelope of type `mail`.

Neither daemon has to be online at the same time as the other. The sender keeps the mail in
its **outbox** and resends it until the recipient **acks** it. The recipient
**dedupes** by `(from, id)`. Examples of application messages are requests,
accept/decline/defer, results, grants and acks. Interactive traffic (ping, later live
streams) stays on Noise XX sessions ([session.md](session.md)).

Security properties, accepted in owner decision 3:

- **Confidentiality** against the relay and anyone without the recipient's mailbox private key.
- **Sender authentication, no KCI.** Stealing the recipient's keys does not allow forging
  mail from others, because it needs the sender's identity key.
- **Non-repudiation.** The inner signature proves to third parties that the sender sent it.
- **Forward secrecy only through rotation.** Compromising a mailbox private key exposes mail
  sealed to that key. Keys are deleted 21 days after creation.
- **Replay.** Ciphertexts are replayable. The receiver's `mail_seen` table plus the `created`
  window make replays harmless.

Byte strings written `"...\n"` are ASCII with a single LF (0x0A). `‖` is concatenation.
**Canonical JSON** means [agent-card.md §Canonical serialisation](agent-card.md#canonical-serialisation),
and strict parsing means rules 6 there plus integers only. Keys (`<key>`) are Ed25519 identity keys
in wire form: base64url without padding, 43 characters.

## Mailbox keys

| Property | Value |
|---|---|
| Algorithm | X25519 (`ecdh.X25519()`), private key 32 bytes from `crypto/rand` (`GenerateKey`) |
| `key_id` | `SHA-256(pub)[0:8]`, 8 bytes. In JSON: 16 lowercase hex characters. In payloads: raw bytes |
| Keystore | One secret per key, raw 32-byte private key. Keychain: service `dorylinae`, account `mailbox-<id>-<key_id hex>` (`<id>` as for the identity, [agent-card.md §Key storage](agent-card.md#key-storage)). File fallback: `<config dir>/mailbox/<key_id hex>.key`, owner-only, same rules as `identity.key`. Same `DORYLINAE_KEYSTORE` handling as the identity |
| Own table | `mailbox_keys_own`, see [Tables](#tables) |

### Lifecycle

For a key created at time `t`:

| Time | Event |
|---|---|
| `t` | Created, stored, announced with `not_after = t + 14 d`. Becomes the **current** key |
| `t + 7 d` | Rotation. A new key becomes current and this key is **retired** (`retired = now`). Retired keys still decrypt |
| `t + 21 d` (`not_after + 7 d` queue TTL) | Private key **deleted** from the keystore (`deleted = now`). Mail sealed to it is now a [key miss](#key-miss-recovery) |

- A rotation job runs at daemon start and then hourly. If there is no live key, or the current
  key's `created ≤ now − 7 d`, it creates a new key, retires the previous current key, audits
  `mailbox.rotate {key_id, retired}`, and pushes the new announcement to every peer with a
  non-empty `mailbox_keys` as a [`keys` mail](#kind-keys) (outboxed and acked like any mail).
- In the same job, every key with `not_after + 7 d ≤ now` is deleted from the keystore, and
  its row gets `deleted` set. The row itself is kept for audit.
- A key is **live** when it is not deleted. This schedule never has more than **3 live keys**
  (ages below 7, 14 and 21 days). The job also enforces the limit: if a 4th would be live, the
  oldest is deleted early.
- The first key is created on demand by pairing (0.8c, see [pairing.md](pairing.md#mailbox-key-during-pairing))
  or by the rotation job, whichever comes first.
- Decrypting uses any live key selected by `key_id`. Sealing uses the peer's newest announcement.

### Announcement

```json
{
  "announcement": {
    "created": "2026-01-02T03:00:00Z",
    "identity": "<key>",
    "key_id": "67ca2ffd6fe9efab",
    "not_after": "2026-01-16T03:00:00Z",
    "pub": "<base64url, no padding, 32 bytes>",
    "v": 1
  },
  "signature": "<base64url, no padding, 64 bytes>"
}
```

```
signature = Ed25519-Sign(identity_private_key,
                         "dorylinae-mailbox-key-v1\n" ‖ canonical(announcement))
```

All `announcement` members are required, and no others are allowed. Times are RFC 3339 UTC
with `Z` and whole seconds. The wire form, and the form used in the pairing transcript, is
the canonical JSON of the two-member object above.

**Verification** gives `bad_mbox` in pairing and `mail.reject` reason `bad_keys` in a `keys`
mail. The checks are:

1. Strict parse. The top level has exactly the members `announcement` and `signature`.
2. `identity` equals the expected peer key: the `pair_peer` `public_key` in pairing, `msg.from` in mail.
3. `signature` decodes to 64 bytes and verifies under `identity` over the domain-prefixed
   canonical `announcement`, **generically parsed**, as for cards.
4. `v` = 1. `pub` decodes to 32 bytes. `key_id` = hex(SHA-256(pub)[0:8]).
5. `created ≤ now + 10 min`, `created < not_after ≤ created + 30 d`, and `not_after > now`.

### Peer storage

`peers.mailbox_keys` is a JSON array of at most **2** verified signed-announcement objects,
newest `created` first. To accept a new announcement from a peer:

- If its `key_id` is already stored, ignore it (idempotent).
- If its `created` is **not newer** than the newest stored `created`, ignore it. This stops
  rollback to an old key.
- Otherwise prepend it and truncate the array to 2.

Pairing (v1 → v2 re-pair, or any v2 re-pair) merges its announcement by the same rule.

## Message

The **msg** object, serialised as canonical JSON:

| Member | Type | Rules |
|---|---|---|
| `v` | integer | `1` |
| `id` | string | `m-` + 32 lowercase hex characters (16 bytes from `crypto/rand`). Equals the envelope `id`. Unique per sender |
| `from` | string | Sender's identity key |
| `to` | string | Recipient's identity key |
| `created` | string | Sender's clock at submit time, RFC 3339 UTC, `Z`, whole seconds. **Not** changed on resend or re-seal |
| `kind` | string | 1–64 characters from `[a-z0-9._-]`. See [Kinds](#kinds) |
| `body` | object | Kind-specific JSON object (may be `{}`). Integers only, per canonical rules |

No other members are allowed. The **signed** object:

```json
{"msg": { ... }, "sig": "<base64url, no padding, 64 bytes>"}
```

```
sig       = Ed25519-Sign(sender_identity_private_key, "dorylinae-mail-v1\n" ‖ canonical(msg))
plaintext = canonical(signed)                  // UTF-8; at most 716800 bytes (MaxMailPlaintext)
```

The size cap keeps the base64 envelope under the relay's 1 MiB frame limit. The payload is
716800 + 57 bytes, which is 955812 base64 characters, plus under 1 KiB of envelope fields.

## Sealing

HPKE (RFC 9180), **base mode** (`mode_base` = 0x00), with the suite:

| Component | ID | Go (`crypto/hpke`, Go 1.27) |
|---|---|---|
| KEM | `0x0020` DHKEM(X25519, HKDF-SHA256) | `hpke.NewDHKEMPublicKey(ecdhPub)` / `hpke.NewDHKEMPrivateKey(ecdhPriv)` |
| KDF | `0x0001` HKDF-SHA256 | `hpke.HKDFSHA256()` |
| AEAD | `0x0003` ChaCha20-Poly1305 | `hpke.ChaCha20Poly1305()` |

```
info = "dorylinae-mail-v1\n" ‖ from ‖ "\n" ‖ to ‖ "\n" ‖ key_id      // from/to: 43 ASCII chars each; key_id: 8 raw bytes
aad  = id                                                           // the envelope/msg id, ASCII (34 bytes)

enc, sender = hpke.NewSender(recipient_mailbox_pub, HKDFSHA256, ChaCha20Poly1305, info)   // enc: 32 bytes
ct          = sender.Seal(aad, plaintext)                                                 // len(plaintext) + 16
```

Use `NewSender` + `Sender.Seal` (and `NewRecipient` + `Recipient.Open`). **Do not use the
one-shot `hpke.Seal` / `hpke.Open`.** They take no `aad` and concatenate `enc` differently.
Each mail uses its own HPKE context, with exactly one `Seal` and one `Open` (sequence number
0). The ephemeral key comes from `crypto/rand`, so sealing is not reproducible. The
[test vectors](#test-vectors) are checked on the open side.

## Envelope

| Field | Value |
|---|---|
| `from`, `to` | Sender and recipient identity keys (= `msg.from`, `msg.to`) |
| `team` | `""` (Phase 1 may set a team label; it is routing metadata and is not covered by the seal) |
| `type` | `mail` for every kind. The relay learns only that mail was sent, and its size |
| `id` | `msg.id` |
| `ts` | Sender clock at the time of this (re)send, RFC 3339 |
| `payload` | Standard base64 of the binary payload below |

Payload layout, all fixed offsets:

| Offset | Length | Content |
|---|---|---|
| 0 | 1 | Version `0x01` |
| 1 | 8 | `key_id` of the recipient mailbox key used |
| 9 | 32 | HPKE `enc` (X25519 ephemeral public key) |
| 41 | n + 16 | HPKE ciphertext of `plaintext` (n bytes) including the Poly1305 tag |

The minimum length is 57 bytes.

## Sending

`agentnet` commands submit mail through the daemon with the IPC method `mail_submit`
([ipc.md](ipc.md#mail_submit)); the only CLI front end today is the debug command
[`agentnet mail send`](../cli/mail.md). `mail.Submit(to, kind, body)`:

1. `to` must be a paired peer (otherwise `unpaired`) with a non-empty `mailbox_keys`
   (otherwise `no_mailbox_key`: the peer was paired with v1 and must re-pair). Phase-1 policy
   may also refuse `trust=relay` peers ([pairing.md](pairing.md#storage-and-trust-states)).
2. Build `msg` with a fresh `id` and `created = now`, sign it, and seal it to the peer's newest announcement.
3. In one transaction, insert the outbox row (`state = queued`, `next_attempt = now`).
   Return `{id, state: "queued"}` to the caller. This must take **under 2 s**, and must not
   wait for the relay.
4. The outbox worker sends it ([Outbox](#outbox)).

Acks (`kind: ack`) and key-miss replies are **not** submitted through the outbox. They are
sealed and sent once, directly.

## Receiving: verification order

**`relayclient` must hand up every `mail` envelope, including repeats of a `(from, id)`
it has already handed up** (it still acks each one to the relay). Its in-memory seen-set
([envelope.md §Offline queue](envelope.md#offline-queue)) applies to other types only.
The mail layer needs to see repeats: a resend after a lost ack must reach
[dedupe](#dedupe-and-inbox) so it is re-acked, and a re-sealed copy after a
[key miss](#key-miss-recovery) has the same id as the copy that failed. With the seen-set
in the way, both would be dropped silently until the id left the 8192-entry window, and the
sender's row would end `expired` although the mail was delivered. This is a `relayclient`
change, made in 1.0d.

For each envelope with `type = mail` handed up by `relayclient`, the daemon runs these steps
in order. The first failure rejects the envelope. Nothing is stored, no ack is sent, and the
audit event is `mail.reject {peer, id, reason}`. That event is rate-limited like
`session.reject`: at most 30 per minute, with the rest counted in the log as
`event=mail_reject_suppressed`.

| # | Check | Reject reason |
|---|---|---|
| 1 | Envelope `from` is a paired peer | `unpaired` |
| 2 | Payload is valid base64, at least 57 bytes, first byte `0x01` | `malformed` |
| 3 | `key_id` (bytes 1–8) names a **live** own mailbox key | `key_miss` (then run [key-miss recovery](#key-miss-recovery)) |
| 4 | `NewRecipient(enc, key, …, info)` and `Open(aad = envelope id, ct)` succeed. `info` is built from envelope `from`/`to` and the payload `key_id` | `decrypt` |
| 5 | Plaintext parses strictly as an object with exactly `msg` and `sig`. `msg` has exactly the members in [Message](#message) with the right types, and `sig` decodes to 64 bytes | `malformed` |
| 6 | `msg.from` = envelope `from` | `sender_mismatch` |
| 7 | `sig` verifies under `msg.from` over `"dorylinae-mail-v1\n" ‖ canonical(msg)`, with `msg` **as parsed generically** | `bad_signature` |
| 8 | `msg.to` = own identity key | `wrong_recipient` |
| 9 | `msg.id` = envelope `id`, and it matches the `id` format | `id_mismatch` |
| 10 | `v` = 1 | `malformed` |
| 11 | `now − 30 d ≤ created ≤ now + 10 min` (receiver clock) | `stale` |
| 12 | For kinds `ack` and `keys` only: `body` has exactly the members defined in [Ack](#ack) / [Kind `keys`](#kind-keys), every listed id matches the `id` format of [Message](#message), and the array lengths are within limits. For `keys`, the announcement verifies ([Announcement](#announcement), `identity` = `msg.from`) | `malformed` (`bad_keys` for the announcement) |

Steps 6, 8 and 9 are also covered cryptographically (by `info`, `aad` and the signature). The
explicit checks give precise reasons. Step 12 runs before dedupe, so a rejected `keys` mail
is not recorded in `mail_seen` and a corrected resend is still processed.

Then, by kind:

- **`ack`**: process it ([Ack](#ack)). It is not deduped, not stored in `mail_seen`, and never acked.
- **`keys`**: [dedupe](#dedupe-and-inbox), apply, and ack.
- **Every other kind**: first the [receive age limit](#receive-age-limit), then dedupe, process, and ack.

### Receive age limit

Step 11 accepts `created` up to 30 days old, which keeps `mail_seen` pruning safe. Application
mail has a tighter limit: `ReceiveMaxAge` = **14 days** (`internal/mail/receiver.go`), the
bound on how long a sender can still be trying (relay queue TTL 7 d, then the 7 d outbox
lifetime). A mail of any kind except `ack` and `keys` whose `now − created` exceeds 14 days
(receiver clock) is:

- **not stored, not deduped and not applied**: no `mail_seen` row, no inbox row, no `mail.in`;
- audited as `mail.reject {peer, id, reason: "stale"}` (same rate limit as other rejects);
- **acked under `unsupported`**, so a late resend stops instead of running for the rest of
  the sender's 7 days.

A mail exactly 13 days old is accepted, and deduped on repeat. The consequence for senders is
described under [`expired`](#states).

## Dedupe and inbox

In **one SQLite transaction**:

1. `INSERT INTO mail_seen (from_key, id, received_at)`. If the row already exists, this is a
   **duplicate**. Roll back, do not process it again, and **re-send the ack** for `id`.
2. Process by kind:
   - `keys`: apply the announcement ([below](#kind-keys)). There is no inbox row.
   - A known application kind (Phase 1+): insert the `mail_inbox` row with the verified
     plaintext kept as proof, plus the kind-specific rows.
   - An unknown kind: nothing is stored beyond `mail_seen`. The ack lists it under `unsupported`.
3. Commit. Audit `mail.in {peer, id, kind}` for every kind except `ack` and `keys`. This is the
   daemon-side per-kind count of owner decision 4.
4. **After commit**, send the ack.

`mail_seen` rows are pruned when `received_at < now − 35 d`, which runs daily and at start.
A replay of a pruned id carries a `created` older than `now − 30 d + 10 min`, so step 11
rejects it. Pruning can never re-admit a replay.

## Ack

An ack is a mail with `kind: "ack"` sent to the original sender. It is sealed to the
sender's newest mailbox key and has a fresh `id`.

```json
{"ids": ["m-..."], "unsupported": ["m-..."]}
```

- `ids`: mail accepted and processed, or recognised as duplicates. `unsupported`: accepted
  and recorded in `mail_seen`, but of a kind this daemon does not understand; also mail
  refused by the [receive age limit](#receive-age-limit) (not recorded). Each member is
  optional and holds 1–256 ids when present. At least one must be present. No other members
  are allowed.
- The receiver may hold acks for up to 1 s to batch them per peer.
- Acks are **not** outboxed, not deduped and not acked. A lost ack means the sender resends,
  and the receiver's dedupe re-acks it.
- On receiving an ack, the sender, for each id that is an outbox row addressed to the ack's
  `from` and not final, sets `ids` rows to `delivered` and `unsupported` rows to `failed`
  (error `unsupported_kind`). Ids that are unknown or already final are ignored.
- If the sender has no mailbox key for the acking peer, the ack cannot be sealed. It is
  dropped and logged (`event=ack_no_mailbox_key`).

## Kinds

| Kind | Body | Outboxed / acked | Ticket |
|---|---|---|---|
| `ack` | see [Ack](#ack) | no / no | 1.0d |
| `keys` | see below | rotation push: yes / yes. Key-miss reply: no / yes | 1.0b, 1.0e |
| `note` | `{"text": "..."}`, stored to `mail_inbox`, no other effect. **Debug only**: registered when the daemon runs with `DORYLINAE_DEBUG=1` | yes / yes | 1.0f |
| `team.roster`, `team.join`, `team.leave` | [team.md](team.md#kinds) | yes / yes | 1.1b |
| `request`, `request.accept`, `request.decline`, `request.defer`, `request.complete`, `request.cancel`, `request.cancelled` | [request.md](request.md) | yes / yes | 1.4c, 1.6a |
| `ws.result`, `ws.state`, `ws.cancel` | [work-session.md](work-session.md#kinds) (draft) | yes / yes | 2.1a |
| `grant`, `grant.revoke` | [grant.md](grant.md#kinds) (draft) | yes / yes | 2.2c |
| `device.link`, `device.unlink` | [device.md](device.md#kinds) (draft) | yes / yes | 2.D1 |

Presence heartbeats reuse this seal and signature with kind `presence`, but as envelope type
`presence`, not `mail` ([presence.md](presence.md)). From 1.4b, an `Apply` error wrapping
`mail.ErrBadBody` is recorded in `mail_seen`, audited `mail.reject` reason `bad_body` and
acked as `unsupported` ([request.md §Invalid bodies](request.md#invalid-bodies)).

### Kind `keys`

```json
{"announcement": {"announcement": {...}, "signature": "..."}, "retry": ["m-..."]}
```

- `announcement` (required): the sender's current signed announcement object. Verify it as
  in [Announcement](#announcement) with `identity` = `msg.from` (failure: `mail.reject`
  reason `bad_keys`, no ack), then merge it per [Peer storage](#peer-storage).
- `retry` (optional, 1–256 ids): envelope ids the sender could not decrypt. See below.

## Key-miss recovery

When B receives mail from paired peer A whose `key_id` is not live (step 3):

1. If the envelope id matches the mail `id` format (`m-` + 32 lowercase hex), B adds it to
   a per-peer `pending_retry` set, which is held in memory and capped at 256 ids. Any other
   id is ignored (no reply): the payload is not yet authenticated at step 3, and a `keys`
   mail whose `retry` held a malformed id would be rejected at step 12.
2. If B has not sent a key-miss reply to A in the last **10 minutes**, B sends one now: a
   `keys` mail with its current announcement and `retry` = the set. It is sealed to A's
   newest mailbox key, sent directly and not outboxed. B then clears the set. Otherwise the
   set is sent when the 10-minute window ends. If B has no mailbox key for A, nothing is sent.
3. When A accepts that `keys` mail, then for every id in `retry` that is an outbox row to B
   and not final: if the row's `key_id` differs from B's newest announcement, A **re-seals**
   the stored signed plaintext to the new key. It keeps the same `id` and `created`, and
   builds a new envelope with a new `ts`. A then replaces the stored frame, sets `key_id`,
   and sends it immediately. If the `key_id` already matches, A does nothing. Normal backoff
   continues, which prevents loops.
4. The dedupe key is `(from, id)`, so a re-sealed copy that arrives after the original was
   somehow processed is a duplicate and is only re-acked.

The ids in `retry` are routing metadata the relay already has, so the reply leaks nothing new.

## Outbox

### States

```
queued ──send ok──▶ relayed ──ack──▶ delivered
  ▲  │                 │  │
  │  └──(any state)────┼──┴──▶ expired   (now ≥ created + 7 d)
  └──queue_full/internal/not connected   failed    (unsupported ack, peer removed, relay bad_envelope/bad_sender)
```

| State | Meaning | Final |
|---|---|---|
| `queued` | Stored. No successful hand-off to the relay yet, or the relay refused it temporarily | no |
| `relayed` | `relayclient.Send` succeeded and no `error` frame with `ref = id` arrived. This includes a `queued` frame from the relay and a silent direct forward | no |
| `delivered` | Ack received | yes |
| `expired` | Not acked within 7 days of `created`: **delivery unknown**. The peer may still process the mail (until about `created + 14 d`, see [Receive age limit](#receive-age-limit)), or it may have processed it and the ack was lost. Audit `mail.expired {peer, id, kind}` | yes |
| `failed` | Permanent failure. `error` holds the reason | yes |

- Transitions to a final state clear `signed` and `frame`, so no plaintext stays at rest.
  Final rows are deleted 30 days after `updated`.
- A relay `error` frame with `ref = id`: `queue_full`, `internal`, `peer_offline` or
  `peer_busy` → `queued` (keep backoff). The relay now queues mail for an offline peer instead
  of answering `peer_offline`, but a relay that still does is treated the same way: the mail
  stays queued and is never failed for it. `bad_envelope` or `bad_sender` → `failed` (these
  indicate a bug).
- **`expired` means delivery unknown, not "not delivered".** A caller must not assume the peer
  never acted on it. Any Phase 1 code that resubmits after `expired` must therefore be
  **idempotent**: the resubmission is a new mail with a new `id`, so the kind's own body must
  carry whatever the receiver needs to recognise the repeat (for example a request id).
- Peer removed (`peers remove`) → all its non-final rows become `failed` (`unpaired`).

### Sending and backoff

- The worker sends rows whose `next_attempt ≤ now` and that are not final. It sends the
  **stored frame unchanged**: same `id`, same `ts`. Only a re-seal builds a new frame. The
  relay dedupes by `(to, from, id)` while the envelope is still queued.
- After each hand-off attempt, `attempts += 1` and `next_attempt = now + delay(attempts) ×
  U(0.9, 1.1)`, where `delay(1) = 1 min`, `delay(2) = 5 min`, `delay(3) = 30 min`, and
  `delay(n ≥ 4) = 6 h`. `U` is uniform jitter.
- Immediate sends, which ignore `next_attempt`: all `queued` rows on every relay `ready`
  (reconnect); all non-final rows to a peer on that peer's presence-online edge (1.2; a
  no-op hook until then); re-sealed rows (key-miss recovery).
- Expiry is checked by the worker every minute: `created + 7 d ≤ now` → `expired`.
- `agentnet status` reports `outbox: {queued, relayed, expired, pending, delivered, failed}` (`pending` = `queued` + `relayed`): the `outbox` member of
  `--json`, and an `outbox:` line in the human output ([status.md](../cli/status.md)) (1.0f).

## Tables

These are new migrations, appended in ticket order. The `peers.trust` and
`peers.mailbox_keys` columns are in [pairing.md](pairing.md#storage-and-trust-states).

```sql
-- 0.8c (first key) / 1.0b
CREATE TABLE mailbox_keys_own (
    key_id       TEXT PRIMARY KEY,                -- 16 lowercase hex
    pub          TEXT NOT NULL,                   -- base64url, 32 bytes
    created      TEXT NOT NULL,                   -- RFC 3339 UTC
    not_after    TEXT NOT NULL,
    retired      TEXT,                            -- NULL while current
    deleted      TEXT,                            -- NULL while the private key is live
    announcement TEXT NOT NULL CHECK (json_valid(announcement))   -- canonical signed announcement
);

-- 1.0d
CREATE TABLE mail_seen (
    from_key    TEXT NOT NULL,
    id          TEXT NOT NULL,
    received_at TEXT NOT NULL,
    PRIMARY KEY (from_key, id)
) WITHOUT ROWID;
CREATE INDEX mail_seen_received ON mail_seen (received_at);

CREATE TABLE mail_inbox (
    from_key    TEXT NOT NULL,
    id          TEXT NOT NULL,
    kind        TEXT NOT NULL,
    created     TEXT NOT NULL,                   -- msg.created
    received_at TEXT NOT NULL,
    signed      TEXT NOT NULL,                   -- verified plaintext (proof of origin)
    PRIMARY KEY (from_key, id)
);

-- 1.0e
CREATE TABLE outbox (
    id           TEXT PRIMARY KEY,               -- msg.id = envelope id
    to_key       TEXT NOT NULL,
    kind         TEXT NOT NULL,
    created      TEXT NOT NULL,                  -- msg.created
    key_id       TEXT,                           -- recipient mailbox key sealed to (hex)
    signed       TEXT,                           -- canonical signed plaintext; NULL once final
    frame        TEXT,                           -- envelope frame as sent; NULL once final
    state        TEXT NOT NULL CHECK (state IN ('queued','relayed','delivered','expired','failed')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    next_attempt TEXT,                           -- NULL once final
    updated      TEXT NOT NULL,
    error        TEXT
);
CREATE INDEX outbox_due ON outbox (state, next_attempt);
```

All times are RFC 3339 UTC with milliseconds (`2006-01-02T15:04:05.000Z`), so text order is
time order. `msg.created` stays in whole seconds.

## Audit

| Action | Actor | Detail |
|---|---|---|
| `mailbox.rotate` | daemon | `{key_id, retired}` (`retired`: key_id or `""`) |
| `mail.reject` | daemon | `{peer, id, reason}` (rate-limited, see above; reason `stale` also for the [receive age limit](#receive-age-limit)) |
| `mail.in` | daemon | `{peer, id, kind}` |
| `mail.expired` | daemon | `{peer, id, kind}` |

Never plaintext, ciphertext, bodies or key material.

## What the relay sees

It sees `from`, `to`, `team`, `type: "mail"`, `id`, `ts` and the payload size, and when mail
is sent and acked. It does not see kinds, bodies or `created`.

## Test vectors

Produced by `go run ./tools/specvectors`. They use the pairing vector keys
([pairing.md §Test vectors](pairing.md#test-vectors)). The sender is the issuer (identity
seed `00…1f`), and the recipient is the redeemer (identity seed `20…3f`, mailbox private key
`60 61 … 7f`). HPKE draws a random ephemeral key, so this is **one recorded run**. An
implementation must **open** it (`go run ./tools/specvectors -open <payload>` does) and get
exactly the plaintext below. It cannot reproduce it by sealing.

Recipient mailbox key:

```
priv    606162636465666768696a6b6c6d6e6f707172737475767778797a7b7c7d7e7f
pub     675dd574ed7789310b3d2e7681f3790b466c773b1521fecf36577958371ea52f
key_id  16786d4e5ef74411
```

Plaintext (canonical signed msg, UTF-8, one line; `sig` is deterministic Ed25519):

```
{"msg":{"body":{"ids":["m-fedcba9876543210fedcba9876543210"]},"created":"2026-01-02T03:10:00Z","from":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","id":"m-0123456789abcdef0123456789abcdef","kind":"ack","to":"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc","v":1},"sig":"bKEyJ9nXxE2wmtRQaF3EuIYy08KkKNXseuZ19j5p3i0j9-vfPvgn5aAVjMsn-OIQiKSqFT2xIpFa2h_azVZiAg"}
```

`info` and `aad` (hex):

```
info  646f72796c696e61652d6d61696c2d76310a41364548765f504f454c3464634e3059353076416d57666b316a436270513166486479475a424a564d62670a4b61793634554738797643794c6871553030304c787a5965556d304c5f684c496c3553386b794b576264630a16786d4e5ef74411
aad   6d2d3031323334353637383961626364656630313233343536373839616263646566   ("m-0123456789abcdef0123456789abcdef")
```

Recorded seal (hex), then the full envelope `payload` (standard base64):

```
enc   4248142cb2a1d44b7c8c0a46dc3ba60fc5cd28afb6670db04585575ebebca718
ct    1e62a05cd5aaf428886169ff1397ee3b9e2bab261cb03cb4566425ec0c61ac956404bff4a747f86a0617ce5038e8bb90bead666232a2618e49d795732aaeadea9f42b04e185d5cfa7a0b1d232526b1505b4231874878df2419c1f79198a9f6eeda8d8f5bafd79e19ce20a2c5f08a230f8f9385141106e91315e231e7b7ad833772ca55f6baf2707f5203b3b5de649a3dd4b82d2c333170b7da81318424ed3517c0773376e529f19426f784d2bf3788e8b6a0fc67a6fb532739922e753c093b63fc61890c70aa702cec4bc4626c75235330fb6bc7d7fa7e6a97e70333f2b7f26f7ab5e218b0af51d241961d3c4c628959aa61bf53c95fc3d39cf878fa1c81cd6e76a5e2c7dde75e5b4ca971fcbf2798494c6e50199d945078e0abf7d3a6dd8d1c7c9e4e6cf71e63c7066c487dccdde1a924818fb9d4b078fdfc258bb9a46e54150b689ad816d9c4e8fcaa74054fd7c4305c49db82cc0343bf4f5474af06dec1ecd0d3582e3da41c5ef1b12dfe04416b536963c15b

ARZ4bU5e90QRQkgULLKh1Et8jApG3DumD8XNKK+2Zw2wRYVXXr68pxgeYqBc1ar0KIhhaf8Tl+47niurJhywPLRWZCXsDGGslWQEv/SnR/hqBhfOUDjou5C+rWZiMqJhjknXlXMqrq3qn0KwThhdXPp6Cx0jJSaxUFtCMYdIeN8kGcH3kZip9u7ajY9br9eeGc4gosXwiiMPj5OFFBEG6RMV4jHnt62DN3LKVfa68nB/UgOztd5kmj3UuC0sMzFwt9qBMYQk7TUXwHczduUp8ZQm94TSvzeI6Lag/Gem+1MnOZIudTwJO2P8YYkMcKpwLOxLxGJsdSNTMPtrx9f6fmqX5wMz8rfyb3q14hiwr1HSQZYdPExiiVmqYb9TyV/D05z4ePocgc1udqXix93nXltMqXH8vyeYSUxuUBmdlFB44Kv306bdjRx8nk5s9x5jxwZsSH3M3eGpJIGPudSweP38JYu5pG5UFQtomtgW2cTo/Kp0BU/XxDBcSduCzANDv09UdK8G3sHs0NNYLj2kHF7xsS3+BEFrU2ljwVs=
```

Negative checks for 1.0c. Each must fail at the step shown, using the recipient key above:

- Flip any bit of `enc` or `ct`: step 4 `decrypt`.
- Open with `aad = "m-0123456789abcdef0123456789abcdee"`: step 4 `decrypt`.
- Build `info` with `from`/`to` swapped: step 4 `decrypt`.
- Payload byte 0 = `0x02`: step 2 `malformed`.
- Payload `key_id` changed to any other value: step 3 `key_miss`.
- A receiver clock of `2026-02-02T03:10:01Z`, which is more than 30 days after `created`:
  step 11 `stale`.
