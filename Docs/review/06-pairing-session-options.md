# 06 – Pairing trust model and async session design: options

Date: 2026-09-21. Author: Pairing & Session Researcher (Dorylinae team). Research and design only; no source edited.
Inputs: `Docs/review/05-expert-review.md` (H1, H3, M1), `Docs/review/03-crypto-summary.md`, `Docs/protocol/{pairing,session,agent-card}.md`, `Docs/AgentNet Free Tier Build Plan.md`, and the code cited below. Library facts were checked in the local module cache (`flynn/noise@v1.1.0`, `golang.org/x/crypto@v0.57.0`) and the Go 1.27.1 toolchain (`GOROOT/src/crypto/hpke`).

## 0. Summary

| Part | Recommendation |
|---|---|
| A. Pairing trust | **Code-bound MAC ("pairing v2") with an Argon2id-stretched secret, plus key fingerprints.** The code becomes `lookup(5) + secret(10)`. The relay only sees the lookup part. Each side sends `HMAC(K, role ‖ transcript)` over both cards and both mailbox keys. Fingerprints (`agentnet identity` / `peers`) are the migration path for already-paired peers and a manual check. PAKE (CPace) stays as the planned v3 if shorter codes are wanted. It is not the first step, because Go has no mature, audited PAKE library. |
| B. Session durability | **Confirm the concern, push back on the mechanism.** The outbox alone does need both daemons online at once. But IK/KK *sessions* are the wrong fix. Noise static keys are not persistent today, IK/KK first-message payloads have exactly the security of a one-shot sealed box, and a KK session still needs persisted state for message 2 onward. **Recommended:** keep the sender-side outbox, and send every application message as a self-contained **sealed "mail" envelope**: HPKE (stdlib `crypto/hpke`, X25519 / HKDF-SHA256 / ChaCha20-Poly1305) to the peer's rotating, identity-signed **mailbox key**, with an inner Ed25519 signature for sender auth. It is acked by a sealed `ack` mail, with a persistent `(sender, id)` dedupe table on the receiver. Noise XX stays for interactive traffic (ping, later live streams). Noise transport state is **not** persisted. |
| Tickets | **11 tickets** (§5): 0.8a–0.8e (pairing v2, before any hosted or non-loopback relay use), 1.0a–1.0f (mail and outbox, before 1.4/1.9). |

---

## Part A — Pairing trust model

### A.1 Current state (why it is broken against a hostile relay)

- The relay generates the code (`internal/relay/pairing.go:142-154`) and keeps both cards (`:156`, `:224`, `:234`). The only secret in the protocol is known to the relay.
- The daemon accepts any validly self-signed card whose `public_key` equals the key the relay says it authenticated (`internal/peers/pairing.go:325-327`). A relay can mint its own identity keys, so this check proves nothing against it.
- Noise then binds sessions to *whatever key was stored* (`internal/noise/noise.go:84-101`, prologue `:104-106`). A relay that substituted keys at pairing sits in the middle of every session from then on, invisibly.
- Re-pairing a known key silently refreshes the card (`internal/peers/store.go:41-44`). There is no `peers remove` (no such command in `Docs/cli/peers.md`) and no trust or verification state per peer.
- `Docs/protocol/agent-card.md:86-87` says "Trust in a key comes from pairing". That is not true against a malicious relay.

### A.2 Threat model used below

The attacker controls the relay (hosted relay 4.1 compromised, or a malicious operator). It can read, drop, delay, reorder and inject any frame, and it can create any number of identity keys. It does **not** control either endpoint, or the channel the human uses to carry the code (voice, chat, in person). Pairing window: the code TTL, 10 min (`internal/relay/pairing.go:15`). Goal: after pairing, each side has stored the *real* other side's identity key, or pairing fails visibly.

### A.3 Options compared

**(1) Code-bound MAC.** The issuer daemon, not the relay, generates the code as `lookup ‖ secret`. Only `lookup` goes to the relay. Both sides derive `K = KDF(secret)` and exchange `HMAC(K, role ‖ transcript)`, where the transcript covers both cards.

