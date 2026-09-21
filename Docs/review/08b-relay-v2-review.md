# 08b – Security review: pairing v2, relay side (0.8d)

Date: 2026-09-21. Reviewer: W2-RelayReviewer. Target: worktree `AgentNet-wt/relay-v2`
(branch `w2/relay-v2`, commit f06f841): `internal/envelope/{frames,pairing}.go`,
`internal/relay/{pairing,relay}.go`, `cmd/relay/main.go`, `internal/relay/pairing_v2_test.go`,
`Docs/cli/relay.md`. Spec: `Docs/protocol/pairing.md`, `envelope.md`, `Docs/review/07-spec-review.md`, HANDOFF §3.

**Verdict: merge after the fixes applied here.** Counts: Critical 0, High 0, Medium 2 (fixed), Low 7 (not fixed).
`go build ./...`, `go vet ./...`, `go test ./... -count=1` and `go run ./tools/verifyvectors` all pass.

## Findings

| # | Sev | Location | Issue | Fix |
|---|---|---|---|---|
| M1 | Medium | `relay/pairing.go` `pairNew` | **`pair_new` + `pair_cancel` is a free lookup-existence oracle that bypasses the redemption limiter.** `pair_new{lookup}` answers `pair_lookup_taken` if any key holds that lookup. Otherwise it stores an entry, and `pair_cancel` frees the slot at once. Neither step touched a limiter. So one key could sweep all 2^25 lookups at wire speed, then `pair_redeem` only the hits. Hits are never counted as failures, so the 5/min limiter never fired. Each hit burns one of the issuer's 3 attempts (3 hits delete the entry) and returns the issuer's card and mailbox announcement. The result is relay-wide denial of every pending pairing, plus card harvesting, from one key. It does not break the MAC. | **Applied.** A second per-key limiter (`pairings.newLim`) counts every v2 `pair_new` whatever its outcome: 10 per key per `PairFailWindow` (1 min), then `pair_rate_limited`. An honest issuer sends at most 4 per pairing (1 + 3 retries). Enumeration via `pair_new` now costs about the same as via `pair_redeem`. v1 `pair_new` is not counted, because it cannot choose the code. Test `TestPairV2NewIsRateLimited`. Spec: `pairing.md` §`pair_new` and the error table. |
| M2 | Medium | `relay/pairing.go` `pairNew` | **The number of pairing entries was unbounded.** The only cap was 5 per key, and keys are free. Each entry holds up to 16 KiB of card and 4 KiB of mbox, so 10k keys come to about 1 GB. `pairNew` also scans every entry under the global `p.mu`, so CPU cost grew quadratically. The same map made lookup squatting (pre-filling the 25-bit space) unbounded. | **Applied.** A relay-wide cap, `Options.PairMaxCodes` (default 10000, about 200 MB worst case), applies to v1 and v2 together. Past the cap, `pair_new` gets `pair_limit`. The cap also bounds the scan and the squatting fraction (10000 / 2^25 ≈ 0.03 %). Test `TestPairRelayWideCodeCap`. Spec updated. |
| L1 | Low | `relay/relay.go` `Options.DisablePairingV1` | The zero value leaves v1 **on**. Today this is harmless: the package is `internal`, the only production caller is `cmd/relay`, and `cmd/relay` always sets the field explicitly. A future hosted relay (4.1) built on `relay.Open` would inherit v1 silently. | Not fixed. In 4.1, flip the field to `AllowPairingV1` (zero value = off) or set it explicitly. |
| L2 | Low | `relay/pairing.go` `checkPairRequest` / `pairRedeem` | On v1 `pair_redeem` (no `lookup`), `mbox` is not validated, yet it is forwarded to the issuer in `pair_peer`. Size is bounded only by `MaxFrameBytes`. v1 daemons ignore it. | Not fixed. Drop `Mbox` from v1 `pair_peer` frames, or reject a v1 frame that has `mbox`. |
| L3 | Low | `relay/pairing.go` | Expired entries are purged only when some key sends `pair_new`, or lazily on redeem. `sweepLoop` does not purge them. After M2 the memory held is bounded. | Not fixed. Purge them in `sweepLoop`. |
| L4 | Low | `relay/pairing.go` `pairNew` | The per-key outstanding count is an O(entries) scan under `p.mu`. After M1 and M2 this is at most 10000 iterations per `pair_new`, rate-limited. | Not fixed. A per-issuer counter would make it O(1). |
| L5 | Low | design (07 L12) | All limits are per key, and keys are free. A Sybil attacker can still guess lookups with many keys and burn attempts: with N outstanding entries, one hit per ~2^25/N guesses at 5 misses/min/key. It can also fill `PairMaxCodes` with about 2000 keys and deny issuing to everyone. This is DoS only; the MAC is unaffected. | Not fixed. Per-IP or per-account limits in 4.1/4.2. |
| L6 | Low | `cmd/relay/main.go` `requireLoopback` | The literal `localhost` counts as loopback, whatever the hosts file resolves it to. This predates 0.8d. `LOCALHOST`, empty host, `0.0.0.0` and `::` are all treated as non-loopback, so they fail safe. | Not fixed. Consider resolving the name, or accepting only IP literals. |
| L7 | Low | `relay/pairing.go` limiter | `allow` and `fail` are two separate lock sections. During the short overlap when a reconnecting key's old connection is still reading, two connections of the same key can each pass `allow` once. The extra attempts are negligible. | Not fixed. |

