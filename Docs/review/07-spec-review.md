# 07 – Adversarial review: pairing v2 (0.8a) and mail (1.0a) specs

Date: 2026-09-21. Reviewer: W1-SpecReviewer. Target: worktree `AgentNet-wt/specs` (branch `w1/specs`, base a4fa53a):
`Docs/protocol/{pairing,mail,agent-card,envelope,session,README}.md`, `tools/specvectors/main.go`.
Basis: `Docs/review/06-pairing-session-options.md` (§5 tickets, §7 owner decisions).

**Verdict: ready-with-changes.** The changes are applied in this worktree. Counts: Critical 0, High 2, Medium 3, Low 15.
After the fixes, a worker could implement 0.8b–e and 1.0b–e from the docs. The ticket-table updates listed at the end are still needed.

## Findings

| # | Sev | Location | Issue | Fix |
|---|---|---|---|---|
| H1 | High | `pairing.md` §Threat model, §Flow, §Daemon behaviour | **The issuer sent its tag first, so a hostile relay got an unbounded offline window against the redeemer.** The relay sees `lookup` in `pair_new`. It can redeem immediately with its own key and get `tag_I` at t≈0. Then it can crack for as long as it takes the human to type the code on R. R has no way to know the code's age, because it never trusts the relay's `expires`. Once the relay has K, it poses as the issuer to R with its own card and a valid `tag_I`, and R stores the relay's key with `trust=code`. The "≤ 600 s" bound held only on the issuer side. A second problem: any stranger who guessed a lookup also got a `tag_I` sample. | **Applied.** The redeemer now sends `tag_R` first. The issuer sends `tag_I` only after it has verified a correct `tag_R`, and stores the peer before sending it. The relay's only sample is R's `tag_R`, and it must be used within R's 60 s confirm wait (to fool R) or before the issuer's local 10-min TTL (to fool I). Both limits are honest-daemon clocks. Strangers get no sample. Updated: the threat model, the KDF window text, the flow diagram, `pair.confirm` acceptance rule 2, the issuer and redeemer steps, the lost-tag paragraph and the confirm-wait timer. Tag formulas and vectors are unchanged. |
| H2 | High | `mail.md` §Receiving, `envelope.md` §Offline queue / §Client behaviour | **`relayclient` swallows mail resends.** `internal/relayclient/seen.go` drops any repeated `(from,id)` (8192-entry window, all types) before `OnEnvelope`. Mail resends keep the same id by design. So a resend after a lost ack never reaches `mail_seen`, and no re-ack is sent. Likewise, a re-sealed copy after a key miss is dropped. The sender's row ends `expired` even though the mail was delivered. This breaks the 1.0d acceptance ("3 deliveries → 3 acks") and key-miss recovery. | **Applied.** `mail` envelopes bypass the seen-set: they are handed up every time and still acked to the relay. The mail layer's persistent dedupe handles repeats. Documented in `mail.md` (with the reason) and in `envelope.md`. The code change belongs to 1.0d. |
| M1 | Medium | `pairing.md` §Attempts | Attempts could overlap, and only *failures* were counted. A relay could send N `pair_peer` frames and get N `tag_I` sends before any failure was counted. The claim "3 `tag_I` samples per code" was false, and it gave the relay unbounded send amplification. | **Applied.** At most 3 attempts per code, counted when `pair_peer` arrives. Later ones are ignored. Tags are checked one at a time under the pairing lock. (H1 also removes the samples.) |
| M2 | Medium | `pairing.md` §Redeemer | If the relay failed a pairing and the user retried **the same code**, the relay got another sample, and its cracking window stretched to the time of the retry. | **Applied.** Redeemer single-use: it keeps `SHA-256("dorylinae-pair-used-v2\n" ‖ code)` in memory for 24 h and fails with the new code `code_used`. A retry needs a new code. |
| M3 | Medium | `mail.md` §verification, §Key-miss recovery | The `ack` and `keys` body schemas were never validated: id format, allowed members, and where the check sits relative to dedupe. Key-miss put **unauthenticated** envelope ids (any `[A-Za-z0-9._:-]{1,128}`) into `retry`. A peer that validates `retry` would then reject the whole `keys` mail, and recovery would stall. | **Applied.** New step 12 (body schema, id format, limits, announcement check) runs before dedupe. Key-miss only records ids in mail-id format. |
| L1 | Low | `pairing.md` §KDF | The rationale "one lane is 4× cheaper for the honest side" was wrong. Lanes do not change total work or memory. | Fixed incidentally while editing that section. |
| L2 | Low | impl. note | `agentcard`'s strict parser and generic canonicaliser (`parseStrict`, `canonicalize`) are unexported. 0.8c, 1.0b and 1.0c all need them for card envelopes, `mbox` and `msg`. | Not fixed. Export them (or move them to `internal/canon`) in 0.8c. |
| L3 | Low | `pairing.md` §Issuer | There is no explicit rejection of a `pair_peer` whose `public_key` is the daemon's own key. It is not exploitable, because role labels separate the tags. | Not fixed. Add as a `bad_card` case. |
| L4 | Low | `mail.md` §Sending | Behaviour when the peer's newest announcement is past `not_after` is unstated (the sender still seals to it, and key-miss recovers). | Not fixed. State it. |
| L5 | Low | `mail.md` §Kind `keys` | The check `not_after > now` can reject a legitimately delayed rotation push. Worst case it arrives at created + 14 d = `not_after`. No ack is sent, and the sender resends until the row expires. Harmless. | Not fixed. |
| L6 | Low | `mail.md` §Dedupe | The prune argument says "older than now − 30 d + 10 min". The tight bound is `created < now − 35 d + 10 min`. The conclusion (no re-admission) holds. | Not fixed. |
| L7 | Low | `mail.md` §Sealing | The one-shot `hpke.Seal` concatenates `enc‖ct` exactly like our layout (`hpke.go:183-193`). The only reason to avoid it is that it takes no `aad`. | Not fixed. Reword. |
| L8 | Low | `mail.md` §Key-miss | The relay's queue dedupe on `(to,from,id)` can swallow a re-sealed frame while the undecryptable original is still queued and unacked. It self-heals on the next backoff, which can be up to 6 h away. | Not fixed. Suggest setting `next_attempt = now + 1 min` after a re-seal. |
| L9 | Low | `pairing.md` §Redeemer / Issuer | 16 pending pairings × 64 MiB Argon2id comes to about 1 GiB. | Not fixed. Serialise derivations (one at a time). |
| L10 | Low | `mail.md` §Lifecycle | Crash ordering between the keystore write and the `mailbox_keys_own` row is unspecified. | Not fixed. Suggested order: keystore first, then the row in a tx. At start, a row whose secret is missing is marked `deleted`. |
| L11 | Low | `mail.md` §verification | A sender whose clock is > 10 min ahead or > 30 d behind is rejected `stale` silently, with no ack, until the row expires. | Not fixed. Consider surfacing it in `status`. |
| L12 | Low | `pairing.md` §pair_new | `H(lookup)` at the relay gives log hygiene only (25 bits, brute-forceable). Lookup guessing across many keys relies on M2 per-IP limits. After H1 it can only burn attempts (DoS). | Not fixed. Say so. |
| L13 | Low | `mail.md` §Ack | With asymmetric pair state (only one side holds the other's mailbox key), acks are dropped (`ack_no_mailbox_key`) and the sender's rows expire. This is documented, but invisible to the user. | Not fixed. |
| L14 | Low | `Docs/protocol/README.md` | It still says "Nothing is specified yet". | Not fixed. |
| L15 | Low | `tools/specvectors/main.go` | The file has CRLF line endings, so `gofmt -l` flags it. This predates this review. `go vet` is clean. | Not fixed. |

Checked and found sound:
- Domain separation: every hash, signature, KDF salt and HPKE `info` has a distinct `dorylinae-…-v1/v2\n` prefix. The tag roles `issuer\n` and `redeemer\n` stop reflection. A reflected `tag_I` is never accepted as `tag_R`, and the receiver only ever *computes* the other role.
- Transcript binding: T binds the lookup, both cards and both mailbox announcements, with length prefixes and fixed order. Canonicalisation is unambiguous under agent-card rules 1–6: sorted keys, literal non-ASCII, integers only, duplicates rejected. Any change the relay makes shows up in T.
- HPKE: base mode, X25519/HKDF-SHA256/ChaCha20-Poly1305. `info` binds from, to and key_id, and `aad` binds the id.
- Inner signature: covers v, id, from, to, created, kind and body. A mail cannot be forwarded to a third party: `to` is signed, so it fails at step 8.
- Verification order: cheap checks and decrypt come before the signature, and the signature before the schema/time checks.
- Time windows are consistent: dedupe 35 d > `created` window 30 d > worst arrival created + 14 d (outbox 7 d + relay TTL 7 d). A key is deleted at t + 21 d, and key-miss covers the rest.
- Exactly-once: single-tx insert, ack after commit, re-ack on duplicate. This holds only with the H2 fix.
- Byte encodings: the payload layout, the 716800-byte cap (base64 955812 characters < 1 MiB) and the fingerprint bit order were all rechecked.

## Vector check

I re-ran `go run ./tools/specvectors` (Go 1.27.1). The output matches `pairing.md` byte for byte: salt, K, T (1314-byte input), tag_I, tag_R, both base64url forms, card and mbox lengths (333/271/330/330), key_ids and fingerprints. `-open` on the published `mail.md` payload succeeds, and the published `enc`/`ct` hex equal the payload slices.

Independent check, sharing no code with the generator. In Python:
- **K:** argon2-cffi (the reference C Argon2id).
- **T, tags, fingerprints, key_ids, `pair.confirm` base64:** `hashlib` and `hmac`.
- **HPKE:** `cryptography` for X25519 and ChaCha20-Poly1305, with the RFC 9180 labelled-HKDF key schedule written by hand. It opens the published mail payload to the exact plaintext.
- **Signatures:** the mail signature and both announcement signatures verify under the vector identity keys (Ed25519).
- **Canonical form:** the published cards and mbox re-canonicalise to the same bytes.
- **Negative check:** secret `9XHF4TRW8P` gives a different K (`e0f37a5a…`).

**All match.** The generator now also prints the redeemer's `pair.confirm` payload (added to `pairing.md`, because the redeemer now sends first). All other vectors are unchanged.

## Follow-ups for the Orchestrator (not owner decisions)

- Ticket 0.8c acceptance in 06 §5 needs updating for the new order. In the MITM test, the issuer fails with `bad_confirm` and the redeemer with `confirm_timeout`, not `bad_confirm` on both. Add tests for `code_used` and for "the issuer sends no tag before a valid `tag_R`".
- Ticket 1.0d scope needs the `relayclient` seen-set exemption for `mail` (H2).
- 0.8c should export the canonical/strict-parse helpers (L2).

## Owner decisions needed

1. **Confirm the order reversal (H1):** the redeemer sends first, not the issuer as in 06 §A.5. It is a security fix with no cost to UX or code length, but it changes a design point in 06.
2. **Redeemer single-use memory (M2):** in memory for 24 h (as specified, lost on restart), or persisted in SQLite.