**(2) PAKE (CPace or SPAKE2).** The code is a low-entropy password. The two daemons run a balanced PAKE through the relay and key-confirm over the transcript of both cards.

**(3) SAS / fingerprint comparison.** Each side shows a short string derived from the keys, and the humans compare them out of band.

| Criterion | (1) Code-bound MAC + Argon2id | (2) PAKE (CPace) | (3a) Short SAS over both keys | (3b) Per-key fingerprint (≥ 80 bit) |
|---|---|---|---|---|
| **Hostile relay can MITM?** | Only by recovering `secret` *offline* from an honest side's tag within the pairing window (≤ 10 min). The relay always gets to see one honest tag first, so offline guessing is inherent to MAC schemes. With 50-bit secret and Argon2id (64 MiB, t=3, ~0.2–0.5 s/guess), 2^49 guesses in 600 s needs ~10^12 Argon2id evaluations per second: not feasible. Without stretching (plain HMAC), 2^50 SHA-256 in 10 min is ~400 high-end GPUs: **marginal, so stretching is mandatory.** | Only by guessing the code online: **one guess per protocol run**, and the run then fails visibly. With a 50-bit code, success is 2^-50. The relay learns nothing to attack offline. Strongest option. | 6-digit SAS without commitment: the relay grinds keys until the SAS matches (2^20 keygens, trivial). Needs commit-then-reveal (ZRTP / Bluetooth numeric comparison) to be safe, which is new protocol work comparable to (1). | Relay needs a key whose truncated hash matches the real one (second preimage on ≥ 80 bits): not feasible. **Secure only if the humans actually compare it.** |
| **Online guessing by strangers (not the relay)** | Guessing `lookup` (25 bits, 5 outstanding codes per key, `pairing.go:137`) gets a match, but the attacker must still produce a valid tag: 2^-50 per attempt. The issuer daemon aborts after 3 bad tags, so the main effect is DoS on that code, bounded by the existing 5 fails/min/key limiter (`pairing.go:176`) and by M2's per-IP limits. | Same DoS surface. One guess per run. | n/a | n/a |
| **Code entropy / length** | 15 chars `XXXXX-XXXXX-XXXXX` (25-bit lookup + 50-bit secret). 5 more chars than today. | Could stay at 10 chars, or shrink to ~8, since only online attacks matter. | n/a (display only) | 20 chars shown, e.g. `K7Q2 M9XH F4TR W8NC 3JDA` |
| **UX** | Unchanged flow (`pair --new` prints the code, `pair <code>` on the other side), with a slightly longer code. | Unchanged flow, current code length. | Both humans must look at both screens at the same time (or on a call). | Optional, asynchronous: paste your fingerprint into chat, and the peer runs `agentnet peers verify @alice <fp>`. |
| **Library / maturity (Go)** | stdlib `crypto/hmac`, `crypto/sha256`; `golang.org/x/crypto/argon2` (x/crypto is already an indirect dependency, `go.mod`). Mature. | No PAKE in stdlib or x/crypto. Candidates: `filippo.io/cpace` (small, CFRG-draft CPace over ristretto255, v0 / experimental, unaudited as far as known); `salsa.debian.org/vasudev/gospake2` (SPAKE2, used by `psanford/wormhole-william`, unaudited); `bytemare/*` (experimental). CPace itself is still an IRTF CFRG draft. Hand-rolling CPace is possible but risky. | stdlib only | stdlib only |
| **Implementation cost vs current code** | S–M. Daemon: generate the code, Argon2id, and the tags in `internal/peers/pairing.go`, plus one new envelope type `pair.confirm`. Relay: store and look up by `lookup` instead of the full code (`pairing.go:74`, `:142-156`, `:190-195`), with a collision retry. The relay no longer generates the code. About 300–400 LoC plus tests. | M–L. Same relay changes, plus one more round trip (PAKE msg and confirm in each direction), plus a new, less mature dependency in the most security-critical path. | M (commitment round plus SAS UI) | S. A fingerprint function, CLI output, a `peers verify` command and a `trust` column. |
| **Protocol / doc changes** | `pairing.md` v2 (new code format, `pair_new{lookup}`, `pair.confirm` envelope, transcript, KDF parameters, threat model). `agent-card.md:86` rewritten. `envelope.md` gains the `pair.confirm` type. | Same, plus the PAKE message formats and a ciphersuite pin. | New SAS section | `identity.md` / `peers.md` CLI docs, and a `trust` column in `pairing.md` storage |
| **Migration of existing peers** | Existing rows get `trust='relay'`. Re-pairing with v2 upgrades them to `code`. Fingerprint verify upgrades them to `fingerprint`. **If the relay did substitute a key, re-pairing produces a different key**, so a `peers remove` command is needed. | Same | Same | This *is* the migration tool. |
| **Impact on Phases 1–4** | 1.1 `team invite` (one-time code) must reuse the same v2 code mechanism, not a relay-issued code. The mailbox key (Part B) rides in the confirmed transcript. It is a **hard prerequisite for 4.1** (hosted relay), so relay-issued v1 codes can be disabled on hosted relays. | Same | Same | Stays useful forever (auditing, re-verification after a suspected compromise). |

