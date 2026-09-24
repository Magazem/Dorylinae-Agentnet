# 33 — Flake: TestPairingTagCompletedOnSuccess (INV-1)

## Symptom

`internal/peers/pairtag_test.go:57` `TestPairingTagCompletedOnSuccess` failed with
"timed out waiting for pairing state complete" after 23.12 s:

- run 35837526227 attempt 1, `test (macos-latest)`, on a docs-only push; attempt 2 passed.
- run 35844996198, `test (windows-latest)`, same line, 25.23 s.

Across the last 60 CI runs, no other `internal/peers` test failed.

## Root cause

The failure comes from the test timing, not from production code. The shared `newNode` helper
(`internal/peers/pairing_v2_test.go`) set `ConfirmWait: 1500ms`. Under CPU load that
window is too short for the work it has to cover.

The sequence:

1. `RedeemTagged` starts the redeemer's Argon2id (64 MiB, t=3, 1 lane) and sends
   `pair_redeem`.
2. The bus immediately delivers `pair_peer` to both sides. The issuer's
   `onPeerIssuer` starts the attempt timer, `time.AfterFunc(ConfirmWait, …)`.
3. The redeemer can send `tag_R` only after its Argon2id finishes
   (`sendRedeemerTag` waits on `kd.ready`).
4. The issuer's window therefore has to cover the redeemer's entire derivation. When the
   derivation takes longer than 1.5 s, the issuer's attempt fails with `confirm_timeout`, and
   `tag_R` arrives to "no waiting attempt" and is dropped. The redeemer then waits its
   own 1.5 s for `tag_I`, gets nothing, and ends `failed/confirm_timeout`.
5. `waitState(complete)` kept polling a pairing that had already failed, until its 20 s
   deadline. 3 s of `RedeemTagged` Wait plus 20 s gives the observed 23 s.

Production is not affected. Its default ConfirmWait is 60 s, which is far more than one
Argon2id derivation takes.

## Evidence

- **Argon2id cost (local, 8 cores):** 180–250 ms for one derivation. With contention the
  worst case rises: 16 concurrent derivations took 0.70 s at GOMAXPROCS=8, and 8 or 16
  concurrent took 1.15 s and 1.76 s at GOMAXPROCS=1. CI runners are small (3–4 vCPU), and
  `go test ./...` runs packages in parallel with it. In the failing run,
  `internal/daemon` (36 s, which also does pairings) ran concurrently with `internal/peers`.
- **Reproduction:** 24 background Argon2id loops, and the test run at GOMAXPROCS=2 with
  the old 1.5 s value. The test failed 2/15, 6/15 and 1/10 in separate runs, always at
  line 57. The audit and logs captured on failure show the chain from step 4:
  - issuer: `pair.attempt_fail {"code":"confirm_timeout"}` and
    `pair.confirm dropped reason="no waiting attempt"`
  - redeemer: `pair.fail {"code":"confirm_timeout","reason":"the peer did not confirm in time"}`
- **Other causes ruled out:** the bus delivers every frame on its own goroutine and takes
  no lock while calling the manager, so there is no lost wake-up. `waitFor` polls, so it
  cannot miss a signal. The logs show the only failing step is the issuer's timer
  expiring before `tag_R` arrives.

## Fix

These changes are test-only; production code is unchanged. The task asked for test-only KDF parameters through an existing seam, but none
exists: `deriveK` uses package constants, and the one "slow K" test swaps `s.kd` from inside
the package. So the deadline is now sized to the work:

- `internal/peers/pairing_v2_test.go` `node.open`: the default `ConfirmWait` goes from 1.5 s to
  10 s. That is more than five times the worst derivation measured under heavy contention, and still below
  `waitFor`'s 20 s deadline. Success paths never wait for this window, so their run time
  is unchanged (per-test times before and after are identical to within 0.01 s). Tests that
  expect a confirm timeout already set a short `ConfirmWait` on the redeemer (300/400/700 ms).
  The redeemer's window opens only after its own KDF, so those tests still work.
- `waitState` now fails at once if the pairing ends in the other terminal state, and the
  failure message includes the pairing's `Status.Error`. A recurrence would then show up as
  "ended failed, want complete (error: confirm_timeout …)" instead of a silent 20 s timeout.

After the fix, under the same load, `TestPairingTag*`, `TestV2PairsThroughRealRelay`,
`TestV2RelayMITMIsDetected` and `TestV2WrongSecretRightLookupFails` passed 60/60.

## Other tests affected

The tests below go through `newNode` and depend on the issuer's default window covering the redeemer's Argon2id.
All of them are fixed by the helper change. None of them failed on CI or in the load runs.

- `TestPairingTagIssuerCompletedBeforeTagI` and `TestV2PairsThroughRealRelay`: success
  paths, same failure mode.
- `TestV2RelayMITMIsDetected`: asserts the issuer records `bad_confirm`. If the issuer's
  window expired first it would record `confirm_timeout` and the assertion would fail.
- `TestV2WrongSecretRightLookupFails` and `TestPairingTagCompletedOnFailureCarriesNoPeer`: exposed
  only in timing. Their assertions accept either failure code.

Tests built on `newEnv` or `newRevManager` use fake peers, and no peer-side Argon2id sits on the
timed path, so they are not affected.

Outside `internal/peers`, `TestRequestIdempotencyKey`
(`request_test.go:158`, "timed out waiting for pairing code", windows, run 35855160670)
failed differently: it waited for the relay's code, not for a confirm. That test is out of scope and is not investigated here.
