# 08 – Wave 2 security reviews

## 1.0c code review

Date: 2026-09-21. Reviewer: W2-MailReviewer. Target: worktree `AgentNet-wt/mail` (branch `w2/mail`, base `baa55f6`),
package `internal/mail` (`mail.go`, `open.go`, `body.go`, `audit.go`, `canonical.go`, `mail_test.go`).
Basis: `Docs/protocol/mail.md`, `Docs/review/07-spec-review.md`, `Docs/orchestration/HANDOFF.md` §3.

**Verdict: merge after the fixes below, which are already applied in the worktree (uncommitted).** Counts: Critical 0,
High 0, Medium 2 (both fixed), Low 10 (not fixed).

Gate after the fixes: `go build ./...`, `go vet ./...` and `go test ./... -count=1` pass. `go run ./tools/verifyvectors`
prints "all vectors reproduced". `FuzzOpen` ran for 60 s (≈490k execs) with no failure. I could not run `-race` locally
because there is no cgo toolchain; the CI race job covers it.

Files changed: `internal/mail/body.go`, `internal/mail/open.go`, `internal/mail/review_test.go` (new), and this file (new).

### Findings

| # | Sev | Location | Issue | Status |
|---|---|---|---|---|
| M1 | Medium | `body.go` `parseTime`, `open.go` `parseSigned` | **Timestamps with fractional seconds were accepted.** `time.Parse` accepts a fractional second after the seconds field even when the layout has none. So `msg.created` `"…03:10:00.5Z"`, and announcement `created`/`not_after` such as `"…03:00:00.000Z"`, passed step 5 and the announcement checks. The spec requires whole seconds. A strict verifier would reject these values, so the two would disagree, and non-canonical `created` strings would break the text-ordered "newer `created`" rollback check in peer storage (1.0b). Only the signer can produce such values, so there is no forgery. | **Fixed.** `parseTime` now requires the string to round-trip through `timeFmt`, and `parseSigned` uses `parseTime`. |
| M2 | Medium | `mail_test.go` | **Several negative cases had no tests.** There were no tests for step 5 (strict parse, members and types), step 9 (`id_mismatch`, both mismatch and format), step 10 (`v` ≠ 1), the step 2 upper bound, or announcement time formats. There was also no fuzz test and no concurrent test of the audit limiter. | **Fixed.** New `review_test.go` adds a 28-case step-5 table, which includes nesting 20000 deep, dup keys, bad UTF-8, `-0`, 2^53, fractional/offset/lowercase-z times and padded sig. It also adds step 9 and 10 tests, the payload bounds `MaxPayload`/`+1`/empty, the announcement whole-second check, `TestRejectAuditConcurrent`, and `FuzzOpen` (raw payloads plus arbitrary plaintexts under a valid HPKE context, with invariants checked on accept). |
| L1 | Low | `mail.go` `b64u`, `decodeKey`, sig/pub decoding | `base64.RawURLEncoding` is not `.Strict()`, so each 43-char key, 86-char sig and 43-char pub has 4 string forms. In `mail` this has no impact, because `from`/`to`/`identity` are compared as strings and `IsPaired` matches strings. | Use `.Strict()` when the helpers are de-duplicated in 0.8c. Do the same in `envelope.ParseKey`. |
| L2 | Low | `open.go` step 5 / `Opened.Signed` | `Signed` holds the received bytes, which are not checked to be `canonical(signed)`. A sender can send whitespace, escapes or a non-strict sig string. The signature still verifies through generic canonicalisation, so proof of origin holds, but the stored proof is not canonical. | Either require `plain == canonical(doc)` at step 5 (spec change), or store the re-canonicalised form. |
| L3 | Low | `open.go` `Open` | Open does not check `env.Type == "mail"` or call `env.Validate()`. It relies on the caller (`envelope.Parse` in relayclient). | Document it on `Open`, or add a cheap check. |
| L4 | Low | `audit.go` | (a) Every reject is logged at Info before the limiter, so an attacker can drive log volume. `session.reject` does the same. (b) The suppressed count is only emitted when the next reject arrives after the window ends. (c) A zero-value `RejectAudit`, or one with a nil sink, panics. (d) `Report` runs inside `Open` with a 2 s budget, so the limiter allows at most 30 blocking writes a minute. | Accept, or move to Debug/async when both limiters are unified. |
| L5 | Low | `mail.go` | There is no re-seal API for "re-seal the stored signed plaintext" (§Key-miss recovery step 3). `Seal` always re-signs. | 1.0e needs a `Reseal(signed, from, to, mailboxPub)`. |
| L6 | Low | `mail.go` `Seal` | `Seal` does not validate `Created`. A zero or year > 9999 value produces mail that every receiver rejects. | Validate in `Seal`. |
| L7 | Low | `open.go` `parseSigned` | `msg.V = int(vi)` truncates on 32-bit platforms (`v = 2^32+1` becomes 1). The value is signed by the sender, so there is no forgery. | Compare as `int64`. |
| L8 | Low | `internal/envelope` + choice (1) | Invalid base64 fails `envelope.Parse`, so such a mail never reaches step 2 and gets no `mail.reject` audit. The spec lists it under step 2 `malformed`. | Accept, or note it in the spec. |
| L9 | Low | `open.go` step 5 | Step 5 also enforces the `kind` pattern and the `created` format (choice 3). This is reasonable, but the spec only says "right types". | Add one line to `mail.md` step 5. |
| L10 | Low | `internal/mail/*.go` | The package files have CRLF line endings, so `gofmt -l` lists them. `review_test.go` is LF. | Normalise on commit. |