### A.4 Recommendation for A

**Adopt (1) code-bound MAC with Argon2id, and ship (3b) fingerprints alongside it.** Keep the protocol versioned so (2) CPace can replace the MAC step later without another migration.

Rationale:
- (1) moves the relay from "can MITM every pairing for free" to "must break a memory-hard KDF over 2^50 inside 10 minutes". That is adequate for the free-tier beta and for a hosted relay. It uses only stdlib and x/crypto and touches roughly two files per side.
- (2) is strictly stronger (online-only guessing), but the Go PAKE ecosystem is unaudited, v0 code. That risk lands in the one place where a bug silently voids all end-to-end security. Revisit when `filippo.io/cpace` or an x/crypto PAKE reaches v1, or if users complain about the 15-char code.
- (3b) is cheap. It is the only way to detect an *already* substituted key (existing peers, and any pairing done over a v1 relay). It also gives security-minded users an independent check. (3a) short SAS is not recommended: to be safe it needs a commit/reveal round, which costs as much as (1) and additionally requires both people at their screens at once.

### A.5 Proposed pairing v2 protocol

Constants: `lookup` = 5 Crockford chars (25 bits), `secret` = 10 Crockford chars (50 bits), both from `crypto/rand` **on the issuer daemon**. Display `LLLLL-SSSSS-SSSSS`. Normalisation as today (`internal/envelope/pairing.go:46`).

```
K  = Argon2id(password = secret, salt = "dorylinae-pair-v2\n" || lookup,
              time = 3, memory = 64 MiB, threads = 1, len = 32)
T  = SHA-256("dorylinae-pair-v2-transcript\n" || lookup
             || len32(card_I) || card_I || len32(card_R) || card_R
             || mbox_I || mbox_R)             // cards as received bytes; mbox = signed mailbox-key announcement (Part B)
tag_I = HMAC-SHA256(K, "issuer\n"   || T)
tag_R = HMAC-SHA256(K, "redeemer\n" || T)
```

```
Issuer I                         relay                         Redeemer R
 pair_new{lookup, card_I, mbox_I}  ------>        (relay stores SHA-256(domain||lookup); rejects collision with pair_lookup_taken)
 <------ pair_code{expires}                       (no code in reply any more; issuer already knows it)
                                      <------ pair_redeem{lookup, card_R, mbox_R}
 <------ pair_peer{key_R, card_R, mbox_R}      pair_peer{key_I, card_I, mbox_I} ------>
 envelope pair.confirm{tag_I}  --------------------------------->  R verifies tag_I (constant-time)
 <---------------------------------  envelope pair.confirm{tag_R}  R stores I (trust=code)
 I verifies tag_R, stores R (trust=code)
```

