# Pairing (relay half)

Status: v1, introduced by ticket 0.5a. The relay half is implemented in
`internal/relay` (`pairing.go`) and `internal/envelope` (`pairing.go`, control
frame fields). The daemon half (ticket 0.5b: card verification, peer storage,
the `pair` and `peers` commands) is in `internal/peers`; see "Daemon side"
below.

Pairing lets two daemons that have never met exchange [Agent Cards](agent-card.md)
through the relay using a short one-time code that a human carries from one
machine to the other. The relay **only routes**: it does not parse, verify or
vouch for either card. Each daemon must verify the card it receives itself
(signature valid, and `card.public_key` equal to the `public_key` of the
`pair_peer` frame, which is the key the relay authenticated).

All pairing frames are control frames (they carry `op`) on the authenticated
connection described in [envelope.md](envelope.md). They are only valid after
`ready`.

## Flow

```
A (issuer)                      relay                       B (redeemer)
  pair_new{card_A} ------------>
  <------------ pair_code{code, expires}
                                            (human types the code on B)
                              <------------ pair_redeem{code, card_B}
  <---- pair_peer{key_B, card_B}
                              pair_peer{key_A, card_A} ---->
```

## Frames

`ref` is an optional client-chosen correlation string (at most 128 bytes). The
relay echoes it on the reply to that request, including `error` frames. The
`pair_peer` frame sent to the issuer carries the `ref` of its `pair_new`.

### `pair_new` (daemon -> relay)

```json
{"op":"pair_new","card":{ ...signed Agent Card envelope... },"ref":"r1"}
```

`card` is required: a JSON object of at most 16 KiB (`MaxCardBytes`). The relay
keeps it (in memory only) until the code is redeemed or expires.

### `pair_code` (relay -> daemon)

```json
{"op":"pair_code","code":"7KQ2M-9XHF4","expires":"2026-01-02T03:14:05Z","ref":"r1"}
```

`code` is displayed as two groups of five characters. `expires` is issue time
plus 10 minutes, RFC 3339 UTC.

### `pair_redeem` (daemon -> relay)

```json
{"op":"pair_redeem","code":"7KQ2M-9XHF4","card":{ ... },"ref":"r2"}
```

`code` is normalised before use: case-insensitive, `-` and spaces ignored,
Crockford aliases accepted (`O`->`0`, `I`/`L`->`1`). `card` is required, same
rules as `pair_new`.

### `pair_peer` (relay -> daemon)

```json
{"op":"pair_peer","public_key":"<other side's key>","card":{ ... },"ref":"r2"}
```

Sent once to each side on a successful redemption. `public_key` is the key the
relay authenticated for the other side; `card` is byte-for-byte what that side
submitted. The issuer receives the redeemer's card and vice versa.

## Codes

- **Format**: 10 characters from the Crockford base32 alphabet
  (`0123456789ABCDEFGHJKMNPQRSTVWXYZ`), drawn from `crypto/rand`: 50 bits of
  entropy. Displayed as `XXXXX-XXXXX`.
- **TTL**: 10 minutes. A code is valid strictly before `issue + 10m`; at or
  after that instant redemption fails.
- **Single use**: a code is consumed by the first successful redemption. A
  second redemption fails with `pair_invalid`.
- **Storage**: memory only, keyed by `SHA-256("dorylinae-pair-code-v1\n" ||
  normalised code)`. The code itself is never stored or logged after the
  `pair_code` reply is written.
- **Issuer limit**: at most 5 unexpired codes outstanding per key
  (`pair_limit`).

### Guessing defence (choice)

The primary defence is **entropy**: 50 bits against a 10-minute lifetime makes
online guessing infeasible even at high rates. The secondary defence is a
**per-key failure rate limit**: after 5 failed redemptions by one authenticated
key within a 1-minute window, further `pair_redeem` frames from that key get
`pair_rate_limited` until the window ends, and the code is not even looked up
while limited. Every failure counts: unknown, expired or already-used code, or
a malformed code.

Keys are free to create, so the per-key limit alone does not stop a Sybil
attacker; the entropy does. A per-source-IP limit belongs in front of the relay
(deployment proxy) and is not implemented here. A global failure ceiling was
deliberately not added because it would let anyone with a key block all
pairing.

### Failure semantics

An unknown, expired and already-redeemed code are indistinguishable to the
redeemer (`pair_invalid`), so the relay is not an oracle. A redemption that
fails because the issuer is not connected (`peer_offline`) or not keeping up
(`peer_busy`) does **not** consume the code.

## Errors

Sent as `error` frames (see [envelope.md](envelope.md)) with `ref` set to the
request's `ref` when it had one. Pairing errors never close the connection.

| Code | Meaning |
|------|---------|
| `pair_invalid` | Code is unknown, expired or already used |
| `pair_rate_limited` | Too many failed redemptions from this key; retry after the window |
| `pair_limit` | Issuer already has 5 outstanding codes |
| `bad_pairing` | Malformed pairing frame (missing/oversize/non-object `card`, oversize `ref`) or redeeming your own code |
| `peer_offline` | Issuer is not connected; code not consumed |
| `peer_busy` | Issuer's outbound queue is full; code not consumed |

## Logging and counters

The relay logs `pair_issue`, `pair_redeem` and `pair_fail` events with the
abbreviated key (first 8 characters) and, for failures, a reason. It never
logs codes, code hashes or card contents. It keeps in-memory counters
(`Server.PairStats`): issued, redeemed, invalid, rate-limited.

## Daemon side (ticket 0.5b)

CLI usage is in [../cli/pair.md](../cli/pair.md) and [../cli/peers.md](../cli/peers.md).

- The daemon uses a fresh pairing ID (`pair-` + 16 hex characters) as the `ref`
  of each `pair_new` / `pair_redeem` it sends, and matches `pair_code`,
  `pair_peer` and `error` frames to pairings by `ref`.
- **Card verification.** On `pair_peer` the daemon checks that the card is a
  valid signed Agent Card ([agent-card.md](agent-card.md)) with a verifying
  signature, and that `card.public_key` equals the frame's `public_key` (the key
  the relay authenticated). Only then is the peer stored. Any failure ends the
  pairing with error code `bad_card` and stores nothing.
- **Storage.** SQLite table `peers` (migration 2): `public_key` (primary key),
  `name`, `harness`, `skills` (JSON), `card` (the verified envelope as
  received), `paired_at` (RFC 3339 UTC). Re-pairing a known key updates the card
  fields and keeps `paired_at`.
- **Pairing state** is in memory: `pending` -> `complete` | `failed`. A pairing
  with no relay reply fails with `timeout` after 30 s; an issuer's pairing fails
  with `expired` shortly after its code expires. Finished pairings stay
  queryable for one hour. At most 16 pairings may be pending at once.
- **Audit** (`audit_events`): `pair.start` (actor `cli`; detail `{id, role}`),
  `pair.complete` (actor `daemon`; `{id, role, peer}`), `pair.fail` (actor
  `daemon`; `{id, role, code, reason}`). Codes and card contents are never
  recorded.
