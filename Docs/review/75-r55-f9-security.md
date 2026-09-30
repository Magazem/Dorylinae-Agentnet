# Review 75: R55-F9 security review (relay error text, sanitising, backoff)

Scope: commit `3f55883` on `p4/r55-f9` (base `0f6937d`), against spec
`Docs/review/67-r55-f9-spec.md` (including Review 67b), owner decision D52 (OD-R55F9-10 = b),
`Docs/protocol/{envelope,approval,pairing,ipc}.md` and `Docs/cli/{status,doctor,ping,pair}.md`.
Findings covered: R55-014, 041, 156 and 157.

## Verdict

**Approve, with one Low fix recommended (F9S-1).** No relay-chosen text reaches a terminal
sink raw or unbounded. `displaytext.Line` is correct. The misrouted-envelope ack gives a
hostile relay nothing it does not already have. No High or Medium finding.

Targeted tests pass: `internal/relayclient`, `displaytext`, `envelope`, `peers`, `session`,
`mail`, and `cmd/agentnet -run 'Relay|Ping|Pair|Status|Doctor'`. The only failure is my
probe for F9S-1, which fails on purpose. Race detection was not run here; it runs in CI.

## Findings

| ID | Sev | Title |
|----|-----|-------|
| F9S-1 | L | The 1013 floor is skipped on the relay's main load-shedding path (`relay_full` / `rate_limited` in place of `ready`) |
| F9S-2 | L | Upgrade header values and certificate names still put relay-chosen words in `last_error` (residual, now shown by a probe) |
| F9S-3 | I | Misrouted-envelope ack: no delete or suppress capability for the relay |
| F9S-4 | I | Tests re-addressed to the client's own key: legitimate, no regression hidden |
| F9S-5 | I | `status --json`, `ping --json` and `pair --json` pass the daemon's values through (mixed versions only) |
| F9S-6 | I | `pair.fail` audit reason: sanitised relay text, acceptable under D52 |
| F9S-7 | I | `displaytext.Line`: verified, two cosmetic notes |
| F9S-8 | I | Backoff starvation and race |

### F9S-1 (L): the 1013 floor is skipped on the relay's main load-shedding path

- **Where:** `internal/relayclient/relayclient.go:250` (the `Run` switch) and
  `internal/relay/relay.go:573` (`refuseAfterAuth`).
- **What happens:**
  - The relay sheds an authenticated connection with an `error` frame (`relay_full` or
    `rate_limited`) followed by `Close(1013)`. This happens at `relay.go:534` and `relay.go:543`.
  - The client's `handshake` → `readControl(OpReady)` returns on the error frame. So `err` is
    an `envelope.ErrorFrame`, never a `websocket.CloseError`, and the 1013 floor never applies.
  - envelope.md (lines 440–442) explicitly says this case gets the plain growing backoff, so
    the code matches the spec. The gap is in the design, not in the implementation.
- **Scenario:**
  1. A relay restarts. Its SIGTERM drain closes with 1001 (`conn.go:454`), or it crashes, or
     the network drops.
  2. Every daemon that was up for 30 s or more resets to 500 ms and comes back within
     375–625 ms.
  3. The recovering relay is at `--max-conns` or its reconnect limit, so it answers
     `relay_full` in place of `ready`.
  4. The daemons retry at about 1 s, 2 s and 4 s, still in near lockstep. Each attempt costs
     the relay a TLS handshake and a signature verification before the refusal.

  This is the herd that F9R-4 set out to stop; it moved from the 1013 close to the error frame.
- **Proof:** `internal/relayclient/f9sec_probe_test.go` `TestF9SecRefuseAfterAuthGetsFloor`.
  The gap after `relay_full` + 1013 measured 12–14 ms, against a floor of 50 ms and a minimum
  backoff of 10 ms. `LastError` is `relay: relay_full`.
- **Fix direction:** in `Run`, treat an `envelope.ErrorFrame` with code `relay_full` or
  `rate_limited` like 1013: `delay = max(delay, tryAgainFloor)`. Also consider not resetting
  on a 1001 close (relay drain). Update the envelope.md sentence and add the probe case to
  test 10.

### F9S-2 (L): relay-chosen words still reach `last_error` through library errors

- **Where:** `internal/relayclient/relayclient.go:287` (`connError` default branch).
- **What happens:**
  - The coder/websocket v1.8.15 `dial.go` errors at lines 248–313 quote the relay's
    `Connection`, `Upgrade`, `Sec-WebSocket-Accept`, `Sec-WebSocket-Protocol` and
    `Sec-WebSocket-Extensions` values with `%q`.
  - x509 `HostnameError` and `UnknownAuthorityError` quote certificate SANs and CNs.
  - This is the residual Review 67b F9R-2 recorded in envelope.md and status.md.
- **Proof:** `TestF9SecUpgradeHeaderInLastError` (passes). With a hostile `Upgrade` header,
  `last_error` reads: `dial: failed to WebSocket dial: WebSocket protocol violation: Upgrade
  header "evil; reinstall from https://evil.example/fix-now AAAA…"`.
  - It is bounded to 256 bytes and display-safe, as documented.
  - It still puts a relay-chosen call to action on the `status` and `doctor` line, which is
    the harm OD-R55F9-2 removed for the error message.
  - For certificate names this needs a MITM or relay operator holding the TLS key, or a
    loopback relay. For headers it needs only the relay.
- **Fix direction (optional):**
  - Map the coder "WebSocket protocol violation" prefix to fixed text: `dial: the relay's
    upgrade response is invalid`.
  - Map `errors.As` on `x509.HostnameError`, `x509.UnknownAuthorityError` and
    `tls.CertificateVerificationError` to `dial: the relay's TLS certificate was rejected`.
  - If this is not done, keep the residual as documented.