- `pair.confirm` is an ordinary envelope (`from`/`to` = the keys from `pair_peer`). The relay needs no new op. The daemon accepts `pair.confirm` only from the key of a *pending* pairing, and only once.
- The issuer sends first, so a MITM relay sees `tag_I` before it has to forge `tag_R`. That exposure is the reason for the Argon2id stretching.
- Failures: a bad tag on either side gives `pair.fail{code: bad_confirm}` and nothing is stored. The issuer allows at most 3 bad redeem attempts per code, then abandons the code. The daemon enforces the 10-min TTL locally and no longer trusts the relay's `expires`.
- `peers` table migration: add `trust TEXT NOT NULL DEFAULT 'relay'` (`relay` | `code` | `fingerprint`) and `mailbox_keys` (Part B). `Store.Add` must never downgrade `trust`. A re-pair that returns a **different card for a known name** is just a new key; the user resolves it with `peers remove`.
- Fingerprint: `fp(key) = Crockford(SHA-256("dorylinae-fingerprint-v1\n" || pubkey))[:20]`, shown in groups of 4. It is shown by `agentnet identity`, `peers`, and on `pair` completion. `agentnet peers verify <peer> <fp>` sets `trust=fingerprint` on a match and refuses otherwise.
- Relay: `pair_new` without `lookup` (v1) is accepted only when the relay runs with `--allow-pairing-v1` (default **off** from 4.1). The relay never sees `secret`, K or the tags' key.
- Policy (Phase 1): sending requests to a `trust=relay` peer produces a warning. Once a hosted relay is in use, it is refused unless `--allow-unverified`.

---

## Part B — Session durability (sender outbox + async first flight)

### B.1 Current state

- The Noise static key is **generated at every daemon start and held in memory only** (`internal/noise/noise.go:49-66`, called from `internal/daemon/ping.go:50`; `Docs/protocol/session.md:24`). Peers do not know each other's static key in advance. They learn it inside XX messages 2/3 and trust it through the Ed25519 binding (`noise.go:84-101`).
- Handshakes expire after 10 s (`internal/session/session.go:70`, sweep `:803`). Messages wait in a 16-deep in-memory queue per peer (`:73`, `:367-372`). Sessions live only in memory (`session.md:137-139`), and after a restart the peer's data is `unknown_session` (`session.go:43`).
- Relay: SQLite queue with per-(to, from, id) dedupe (`internal/relay/queue.go:85-100`), but the binary runs it in memory (H2, `cmd/relay/main.go:81`). The direct path is at-most-once (M1, `internal/relay/relay.go:300-306`). The client dedupes only an in-memory window of 8192 (`internal/relayclient/seen.go:8`).

### B.2 Is the Orchestrator's concern right?

