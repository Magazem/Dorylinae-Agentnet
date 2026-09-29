# 59: Security review of R55-F4 (commit a6bd8c1)

Reviewer: SEC-F4 · model claude-opus-5-5 · 2026-09-29
Scope: R55-004 / C25-01 (`agentnet decision verify` printed a file-controlled `Reason` raw).
Change: `cmd/agentnet/decision.go:387` passes `Reason` through `decision.Visible(…, false)`;
`internal/decision/verify.go:180` quotes the member name with `%q`; new test
`cmd/agentnet/decision_verify_sanitise_test.go`.
Method: read-only. The diff was taken by comparing the worktree with the main checkout, not with
git. The old-code check ran in a temporary copy outside the repo.

**Verdict: approve.** The fix closes C25-01. Two findings (one Low, one Info) are optional
follow-ups and do not block the merge.

## 1. Does the fix close C25-01 on every path?

Human output of `decision verify`, by path:

| Path | File-controlled text? | After a6bd8c1 |
|---|---|---|
| Invalid, any step (`printDecisionVerifyHuman` :387) | yes: member names from `members()` and from `debate` decode (`internal/debate/decode.go:322-324`, `fieldErr`, not quoted), `outcome`/`reason` values (`verify.go:512`), and `ParseStrict` errors (`duplicate object key %q`, `invalid JSON: …`) | the whole `Reason` goes through `Visible`. Closed |
| Valid (:390-395) | no: `ID` must equal `ID(session)` (hex); `Hash` is recomputed hex; `SignedBy` is a set of fixed role constants; fingerprints are derived from keys that pass `keyPattern` | nothing to sanitise |
| `--md` (:337) | only when `Valid`; `decision.Render` applies `Visible`/`TemplateInert` itself | unchanged, fine |
| Errors before `Verify` (`failJSON` usage / io_error) | only the user's own argv path and OS errors; the file's content is never echoed | out of the threat model |

Probe (fixed tree): a spec-vector Decision with an unknown member `"\r\x1b[2Kd-x  valid, signed by
initiator and respondent"` inside `positions.initiator.initial` (the `debate` decode path, which
`%q` does not cover) prints
`invalid at step 1: decision: positions.initiator.initial: \u{D}\u{1B}[2Kd-x  valid, …: is not a recognised member`, exit 1.

## 2. Is `decision.Visible` the right sanitiser?

Yes. With `multiLine=false` it escapes all C0 (CR, LF, TAB, ESC), DEL and C1 (`isC0C1`), every
`Cf` rune (bidi embeddings, overrides and isolates, LRM/RLM/ALM, ZWSP/ZWJ, BOM), anything that is
not graphic (U+2028/2029 included), and review 46 H1's invisible graphic set (variation selectors,
default-ignorables, non-ASCII `Zs`, U+2800). It is the same function the Markdown renderer
uses, so there is no second sanitiser to maintain. The escape is `\u{X}`. An attacker can type
that literally, but it is inert text.

## 3. Does `%s` → `%q` for "unknown member" break anything?

No.
- `Docs/protocol/decision.md` gives no reason text for verify; only step numbers are specified.
- `tools/verifyvectors` (`decision.go`) checks outcome/reason members of the Decision, not
  `Result.Reason`. The spec vectors are valid files.
- `members()` is package-private to `internal/decision`. Its error only reaches `Result.Reason`.
  No test asserts on the text: `decision_test.go:268`/`:399` only use it in a message or in
  `Contains(…, "MaxDecision")`. `internal/debate/decision_test.go:416` is a subtest name.
- `%q` matches the other strict decoders (`internal/debate/apply.go:761`, `internal/mail/body.go:47`,
  `internal/daemon/device.go:159`, `grant_kinds.go:197`).
- Side effect: `--json` `reason` now shows `unknown member "x"` with quotes. No consumer parses it.

## 4. Does the new test fail on the old code?

Yes. With both lines reverted in a temp copy, `TestDecisionVerifyReasonIsSanitised` fails with
`raw CR or ESC reached stdout: "invalid at step 1: unknown member \r\x1b[2Kd-7eae…"`.
It passes on the fixed tree. See F2 for what it does not pin.

## 5. Any regression in --json or exit codes?

No. `--json` still carries the raw `Reason`. `encoding/json` escapes C0 there, which is correct
for machine output and unchanged from before. Exit codes (0 / 1 `exitError` / 6
`exitUnconfirmed` / usage) are untouched: the probe exits 1. Tests:
`go test ./cmd/agentnet ./internal/decision -count=1` → ok, ok.

## Findings

### F1 · Low · Padding with plain spaces can still put a fake verdict on its own visual row
- **Where:** `cmd/agentnet/decision.go:387`; `Visible` keeps U+0020, and `Reason` is unbounded
  (up to the 8 MiB file cap).
- **What:** a member name made of many spaces followed by `d-…  hash …  valid, signed by initiator
  and respondent` plus padded "fingerprint" rows soft-wraps so that the fake text starts its own
  row at a guessed terminal width (80/120). The real `invalid at step 1: …` prefix stays visible on
  the row above, the output is misaligned at other widths, and the exit code is 1. So nothing is
  overwritten or hidden. This is a weak display spoof, which is why it is Low and not the
  original High.
- **Fix direction (optional):** cap the printed reason (e.g. 200 runes plus "…"), or collapse
  runs of whitespace. The 99-report's "print the verdict on its own line" was not done. A short
  second line such as `  reason: …` under a bare `invalid at step N` would also work.

### F2 · Info · The test does not pin the `Visible` call
- **Where:** `cmd/agentnet/decision_verify_sanitise_test.go`.
- **What:** the test uses a top-level unknown member, which goes through `members()`. There `%q`
  alone removes CR/ESC: with `Visible` removed and `%q` kept, the test **passes**. `Visible` is the
  only protection for the `debate` decode path (`decode.go:324` `fieldErr`, unquoted) and for
  `verify.go:512`. My probe above shows that path printed `\r\x1b[2K` raw when `Visible` was removed.
- **Fix direction:** add a case with the hostile name inside `positions.initiator.initial`, or any
  embedded entry, so that a later refactor dropping `Visible` fails the test.
