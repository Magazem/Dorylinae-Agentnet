# Pairing

Status: **v2**, specified by ticket 0.8a (design: `Docs/review/06-pairing-session-options.md`
Part A, owner decisions §7). Implemented by 0.8b (fingerprints, trust state),
0.8c (daemon), 0.8d (relay) and 0.8e (independent vector check). v1 (tickets
0.5a/0.5b) survives only behind a compatibility flag, see [v1 compatibility](#v1-compatibility).

Pairing lets two daemons that have never met exchange [Agent Cards](agent-card.md)
and [mailbox key announcements](mail.md#announcement) through the relay, using a
one-time code that a human carries from one machine to the other. In v2 the
**issuer daemon** creates the code. Only its first part (the *lookup*) is ever
sent to the relay. The second part (the *secret*) keys a MAC over everything
exchanged, so a relay that substitutes a card or key is detected.

All pairing control frames carry `op` and are sent on the authenticated relay
connection of [envelope.md](envelope.md), only after `ready`. The confirmation
step is an ordinary envelope of type `pair.confirm`.

## Threat model

- **Attacker:** controls the relay. This covers a compromised hosted relay (4.1),
  a malicious operator, or anyone on the path of a plaintext `ws://` link. The attacker can
  read, drop, delay, reorder, replay and inject any frame, and can create any number of
  identity keys and connections.
- **Not attacker-controlled:** both endpoint daemons, and the channel the humans use to
  carry the code (voice, chat, in person). The code is not disclosed to the attacker.
- **Goal:** when a pairing completes, each side has stored the *real* other side's
  identity key, Agent Card and mailbox key announcement. Otherwise pairing fails
  visibly and nothing is stored on the side that detects the failure.
- **Residual risk (accepted):** the **redeemer** sends its tag first, and only after a
  human has given it the code. A hostile relay that poses as the issuer gets that one
  `tag_R` and can try to recover the secret **offline**. To fool the redeemer it must
  succeed within the redeemer's 60 s confirm wait. To fool the issuer it must succeed
  before the issuer's local 10-minute code TTL ends. Both windows are enforced by the
  honest daemons' own clocks. The issuer never sends a tag until it has verified a
  correct `tag_R`, so strangers who guess a lookup get nothing to attack offline.
  Argon2id stretching makes the offline attack infeasible (see [KDF parameters](#kdf-parameters)).
  A PAKE (CPace) would remove it. That is planned as v3 and is not part of v2.
- **Out of scope:** endpoint compromise; a human who reads the code to the attacker;
  denial of service (the relay can always drop frames. Pairing then fails and the user
  retries).

## Code format

```
code    = lookup ‖ secret                    15 characters
lookup  = 5 characters   (25 bits)            sent to the relay
secret  = 10 characters  (50 bits)            never leaves the two daemons and the humans
display = LLLLL-SSSSS-SSSSS                  e.g. 7KQ2M-9XHF4-TRW8N
```

- **Alphabet:** Crockford base32, `0123456789ABCDEFGHJKMNPQRSTVWXYZ`
  (`envelope.PairAlphabet`).
- **Generation:** on the issuer daemon only. Take 15 bytes from `crypto/rand` and map
  each byte `b` to `PairAlphabet[b & 31]`. 256 is a multiple of 32, so there is no bias.
  Characters 0–4 are the lookup and 5–14 are the secret.
- **Normalisation of user input** (same rules as v1, `envelope.NormalizePairCode`):
  case-insensitive; `-` and space are ignored; the aliases `O`→`0` and `I`,`L`→`1` apply.
  Any other character outside the alphabet makes the input invalid. After
  normalisation, **15** characters is a v2 code and **10** characters is a v1 code. Any
  other length is invalid.
- **No check character.** A typo in the lookup gives `pair_invalid` from the relay. A
  typo in the secret gives `bad_confirm` and uses one of the issuer's 3 attempts. Both
  are visible at once, and the user retries.
- **Encoding for the KDF:** `lookup` and `secret` are the normalised ASCII bytes
  (uppercase, no separators).

## Keys and tags

```
K     = Argon2id(password = secret,                         // 10 ASCII bytes
                 salt     = "dorylinae-pair-v2\n" ‖ lookup,  // 18 + 5 = 23 bytes
                 time = 3, memory = 65536 KiB, threads = 1, keyLen = 32)

T     = SHA-256( "dorylinae-pair-v2-transcript\n"            // 29 ASCII bytes
                 ‖ lookup                                    // 5 ASCII bytes
                 ‖ u32(len card_I) ‖ card_I
                 ‖ u32(len card_R) ‖ card_R
                 ‖ u32(len mbox_I) ‖ mbox_I
                 ‖ u32(len mbox_R) ‖ mbox_R )

tag_I = HMAC-SHA256(K, "issuer\n"   ‖ T)                     // 32 bytes
tag_R = HMAC-SHA256(K, "redeemer\n" ‖ T)                     // 32 bytes
```

- `u32(n)` is `n` as 4 bytes, big-endian. In `u32(len X)`, `n` counts the **bytes** of the
  canonical UTF-8 form of `X` (below), not characters: `é` counts 2. The lengths in the
  test vectors are byte counts.
- In Go: `argon2.IDKey([]byte(secret), salt, 3, 64*1024, 1, 32)` from
  `golang.org/x/crypto/argon2`. `threads` changes the output, so it is fixed at 1.
- **`card_X`** is the canonical JSON ([agent-card.md §Canonical serialisation](agent-card.md#canonical-serialisation))
  of the object `{"card": <card>, "signature": <signature>}`, built from the
  generically parsed card envelope. Other top-level members are dropped.
- **`mbox_X`** is the canonical JSON of `{"announcement": <announcement>, "signature": <signature>}`,
  built the same way ([mail.md §Announcement](mail.md#announcement)).
- **Why canonical and not the received bytes:** the relay forwards `card` and `mbox`
  as `json.RawMessage` inside a re-marshalled control frame. `encoding/json` compacts
  them and escapes `<`, `>` and `&` as six-character Unicode escapes (a backslash, `u`, then `003c`, `003e` or `0026`). So the bytes
  a daemon sends and the bytes its peer receives differ in general (the reference
  card below contains `<é>` and `&`). Canonicalising the parsed value gives both sides the same bytes.
  A relay that changes any value changes the canonical form, and with it `T`.
- Each side computes `T` with **its own** card and mailbox announcement as sent, and the
  peer's as received and verified.
- Compare tags with `hmac.Equal` (constant time). Wipe `K` and the secret from memory
  (overwrite the slices) when the pairing ends.

### KDF parameters

Only a hostile relay can obtain a tag to attack offline: the redeemer's `tag_R`, sent
after the human has typed the code. It must then recover the secret within the
redeemer's **60 s** confirm wait (to fool the redeemer), or before the issuer's local
code TTL ends, at most **600 s** after issuance (to fool the issuer with a forged `tag_R`
of its own). The table uses the longer, 600 s window.

| Stretching | Attacker cost to reach success probability *p* in 600 s |
|---|---|
| None (plain HMAC-SHA256) | 2^50·*p* hashes. For *p* = ½, that is 9.4·10^11 guesses/s, about 100 current GPUs. **Not acceptable.** |
| Argon2id, t = 3, m = 64 MiB, p = 1 | Each guess fills and passes 3 times over 64 MiB. That is about 0.4 GB of memory traffic and 64 MiB of RAM per guess in flight. A GPU with 80 GB of memory and 3 TB/s manages at most ~1,250 guesses in parallel and a few thousand guesses/s. *p* = ½ needs about 10^8 such GPUs for 10 minutes. Even *p* = 2^-10 needs about 10^5 GPUs **per pairing**. **Accepted.** |

Honest cost: one derivation per side per pairing, 64 MiB of RAM, about 0.1–0.5 s on a
laptop. The parameters are RFC 9106's second recommended option with one lane instead of
four. The total work and memory are the same as with four lanes; one lane only gives up
multi-core parallelism, which keeps the derivation to one core. Changing any parameter is
a protocol version change (v3).

## Flow

```
Issuer I (pair --new)                  relay                    Redeemer R (pair <code>)
 generate code; start K = Argon2id(...)
 pair_new{lookup, card_I, mbox_I} ---->   store H(lookup) -> (I, card_I, mbox_I)
 <---- pair_code{expires}                 (no code in the reply)
 show LLLLL-SSSSS-SSSSS                                   human carries the code
                                          <---- pair_redeem{lookup, card_R, mbox_R}
                                                          (R computes K in parallel)
 <---- pair_peer{key_R, card_R, mbox_R}   pair_peer{key_I, card_I, mbox_I} ---->
 verify card_R, mbox_R; compute T                         verify card_I, mbox_I; compute T
 <-----------------------------------  envelope pair.confirm{tag_R}
 check tag_R
 ok: store R (trust=code)
 envelope pair.confirm{tag_I} ----------------------------------->  check tag_I
 pair_cancel{lookup} -> relay                             ok: store I (trust=code)
```

The redeemer always sends its tag first. The issuer never sends a tag before it has
verified a correct `tag_R`, so a redemption with a wrong secret (a stranger who guessed
the lookup, or a typo) gets nothing that could be attacked offline. The redeemer sends
exactly one tag per code (see [Redeemer](#redeemer-agentnet-pair-code)), so a hostile
relay gets at most one `tag_R` sample and must use it within the windows described in
[KDF parameters](#kdf-parameters). This order is what bounds those windows by the honest
daemons' clocks: with the issuer sending first, a hostile relay could redeem the lookup as
soon as `pair_new` arrives and keep cracking `tag_I` until the human finally types the code
on the redeemer, however late that is.

## Relay frames (v2)

`ref` is an optional client-chosen correlation string (at most 128 bytes, `MaxRefLen`),
echoed on the reply and on `error` frames. The daemon uses its pairing id
(`pair-` + 16 hex) as `ref`.

| Field | Rules |
|---|---|
| `lookup` | String, exactly 5 characters of `PairAlphabet` (the relay normalises with the same rules and then requires length 5) |
| `card` | JSON object ≤ 16 KiB (`MaxCardBytes`). Opaque to the relay |
| `mbox` | JSON object ≤ 4 KiB (`MaxMboxBytes`, new). Opaque to the relay. Required in v2 |

### `pair_new` (daemon → relay)

```json
{"op":"pair_new","lookup":"7KQ2M","card":{...},"mbox":{...},"ref":"pair-0123456789abcdef"}
```

The relay stores the entry under `SHA-256("dorylinae-pair-lookup-v2\n" ‖ lookup)`, in memory
only. It never stores or logs the lookup itself, as with v1 codes. If an unexpired v2
entry already has that hash, the relay answers `error{code:"pair_lookup_taken"}` and stores
nothing. The issuer then generates a **completely new code** (lookup and secret) and sends
`pair_new` again, at most 3 times. After that the pairing fails with `pair_lookup_taken`.
The limit of 5 outstanding entries per key (`pair_limit`) counts v1 and v2 entries together.

Every v2 `pair_new` counts towards a second per-key limiter, whatever its outcome: at most
10 per key per minute, then `pair_rate_limited`. Without it, `pair_new` (answered
`pair_lookup_taken` for an outstanding lookup) plus `pair_cancel` (which frees the slot at
once) would let one key test every lookup for existence without touching the redemption
limiter. The relay also holds at most 10000 entries in total (v1 and v2); past that
`pair_new` gets `pair_limit`. An honest issuer sends at most 4 `pair_new` per pairing.

### `pair_code` (relay → daemon)

```json
{"op":"pair_code","expires":"2026-01-02T03:14:05Z","ref":"pair-0123456789abcdef"}
```

In v2 there is **no `code` member**. If a v2 issuer receives a `pair_code` that has
`code`, the relay is v1-only (it ignored `lookup`). The issuer then fails the pairing
with `relay_v1` and does not show the relay's code. `expires` is informational only (see
[Local timers](#local-timers)).

### `pair_redeem` (daemon → relay)

```json
{"op":"pair_redeem","lookup":"7KQ2M","card":{...},"mbox":{...},"ref":"pair-89abcdef01234567"}
```

Exactly one of `lookup` (v2) or `code` (v1) must be present. Both, or neither, gives
`bad_pairing`. Lookup failures (unknown, expired, cancelled or exhausted) give
`pair_invalid` and count towards the limiter exactly like v1 failures: 5 failures per
minute per key, then `pair_rate_limited`. Redeeming your own lookup gives `bad_pairing`.

On a hit, the relay first sends `pair_peer` to the issuer. If the issuer is not connected
it answers `peer_offline`, and if its send buffer is full it answers `peer_busy`. Neither
case counts as a redemption. The relay then sends `pair_peer` to the redeemer and
increments the entry's **redemption count**. **Unlike v1, a successful redemption does
not delete the entry.** The entry is deleted by `pair_cancel` from the issuer, when it
expires (10 min after `pair_new`), or when its redemption count reaches 3. The issuer
enforces the real limit (see [Attempts](#attempts-and-the-3-bad-tag-abort)). The relay cap
only bounds work.

### `pair_peer` (relay → daemon)

```json
{"op":"pair_peer","public_key":"<other side's key>","card":{...},"mbox":{...},"ref":"..."}
```

This is sent once to each side per redemption. `public_key` is the key the relay
authenticated for the other side. `card` and `mbox` are the other side's values as the
relay received them, re-encoded (see [Keys and tags](#keys-and-tags)). The frame sent to
the issuer carries the `ref` of its `pair_new`.

### `pair_cancel` (daemon → relay, new)

```json
{"op":"pair_cancel","lookup":"7KQ2M"}
```

This deletes the sender's own v2 entry for `lookup`. There is no reply. An unknown lookup,
or a lookup issued by another key, is ignored silently, which also avoids acting as an
oracle. The issuer sends it when its pairing ends for any reason: complete, failed or
expired.

### Errors

| Code | Meaning |
|------|---------|
| `pair_invalid` | Code/lookup unknown, expired, cancelled, used (v1) or exhausted (v2) |
| `pair_rate_limited` | Too many failed redemptions, or too many v2 `pair_new`, from this key; retry after the window |
| `pair_limit` | Issuer already has 5 outstanding codes, or the relay holds 10000 |
| `pair_lookup_taken` | **New.** v2 `pair_new` with a lookup whose hash is already outstanding |
| `pair_v1_disabled` | **New.** v1 `pair_new` (no `lookup`) or `pair_redeem` with `code`, on a relay without `--allow-pairing-v1` |
| `bad_pairing` | Malformed frame: missing, oversize or non-object `card`/`mbox`; bad `lookup`; `lookup` and `code` both or neither present; oversize `ref`; own code |
| `peer_offline` | Issuer is not connected. Not counted as a redemption |
| `peer_busy` | Issuer's outbound queue is full. Not counted as a redemption |

Pairing errors never close the connection.

## `pair.confirm` envelope

This is an ordinary envelope ([envelope.md](envelope.md)). The relay needs no new code
to route it, and queues it like any other envelope.

| Field | Value |
|---|---|
| `from` / `to` | Own key / the `public_key` of the `pair_peer` frame of this attempt |
| `team` | `""` |
| `type` | `pair.confirm` |
| `id` | `pc-` + 16 lowercase hex characters from `crypto/rand` |
| `ts` | Sender clock, RFC 3339 |
| `payload` | Standard base64 of the canonical JSON below (plaintext: the tag is a MAC, not a secret) |

```json
{"lookup":"7KQ2M","tag":"<base64url, no padding, 32 bytes>","v":2}
```

The receiver parses the payload strictly: valid UTF-8, a JSON object with exactly the
members `lookup`, `tag` and `v`, `v` equal to `2`, and `tag` decoding to exactly 32
bytes. A `pair.confirm` is **accepted only** if all of these hold:

1. There is a pending attempt whose peer key equals the envelope `from` and whose lookup
   equals `lookup`.
2. The receiver is waiting for a tag on that attempt. The issuer waits for `tag_R` from
   the moment it has verified the attempt's `pair_peer`. The redeemer waits for `tag_I`
   after it has sent `tag_R`.
3. No `pair.confirm` has been accepted for that attempt yet. Each attempt takes exactly one.

Anything else is dropped without a reply. The drop is logged at debug level
(`event=pair_confirm_dropped`) and not audited. `pair.confirm` is handled by the pairing
manager **before** the session layer's `unpaired` check, since the sender is by
definition not paired yet. No other envelope type from an unpaired key is accepted.

The receiver then compares the tag in constant time with the value it computes
itself, using role `issuer` for a received `tag_I` and `redeemer` for a received `tag_R`.

## Daemon behaviour

### Issuer (`agentnet pair --new`)

1. Generate the code. Start computing `K` in the background. It must finish before the
   first `tag_R` is checked.
2. Send `pair_new{lookup, card, mbox, ref}`. Record `issued_at = now` locally.
3. On `pair_code`, show the code as `LLLLL-SSSSS-SSSSS`. `pair` status and `--json` expose
   it as `code` while the pairing is pending, as in v1.
4. On each `pair_peer` (an **attempt**):
   - Verify `card_R`. The Agent Card signature must be valid and `card.public_key` must
     equal the frame's `public_key`. Verify `mbox_R` as in [mail.md](mail.md#announcement):
     signature valid, `identity` equal to the frame's `public_key`, and times valid. On
     failure the attempt fails with `bad_card` or `bad_mbox`.
   - Compute `T`. Send nothing yet.
   - Wait up to **60 s** for `pair.confirm` from `key_R`. If `tag_R` is correct, the
     pairing **completes**: store the peer with `trust=code` and its announcement, then send
     `pair.confirm{tag_I}` to `key_R`, then `pair_cancel{lookup}`. A wrong tag fails the
     attempt with `bad_confirm` and sends nothing. No tag within 60 s fails it with
     `confirm_timeout`.
5. Attempts may overlap, up to the limit in [Attempts](#attempts-and-the-3-bad-tag-abort).
   Received tags are checked one at a time (under the pairing's lock). The first attempt
   that completes ends the pairing, and every other attempt is abandoned: later tags for
   them are dropped unchecked.

### Redeemer (`agentnet pair <code>`)

1. Normalise the input. 15 characters means v2. 10 characters means v1, and the redeemer
   proceeds only with `--v1` (see below). Otherwise the result is `ErrBadCode`.
2. **Single use.** If this daemon has already sent a `tag_R` for this exact code (same
   lookup and secret) in the last 24 hours, fail with `code_used` and send nothing. The
   redeemer keeps `SHA-256("dorylinae-pair-used-v2\n" ‖ code)` for every code it has sent
   a tag for, in the daemon's SQLite store (`pair_used_codes`: `hash BLOB PRIMARY KEY`,
   `used_at INTEGER` unix seconds), and prunes rows older than 24 hours, so a daemon
   restart does not reopen the window. This
   stops a hostile relay from collecting a second `tag_R` sample, or stretching its
   cracking window, by failing a pairing and waiting for the user to retry the same code.
   A retry needs a new code from the issuer.
3. Send `pair_redeem{lookup, card, mbox, ref}` and compute `K` in the background.
4. On `pair_peer`, verify `card_I` and `mbox_I` as above (`bad_card` / `bad_mbox`).
   Compute `T` and `tag_R`. Once `K` is ready, record the code as used (step 2), then
   send `pair.confirm{tag_R}` to `key_I`.
5. Wait up to **60 s** after sending `tag_R` for `pair.confirm` from `key_I` carrying
   `tag_I`. If the tag is correct, **store the peer** (`trust=code`) and complete. If it is
   wrong, fail with `bad_confirm`. If no tag arrives, fail with `confirm_timeout`.
6. A redeemer pairing is a single attempt. It never waits for more than one `pair_peer`,
   and sends at most one tag.

If the issuer's `tag_I` is lost, the issuer has stored the redeemer but the redeemer has
not stored the issuer. The redeemer's attempt times out and the user sees a failure there.
Messages from the issuer are then rejected as `unpaired` until they pair again with a new
code. Re-pairing is idempotent on the issuer side.

### Attempts and the 3-bad-tag abort

An issuer's code allows at most **3 attempts**, counted when each `pair_peer` arrives.
A 4th and later `pair_peer` for the same code is ignored (logged at debug level). A
failed attempt is `bad_card`, `bad_mbox`, `bad_confirm` or `confirm_timeout`. When the
third attempt has failed, the pairing fails with `bad_confirm` (message "too many failed
attempts") and the issuer sends `pair_cancel`. The issuer sends `tag_I` only on the one
attempt that completes, so strangers and a hostile relay get no `tag_I` sample at all, and
online attacks (a forged `tag_R`) get at most 3 chances of 2^-50 each. The same cap bounds
the work a relay can cause by sending `pair_peer` frames.

### Local timers

The daemon does not trust the relay's clock or `expires`.

| Timer | Side | Value | On expiry |
|---|---|---|---|
| Code TTL | issuer | `issued_at + 10 min` | fail `expired` (unless complete), `pair_cancel` |
| Relay reply | both | 30 s after `pair_new` / `pair_redeem` without `pair_code` / `pair_peer` / `error` | fail `timeout` |
| Confirm wait | both | 60 s after `pair_peer` (issuer) or after sending `tag_R` (redeemer) | attempt fails `confirm_timeout` |

An attempt that is still waiting when the code TTL ends is failed too. Finished pairings
stay queryable for one hour. At most 16 pairings may be pending at once, as in v1.

### Failure codes (daemon)

These appear in `pair.fail` audit rows and in the pairing status `error.code`, alongside
the relay codes above: `bad_card`, `bad_mbox` (new), `bad_confirm` (new), `confirm_timeout`
(new), `relay_v1` (new), `code_used` (new), `pair_lookup_taken`, `store_error`, `timeout`,
`expired`.

### Storage and trust states

`peers` table, migration added by 0.8b:

```sql
ALTER TABLE peers ADD COLUMN trust TEXT NOT NULL DEFAULT 'relay'
    CHECK (trust IN ('relay', 'code', 'fingerprint'));
ALTER TABLE peers ADD COLUMN mailbox_keys TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(mailbox_keys));
```

| `trust` | Meaning | Set by |
|---|---|---|
| `relay` | Key came from a v1 pairing. A hostile relay could have substituted it | Migration default for existing rows; any v1 pairing |
| `code` | Key confirmed by a v2 code MAC | v2 pairing |
| `fingerprint` | A human compared the fingerprint out of band | `agentnet peers verify` |

Rank: `relay` < `code` < `fingerprint`. **`Store.Add` never lowers `trust`.** It stores
the higher of the old and new values. A re-pair of a known key updates `name`, `harness`,
`skills` and `card`, keeps `paired_at`, and merges the announcement into `mailbox_keys`
(see [mail.md](mail.md#peer-storage)). A re-pair that returns a **different key** for a
known name is simply a new peer. The user resolves it with `agentnet peers remove`
(0.8b). `peers remove` deletes the row, so later envelopes from that key are rejected as
`unpaired`.

`mailbox_keys` is a JSON array of at most 2 signed announcement objects, newest first,
defined in [mail.md](mail.md#peer-storage).

**Policy (owner decision 2).** From Phase 1 (1.4), if the configured relay is not on a
loopback address, the daemon refuses to send requests to, and to accept requests from,
peers with `trust=relay`. The error is `unverified_peer`, and the user must re-pair (v2)
or run `peers verify`. On a loopback relay it only warns.

### Mailbox key during pairing

`mbox` is the sender's **current** mailbox key announcement ([mail.md](mail.md#announcement)).
The pairing manager needs one from 0.8c onwards. **0.8c therefore implements creating the
first mailbox key and its announcement**: generate the key, store it in the keystore and
`mailbox_keys_own`, and sign the announcement. It also verifies received announcements.
Rotation and deletion stay in 1.0b.

## Fingerprints

```
digest = SHA-256("dorylinae-fingerprint-v1\n" ‖ pub)          // pub: 32 raw Ed25519 bytes
fp     = first 20 characters of Crockford-base32(digest)     // 100 bits
display: 5 groups of 4 separated by one space, e.g. "2ED9 TGVE R471 63MC C451"
```

**Crockford-base32(bytes)** reads the bytes as a bit string, most significant bit first,
and emits one `PairAlphabet` character per 5 bits. There is no padding. A final group
shorter than 5 bits is padded with zero bits on the right. It does not matter here,
because only the first 20 characters (100 of 256 bits) are used.

- Shown by `agentnet identity` (own), `agentnet peers` (each peer) and on `pair` completion
  (the peer's). In `--json` the member is `"fingerprint"` holding the 20 characters without
  spaces. CLI doc updates are part of 0.8b.
- `agentnet peers verify <peer> <fp>`: normalise `<fp>` like a code (case, `-`/space,
  aliases), require 20 characters, and compare in constant time with `fp(peer key)`. A match
  sets `trust=fingerprint` and exits 0. A mismatch exits non-zero and changes nothing.
  `<peer>` is a public key or a unique peer name.
- A fingerprint is the only way to detect a key substituted during an earlier v1 pairing.

## v1 compatibility

- **Relay:** `--allow-pairing-v1` enables the v1 frames: `pair_new` without `lookup` (the
  relay generates a 10-character code and replies `pair_code{code}`), and `pair_redeem` with
  `code` (single use, deleted on redemption). Default: **on** when the relay listens only on
  loopback, **off** otherwise (`--allow-non-loopback`), and off everywhere from 4.1. With
  the flag off, both v1 frames get `pair_v1_disabled`.
- **Daemon:** always issues v2 codes. It redeems a 10-character code only with
  `agentnet pair --v1 <code>`, and stores the result as `trust=relay` with an empty
  `mailbox_keys`. A v1 peer has no mailbox key, so sending mail to it fails with
  `no_mailbox_key` ([mail.md](mail.md#sending)). The remedy is a v2 re-pair.
- **Downgrade:** a v2 code is 15 characters and can never be redeemed as v1. A relay can
  refuse v2, which is visible as `relay_v1` or an error, but it cannot turn a v2 pairing into
  a v1 one.

### Migration of existing installations

1. Migration (0.8b) sets `trust='relay'` and `mailbox_keys='[]'` on every existing row.
2. Users re-pair each peer with v2. Both rows become `code` and gain mailbox keys. They may
   also compare fingerprints, which sets `fingerprint`.
3. If a re-pair shows a different key for a known name, the old row may be the result of a
   relay substitution. Compare fingerprints, then `agentnet peers remove` the wrong one.

## Logging, audit, counters

- The relay logs `pair_issue`, `pair_redeem`, `pair_fail` and `pair_cancel` with abbreviated keys
  (first 8 characters). It never logs lookups, lookup hashes, codes, cards or announcements.
  Counters (`Server.PairStats`): issued, redeemed, invalid, rate-limited, plus a new one,
  lookup-taken.
- Daemon audit (`audit_events`): `pair.start` (actor `cli`; `{id, role, version}`),
  `pair.complete` (actor `daemon`; `{id, role, peer, trust}`), `pair.fail` (actor `daemon`;
  `{id, role, code, reason}`), and `pair.attempt_fail` (new; actor `daemon`;
  `{id, peer, code}`) for each failed issuer attempt. **The code, lookup, secret, K,
  tags, cards and announcements are never logged or audited.**

## Test vectors

Produced by `go run ./tools/specvectors` (a spec tool). 0.8e adds an independent check.
All values are deterministic. The keys are the identity seeds `00 01 … 1f` (issuer, the same
key as the [Agent Card vector](agent-card.md#test-vector)) and `20 21 … 3f` (redeemer). The
mailbox keys are the X25519 private keys `40 41 … 5f` (issuer) and `60 61 … 7f` (redeemer),
used as raw 32-byte scalars (`ecdh.X25519().NewPrivateKey`).

```
key_I     A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg
key_R     Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc
fp(key_I) 2ED9 TGVE R471 63MC C451
fp(key_R) 2P56 R8XN KZYG XBXC JB4S

code      7KQ2M-9XHF4-TRW8N      lookup = "7KQ2M", secret = "9XHF4TRW8N"
```

`card_I`: 333 bytes (not characters: the name contains `é`, which is 2 bytes in UTF-8), canonical:

```
{"card":{"created":"2026-01-02T03:04:05Z","harness":"custom","name":"Ada \"test\" <é>","public_key":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","skills":[{"description":"a/b & c","id":"review","name":"Code review"}],"version":1},"signature":"XN3GYSED9twF4mei-x7TUzHYzOMQU7aonCRQkebGdcXr8MvkkjLQVjZmtPiCNLTNigKIskMMBqF9hgQW5jdPDA"}
```

`card_R`: 271 bytes:

```
{"card":{"created":"2026-01-02T03:05:00Z","harness":"claude-code","name":"bob-laptop","public_key":"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc","skills":[],"version":1},"signature":"iQYZKqktLeJmQ-5oOyZGlE0x0mOIQig-IehHbzqfg_vequeSqg_q1jHkj4up6ZEx3D6_la-yxcDzfWqaD0eHAg"}
```

`mbox_I`: 330 bytes (key_id `67ca2ffd6fe9efab`):

```
{"announcement":{"created":"2026-01-02T03:00:00Z","identity":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg","key_id":"67ca2ffd6fe9efab","not_after":"2026-01-16T03:00:00Z","pub":"eaYx7t4b-cmPEgMs3q3Q56B5OY_HhriMyEbsia-FpRo","v":1},"signature":"atOsE_ZJ-DFU33ceBR82Ws02HDvWF_Rslw6OqGxYgSQLn3E973R7yQ3doveCNdVLVitHrc7wksmLa7qIx50OBA"}
```

`mbox_R`: 330 bytes (key_id `16786d4e5ef74411`):

```
{"announcement":{"created":"2026-01-02T03:00:00Z","identity":"Kay64UG8yvCyLhqU000LxzYeUm0L_hLIl5S8kyKWbdc","key_id":"16786d4e5ef74411","not_after":"2026-01-16T03:00:00Z","pub":"Z13VdO13iTELPS52gfN5C0ZsdzsVIf7PNld5WDcepS8","v":1},"signature":"pwWy0FSMPGVng-ss-MTEcBceJ7Rb0-fmFI3gxD_iNAlKDMN6xPecIXLBE7IEu3uhrhxeHUydaM3f1A0wwFvKBQ"}
```

Derived values (hex unless noted):

```
salt   646f72796c696e61652d706169722d76320a374b51324d
K      5f88441eb745d2b0e0b7fa7dc82a9fecc174bda5e24129e133f7588830b07022
T      f59103e3521337c0e8fb3a0768907d916b77ad1e8128ba946bb839f50dfd5de1   (SHA-256 input: 1314 bytes)
tag_I  2f507289395dea825a26c1327d1f0cbc6b83792d543af6286e540d6a40a9ecee
       base64url: L1ByiTld6oJaJsEyfR8MvGuDeS1UOvYoblQNakCp7O4
tag_R  c645c221f9ca3c0e1ded71f2bc03294b7a0dc158245da4f83ebe211769dd69b9
       base64url: xkXCIfnKPA4d7XHyvAMpS3oNwVgkXaT4Pr4hF2ndabk
```

`pair.confirm` payloads: the plaintext, then its standard base64 as it appears in `payload`.
Redeemer's (sent first):

```
{"lookup":"7KQ2M","tag":"xkXCIfnKPA4d7XHyvAMpS3oNwVgkXaT4Pr4hF2ndabk","v":2}
eyJsb29rdXAiOiI3S1EyTSIsInRhZyI6InhrWENJZm5LUEE0ZDdYSHl2QU1wUzNvTndWZ2tYYVQ0UHI0aEYybmRhYmsiLCJ2IjoyfQ==
```

Issuer's:

```
{"lookup":"7KQ2M","tag":"L1ByiTld6oJaJsEyfR8MvGuDeS1UOvYoblQNakCp7O4","v":2}
eyJsb29rdXAiOiI3S1EyTSIsInRhZyI6IkwxQnlpVGxkNm9KYUpzRXlmUjhNdkd1RGVTMVVPdllvYmxRTmFrQ3A3TzQiLCJ2IjoyfQ==
```

Negative checks for 0.8c / 0.8e: changing any single byte of `card_R` or `mbox_R`, or swapping
the roles (computing `tag_I` with `"redeemer\n"`), must give a different tag. A secret of
`9XHF4TRW8P` must give a different `K`.