**Yes.** With XX, the initiator can send nothing encrypted until it has read message 2, and message 2 needs the responder online. An outbox that holds a message until an XX session opens therefore needs both daemons online in overlapping windows (A must still be up when B's `session.resp` arrives). That is the normal case for laptops that sleep. The relay's 7-day queue would only ever carry `session.init` frames, which are useless on their own. A non-interactive first flight is needed.

### B.3 Options compared

| | (a) Outbox + XX only | (b) Outbox + persisted Noise transport state | (c) Outbox + Noise IK/KK first-message payload | (d) **Outbox + sealed mail (HPKE + Ed25519 sig), rotating mailbox keys** | (e) Outbox + Noise one-way `K` pattern + sig |
|---|---|---|---|---|---|
| Both online at once? | **Yes, required** | No, after the first ever handshake. A first contact, or a contact after a key/state loss, still needs co-presence. | No for msg 1. Any *continuation* of the session needs msg 2 back, so it is a session again. | **No, ever** | No |
| Forward secrecy | Full (ephemeral-ephemeral) | Full per session, but long-lived sessions mean long exposure. Persisted keys on disk are exposed to disk theft. | msg 1 payload: **none against compromise of the recipient's static key** (Noise §7.7 payload property: confidentiality 2) | None against the recipient's mailbox key compromise. **Bounded by rotation** (weekly key, deleted after TTL + grace, ≈ 2–3 weeks exposure) | Same as (d) if the static key rotates |
| KCI | No KCI | No KCI | **Yes**: with B's static key, an attacker can forge msg 1 "from A" (the `ss` term is computable). Source-auth property 1. | No: the Ed25519 signature requires A's identity key | Without a signature, KCI like (c). With a signature, none. |
| Replay | Counters (`noise.go:235-244`) | Counters, **but a restored backup or crash rollback reuses nonces: catastrophic for ChaCha20-Poly1305** | **msg 1 is replayable** by the relay or anyone; the responder must dedupe at app level | Replayable ciphertext; persistent `(from, id)` dedupe plus a `created` window | Same as (d) |
| New key material | none | per-peer session keys at rest (encrypted with a keystore key) | a **persistent** X25519 static key, distributed at pairing | a rotating X25519 mailbox key, signed by the identity key, distributed at pairing and by `keys` mail | a persistent or rotating X25519 static key |
| Library support | flynn/noise (in use) | flynn/noise has `CipherState.UnsafeKey`, `SetNonce`, `UnsafeNewCipherState` (`flynn/noise@v1.1.0/state.go:38`, `:102`, `:109`); no HandshakeState serialisation | flynn/noise has `HandshakeIK` / `HandshakeKK` (`patterns.go:83`, `:29`); msg 1 payload is encrypted after `es`; config needs `PeerStatic` | **stdlib `crypto/hpke`** in Go 1.27 (`hpke.Seal/Open`, `NewSender/NewRecipient`, `DHKEM(ecdh.X25519())`). Base mode only (`hpke.go:53`), so a signature is needed anyway. RFC 9180. | flynn/noise `HandshakeK` (`patterns.go:126`) |
| Complexity | lowest, but does not meet 0.7 / 1.9 | high (at-rest encryption, rollback safety, a state machine across restarts) | high: a new handshake family, persistent statics, per-message session bookkeeping, still needs dedupe and persistence for continuations | medium: one stateless seal/open, plus outbox and dedupe tables | medium, with a second key type and no advantage over (d) |

### B.4 Verdict on the IK/KK proposal

**Push back.** The goal (a non-interactive, relay-queueable first flight) is right. The mechanism is not the best choice:

1. **Prerequisite missing.** IK/KK need the peer's static key before the handshake starts. Today static keys are ephemeral per process (`noise.go:51-52`). So they must become persistent keystore secrets distributed and verified at pairing: the same key-distribution work (d) needs, with none of the stateless benefit.
2. **No security gain over a sealed box.** The IK/KK msg-1 payload has exactly the properties of a one-shot authenticated sealed box: no forward secrecy against the recipient's static key, KCI-vulnerable sender auth, and replayable. Calling it a "session" suggests XX-grade guarantees it does not have.
3. **It does not remove state.** Only msg 1 is async. Using the session afterwards needs msg 2 to come back and both sides to keep handshake or transport state across restarts: back to (b) and its nonce-rollback hazard. Using a fresh IK per app message is just (e) with extra baggage.
4. **The ack is itself async.** B's ack to an offline A must also be a first-flight message, so every message in both directions is a first flight. That is exactly the mail model.

So the design becomes: **mail for application messages, XX for interactive ones.** Requests, accept/decline/defer, results, grants and acks go as mail. Ping and later live streams (2.5 consult streaming, if needed) stay on XX, which keeps full forward secrecy where both sides are online anyway.

### B.5 Recommended design (d): sealed mail + outbox + dedupe

**Keys.**
- Mailbox key: X25519, generated by the daemon and stored in the keystore next to the identity seed (account `mailbox-<id>-<n>`). It rotates every 7 days. A retired private key is deleted after `queue_ttl (7 d) + grace (7 d)`, and at most 3 are live.
- Announcement: `mbox = {v:1, identity, key_id (8 bytes, SHA-256(pub)[:8]), pub, not_after}` signed by the identity key under the domain `"dorylinae-mailbox-key-v1\n"` (canonical JSON, as in `agent-card.md` §Canonical). It is exchanged in pairing v2 (it sits inside the MAC transcript, §A.5) and pushed to peers as a `keys` mail on rotation. Peers store the latest 2 announcements per peer in `peers.mailbox_keys`.

**Seal (sender A → recipient B).**
```
msg   = canonical JSON {v:1, id, from:A, to:B, created, kind, body}
signed = {msg, sig: Ed25519(A_identity, "dorylinae-mail-v1\n" || canonical(msg))}
info  = "dorylinae-mail-v1\n" || A || "\n" || B || "\n" || key_id
aad   = envelope id
enc, ct = HPKE.Seal(pk = B.mailbox[key_id], DHKEM(X25519)/HKDF-SHA256/ChaCha20Poly1305, info, aad, canonical(signed))
envelope {from:A, to:B, type:"mail", id, ts, payload: 0x01 || key_id(8) || enc(32) || ct}
```
The relay sees only `type: "mail"`, the ids and the size. There is one envelope type for all kinds.

**Open (B).** Look up the key by `key_id`, then HPKE.Open. Verify: `sig` under `msg.from`; `msg.from == envelope.from` and is a paired peer; `msg.to == self`; `msg.id == envelope.id`; `created` within `[now − 30 d, now + 10 min]`. Then run the dedupe below. Any failure gives an audit event `mail.reject{reason}` (rate-limited as in `session.md:156-159`), and no ack is sent.

**Exactly-once (effectively-once) processing.**
- Receiver: a single SQLite transaction inserts `mail_seen(from, id, received_at)` (primary key `(from,id)`) and the inbox row. It commits, then sends the ack. On a duplicate (`(from,id)` present) it skips processing and **re-sends the ack**. `mail_seen` rows are kept 35 days, which is longer than the `created` acceptance window, so pruning can never re-admit a replay.
- Sender outbox: `outbox(id, to, kind, sealed_payload, key_id, created, next_attempt, attempts, state)`. States: `queued → relayed → delivered | expired | failed`. On submit it writes the row, then sends. `agentnet request` returns `queued` in under 2 s, which is the 1.9 acceptance. It resends the **same envelope id** (the relay dedupes, `queue.go:94`) with backoff: 1 min, 5 min, 30 min, then every 6 h while the peer shows offline. It resends immediately on a presence-online edge (1.2). After 7 days it moves to `expired` and the requesting agent gets a machine-readable status.
- Ack: `kind:"ack", body:{ids:[...]}` sent as mail back to A. Acks are **not** themselves outboxed or acked. A lost ack just means A resends and B re-acks. A marks `delivered` and deletes the sealed payload.
- Key miss: if B no longer has the `key_id` (for example, rotated and deleted), it cannot decrypt. It replies with a `keys` mail carrying its current announcement plus `body.retry:[envelope ids]`. That leaks nothing new, because the ids are routing metadata the relay has anyway. A re-seals the outbox entries to the new key and resends them with the same id. The recipient's dedupe keys on `msg.id`, so this is safe.
- Ordering: not guaranteed and not needed (the inbox sorts by priority, 1.6). `created` is carried for display.

**Relay dependencies.** H2 (durable queue via `--queue-db`) is required, because the outbox resends but a 7-day offline window relies on the relay holding the mail. M1 (store-then-forward) becomes nice-to-have: the outbox covers a lost direct-path frame by resending.

**What changes in `session`.** Nothing for XX. `session.md` gains a "Scope" line: interactive only. The 16-message in-memory queue (`session.go:73`) stays for pings. `Docs/protocol/mail.md` is new.

**Rejected alternative, (b) persisted transport state.** Rejected because of the nonce-reuse hazard on rollback (restored backup, VM snapshot, crash between send and persist), at-rest key material, and because it still fails first contact after state loss.

---

## 3. Proposed protocol message changes (consolidated)

| Where | Change |
|---|---|
| `pair_new` (control) | + `lookup` (5 chars), + `mbox`. Relay-generated code only with `--allow-pairing-v1` |
| `pair_code` (control) | `code` omitted in v2. Keeps `expires` and `ref` |
| `pair_redeem` (control) | `lookup` replaces `code` in v2, + `mbox` |
| `pair_peer` (control) | + `mbox` (opaque, forwarded byte-for-byte) |
| new error | `pair_lookup_taken` (issuer regenerates the lookup) |
| new envelope type `pair.confirm` | payload `{v:2, tag}` (tag base64url, 32 bytes). Accepted only for a pending pairing |
| new envelope type `mail` | payload `0x01 ‖ key_id(8) ‖ enc(32) ‖ ct`. Kinds inside: `ack`, `keys`, and later `request`, `request.accept`, `request.decline`, `request.defer`, `result`, `grant`, ... |
| `peers` table | + `trust`, + `mailbox_keys` (JSON) |
| new tables | `outbox`, `mail_seen`, `mailbox_keys_own` (key_id, created, retired, deleted) |
| docs | `pairing.md` v2 plus threat model; `agent-card.md:86` wording; `session.md` scope; new `mail.md`; `envelope.md` type list; CLI `identity`/`peers` (fingerprint, `verify`, `remove`), `relay.md` (`--allow-pairing-v1`) |

## 4. Impact on the plan

- **Phase 0 exit:** H3's "stop B, send from A, start B, arrives once" is met by 1.0e at the mail level. Ping stays interactive, so fix `Docs/cli/ping.md` to say ping needs both online.
- **Phase 1:** 1.4 request, 1.6 inbox and 1.9 offline handling are built on mail (1.0a–1.0f are prerequisites). 1.1 `team invite` reuses the pairing v2 code mechanism. 1.2 presence drives outbox retry timing.
- **Phase 2:** grants and results ride in mail. Consult streaming may use XX when both are online. The grant revocation latency target ("within one second", 2.3) is enforced locally on the grantor, so it is unaffected.
- **Phase 3:** the 3.6 hash-chained audit can record mail `id`s. Inner signatures give durable, verifiable proof of who sent a decision.
- **Phase 4:** 4.1 hosted relay **requires** 0.8a–0.8d and H2, and needs `--allow-pairing-v1` off. 4.6 telemetry sees only `type: mail` counts, so it no longer learns per-kind counts ("requests by type and urgency" can no longer be counted relay-side; count them daemon-side with opt-in reporting, or drop them from 4.6).

## 5. Ticket list

Add these to the plan's phase tables first (plan §8). Pairing tickets land before any non-loopback or hosted relay use. Mail tickets land before 1.4.

| ID | Ticket | Depends on | Acceptance test |
|---|---|---|---|
| **0.8a** | Spec: pairing v2 (`pairing.md` rewrite with threat model, code format, KDF params, transcript, `pair.confirm`; `agent-card.md:86`; `envelope.md`) | – | Doc reviewed. Includes test vectors (secret, lookup, cards, mbox → K, T, tag_I, tag_R) |
| **0.8b** | Fingerprints and trust state: `fp()` helper; `identity`/`peers`/`pair` show fingerprints; migration adds `peers.trust` (existing rows `relay`); `agentnet peers verify <peer> <fp>`; `agentnet peers remove <peer>` | 0.8a | `peers verify` with the correct fp sets `trust=fingerprint`, with a wrong fp exits non-zero and changes nothing. `peers remove` deletes the row and later `session.*` from that key is rejected `unpaired`. Existing DB migrates with `trust=relay`. |
| **0.8c** | Pairing v2, daemon: code generation on the issuer, Argon2id K, tags, `pair.confirm` handling, 3-bad-tag abort, local TTL, `trust=code` | 0.8a, 0.8b | Two daemons pair via v2 and both list `trust=code`. **MITM test:** a test relay that swaps in its own card and key on both sides causes `bad_confirm` on both and stores nothing. Wrong secret with the right lookup fails. Same code a second time fails. The code's secret never appears in relay logs or frames (grep the captured frames). |
| **0.8d** | Pairing v2, relay: lookup-hash storage, `pair_lookup_taken`, `--allow-pairing-v1` (default on for loopback until 4.1, off for non-loopback) | 0.8a | Relay unit tests: v2 redeem by lookup; collision rejected; v1 refused without the flag; limiter unchanged (5 fails/min) |
| **0.8e** | Test vectors and an independent check: extend `tools/` (stdlib plus x/crypto only) to recompute K, T and tags from the vectors | 0.8a | The tool reproduces the vectors from 0.8a byte-for-byte |
| **1.0a** | Spec: `mail.md` (mailbox keys and rotation, seal/open, envelope payload, verification order, ack, dedupe window, outbox states and backoff, key-miss recovery); `session.md` scope line | 0.8a (mbox in transcript) | Doc reviewed, with open-side test vectors (fixed recipient key + recorded `enc`/`ct`; stdlib `hpke.Seal` draws its ephemeral from `crypto/rand`, so vectors are verified on Open, not reproduced on Seal) |
| **1.0b** | Mailbox keys: generate, store in keystore, signed announcements, rotation job, deletion after TTL + grace; exchanged in pairing v2 and stored in `peers.mailbox_keys` | 1.0a, 0.8c | After pairing, both sides hold each other's verified announcement. A forged announcement (bad sig) is rejected. The clock-driven rotation test yields ≤ 3 live keys, and the oldest is deleted at 14 d. |
| **1.0c** | `internal/mail`: Seal/Open with stdlib `crypto/hpke` + Ed25519, all verification checks, `mail.reject` audit (rate-limited) | 1.0a | Unit tests: round trip; tamper in enc/ct/aad → reject; `from` mismatch → reject; `to` ≠ self → reject; stale `created` → reject; signature by another paired key → reject; vectors from 1.0a pass |
| **1.0d** | Receiver dedupe and ack: `mail_seen` table; single-tx insert with the inbox row; ack after commit; re-ack on duplicate; prune at 35 d | 1.0c | The same envelope delivered 3 times gives exactly 1 inbox row and 3 acks. After a daemon restart, a replay is still deduped. |
| **1.0e** | Sender outbox: table, submit returns `queued` in under 2 s, resend with the same id and backoff, presence-edge resend hook (no-op until 1.2), `delivered`/`expired` states, key-miss re-seal | 1.0c, 1.0d, **H2** | **Harness (two daemons, real relay with `--queue-db`):** stop B; A sends; stop A; start B (mail arrives from the relay queue, processed once, ack queued); start A → outbox shows `delivered`. The relay restarted in the middle still delivers. Payloads on the wire and in relay logs are ciphertext only. |
| **1.0f** | Doc and CLI reconciliation: `envelope.md` types, `ping.md` (interactive only), `status --json` shows outbox counts; plan tables updated (0.8x, 1.0x; 4.6 telemetry note) | 1.0e | Docs match behaviour. `status --json` includes `outbox: {queued, relayed, expired}`. |

Count: **11 tickets** (0.8a–e, 1.0a–f).

## 6. Open points for the owner

1. Accept a **15-character** pairing code (v2 MAC) now, versus waiting for a mature PAKE to keep 10 characters?
2. Refuse requests to `trust=relay` peers on a hosted relay (recommended), or only warn?
3. Accept **non-repudiation** of mail (inner Ed25519 signatures make "A sent this" provable to third parties)? It suits decisions and audit (Phase 3). Deniable alternatives (Noise K without signature) reintroduce KCI.
4. 4.6 telemetry loses relay-side per-kind counts because all app traffic is `type: mail`. Is that acceptable?

## 7. Owner decisions (2026-09-21)

1. Accept the 15-character pairing code (v2 MAC) now; PAKE deferred to v3.
2. Refuse requests from `trust=relay` peers on a hosted relay (after the re-pair path exists).
3. Accept non-repudiation: mail carries an inner Ed25519 signature.
4. Relay telemetry counts only `mail`; per-kind counts move to daemon-side audit.
5. Session durability: sealed mail + sender outbox + receiver dedupe (§Part B). Noise XX kept for ping/interactive only.
6. Licence: PolyForm Shield 1.0.0.