### Implementer's choices

1. **Open takes an already-decoded Payload.** OK. `envelope.Envelope.Payload` is `[]byte`, which `encoding/json` decodes from standard base64 in `envelope.Parse`. See L8.
2. **Canonical helpers copied from `agentcard`.** Verified by diff. `parseStrict`, `parseValue`, `canonicalize`, `utf16Less` and `writeString` are identical. The only difference is the `canonical` wrapper, which differs from `agentcard.canonicalBytes` only in error wrapping.
3. **Step 5 rejects malformed `kind`/`created`; step 2 caps the payload at 57 + 716800.** OK. `MaxPayload` is correct, and the bound is now tested.
4. **A wrong envelope `from` with a valid seal fails at decrypt.** Correct, because `info` binds `from` (`TestFromMismatch`). Step 6 is reachable only when a paired peer seals under its own `from` and signs as someone else (`TestSenderMismatchStep6`).

### Checked and found sound

- **HPKE.** Base mode, DHKEM(X25519)/HKDF-SHA256/ChaCha20-Poly1305 through `NewSender`/`NewRecipient`, one Seal and one Open per context. `info = tag‖from‖\n‖to‖\n‖key_id(raw)` and `aad = id` match the spec and the published vector bytes. The payload offsets are 0/1/9/41.
- **Verification order.** Steps 1–12 run in spec order. Before authentication, only a map lookup (steps 1 and 3) and length/version checks run. HPKE `Open` runs only on a payload ≤ `MaxPayload`.
- **Unauthenticated plaintext.** HPKE base mode does not authenticate the sender, so anyone who knows the mailbox pub, including the relay, can reach step 5 with an arbitrary plaintext. This is by spec. Parsing is bounded: `encoding/json` caps nesting at 10000, so a 716 KB, 358k-deep plaintext is rejected in about 3 ms with about 1 MB allocated. The size is capped at 716800, and integers are bounded to 2^53.
- **Signatures.** `dorylinae-mail-v1\n` and `dorylinae-mailbox-key-v1\n` are distinct from each other and from the card domain. The signature covers all 7 msg members as parsed generically. Duplicate keys are rejected. Announcement checks run in spec order (identity, then signature, then members and `v`/`pub`/`key_id`, then the window), and the window arithmetic matches §Announcement 5.
- **Error oracle.** Rejects produce no network output. Only `key_miss` has an observable reply, and the spec defines that reply (key-miss recovery, handled by the caller). `RejectError.Err` never leaves the process, and the audit detail has only `{peer, id, reason}`, with no payload bytes.
- **Constant time.** No secrets are compared outside the AEAD. The key_id lookup and the identity comparisons use public data.
- **Replay and staleness.** Step 11 includes both bounds (`now − 30 d` and `now + 10 min`), matching the spec. Dedupe is 1.0d.
- **Panics.** No panics on malformed input: the length is checked before slicing and fixed-size array conversions, and `FuzzOpen` ran for 60 s. A nil `Peers`/`Keys` is a configuration error, not reachable from input.
- **Audit limiter.** 30 per fixed minute window under a mutex, the same as `session.reject`. The concurrent test gives exactly 30.