### F9S-3 (I): misrouted-envelope ack

- **Where:** `internal/relayclient/relayclient.go:526`.
- **Why the ack gives the relay nothing new:**
  - The ack is `{op: ack, from, ref}`, and the relay deletes by `(c.key, from, id)`
    (`internal/relay/pairing.go:157`, `queue.go:360`). A misrouted ack can only delete an
    entry in this key's own queue with the same `(from, id)`.
  - Only a relay can produce a misrouted frame, and it can already drop or delay any queued
    mail.
  - The mail outbox's end-to-end ack mail (`receiver.go`) means a relay-side delete never
    counts as delivery for the sender.
- **Why legitimate mail cannot be suppressed:**
  - `to` is strict unpadded base64url (`envelope.go:31`), so it has exactly one form and
    string comparison with `KeyString` is exact.
  - The relay routes with the same Go struct decoding (`ParseHeader`), so a peer cannot build
    a `to` that the relay and the client read differently.
  - Legitimate traffic always has `to` equal to the client's own key.
- **Other notes:** the per-frame ack and the Debug line are relay-driven at 1:1 and fall
  under F14's rate limiting. No change needed.

### F9S-4 (I): tests re-addressed to the client's own key

- **Tests changed:**
  - `ephemeral_test.go:21`, `mail_test.go:21` and `relayclient_test.go:151` changed `to`
    from a random key to the client's key.
  - This is required by the new drop rule. The behaviours they test (seen-set bypass, dedupe,
    ack) are unchanged.
  - The drop itself is covered by `relaytext_test.go:242` (queued type acked, presence not
    acked, seen-set untouched).
- **Other users of `relayclient` outside its package:**
  - `internal/relay/relay_test.go:182`, `queue_test.go:143` and the cmd tests go through a
    real relay and assert positive delivery, so nothing is hidden.
- **Caveat for the future:** a relay-routing test that asserts *non-delivery* through
  `relayclient.OnEnvelope` would now pass even if the relay misrouted. Such tests must use a
  raw websocket.

### F9S-5 (I): `--json` outputs

- **Where:** `cmd/agentnet/main.go:245` encodes `last_error` as the daemon sent it; `ping` and
  `pair --json` do the same.
- **Why it is not a finding:**
  - With this daemon the values are already converted: `connError`, `ErrorText`, and the
    `cleanFailure` choke point.
  - Only an older daemon could serve raw text. That is F9R-9, accepted (ship together).
  - `doctor --json` re-sanitises, because the detail goes through `Line`.

### F9S-6 (I): `pair.fail` audit reason

- **Where:** `internal/peers/pairing.go:1047`.
- **Checks:**
  - The reason is the relay message after `errorFrame` (`Line(…, 200)`) and again after
    `cleanFailure`.
  - It is unexported (`Failure.reason`), so it never reaches IPC, JSON or `pair_status`.
  - `agentnet log` prints it JSON-quoted through `notify.Clean` (`cmd/agentnet/log.go:262`).
- **Residual:** a relay can still put words or a URL into the audit row. D52 chose this
  explicitly, and sanitised text is acceptable there.

### F9S-7 (I): `displaytext.Line`

- **Property probe:** `internal/displaytext/f9sec_probe_test.go` ran 20 000 random inputs up
  to 6 KB from a hostile alphabet: C0 and C1 (as raw bytes and as UTF-8), ESC, invalid and
  truncated UTF-8, bidi, zero-width, tag characters, the R46 set, stacked marks, emoji and
  `…`. Maximum sizes ranged from 4 to 300. For every input the output was:
  - at most the maximum size, "…" included;
  - valid UTF-8;
  - free of hidden runes;
  - either exactly `clean(s)`, or a prefix of it followed by "…".
- **Code checks:**
  - The 4 KiB pre-cut walks back to a rune start (`displaytext.go:146`).
  - An invalid byte becomes U+FFFD (3 bytes). The byte budget absorbs the 3× growth.
  - A cut never lands inside a rune.
- **Cosmetic notes, no action:**
  - Spacing marks (Mc) are not capped, the same as `Name`; the byte bound limits them.
  - Strong right-to-left letters are graphic and kept, so they can reorder neutral characters
    visually. No bidi control survives. This matches approval.md's character rule.

### F9S-8 (I): backoff starvation and race

- **No starvation:**
  - The wait is at most `jitter(MaxBackoff)`, about 37.5 s.
  - The 1013 floor is `max(delay, 5 s)` and is then capped by `min(…, MaxBackoff)`. A
    `MaxBackoff` below the floor costs one longer wait only.
- **Honest-network note:** a middlebox that kills connections in under 30 s keeps the delay
  at its cap, giving about 50 % downtime. This is an availability note, and a relay could
  cause it anyway.
- **Race:** `lastErr` is written under `c.mu`. `stableAfter` and `tryAgainFloor` are set only
  in `New`. `cleanFailure` builds a fresh `Failure` before taking `m.mu`. No new shared state
  is unguarded.

## Files created by this review

- `Docs/review/75-r55-f9-security.md` (this file)
- `internal/relayclient/f9sec_probe_test.go`:
  - `TestF9SecRefuseAfterAuthGetsFloor` **fails on 3f55883 on purpose** (F9S-1). Remove it,
    or keep it as test 10's new case once the fix lands.
  - `TestF9SecUpgradeHeaderInLastError` passes.
- `internal/displaytext/f9sec_probe_test.go`: `TestF9SecLineProperties` passes.