## Checked and found sound

- **Secret:** v2 frames carry only `lookup`. The relay has no field or code path for the secret. The relay never logs the lookup, its hash, cards or mbox (`TestPairV2RelayOutputHoldsNoSecretOrLookup`). Error messages do not echo input.
- **v1/v2 separation:** v1 codes and v2 lookups hash with different domains (`…-code-v1`, `…-lookup-v2`). A v2 redeem can never hit a v1 entry, or the reverse. `pair_cancel` acts only on `v2` entries. A v2 `pair_code` never carries `code`.
- **`pair_cancel`:** silent. It deletes only an entry whose issuer is the sender. A foreign or unknown lookup is ignored the same way, so it is no oracle and no one can cancel another key's entry.
- **Malformed lookup, both, or neither → `bad_pairing`, not counted:** these are rejected before any map access, so they reveal nothing and are no free guessing channel. The same holds for the disabled v1 frames, refused before the limiter. Own lookup → `bad_pairing` only reveals the caller's own entry.
- **3-redemption cap vs D9:** the relay counts a redemption only after `pair_peer` has been queued to the issuer. `peer_offline` and `peer_busy` do not count. So the issuer never sees more than 3 `pair_peer` frames per entry from an honest relay, matching its own 3-attempt count. `peer_offline` on a hit reveals only what a hit already reveals, and misses are still counted.
- **Order `pair_limit` before collision:** a key at its limit learns nothing about lookup existence.
- **TTL:** expired entries are treated as absent on redeem, freed for re-issue on `pair_new`, and the boundary is exact (`!now.Before(expires)`).
- **Size limits:** `card` ≤ 16 KiB and `mbox` ≤ 4 KiB are checked before the copy. The frame itself is bounded by the read limit (`MaxFrameBytes`) before decoding.
- **Concurrency:** lock order is always `p.mu` → `s.mu`, and nothing takes them in reverse. `conn.send` is non-blocking under the lock. Entry fields read after unlock are immutable. Counters are atomic.
- **`pair.confirm`:** an ordinary envelope. The relay routes and queues it byte for byte, with no pairing-specific code.
- **`--allow-pairing-v1`:** an explicit flag wins, including `=false` on loopback. The default is on only when the loopback check passes: `127.0.0.0/8`, `[::1]`, `localhost`. `0.0.0.0`, `::`, empty host and hostnames give off.

## Files changed

- `internal/relay/pairing.go`: M1 `newLim`, M2 `maxCodes` cap.
- `internal/relay/relay.go`: `Options.PairMaxCodes`, doc on `PairFailWindow`.
- `internal/relay/pairing_v2_test.go`: `TestPairV2NewIsRateLimited`, `TestPairRelayWideCodeCap`.
- `Docs/protocol/pairing.md`: §`pair_new` limits, error table (`pair_rate_limited`, `pair_limit`).
- `Docs/review/08b-relay-v2-review.md`: this file.

## Owner decisions needed

None blocking. For 4.1: per-IP or account limits (L5), and the v1 default for library callers (L1).
