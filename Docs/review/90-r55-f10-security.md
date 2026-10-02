# 90: R55-F10 security review (shared terminal sanitiser, card text rule, --json escaping, O-100 sweep)

Reviewer: R55-F10sec-Opus (Opus, `claude-opus-5-5`), 2026-10-02. Branch `p4/r55-f10`, ticket
commit `96726d1`. Spec: `Docs/review/82-r55-f10-spec.md` with review 82b (owner D70: ODs 1-9
(a)). Report only. The reviewer added one probe test file (see [Files](#files)). In this
file, code points are written in ASCII notation (`U+202E`), never raw.

## Verdict

**Approve after one fix (S90-1, Medium).** The sanitiser is sound and the sweep is complete
for `cmd/agentnet`, with one exception: `agentnet decision <id> --json` without `--out`
writes the signed file to the terminal without `displaytext.JSON`. Peer text inside it can
hold raw bidi controls. The fix is one line. S90-2 (Low) is defence in depth and should go
in with it. The rest are Info. Verify/VerifyStored, the grant branch rule and the vectors
check out.

## Findings

| # | Severity | Where | Finding |
|---|---|---|---|
| S90-1 | Medium | `cmd/agentnet/decision.go:96-100` (`signedFileBytes` :133, `writeDecisionOutput` :186) | `decision <id> --json` without `--out` prints raw bidi controls to the terminal |
| S90-2 | Low | `internal/displaytext/displaytext.go:520` (`JSON`), `cmd/agentnet/jsonout.go:560` | Invalid UTF-8 inside a `json.RawMessage` passes through `writeJSON` unchanged |
| S90-3 | Info | `cmd/agentnet/team.go:345,410` | Team invite/join failure text uses `Term`, not `failureText`/`Line` (F9) |
| S90-4 | Info | `cmd/agentnet/log.go:283` (`formatEvent`) | A detail **key** keeps spaces and `=`, so a key could fake further `key=value` pairs |
| S90-5 | Info | `cmd/agentnet/fetch.go:233`, `cmd/agentnet/identity.go:75` | Raw outputs that remain by design |
| S90-6 | Info | `internal/displaytext/displaytext.go:509` | `JSON` escapes only the hidden set: no combining-mark cap, and U+FFFD kept |

### S90-1 (Medium): `decision --json` on stdout bypasses `displaytext.JSON`

- **Scenario.** A peer opens a debate with a request title, topic or position holding U+202E.
  - `request.ValidateTitle` (`internal/request/shared.go:17`) refuses only controls.
  - `debate/text.go` refuses C0, C1 and U+2028/U+2029, but not `Cf`.
  - So the text enters the Decision. `agentnet decision <id> --json` then writes
    `signedFileBytes` straight to stdout (`writeDecisionOutput`, `decision.go:186`).
    `signedFileBytes` uses its own encoder with `SetEscapeHTML(false)`, and
    `encoding/json` writes `Cf` raw.
  - The terminal shows the JSON reordered: the Trojan-source effect this ticket removes
    everywhere else.
  - C0 cannot get through: the canonical form escapes it, and debate text refuses C1.
- **Proof.** `TestSec90DecisionJSONStdoutRawBidi` (`cmd/agentnet/zz_sec90_test.go`) fails
  today: the output holds a raw U+202E.
- **Why it was missed.** The spec excluded "the Decision file written by `decision export`
  (`decision.go:134`, exact signed bytes)". But the same bytes go to the terminal when
  `--out` is absent, and that is a human-facing `--json` output. `decision export` does not
  exist; the code path is `decision --json`.
- **Fix direction.**
  - Pass `data` through `displaytext.JSON` in the `--json` branch (`decision.go:97`), at
    least when writing to stdout. Doing it for `--out` too is safe:
    - `decision verify` re-canonicalises the parsed value (`internal/decision/verify.go:101`),
      so escaped bytes give the same hash and signatures;
    - the size budget holds: a 3-byte hidden rune becomes 6 bytes, so at most 2 ×
      `MaxDecision` (768 KiB) plus indentation, well under `maxDecisionFile` (8 MiB).
  - Correct the spec 82 exclusion sentence and approval.md §Sanitising (JSON output) to
    match.
  - Keep the probe test as the regression test, inverted to assert that the six-character escape (backslash, `u202e`) appears.

### S90-2 (Low): invalid UTF-8 in a RawMessage survives `writeJSON`

- **What happens.** `encoding/json` copies a `json.RawMessage` through `compact`, which does
  **not** check UTF-8 inside strings. `displaytext.JSON` then copies a byte that does not
  decode (`r == RuneError && n <= 1`) as is. So a lone 0x9B byte (the 8-bit CSI) reaches
  `--json` output. Some terminals in 8-bit mode act on 0x9B.
- **Proof.** `TestSec90RawMessageInvalidUTF8` fails today.
- **Reachability: none found.** The RawMessage members are:
  - `DecisionShowResult.Decision`;
  - the debate `Entry`;
  - the request `Position`;
  - `DeviceScopeSetResult.Scope`;
  - the audit `Detail`;
  - the fetch `Result`.

  Each source I traced is either validated with `agentcard.ParseStrict`, which refuses
  invalid UTF-8 (`internal/agentcard/canonical.go:35`), or produced by `json.Marshal`. The
  contract "every hidden rune is escaped, the output is safe" therefore depends on every
  current and future producer.
- **Fix direction.** In `displaytext.JSON`, write an undecodable byte as `�`. Go's own
  decoder already maps it to U+FFFD, so for Go consumers the decoded value does not change.
  Alternatively, make `writeJSON` refuse non-UTF-8 output.

### S90-3 (Info): team invite/join failure text

`team.go:345,410` build `"invite failed: " + res.Error.Message + " (" + code + ")"` and print
it through `Term`. That is injection-safe. But `pair` (`pair.go:25` `failureText`) does two
more things for the same `PairStatus.Error`, and team invite/join does neither:
- it replaces the message of a known code with `envelope.ErrorText`;
- it cuts the message with `displaytext.Line` (F9).

So a relay or peer message reaches the user's terminal at any length. Fix direction: use
`failureText` there.

### S90-4 (Info): `log` detail keys

`formatEvent` prints `Term(k) + "=" + Term(value)`. A value is compact JSON, so it is quoted
and unambiguous. A key is the decoded string, so `"a=1 b"` would read as two pairs. Every
audit detail key is a daemon constant, so no path exists today. If a key can ever come from
peer data, print keys with `displaytext.Quote` or refuse keys outside `[a-z_]`.

### S90-5 (Info): remaining raw outputs (by design)

- **`fetch` without `--out` or `--json`** (`fetch.go:233`) writes the grantor's file bytes,
  ESC included, to stdout. This is pre-existing and out of scope (spec §Not in scope). A
  later ticket could refuse this when stdout is a TTY, or require `--out`.
- **`identity --json`** (`identity.go:75`) is raw, per review 82b F4. It is only the user's
  own card, and creation now refuses bidi controls, so only a pre-F10 own card can hold one.
  Accepted.

### S90-6 (Info): `JSON` scope

`displaytext.JSON` escapes the hidden set only. Stacked combining marks and U+FFFD stay raw
in `--json`, while `Term` caps or escapes them. This matches the spec (OD-F10-4 (a)) and is
recorded only so it is not mistaken for a gap.

## Checked and found sound

**Sanitiser (`internal/displaytext/displaytext.go:433-543`).** Probes:
- ESC, C1 U+009B, CR, LF and NUL become `\u{XXXX}` text. Invalid bytes become `\u{FFFD}`.
- U+202E, U+200D (ZWJ), U+3164, U+1160, tag characters, U+0600/U+0601 and U+FFF9 are escaped.
- `e` + 3 × U+0301 keeps 2 marks and escapes the 3rd. A mark after an escaped rune attaches
  to the visible `}`.
- Spacing marks (Mc) are not capped, but each one takes a column.
- Tab is escaped by `Term`, so `tabwriter` cells cannot be split.
- `Block` escapes CR, U+0085 and U+2028 and indents continuation lines.
- `Term` is idempotent (`\`, `{` and `}` are graphic). The residual where a literal
  `\u{202E}` typed by a peer looks like an escape (OD-F10-6), and the display-width residual
  (OD-F10-9), are accepted.

**`JSON`.**
- It touches only runes of U+007F and above.
- `encoding/json` output has such bytes only inside strings, including RawMessage after
  `compact`.
- U+007F is escaped.
- Surrogate pairs are correct for tag characters.

**Print-site sweep (`cmd/agentnet`, all non-test files).** I grepped every
`fmt.Print*`/`Fprint*`, every `tabwriter` row, every `json.NewEncoder`/`Marshal`, and every
multi-line `Fprintf` argument list.
- **The 68 sweep rows are present.** Peer, relay and owner text goes through `Term`, `Block`,
  `Line`, `writeJSON` or `DisplayQuote`, and the helpers (`devicePeerLabel`, `peerLabel`,
  `skillList`, `formatArtifact`) apply `Term` themselves.
- **Unwrapped values are daemon-validated:**
  - result `status` (`request.ValidateComplete` enum; also validated on receive,
    `worksession/receive.go:74`);
  - debate `outcome` (`validClose`, `debate/apply.go:528`);
  - relay `min_client` (`validMinClient` plus `ParseRelease`);
  - `auth` (a constant);
  - `introduced_by` (a stored key);
  - `notify --test` results (constants);
  - doctor details: the relay error goes through `Line`, and the `peers` row prints only
    keys and counts;
  - `approve --list` summaries (`displaytext.Safe` on insert, `internal/approval/store.go:73`;
    tab refused).
- **No other encoder:** apart from `identity --json` and `decision --json` (S90-1), every
  `--json` and `failJSON` output uses `writeJSON`.

**Verify vs VerifyStored.**
- **`Verify` call sites.** Pairing v1 and v2 (`internal/peers/pairing.go:717,765`, team join
  included) are the only callers of strict `Verify`. `VerifyStored` is used for the own card
  (`identity.go:193`), `RescueStored` (`cardcheck.go`), `forwardCard` (`team/store.go:751`)
  and roster intake (`team/kinds.go:367`).
- **The one way in.** A card refused at pairing can still enter, but only through an owner's
  roster (`peers.Store.Introduce`, `peers/store.go:373`). It is then printed escaped
  everywhere, as above.
- **Roster intake does not overwrite paired peers.** It never overwrites a directly paired
  row's card (`store.go:402-406`, `introduced_by` NULL), so an owner cannot replace a paired
  peer's card with a bidi one.
- **Re-forwarding never breaks a roster:**
  - an F10 owner forwards with `VerifyStored`;
  - F10 members accept with `VerifyStored`;
  - pre-F10 members accept, because the old `Verify` has no text rule.
- **Mixed versions.** In both directions no roster breaks. Only direct pairing with a legacy
  card fails (review 82b F11, accepted).

**Grant branch.**
- `checkBranch` (`internal/capability/token.go:251`) refuses every `Hidden` rune. It runs at
  issuance and receipt (`validate`, `:213`) and when serving (`git.go:232,252`).
- Stored grants are not re-validated on list (`scanGrant`), so `grant list` keeps working
  with an old record.
- An old policy on such a branch fails only when it issues.
- Request artifact `branch` stays lenient, as specified.

**Vectors.**
- `go run ./tools/verifyvectors`: "all vectors reproduced". N18 and N19 fail at step 5;
  P3 verifies, with its name checked.
- `go run ./tools/specvectors` (into a temp directory) regenerates N18, N19 and P3 with the
  same signatures as `tools/verifyvectors/vectors.json`.
- The bidi set in `tools/verifycard` and `tools/verifyvectors` is exactly Unicode
  `Bidi_Control`: U+061C, U+200E, U+200F, U+202A-U+202E and U+2066-U+2069. It shares no
  code with the daemon.

**Tests run (targeted, my probe skipped).** `go test -skip Sec90` passed on all of these:
- `./internal/{displaytext,agentcard,peers,team,capability,identity,decision,request}/...`
- `./cmd/agentnet/...`
- `./tools/...`

## Files

Created by the reviewer:
- `Docs/review/90-r55-f10-security.md` (this file).
- `cmd/agentnet/zz_sec90_test.go`: two probe tests, `TestSec90DecisionJSONStdoutRawBidi`
  (S90-1) and `TestSec90RawMessageInvalidUTF8` (S90-2). **Both fail until the fixes land.**
  Keep them, inverted as regression tests once fixed, or delete them before merge. The file
  holds no raw invisible character: code points are built with `rune(...)`.
- One temp file, `/tmp/tmp.woL76S2lsm/out.txt` (the `specvectors` output), already removed.

No other file was changed. Git use was read-only (`git log`, `git show`).
